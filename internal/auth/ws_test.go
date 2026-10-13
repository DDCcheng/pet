package auth

import (
	"fmt"
	"testing"
	"time"
)

// fakeClient 只有 send/quit，没有真连接，也不起 writeLoop：缓冲不会被消费，模拟"对端不读"
func fakeClient(buf int) *WsClient {
	return &WsClient{send: make(chan []byte, buf), quit: make(chan struct{})}
}

func closed(c *WsClient) bool {
	select {
	case <-c.quit:
		return true
	default:
		return false
	}
}

func TestSendFullKicksConn(t *testing.T) {
	d0, k0 := SendDropped(), SlowKicked()
	c := fakeClient(2)
	if !c.Send([]byte("1")) || !c.Send([]byte("2")) {
		t.Fatal("缓冲没满时 Send 应该成功")
	}
	if closed(c) {
		t.Fatal("没写满不该踢")
	}
	if c.Send([]byte("3")) {
		t.Fatal("写满后 Send 应返回 false")
	}
	if !closed(c) {
		t.Fatal("写满后连接应被关闭")
	}
	if d, k := SendDropped()-d0, SlowKicked()-k0; d != 1 || k != 1 {
		t.Fatalf("dropped=%d kicked=%d, want 1/1", d, k)
	}
	// 踢掉之后再发：走 quit 分支，不再重复计数
	for i := 0; i < 5; i++ {
		if c.Send([]byte("x")) {
			t.Fatal("已踢掉的连接 Send 应返回 false")
		}
	}
	if k := SlowKicked() - k0; k != 1 {
		t.Fatalf("同一连接被计了 %d 次踢出，want 1", k)
	}
}

// 并发写满时，只踢一次、只计一次（-race 下跑）
func TestSendFullConcurrentKickOnce(t *testing.T) {
	k0 := SlowKicked()
	c := fakeClient(1)
	c.Send([]byte("fill"))
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				c.Send([]byte("x"))
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if k := SlowKicked() - k0; k != 1 {
		t.Fatalf("kicked=%d want 1", k)
	}
}

// 慢客户端写满被踢，对手照常收到每一条；房间那一侧的 SendTo 全程不阻塞
func TestSlowClientDoesNotAffectOpponent(t *testing.T) {
	slow, fast := fakeClient(4), fakeClient(256)
	s := &Server{Conns: map[string]*WsClient{"alice": slow, "bob": fast}}

	const n = 100
	start := time.Now()
	for i := 0; i < n; i++ {
		msg := []byte(fmt.Sprintf(`{"seq":%d}`, i))
		s.SendTo("alice", msg)
		s.SendTo("bob", msg)
	}
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Fatalf("SendTo 花了 %v，房间协程被拖住了", el)
	}
	if !closed(slow) {
		t.Fatal("慢客户端应被踢掉")
	}
	if closed(fast) {
		t.Fatal("对手不该被踢")
	}
	if got := len(fast.send); got != n {
		t.Fatalf("对手收到 %d 条，want %d", got, n)
	}
	for i := 0; i < n; i++ {
		if want := fmt.Sprintf(`{"seq":%d}`, i); string(<-fast.send) != want {
			t.Fatalf("对手第 %d 条不是 %s，顺序乱了", i, want)
		}
	}
}
