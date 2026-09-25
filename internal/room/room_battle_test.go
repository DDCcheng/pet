package room

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DDCcheng/pet/internal/battle"
	"github.com/DDCcheng/pet/internal/deck"
)

// ---------- helpers ----------

// 12 张合法卡组：卡表每张 2 张
func testDeck() []string {
	return []string{"slime", "slime", "wolf", "wolf", "knight", "knight",
		"dragon", "dragon", "bolt", "bolt", "heal", "heal"}
}

func loadCat(t *testing.T) *deck.Catalog {
	t.Helper()
	c, err := deck.Load("../../configs/cards.yaml") // go test 的工作目录是包目录
	if err != nil {
		t.Fatalf("load cards: %v", err)
	}
	return c
}

func newBattle(t *testing.T, seed int64) *battle.State {
	t.Helper()
	st, err := battle.NewState("r1", loadCat(t),
		battle.PlayerInit{ID: "alice", Cards: testDeck()},
		battle.PlayerInit{ID: "bob", Cards: testDeck()}, seed)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// battleWithSlime 找一个让 alice 起手有 slime（1 费，第一回合打得出）的 seed，
// 这样测试不依赖洗牌运气。
func battleWithSlime(t *testing.T) *battle.State {
	t.Helper()
	for seed := int64(1); seed < 500; seed++ {
		st := newBattle(t, seed)
		for _, c := range st.ViewFor(0).Me.Hand {
			if c == "slime" {
				return st
			}
		}
	}
	t.Fatal("找不到 alice 起手有 slime 的 seed")
	return nil
}

// gameMsg 是客户端收到的 game_state / error / game_over 的并集，测试里按需取字段
type gameMsg struct {
	Type   string `json:"type"`
	Code   string `json:"code"`
	Winner string `json:"winner"`
	battle.View
}

// msgs 取某玩家收到的、type 为 typ 的所有消息
func (s *sink) msgs(t *testing.T, pid, typ string) []gameMsg {
	t.Helper()
	s.mu.Lock()
	raw := append([]string(nil), s.got[pid]...)
	s.mu.Unlock()
	var out []gameMsg
	for _, r := range raw {
		var m gameMsg
		if err := json.Unmarshal([]byte(r), &m); err != nil {
			t.Fatalf("bad json %q: %v", r, err)
		}
		if m.Type == typ {
			out = append(out, m)
		}
	}
	return out
}

// eventually 轮询等房间 goroutine 处理完，比固定 sleep 稳
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("超时：%s", what)
}

func data(v string) json.RawMessage { return json.RawMessage(v) }

// startRoom 起房间并让两人 join，等到双方都收到开局 game_state
func startRoom(t *testing.T, st *battle.State) (*Room, *sink) {
	t.Helper()
	s := newSink()
	r := New("r1", "alice", "bob", s.fn)
	r.Battle = st
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx)

	r.Send(Cmd{PlayerId: "alice", Type: "join"})
	r.Send(Cmd{PlayerId: "bob", Type: "join"})
	eventually(t, "双方收到开局 game_state", func() bool {
		return len(s.msgs(t, "alice", "game_state")) == 1 && len(s.msgs(t, "bob", "game_state")) == 1
	})
	return r, s
}

// ---------- 1. 开局：两人到齐推一次，seq=1，视角各不相同 ----------

func TestRoomStartPushesState(t *testing.T) {
	_, s := startRoom(t, newBattle(t, 1))

	a := s.msgs(t, "alice", "game_state")[0]
	b := s.msgs(t, "bob", "game_state")[0]
	if a.Seq != 1 || b.Seq != 1 {
		t.Fatalf("开局 seq: alice=%d bob=%d want 1", a.Seq, b.Seq)
	}
	if !a.YourTurn || b.YourTurn {
		t.Fatalf("开局应是 alice 回合: alice=%v bob=%v", a.YourTurn, b.YourTurn)
	}
	if a.Me.ID != "alice" || b.Me.ID != "bob" {
		t.Fatalf("视角错位: alice 收到 me=%s, bob 收到 me=%s", a.Me.ID, b.Me.ID)
	}
	if b.Opp.Hand != nil || a.Opp.Hand != nil {
		t.Fatal("开局就泄露了对手手牌")
	}
}

// ---------- 2. 过关标准：A 出牌，B 立刻看到场上变化 ----------

func TestRoomPlayCardBroadcast(t *testing.T) {
	r, s := startRoom(t, battleWithSlime(t))
	before := s.msgs(t, "bob", "game_state")[0]

	r.Send(Cmd{PlayerId: "alice", Type: "play_card", Data: data(`{"room_id":"r1","card_id":"slime"}`)})
	eventually(t, "bob 收到第 2 条 game_state", func() bool {
		return len(s.msgs(t, "bob", "game_state")) == 2
	})

	after := s.msgs(t, "bob", "game_state")[1]
	if after.Seq != 2 {
		t.Fatalf("seq=%d want 2", after.Seq)
	}
	if len(after.Opp.Board) != 1 || after.Opp.Board[0].CardID != "slime" {
		t.Fatalf("bob 眼里 alice 的场: %+v，want 一只 slime", after.Opp.Board)
	}
	if after.Opp.HandCount != before.Opp.HandCount-1 {
		t.Fatalf("alice 手牌数 %d→%d，应该少 1", before.Opp.HandCount, after.Opp.HandCount)
	}
	if after.Opp.Hand != nil {
		t.Fatalf("bob 看到了 alice 的手牌: %v", after.Opp.Hand)
	}
	// alice 自己也收到同一 seq 的新状态
	a := s.msgs(t, "alice", "game_state")
	if len(a) != 2 || a[1].Seq != 2 || len(a[1].Me.Board) != 1 {
		t.Fatalf("alice 的第 2 条 game_state 不对: %+v", a)
	}
}

// ---------- 3. 非法操作：错误只发给操作者，seq 不变 ----------

func TestRoomErrorOnlyToActor(t *testing.T) {
	r, s := startRoom(t, newBattle(t, 1))

	// 不是 bob 的回合
	r.Send(Cmd{PlayerId: "bob", Type: "end_turn", Data: data(`{"room_id":"r1"}`)})
	eventually(t, "bob 收到 error", func() bool { return len(s.msgs(t, "bob", "error")) == 1 })

	e := s.msgs(t, "bob", "error")[0]
	if e.Code != string(battle.CodeNotYourTurn) {
		t.Fatalf("error code=%q want %q（sendErr 没取到 *battle.Err？）", e.Code, battle.CodeNotYourTurn)
	}
	if n := len(s.msgs(t, "alice", "error")); n != 0 {
		t.Fatalf("alice 不该收到 bob 的错误，收到 %d 条", n)
	}
	// 失败不推状态
	if n := len(s.msgs(t, "alice", "game_state")); n != 1 {
		t.Fatalf("失败后多推了状态: alice 共 %d 条 game_state", n)
	}

	// 之后合法操作 seq 接着是 2，说明失败没占号
	r.Send(Cmd{PlayerId: "alice", Type: "end_turn", Data: data(`{"room_id":"r1"}`)})
	eventually(t, "end_turn 后推状态", func() bool { return len(s.msgs(t, "bob", "game_state")) == 2 })
	g := s.msgs(t, "bob", "game_state")[1]
	if g.Seq != 2 || !g.YourTurn {
		t.Fatalf("end_turn 后 bob: seq=%d your_turn=%v, want 2/true", g.Seq, g.YourTurn)
	}
}

// ---------- 4. 坏 JSON / 未知卡：不崩，回错误 ----------

func TestRoomBadAction(t *testing.T) {
	r, s := startRoom(t, newBattle(t, 1))
	r.Send(Cmd{PlayerId: "alice", Type: "play_card", Data: data(`{not json`)})
	r.Send(Cmd{PlayerId: "alice", Type: "play_card", Data: data(`{"card_id":"no_such_card"}`)})
	eventually(t, "两条 error", func() bool { return len(s.msgs(t, "alice", "error")) == 2 })
	if n := len(s.msgs(t, "alice", "game_state")); n != 1 {
		t.Fatalf("坏请求不应推状态，alice 共 %d 条 game_state", n)
	}
}

// ---------- 5. 重连：只给重连者发快照，seq 不变，对手不受打扰 ----------

func TestRoomRejoinGetsSnapshot(t *testing.T) {
	r, s := startRoom(t, battleWithSlime(t))
	r.Send(Cmd{PlayerId: "alice", Type: "play_card", Data: data(`{"room_id":"r1","card_id":"slime"}`)})
	eventually(t, "出牌后状态", func() bool { return len(s.msgs(t, "bob", "game_state")) == 2 })

	r.Send(Cmd{PlayerId: "bob", Type: "leave"})
	r.Send(Cmd{PlayerId: "bob", Type: "join"})
	eventually(t, "bob 收到快照", func() bool { return len(s.msgs(t, "bob", "game_state")) == 3 })

	snap := s.msgs(t, "bob", "game_state")[2]
	if snap.Seq != 2 {
		t.Fatalf("重连快照 seq=%d want 2（状态没变，不该加）", snap.Seq)
	}
	if len(snap.Opp.Board) != 1 {
		t.Fatalf("快照里棋盘丢了: %+v", snap.Opp.Board)
	}
	if n := len(s.msgs(t, "alice", "game_state")); n != 2 {
		t.Fatalf("bob 重连不该给 alice 推状态，alice 共 %d 条", n)
	}
}

// ---------- 6. 分出胜负：推最终状态 + game_over，房间关闭 ----------

func TestRoomGameOverClosesRoom(t *testing.T) {
	st := newBattle(t, 1)
	// 布置残局：bob 牌库空、只剩 1 血。alice 结束回合 → bob 回合开始空抽 → 疲劳 1 → 死
	st.Players[1].Deck = nil
	st.Players[1].HP = 1

	r, s := startRoom(t, st)
	r.Send(Cmd{PlayerId: "alice", Type: "end_turn", Data: data(`{"room_id":"r1"}`)})

	eventually(t, "双方收到 game_over", func() bool {
		return len(s.msgs(t, "alice", "game_over")) == 1 && len(s.msgs(t, "bob", "game_over")) == 1
	})
	if w := s.msgs(t, "bob", "game_over")[0].Winner; w != "alice" {
		t.Fatalf("winner=%q want alice", w)
	}
	last := s.msgs(t, "bob", "game_state")
	if !last[len(last)-1].Over {
		t.Fatal("game_over 之前应先推一条 over=true 的最终状态")
	}
	select {
	case <-r.quit:
	case <-time.After(time.Second):
		t.Fatal("对局结束后房间没关")
	}
}
