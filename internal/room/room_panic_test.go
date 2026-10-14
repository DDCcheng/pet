package room

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// 一个房间 panic 只关它自己：写 crashed 战报、计数 +1、通知双方，同一个 Manager 里的其他房间照常对局。
// panic 用 Out 回调来制造：房间推第 2 份状态（seq 2）时 Out 直接 panic，等价于房间 goroutine 里任何一处 bug。
func TestRoomPanicIsIsolated(t *testing.T) {
	m := NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	got := map[string][]string{} // room -> 发给 alice 的消息
	out := func(room string, crash bool) func(string, []byte) {
		return func(pid string, b []byte) {
			if crash && strings.Contains(string(b), `"seq":2`) {
				panic("boom")
			}
			if pid == "alice" {
				mu.Lock()
				got[room] = append(got[room], string(b))
				mu.Unlock()
			}
		}
	}
	reps := make(chan Report, 2)
	onFinish := func(rep Report) { reps <- rep }
	bad := m.Create(ctx, newBattle(t, 1), "bad", "alice", "bob", out("bad", true), onFinish)
	good := m.Create(ctx, newBattle(t, 1), "good", "alice", "bob", out("good", false), onFinish)
	for _, r := range []*Room{bad, good} {
		r.Send(Cmd{PlayerId: "alice", Type: "join"})
		r.Send(Cmd{PlayerId: "bob", Type: "join"})
	}
	before := RoomPanics()
	bad.Send(Cmd{PlayerId: "alice", Type: "end_turn", Data: data(`{}`)}) // 记一条动作，推 seq 2 时 panic

	select {
	case <-bad.Done():
	case <-time.After(time.Second):
		t.Fatal("panic 之后房间没关")
	}
	var rep Report
	select {
	case rep = <-reps:
	case <-time.After(time.Second):
		t.Fatal("panic 之后没写战报")
	}
	if rep.RoomID != "bad" || rep.Reason != "crashed" {
		t.Fatalf("战报 room=%s reason=%q，want bad/crashed", rep.RoomID, rep.Reason)
	}
	if len(rep.Actions) != 1 || rep.Actions[0].Type != "end_turn" {
		t.Fatalf("panic 前已经接受的动作要留在战报里：%+v", rep.Actions)
	}
	if d := RoomPanics() - before; d != 1 {
		t.Fatalf("panic 计数 +%d，want +1", d)
	}
	mu.Lock()
	notified := false
	for _, s := range got["bad"] {
		if strings.Contains(s, "room_crashed") {
			notified = true
		}
	}
	mu.Unlock()
	if !notified {
		t.Fatal("玩家没收到 room_crashed")
	}
	eventually(t, "坏房间从 Manager 移除", func() bool { _, ok := m.Get("bad"); return !ok })

	// 另一个房间不受影响，还能继续出动作
	if !good.Send(Cmd{PlayerId: "alice", Type: "end_turn", Data: data(`{}`)}) {
		t.Fatal("好房间收不了指令")
	}
	eventually(t, "好房间推出 seq 2", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range got["good"] {
			if strings.Contains(s, `"seq":2`) {
				return true
			}
		}
		return false
	})
}
