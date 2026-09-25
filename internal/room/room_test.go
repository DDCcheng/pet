package room

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type sink struct {
	mu  sync.Mutex
	got map[string][]string
}

func newSink() *sink { return &sink{got: map[string][]string{}} }
func (s *sink) fn(pid string, b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got[pid] = append(s.got[pid], string(b))
}
func (s *sink) last(pid string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.got[pid]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

func TestJoinFlow(t *testing.T) {
	s := newSink()
	r := New("r1", "alice", "bob", s.fn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	r.Send(Cmd{PlayerId: "alice", Type: "join"})
	time.Sleep(30 * time.Millisecond)
	if !strings.Contains(s.last("alice"), "waiting") {
		t.Fatalf("一个人时应 waiting, got %s", s.last("alice"))
	}

	r.Send(Cmd{PlayerId: "bob", Type: "join"})
	time.Sleep(30 * time.Millisecond)
	if !strings.Contains(s.last("alice"), "ready") || !strings.Contains(s.last("bob"), "ready") {
		t.Fatalf("人齐时两人都应 ready")
	}
}

func TestStrangerIgnored(t *testing.T) {
	s := newSink()
	r := New("r1", "alice", "bob", s.fn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	r.Send(Cmd{PlayerId: "hacker", Type: "join"})
	time.Sleep(30 * time.Millisecond)
	if s.last("alice") != "" {
		t.Fatal("外人的命令不该产生广播")
	}
}

func TestGraceClosesEmptyRoom(t *testing.T) {
	s := newSink()
	r := New("r1", "alice", "bob", s.fn)
	r.Grace = 120 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	r.Send(Cmd{PlayerId: "alice", Type: "join"})
	r.Send(Cmd{PlayerId: "bob", Type: "join"})
	time.Sleep(30 * time.Millisecond)
	r.Send(Cmd{PlayerId: "alice", Type: "leave"})
	r.Send(Cmd{PlayerId: "bob", Type: "leave"})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("两人都断线后房间没有在宽限期内关闭")
	}
}

func TestGraceCancelledOnRejoin(t *testing.T) {
	s := newSink()
	r := New("r1", "alice", "bob", s.fn)
	r.Grace = 150 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	r.Send(Cmd{PlayerId: "alice", Type: "join"})
	r.Send(Cmd{PlayerId: "bob", Type: "join"})
	time.Sleep(20 * time.Millisecond)
	r.Send(Cmd{PlayerId: "alice", Type: "leave"})
	r.Send(Cmd{PlayerId: "bob", Type: "leave"})
	time.Sleep(60 * time.Millisecond)
	r.Send(Cmd{PlayerId: "alice", Type: "join"}) // 重连，应取消计时

	select {
	case <-done:
		t.Fatal("重连后房间不该关闭")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestManagerRemovesClosedRoom(t *testing.T) {
	m := NewManager()
	s := newSink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := m.Create(ctx, nil, "r1", "alice", "bob", s.fn)
	if m.Len() != 1 {
		t.Fatal("创建后应有 1 个房间")
	}
	r.Close() // ★ 客户端已不能发 game_over 关房间（Day 11 删掉了），直接关
	time.Sleep(50 * time.Millisecond)
	if m.Len() != 0 {
		t.Fatalf("房间关闭后应从 Manager 移除，实际还剩 %d", m.Len())
	}
}
