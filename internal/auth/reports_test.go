package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/DDCcheng/pet/internal/battle"
	"github.com/DDCcheng/pet/internal/deck"
	"github.com/DDCcheng/pet/internal/store"
)

type fakeReports struct {
	list    []store.ReportSummary
	byID    map[string]store.BattleReport
	lastLim int
}

func (f *fakeReports) ListReports(_ context.Context, _ string, limit int) ([]store.ReportSummary, error) {
	f.lastLim = limit
	return f.list, nil
}
func (f *fakeReports) GetReport(_ context.Context, id string) (store.BattleReport, error) {
	r, ok := f.byID[id]
	if !ok {
		return r, sql.ErrNoRows
	}
	return r, nil
}

func reportServer(t *testing.T) (*Server, *fakeReports) {
	t.Helper()
	raw, err := os.ReadFile("../../configs/cards.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := deck.Load("../../configs/cards.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])

	d := []string{"slime", "slime", "wolf", "wolf", "knight", "knight", "dragon", "dragon"}
	decks, _ := json.Marshal([2][]string{d, d})
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	// 开局后没人动就中止的一局：0 个动作，回放只有第 0 帧
	aborted := store.BattleReport{RoomID: "r1", PlayerA: "alice", PlayerB: "bob", EndReason: "aborted", Seed: 5, Rounds: 1,
		InitDecksJSON: decks, ActionsJSON: []byte("[]"), CardsHash: hash, StartedAt: t0, EndedAt: t0.Add(time.Minute)}
	old := aborted
	old.RoomID, old.CardsHash = "r-old", "deadbeef"

	f := &fakeReports{
		byID: map[string]store.BattleReport{"r1": aborted, "r-old": old},
		list: []store.ReportSummary{
			{RoomID: "a", PlayerA: "alice", PlayerB: "bob", Winner: "alice", EndReason: "finished", StartedAt: t0, EndedAt: t0.Add(90 * time.Second)},
			{RoomID: "b", PlayerA: "bob", PlayerB: "alice", Winner: "bob", EndReason: "finished", StartedAt: t0},
			{RoomID: "c", PlayerA: "bob", PlayerB: "alice", Winner: "", EndReason: "finished", StartedAt: t0},
			{RoomID: "d", PlayerA: "alice", PlayerB: "carol", EndReason: "abandoned", StartedAt: t0},
		},
	}
	s := &Server{
		Tokens:  NewMemTokens(time.Hour, map[string]string{"ta": "alice", "tc": "carol"}),
		Reports: f, Cat: cat, CardsHash: hash,
	}
	return s, f
}

func getReq(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func reportMux(s *Server) *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /reports", s.ListReports)
	m.HandleFunc("GET /reports/{room_id}/replay", s.ReplayReport)
	return m
}

func TestListReports(t *testing.T) {
	s, f := reportServer(t)
	rec := getReq(t, reportMux(s), "/reports?limit=500", "ta")
	if rec.Code != 200 {
		t.Fatalf("code %d %s", rec.Code, rec.Body)
	}
	if f.lastLim != 100 {
		t.Fatalf("limit should be capped at 100, got %d", f.lastLim)
	}
	var body struct{ Reports []reportItem }
	json.Unmarshal(rec.Body.Bytes(), &body)
	want := []struct {
		opp, result string
		first       bool
	}{{"bob", "win", true}, {"bob", "lose", false}, {"bob", "draw", false}, {"carol", "unfinished", true}}
	if len(body.Reports) != len(want) {
		t.Fatalf("got %+v", body.Reports)
	}
	for i, w := range want {
		g := body.Reports[i]
		if g.Opponent != w.opp || g.Result != w.result || g.FirstHand != w.first {
			t.Errorf("row %d: got %+v want %+v", i, g, w)
		}
	}
	if body.Reports[0].DurationMs != 90000 {
		t.Errorf("duration %d", body.Reports[0].DurationMs)
	}
	if rec := getReq(t, reportMux(s), "/reports", ""); rec.Code != 401 {
		t.Fatalf("no token: %d", rec.Code)
	}
}

func TestReplayReport(t *testing.T) {
	s, _ := reportServer(t)
	m := reportMux(s)

	rec := getReq(t, m, "/reports/r1/replay", "ta")
	if rec.Code != 200 {
		t.Fatalf("participant: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") == "" {
		t.Error("replay of a stored report should be cacheable")
	}
	var res struct {
		Frames []struct {
			Index int
			State battle.GodView
		}
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Frames) != 1 || len(res.Frames[0].State.Players[1].Hand) == 0 {
		t.Fatalf("want 1 opening frame with both hands: %s", rec.Body)
	}

	for _, c := range []struct {
		path, tok string
		code      int
	}{
		{"/reports/r1/replay", "tc", 404},    // 不是这局的玩家：和不存在一样
		{"/reports/nope/replay", "ta", 404},  // 不存在
		{"/reports/r-old/replay", "ta", 409}, // 卡表改过
		{"/reports/r1/replay", "", 401},
	} {
		if rec := getReq(t, m, c.path, c.tok); rec.Code != c.code {
			t.Errorf("%s as %q: %d, want %d (%s)", c.path, c.tok, rec.Code, c.code, rec.Body)
		}
	}
}
