package room

import (
	"reflect"
	"testing"
	"time"

	"github.com/DDCcheng/pet/internal/battle"
)

func TestStepsFromLog(t *testing.T) {
	log := []LogEntry{
		{Seq: 2, Player: "alice", Type: "play_card", Data: data(`{"room_id":"r1","card_id":"slime","type":"end_turn"}`)},
		{Seq: 3, Player: "alice", Type: "end_turn", Auto: true},
		{Seq: 4, Player: "bob", Type: "attack", Data: data(`{"room_id":"r1","inst_id":"m1","target":"face"}`)},
	}
	got, err := Steps(log)
	if err != nil {
		t.Fatal(err)
	}
	want := []battle.Step{
		{Player: "alice", Action: battle.Action{Type: "play_card", CardID: "slime"}},
		{Player: "alice", Action: battle.Action{Type: "end_turn"}},
		{Player: "bob", Action: battle.Action{Type: "attack", InstID: "m1", Target: "face"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if _, err := Steps([]LogEntry{{Type: "play_card", Data: data(`{bad`)}}); err == nil {
		t.Fatal("bad data should error")
	}
}

// 真房间：手动出牌 + 回合超时自动换手 + 关服中止，战报重放出来的局面必须和房间里的一模一样
func TestReportReplaysToSameState(t *testing.T) {
	st := battleWithSlime(t)
	r, s := startTimedRoom(t, st, 40*time.Millisecond)
	reps := make(chan Report, 1)
	r.OnFinish = func(rep Report) { reps <- rep }
	eventually(t, "开局", func() bool { return len(s.msgs(t, "alice", "game_state")) >= 1 })

	r.Send(Cmd{PlayerId: "alice", Type: "play_card", Data: data(`{"room_id":"r1","card_id":"slime"}`)})
	// 之后没人操作，靠超时一轮轮换手
	eventually(t, "几次超时换手", func() bool { return len(s.msgs(t, "bob", "game_state")) >= 6 })
	r.cancel()
	rep := waitReport(t, reps)

	auto := 0
	for _, e := range rep.Actions {
		if e.Auto {
			auto++
		}
	}
	if auto < 3 || rep.Actions[0].Type != "play_card" {
		t.Fatalf("战报应该有手动出牌和多次自动换手: %+v", rep.Actions)
	}

	steps, err := Steps(rep.Actions)
	if err != nil {
		t.Fatal(err)
	}
	got, err := battle.Replay(loadCat(t), rep.RoomID,
		battle.PlayerInit{ID: rep.Players[0], Cards: rep.InitDecks[0]},
		battle.PlayerInit{ID: rep.Players[1], Cards: rep.InitDecks[1]},
		rep.Seed, steps, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Run 已经退出（战报是它最后交出来的），这里读 st 不会和房间 goroutine 竞争
	if !reflect.DeepEqual(got.GodView(), st.GodView()) {
		t.Fatalf("重放和房间不一致:\n房间 %+v\n重放 %+v", st.GodView(), got.GodView())
	}
}
