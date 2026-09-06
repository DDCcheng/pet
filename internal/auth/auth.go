package auth

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DDCcheng/pet/internal/deck"
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
	DB     *sql.DB
	Tokens map[string]string //token->playerid
	Conns  map[string]*WsClient
	mu     sync.RWMutex
}

type saveDeckReq struct {
	Cards []string `json:"cards"`
}

func readJSON(r *http.Request, dest any) error {
	return json.NewDecoder(r.Body).Decode(dest)
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
	//valid body
	var req loginReq
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, loginResp{Error: "bad json"})
		return
	}
	_, pass, err := store.FindUserByName(s.DB, req.Username)
	if err == sql.ErrNoRows {
		// 401
		writeJSON(w, http.StatusUnauthorized, loginResp{Error: "invalid credentials"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, loginResp{Error: "db error"})
		return
	}
	if pass != req.Password {
		writeJSON(w, http.StatusUnauthorized, loginResp{Error: "invalid credentials"})
		return
	}
	token := fmt.Sprintf("%s-%d", req.Username, time.Now().UnixNano())
	s.Tokens[token] = req.Username
	writeJSON(w, http.StatusOK, loginResp{Token: token, PlayerId: req.Username})
}

func (s *Server) PlayerFromToken(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", fmt.Errorf("missing token")
	}
	token := strings.TrimPrefix(h, prefix)
	username, ok := s.Tokens[token]
	if !ok {
		return "", fmt.Errorf("invalid token")
	}
	return username, nil
}

func (s *Server) Savedecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	//valid method
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, loginResp{Error: "method not allowed"})
		return
	}

	username, err := s.PlayerFromToken(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(loginResp{Error: err.Error()})
		return
	}
	//valid body
	var req saveDeckReq
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, loginResp{Error: "bad json"})
		return
	}

	cat := deck.Catalog()
	cards := make([]deck.Card, 0, len(req.Cards))
	for _, id := range req.Cards {
		c, ok := cat[id]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(loginResp{Error: "unknown card" + id})
			return
		}
		cards = append(cards, c)
	}

	d := deck.Deck{Owner: username, Cards: cards}
	if err := deck.ValidateDeck(d); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(loginResp{Error: err.Error()})
		return
	}

	userId, _, err := store.FindUserByName(s.DB, username)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(loginResp{Error: "user not found"})
		return
	}
	raw, _ := json.Marshal(req.Cards)
	err = store.UpsertDeck(s.DB, userId, string(raw))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(loginResp{Error: "db error"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{
		"ok":    "true",
		"owner": username,
	})
}
