package auth

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/DDCcheng/pet/internal/replay"
	"github.com/DDCcheng/pet/internal/store"
)

// ReportSource 是战报的读接口。生产用 *store.Reports（MySQL），测试可以换成内存实现。
type ReportSource interface {
	ListReports(ctx context.Context, player string, limit int) ([]store.ReportSummary, error)
	GetReport(ctx context.Context, roomID string) (store.BattleReport, error)
}

type reportItem struct {
	RoomID     string    `json:"room_id"`
	Opponent   string    `json:"opponent"`
	Result     string    `json:"result"` // win / lose / draw / unfinished
	EndReason  string    `json:"end_reason"`
	Rounds     int       `json:"rounds"`
	FirstHand  bool      `json:"first_hand"` // 我是不是先手
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms"`
}

// ListReports：GET /reports?limit=20，返回我打过的对局，新的在前。
func (s *Server) ListReports(w http.ResponseWriter, r *http.Request) {
	me, err := s.PlayerFromToken(r)
	if err != nil {
		code, msg := tokenErr(err)
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	limit := 20
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = min(v, 100)
	}
	rows, err := s.Reports.ListReports(r.Context(), me, limit)
	if err != nil {
		log.Printf("list reports for %s: %v", me, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	items := make([]reportItem, 0, len(rows))
	for _, x := range rows {
		it := reportItem{
			RoomID: x.RoomID, EndReason: x.EndReason, Rounds: x.Rounds,
			FirstHand: x.PlayerA == me, StartedAt: x.StartedAt,
			DurationMs: x.EndedAt.Sub(x.StartedAt).Milliseconds(),
			Opponent:   x.PlayerB,
		}
		if !it.FirstHand {
			it.Opponent = x.PlayerA
		}
		switch {
		case x.EndReason != "finished":
			it.Result = "unfinished"
		case x.Winner == "":
			it.Result = "draw"
		case x.Winner == me:
			it.Result = "win"
		default:
			it.Result = "lose"
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": items})
}

// ReplayReport：GET /reports/{room_id}/replay，服务端重放整局，返回上帝视角的全部帧。
// 只有对局双方能看；别人请求和战报不存在一样回 404，不暴露"这个房间存在"。
func (s *Server) ReplayReport(w http.ResponseWriter, r *http.Request) {
	me, err := s.PlayerFromToken(r)
	if err != nil {
		code, msg := tokenErr(err)
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	roomID := r.PathValue("room_id")
	rep, err := s.Reports.GetReport(r.Context(), roomID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && rep.PlayerA != me && rep.PlayerB != me) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "report not found"})
		return
	}
	if err != nil {
		log.Printf("get report %s: %v", roomID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	res, err := replay.Build(s.Cat, s.CardsHash, rep)
	switch {
	case errors.Is(err, replay.ErrCardsChanged):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cards_changed"})
		return
	case err != nil:
		// 重放和战报对不上 = 规则代码或数据出了问题，要在服务端日志里看得到
		log.Printf("replay %s: %v", roomID, err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "replay_mismatch"})
		return
	}
	// 战报落库后不会再变，同一局的回放结果永远一样，可以放心让浏览器缓存（private：只给本人）
	w.Header().Set("Cache-Control", "private, max-age=86400")
	writeJSON(w, http.StatusOK, res)
}
