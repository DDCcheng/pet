package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DDCcheng/pet/internal/battle"
	"github.com/DDCcheng/pet/internal/deck"
	"github.com/DDCcheng/pet/internal/room"
	"github.com/DDCcheng/pet/internal/store"
)

func loadCat(t *testing.T) *deck.Catalog {
	t.Helper()
	c, err := deck.Load("../../configs/cards.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var testDeck = []string{"slime", "slime", "wolf", "wolf", "knight", "knight", "dragon", "dragon"}

// finishedReport 打一局真对局：能出随从就出、能打脸就打脸，否则结束回合，直到分出胜负。
// 返回的战报格式和房间落库的一模一样。
func finishedReport(t *testing.T, cat *deck.Catalog) store.BattleReport {
	t.Helper()
	st, err := battle.NewState("rp", cat, battle.PlayerInit{ID: "alice", Cards: testDeck}, battle.PlayerInit{ID: "bob", Cards: testDeck}, 11)
	if err != nil {
		t.Fatal(err)
	}
	var log []room.LogEntry
	do := func(a battle.Action) bool {
		pid := st.Players[st.Turn].ID
		if st.Apply(pid, a) != nil {
			return false
		}
		d, _ := json.Marshal(map[string]string{"room_id": "rp", "card_id": a.CardID, "inst_id": a.InstID, "target": a.Target})
		log = append(log, room.LogEntry{Seq: uint64(len(log) + 2), Player: pid, Type: a.Type, Data: d, AtMs: int64(len(log)) * 1000, Hash: st.Checksum()})
		return true
	}
	for i := 0; i < 500 && !st.Over; i++ {
		me := st.Players[st.Turn]
		acted := false
		for _, c := range me.Hand {
			if do(battle.Action{Type: "play_card", CardID: c}) {
				acted = true
				break
			}
		}
		if !acted {
			for _, m := range me.Board {
				if m.CanAttack && do(battle.Action{Type: "attack", InstID: m.InstID, Target: "face"}) {
					acted = true
					break
				}
			}
		}
		if !acted {
			do(battle.Action{Type: "end_turn"})
		}
	}
	if !st.Over {
		t.Fatal("对局没打完")
	}
	decks, _ := json.Marshal(st.InitDecks)
	acts, _ := json.Marshal(log)
	return store.BattleReport{
		RoomID: "rp", PlayerA: "alice", PlayerB: "bob", Winner: st.Winner, EndReason: "finished",
		Seed: 11, Rounds: st.DisplayRound(), InitDecksJSON: decks, ActionsJSON: acts, CardsHash: "h",
		StartedAt: time.Unix(0, 0), EndedAt: time.Unix(60, 0),
	}
}

func TestBuildFrames(t *testing.T) {
	cat := loadCat(t)
	rep := finishedReport(t, cat)
	res, err := Build(cat, "h", rep)
	if err != nil {
		t.Fatal(err)
	}
	var log []room.LogEntry
	json.Unmarshal(rep.ActionsJSON, &log)
	if len(res.Frames) != len(log)+1 {
		t.Fatalf("%d frames for %d actions", len(res.Frames), len(log))
	}
	f0 := res.Frames[0]
	if f0.Index != 0 || f0.Action != nil || f0.State.Over {
		t.Fatalf("frame 0 should be the opening: %+v", f0)
	}
	if len(f0.State.Players[0].Hand) == 0 || len(f0.State.Players[1].Hand) == 0 {
		t.Fatal("god view: both hands visible")
	}
	last := res.Frames[len(res.Frames)-1]
	if !last.State.Over || last.State.Winner != rep.Winner || last.Index != len(log) {
		t.Fatalf("last frame: %+v", last)
	}
	for i, f := range res.Frames[1:] {
		if f.Action == nil || f.Player == "" || f.AtMs != log[i].AtMs || f.Seq != log[i].Seq {
			t.Fatalf("frame %d: %+v", i+1, f)
		}
	}
	// 各帧是独立快照：前后两帧的局面不能指向同一份数据
	if fmt.Sprint(res.Frames[0].State) == fmt.Sprint(last.State) {
		t.Fatal("frames should differ")
	}
	t.Logf("%d frames, winner %s, rounds %d", len(res.Frames), res.Winner, res.Rounds)
}

func TestBuildRejects(t *testing.T) {
	cat := loadCat(t)
	good := finishedReport(t, cat)

	if _, err := Build(cat, "other", good); !errors.Is(err, ErrCardsChanged) {
		t.Fatalf("cards hash: %v", err)
	}

	tamper := func(f func(r *store.BattleReport)) error {
		r := good
		f(&r)
		_, err := Build(cat, "h", r)
		return err
	}
	cases := map[string]func(r *store.BattleReport){
		"winner":  func(r *store.BattleReport) { r.Winner = "mallory" },
		"rounds":  func(r *store.BattleReport) { r.Rounds++ },
		"reason":  func(r *store.BattleReport) { r.EndReason = "aborted" },
		"seed":    func(r *store.BattleReport) { r.Seed++ },
		"players": func(r *store.BattleReport) { r.PlayerA, r.PlayerB = r.PlayerB, r.PlayerA },
		"truncated": func(r *store.BattleReport) {
			var log []room.LogEntry
			json.Unmarshal(r.ActionsJSON, &log)
			r.ActionsJSON, _ = json.Marshal(log[:len(log)-1])
		},
	}
	for name, f := range cases {
		if err := tamper(f); !errors.Is(err, ErrMismatch) {
			t.Errorf("%s: want ErrMismatch, got %v", name, err)
		}
	}
	if err := tamper(func(r *store.BattleReport) { r.ActionsJSON = []byte("{bad") }); err == nil {
		t.Error("bad actions json should error")
	}
}

// 老战报（加校验和之前落库的）没有 h 字段，照样能回放，只是退回到核对终局
func TestBuildOldReportWithoutChecksums(t *testing.T) {
	cat := loadCat(t)
	r := finishedReport(t, cat)
	var log []room.LogEntry
	json.Unmarshal(r.ActionsJSON, &log)
	for i := range log {
		log[i].Hash = ""
	}
	r.ActionsJSON, _ = json.Marshal(log)
	if _, err := Build(cat, "h", r); err != nil {
		t.Fatal(err)
	}
}

// 校验和能精确指出第几步开始分叉
func TestBuildChecksumPinpointsStep(t *testing.T) {
	cat := loadCat(t)
	r := finishedReport(t, cat)
	var log []room.LogEntry
	json.Unmarshal(r.ActionsJSON, &log)
	log[5].Hash = "0000000000000000"
	r.ActionsJSON, _ = json.Marshal(log)
	_, err := Build(cat, "h", r)
	if !errors.Is(err, ErrMismatch) || !strings.Contains(err.Error(), "from action 5") {
		t.Fatalf("got %v", err)
	}
}
