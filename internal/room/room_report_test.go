package room

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DDCcheng/pet/internal/battle"
)

// startReportRoom 和 startRoom 一样起房间并让两人 join，但先挂上 OnFinish（必须在 Run 之前赋值）
func startReportRoom(t *testing.T, st *battle.State) (*Room, context.CancelFunc, chan Report) {
	t.Helper()
	s := newSink()
	r := New("r1", "alice", "bob", s.fn)
	r.Battle = st
	reps := make(chan Report, 1)
	r.OnFinish = func(rep Report) { reps <- rep }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx)

	r.Send(Cmd{PlayerId: "alice", Type: "join"})
	r.Send(Cmd{PlayerId: "bob", Type: "join"})
	eventually(t, "双方收到开局 game_state", func() bool {
		return len(s.msgs(t, "alice", "game_state")) == 1 && len(s.msgs(t, "bob", "game_state")) == 1
	})
	return r, cancel, reps
}

func waitReport(t *testing.T, reps chan Report) Report {
	t.Helper()
	select {
	case rep := <-reps:
		return rep
	case <-time.After(time.Second):
		t.Fatal("OnFinish 没被调用")
		return Report{}
	}
}

func TestReportOnGameOver(t *testing.T) {
	st := newBattle(t, 1)
	// 同 TestRoomGameOverClosesRoom 的残局：bob 牌库空、1 血，alice end_turn 后 bob 疲劳致死
	st.Players[1].Deck = nil
	st.Players[1].HP = 1

	r, _, reps := startReportRoom(t, st)
	r.Send(Cmd{PlayerId: "alice", Type: "end_turn", Data: data(`{"room_id":"r1"}`)})

	rep := waitReport(t, reps)
	if rep.Reason != "finished" || rep.Winner != "alice" {
		t.Fatalf("reason=%q winner=%q, want finished/alice", rep.Reason, rep.Winner)
	}
	if rep.RoomID != "r1" || rep.Players != [2]string{"alice", "bob"} {
		t.Fatalf("RoomID=%q Players=%v，没填上", rep.RoomID, rep.Players)
	}
	if rep.Seed != 1 {
		t.Fatalf("seed=%d want 1", rep.Seed)
	}
	if len(rep.Actions) != 1 || rep.Actions[0].Type != "end_turn" || rep.Actions[0].Player != "alice" {
		t.Fatalf("actions=%+v，want 1 条 alice end_turn", rep.Actions)
	}
	if rep.Actions[0].Seq != 2 {
		t.Fatalf("动作 seq=%d want 2（开局状态是 1）", rep.Actions[0].Seq)
	}
	if len(rep.InitDecks[0]) != 12 || len(rep.InitDecks[1]) != 12 {
		t.Fatalf("InitDecks 长度 %d/%d want 12/12", len(rep.InitDecks[0]), len(rep.InitDecks[1]))
	}
	if rep.StartedAt.IsZero() || rep.EndedAt.Before(rep.StartedAt) {
		t.Fatalf("时间不对 started=%v ended=%v", rep.StartedAt, rep.EndedAt)
	}
}

func TestParentCancelClosesRoomAndReportsAborted(t *testing.T) {
	r, cancel, reps := startReportRoom(t, newBattle(t, 1))
	cancel() // 取消的是父 ctx（关服），不是 r.Close()

	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("父 ctx 取消后房间没关")
	}
	// 注意：父 ctx 取消时 r.ctx 先被 cancel，Done() 可能早于 finish 返回，所以这里要等
	rep := waitReport(t, reps)
	if rep.Reason != "aborted" || rep.Winner != "" {
		t.Fatalf("reason=%q winner=%q, want aborted/空", rep.Reason, rep.Winner)
	}
	if r.Send(Cmd{PlayerId: "alice", Type: "end_turn"}) {
		t.Fatal("房间关了 Send 还返回 true")
	}
}

// 关服顺序靠的是 Manager.Wait：Wait 返回时所有房间的 OnFinish 都已跑完，main 才能关 DB
func TestManagerWaitAfterReports(t *testing.T) {
	m := NewManager()
	s := newSink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var got []Report // 两个房间的 OnFinish 并发写，所以要锁；Wait 之后才读
	ids := []string{"r1", "r2"}
	for _, id := range ids {
		r := m.Create(ctx, newBattle(t, 1), id, "alice", "bob", s.fn, func(rep Report) {
			time.Sleep(20 * time.Millisecond) // 模拟落库耗时
			mu.Lock()
			got = append(got, rep)
			mu.Unlock()
		})
		r.Send(Cmd{PlayerId: "alice", Type: "join"})
		r.Send(Cmd{PlayerId: "bob", Type: "join"})
	}
	eventually(t, "两个房间都开局", func() bool { return len(s.msgs(t, "alice", "game_state")) == 2 })

	cancel()
	m.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("Wait 返回时只有 %d 份战报，want 2", len(got))
	}
	for _, rep := range got {
		if rep.Reason != "aborted" {
			t.Fatalf("reason=%q want aborted", rep.Reason)
		}
	}
	if m.Len() != 0 {
		t.Fatalf("Wait 后还剩 %d 个房间", m.Len())
	}
}

func TestNoReportIfNeverStarted(t *testing.T) {
	s := newSink()
	r := New("r1", "alice", "bob", s.fn)
	r.Battle = newBattle(t, 1)
	called := make(chan Report, 1)
	r.OnFinish = func(rep Report) { called <- rep }
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)

	r.Send(Cmd{PlayerId: "alice", Type: "join"}) // 只来一个人，没开局
	cancel()
	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("房间没关")
	}
	select {
	case <-called:
		t.Fatal("没开局不该写战报")
	case <-time.After(50 * time.Millisecond):
	}
}
