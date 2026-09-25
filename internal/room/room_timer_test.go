package room

import (
	"context"
	"testing"
	"time"

	"github.com/DDCcheng/pet/internal/battle"
)

// startTimedRoom 和 startRoom 一样，但能设回合时限。★ TurnLimit 必须在 go Run 之前设。
func startTimedRoom(t *testing.T, st *battle.State, limit time.Duration) (*Room, *sink) {
	t.Helper()
	s := newSink()
	r := New("r1", "alice", "bob", s.fn)
	r.Battle = st
	r.TurnLimit = limit
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx)
	r.Send(Cmd{PlayerId: "alice", Type: "join"})
	r.Send(Cmd{PlayerId: "bob", Type: "join"})
	return r, s
}

func roomClosed(r *Room) bool {
	select {
	case <-r.quit:
		return true
	default:
		return false
	}
}

// ---------- 1. 没人操作：超时自动换手，一轮接一轮，房间不关 ----------

func TestTurnTimeoutAutoEndTurn(t *testing.T) {
	r, s := startTimedRoom(t, newBattle(t, 1), 50*time.Millisecond)

	// 开局 1 条 + 两次超时换手 2 条
	eventually(t, "两次超时换手", func() bool { return len(s.msgs(t, "bob", "game_state")) >= 3 })

	g := s.msgs(t, "bob", "game_state")
	if g[1].Seq != 2 || !g[1].YourTurn {
		t.Fatalf("第 1 次超时后 bob: seq=%d your_turn=%v, want 2/true", g[1].Seq, g[1].YourTurn)
	}
	if g[2].Seq != 3 || g[2].YourTurn {
		t.Fatalf("第 2 次超时后 bob: seq=%d your_turn=%v, want 3/false（又轮回 alice）", g[2].Seq, g[2].YourTurn)
	}
	// alice 同步收到相同 seq
	a := s.msgs(t, "alice", "game_state")
	if len(a) < 3 || a[2].Seq != 3 || !a[2].YourTurn {
		t.Fatalf("alice 的状态没同步: %+v", a)
	}
	if roomClosed(r) {
		t.Fatal("超时换手不该关房间")
	}
}

// ---------- 2. 手动 end_turn 后，新回合重新计时 ----------

func TestTurnTimerRestartsAfterManualEndTurn(t *testing.T) {
	r, s := startTimedRoom(t, newBattle(t, 1), 300*time.Millisecond)
	eventually(t, "开局", func() bool { return len(s.msgs(t, "bob", "game_state")) == 1 })

	// alice 立刻手动结束回合 → bob 的回合开始计时
	r.Send(Cmd{PlayerId: "alice", Type: "end_turn", Data: data(`{"room_id":"r1"}`)})
	eventually(t, "手动换手", func() bool { return len(s.msgs(t, "bob", "game_state")) == 2 })
	start := time.Now()

	// bob 不动 → 大约 300ms 后被自动结束，轮回 alice
	eventually(t, "bob 回合超时", func() bool { return len(s.msgs(t, "bob", "game_state")) == 3 })
	elapsed := time.Since(start)

	g := s.msgs(t, "bob", "game_state")[2]
	if g.Seq != 3 || g.YourTurn {
		t.Fatalf("bob 超时后: seq=%d your_turn=%v, want 3/false", g.Seq, g.YourTurn)
	}
	// ★ 下限：如果计时器没在换手时重置（还在走 alice 那一轮的剩余时间），会明显早于 300ms
	if elapsed < 200*time.Millisecond {
		t.Fatalf("bob 回合只过了 %v 就超时，计时器没在换手时重新开始", elapsed)
	}
}

// ---------- 3. 还没开局（只来一个人）不计时 ----------

func TestTurnTimerNotStartedBeforeBothJoin(t *testing.T) {
	s := newSink()
	r := New("r1", "alice", "bob", s.fn)
	r.Battle = newBattle(t, 1)
	r.TurnLimit = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	r.Send(Cmd{PlayerId: "alice", Type: "join"})
	time.Sleep(150 * time.Millisecond) // 足够超时好几轮

	if n := len(s.msgs(t, "alice", "game_state")); n != 0 {
		t.Fatalf("没开局就推了 %d 条 game_state（计时器提前启动？）", n)
	}
}

// ---------- 4. 超时换手 → 对方疲劳死 → game_over，房间关闭 ----------

func TestTurnTimeoutCanEndGame(t *testing.T) {
	st := newBattle(t, 1)
	st.Players[1].Deck = nil // bob 牌库空
	st.Players[1].HP = 1     // 一点疲劳就死

	r, s := startTimedRoom(t, st, 50*time.Millisecond)
	eventually(t, "game_over", func() bool { return len(s.msgs(t, "alice", "game_over")) == 1 })

	if w := s.msgs(t, "alice", "game_over")[0].Winner; w != "alice" {
		t.Fatalf("winner=%q want alice", w)
	}
	eventually(t, "房间关闭", func() bool { return roomClosed(r) })
}
