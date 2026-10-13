package room

import "testing"

// 不起 Run：inbox 没人消费，第 65 条起应被丢弃并计数
func TestInboxFullDropsAndCounts(t *testing.T) {
	r := New("r1", "alice", "bob", nil)
	d0 := InboxDropped()
	for i := 0; i < cap(r.inbox); i++ {
		if !r.Send(Cmd{PlayerId: "alice", Type: "end_turn"}) {
			t.Fatalf("第 %d 条就投递失败了", i+1)
		}
	}
	for i := 0; i < 3; i++ {
		if r.Send(Cmd{PlayerId: "bob", Type: "end_turn"}) {
			t.Fatal("inbox 满了 Send 应返回 false")
		}
	}
	if d := InboxDropped() - d0; d != 3 {
		t.Fatalf("dropped=%d want 3", d)
	}
	// 关掉的房间不算"丢弃"：那是 room_closed，不是 inbox 满
	r.Close()
	r.Send(Cmd{PlayerId: "bob", Type: "end_turn"})
	if d := InboxDropped() - d0; d != 3 {
		t.Fatalf("房间关闭后的投递不该计入 inbox 丢弃，dropped=%d", d)
	}
}
