package auth

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/DDCcheng/pet/internal/deck"
	"github.com/DDCcheng/pet/internal/match"
	"github.com/DDCcheng/pet/internal/room"
	"github.com/DDCcheng/pet/internal/store"
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResp struct {
	Token    string `json:"token,omitempty"`
	PlayerId string `json:"player_id,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Server struct {
	DB      *sql.DB
	Tokens  TokenStore           //token->playerid，生产用 Redis，测试可用内存版
	Conns   map[string]*WsClient //playerId->websocket
	mu      sync.RWMutex
	MM      *match.Manager
	RoomMgr *room.Manager
	Cat     *deck.Catalog

	AllowedOrigins []string   // WebSocket 允许的浏览器来源；空表示只允许同源
	LoginLimiter   *IPLimiter // /login 按 IP 限速；nil 表示不限

	Reports   ReportSource // 战报读取（列表 + 回放）
	CardsHash string       // 当前卡表的 sha256，回放时和战报里记的比对
}

type saveDeckReq struct {
	Cards []string `json:"cards"`
}

func readJSON(r *http.Request, dest any) error {
	// 请求体上限 64KB：卡组和登录都只有几百字节，不设上限的话一个超大请求体就能占满内存
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10)).Decode(dest)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	//valid method
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, loginResp{Error: "method not allowed"})
		return
	}
	// 限速放在查库和 bcrypt 之前，被拒的请求几乎不花 CPU
	if s.LoginLimiter != nil && !s.LoginLimiter.Allow(clientIP(r)) {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusTooManyRequests, loginResp{Error: "too many login attempts"})
		return
	}
	//valid body
	var req loginReq
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, loginResp{Error: "bad json"})
		return
	}
	uid, pass, err := store.FindUserByName(s.DB, req.Username)
	if err == sql.ErrNoRows {
		// 401。用户不存在也跑一次 bcrypt，避免靠响应时间探测用户名是否存在
		burnPasswordCheck(req.Password)
		writeJSON(w, http.StatusUnauthorized, loginResp{Error: "invalid credentials"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, loginResp{Error: "db error"})
		return
	}
	ok, needUpgrade := checkPassword(pass, req.Password)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, loginResp{Error: "invalid credentials"})
		return
	}
	if needUpgrade {
		// 老的明文密码：校验通过后顺手改写成哈希。失败只记日志，不影响这次登录
		if h, err := HashPassword(req.Password); err == nil {
			if err := store.UpdatePassword(s.DB, uid, h); err != nil {
				log.Printf("upgrade password for %s: %v", req.Username, err)
			}
		}
	}
	token, err := s.Tokens.Issue(r.Context(), req.Username)
	if err != nil {
		log.Printf("issue token for %s: %v", req.Username, err)
		writeJSON(w, http.StatusServiceUnavailable, loginResp{Error: "token store unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, loginResp{Token: token, PlayerId: req.Username})
}

func (s *Server) PlayerFromToken(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", fmt.Errorf("missing token: %w", ErrInvalidToken)
	}
	token := strings.TrimPrefix(h, prefix)
	return s.Tokens.Lookup(r.Context(), token)
}

func (s *Server) Savedecks(w http.ResponseWriter, r *http.Request) {
	//valid method
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, loginResp{Error: "method not allowed"})
		return
	}
	username, err := s.PlayerFromToken(r)
	if err != nil {
		code, msg := tokenErr(err)
		writeJSON(w, code, loginResp{Error: msg})
		return
	}
	//valid body
	var req saveDeckReq
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, loginResp{Error: "bad json"})
		return
	}
	d := deck.Deck{Owner: username, Cards: req.Cards}
	if err := deck.ValidateDeck(s.Cat, d); err != nil {
		writeJSON(w, http.StatusBadRequest, loginResp{Error: err.Error()})
		return
	}

	userId, _, err := store.FindUserByName(s.DB, username)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, loginResp{Error: "user not found"})
		return
	}
	raw, _ := json.Marshal(req.Cards)
	err = store.UpsertDeck(s.DB, userId, string(raw))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, loginResp{Error: "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"ok":    "true",
		"owner": username,
	})
}

func (s *Server) NotifyMatch(a, b, roomId string) {
	msg, _ := json.Marshal(map[string]string{
		"type": "match_found", "room_id": roomId,
	})
	s.SendTo(a, msg)
	s.SendTo(b, msg)
}

func (s *Server) SendTo(playerId string, payload []byte) {
	s.mu.RLock()
	c := s.Conns[playerId]
	s.mu.RUnlock()
	if c != nil {
		c.Send(payload)
	}
}
