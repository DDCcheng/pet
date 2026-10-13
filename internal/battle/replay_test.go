package battle

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fullFingerprint 比 fingerprint 更严：牌库顺序和下一个 InstID 也算进去。
// 回放要求"一步不差"，牌库顺序不同哪怕当下看不出来，后面抽牌也会分叉。
func fullFingerprint(s *State) string {
	var b strings.Builder
	b.WriteString(fingerprint(s))
	for i, p := range s.Players {
		fmt.Fprintf(&b, "p%d deck=%v\n", i, p.Deck)
	}
	fmt.Fprintf(&b, "nextInst=%d\n", s.nextInstID)
	return b.String()
}

// liveGame 按"当前回合方执行"跑一串动作，只把被接受的记成 Step（和房间记战报的规则一样）。
func liveGame(t *testing.T, acts []Action, seed int64) (*State, []Step) {
	t.Helper()
	s := mustNewState(t, cat(t), PlayerInit{"alice", thickCards(30)}, PlayerInit{"bob", thickCards(30)}, seed)
	var steps []Step
	for _, a := range acts {
		pid := s.Players[s.Turn].ID
		if s.Apply(pid, a) == nil {
			steps = append(steps, Step{Player: pid, Action: a})
		}
	}
	return s, steps
}

var mixedActs = []Action{
	{Type: "play_card", CardID: "slime"},
	{Type: "play_card", CardID: "slime"}, // 费不够，拒
	{Type: "attack", InstID: "m1", Target: "face"},
	{Type: "end_turn"},
	{Type: "play_card", CardID: "slime"},
	{Type: "end_turn"},
	{Type: "attack", InstID: "m1", Target: "face"},
	{Type: "play_card", CardID: "slime"},
	{Type: "play_card", CardID: "slime"},
	{Type: "end_turn"},
	{Type: "attack", InstID: "m2", Target: "m1"},
	{Type: "end_turn"},
	{Type: "attack", InstID: "m3", Target: "m2"},
	{Type: "attack", InstID: "m1", Target: "face"},
	{Type: "end_turn"},
}

func TestReplayMatchesLive(t *testing.T) {
	live, steps := liveGame(t, mixedActs, 7)
	calls := []int{}
	got, err := Replay(cat(t), "r", PlayerInit{"alice", thickCards(30)}, PlayerInit{"bob", thickCards(30)}, 7, steps,
		func(i int, s *State) { calls = append(calls, i) })
	if err != nil {
		t.Fatal(err)
	}
	if fullFingerprint(got) != fullFingerprint(live) {
		t.Fatalf("回放和线上不一致:\n--- 线上 ---\n%s\n--- 回放 ---\n%s", fullFingerprint(live), fullFingerprint(got))
	}
	if len(calls) != len(steps)+1 || calls[0] != -1 || calls[len(calls)-1] != len(steps)-1 {
		t.Fatalf("onStep 调用序列 %v，steps=%d", calls, len(steps))
	}
}

func TestReplayNilOnStep(t *testing.T) {
	_, steps := liveGame(t, mixedActs, 7)
	if _, err := Replay(cat(t), "r", PlayerInit{"alice", thickCards(30)}, PlayerInit{"bob", thickCards(30)}, 7, steps, nil); err != nil {
		t.Fatal(err)
	}
}

func TestReplayReportsDivergentStep(t *testing.T) {
	_, steps := liveGame(t, mixedActs, 7)
	bad := append([]Step(nil), steps...)
	bad[2] = Step{Player: bad[2].Player, Action: Action{Type: "play_card", CardID: "dragon"}} // 手里根本没有
	_, err := Replay(cat(t), "r", PlayerInit{"alice", thickCards(30)}, PlayerInit{"bob", thickCards(30)}, 7, bad, nil)
	var re *ReplayError
	if !errors.As(err, &re) || re.Step != 2 {
		t.Fatalf("want ReplayError at step 2, got %v", err)
	}
	if codeOf(err) != string(CodeCardNotInHand) {
		t.Fatalf("Unwrap 后应该是 card_not_in_hand，got %s", codeOf(err))
	}
}

// 换了玩家顺序（等于换了先手）也必须分叉，不能悄悄"重放成功"
func TestReplaySwappedPlayersDiverges(t *testing.T) {
	_, steps := liveGame(t, mixedActs, 7)
	_, err := Replay(cat(t), "r", PlayerInit{"bob", thickCards(30)}, PlayerInit{"alice", thickCards(30)}, 7, steps, nil)
	if err == nil {
		t.Fatal("swapped players should diverge")
	}
}

func TestGodViewSeesBothHandsAndIsACopy(t *testing.T) {
	s := mustNewState(t, cat(t), PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 42)
	v := s.GodView()
	if len(v.Players[0].Hand) == 0 || len(v.Players[1].Hand) == 0 {
		t.Fatalf("god view should show both hands: %+v", v.Players)
	}
	before := fmt.Sprint(v)
	s.Players[0].Hand[0] = "changed"
	s.Players[0].HP = 1
	if fmt.Sprint(v) != before {
		t.Fatal("GodView must be a deep copy")
	}
}

func TestChecksum(t *testing.T) {
	a := mustNewState(t, cat(t), PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 42)
	b := mustNewState(t, cat(t), PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 42)
	if a.Checksum() != b.Checksum() || len(a.Checksum()) != 16 {
		t.Fatalf("same seed should give same checksum: %s %s", a.Checksum(), b.Checksum())
	}
	c := mustNewState(t, cat(t), PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 43)
	if a.Checksum() == c.Checksum() {
		t.Fatal("different seed should change the checksum")
	}
	// 只换牌库顺序（看不见的部分）也要变
	before := a.Checksum()
	d := a.Players[0].Deck
	d[0], d[len(d)-1] = d[len(d)-1], d[0]
	if a.Checksum() == before && d[0] != d[len(d)-1] {
		t.Fatal("deck order must be part of the checksum")
	}
}
