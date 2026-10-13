package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DDCcheng/pet/internal/auth"
	"github.com/DDCcheng/pet/internal/room"
)

func TestToBattleReport(t *testing.T) {
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		rep         room.Report
		wantActions string
		wantDecks   string
	}{
		{
			name: "正常结束",
			rep: room.Report{
				RoomID: "r1", Players: [2]string{"alice", "bob"},
				Winner: "alice", Reason: "finished", Seed: 42, Rounds: 3,
				Actions: []room.LogEntry{
					{Seq: 2, Player: "alice", Type: "end_turn", AtMs: 10},
					{Seq: 3, Player: "bob", Type: "end_turn", Auto: true, AtMs: 60010},
				},
				InitDecks: [2][]string{{"slime"}, {"wolf"}},
				StartedAt: t0, EndedAt: t0.Add(time.Minute),
			},
			wantActions: `[{"seq":2,"player":"alice","type":"end_turn","at_ms":10},{"seq":3,"player":"bob","type":"end_turn","auto":true,"at_ms":60010}]`,
			wantDecks:   `[["slime"],["wolf"]]`,
		},
		{
			name: "中止且没有动作",
			rep: room.Report{
				RoomID: "r2", Players: [2]string{"a", "b"}, Reason: "aborted",
				InitDecks: [2][]string{{}, {}},
				StartedAt: t0, EndedAt: t0,
			},
			wantActions: `[]`,
			wantDecks:   `[[],[]]`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			br, err := toBattleReport("hash", c.rep)
			if err != nil {
				t.Fatal(err)
			}
			if br.RoomID != c.rep.RoomID || br.PlayerA != c.rep.Players[0] || br.PlayerB != c.rep.Players[1] {
				t.Fatalf("房间/玩家没对上: %+v", br)
			}
			if br.Winner != c.rep.Winner || br.EndReason != c.rep.Reason || br.Seed != c.rep.Seed || br.Rounds != c.rep.Rounds {
				t.Fatalf("结果字段没对上: %+v", br)
			}
			if br.CardsHash != "hash" || !br.StartedAt.Equal(c.rep.StartedAt) || !br.EndedAt.Equal(c.rep.EndedAt) {
				t.Fatalf("hash/时间没对上: %+v", br)
			}
			if string(br.ActionsJSON) != c.wantActions {
				t.Fatalf("actions=%s\nwant    %s", br.ActionsJSON, c.wantActions)
			}
			if string(br.InitDecksJSON) != c.wantDecks {
				t.Fatalf("decks=%s want %s", br.InitDecksJSON, c.wantDecks)
			}
			if !json.Valid(br.ActionsJSON) || !json.Valid(br.InitDecksJSON) {
				t.Fatal("不是合法 JSON，MySQL JSON 列会拒收")
			}
		})
	}
}

func TestRoutesExposeMetrics(t *testing.T) {
	s := &auth.Server{Conns: map[string]*auth.WsClient{}, RoomMgr: room.NewManager()}
	rec := httptest.NewRecorder()
	routes(s).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pet_rooms_active 0") {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}
