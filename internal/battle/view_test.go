package battle

import (
	"encoding/json"
	"reflect"
	"testing"
)

// viewJSON 把视图序列化再解析成 map，模拟"客户端真正收到的东西"。
// ★ 隐私要按 JSON 验，而不是按 Go 结构体：字段 tag 写错（比如漏了 omitempty）只有这样才测得出来。
func viewJSON(t *testing.T, v View) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal view: %v", err)
	}
	return m
}

// ---------- 1. 视角过滤：对手手牌只给数量 ----------

func TestViewHidesOpponentHand(t *testing.T) {
	s := newTestState(t)
	setHand(s, 0, "wolf", "knight", "bolt") // alice 3 张
	setHand(s, 1, "slime", "dragon")        // bob 2 张

	// bob 视角
	v := s.ViewFor(1)

	// 自己：看得到具体手牌
	if !reflect.DeepEqual(v.Me.Hand, []string{"slime", "dragon"}) {
		t.Fatalf("me.hand=%v want [slime dragon]", v.Me.Hand)
	}
	if v.Me.HandCount != 2 {
		t.Fatalf("me.hand_count=%d want 2", v.Me.HandCount)
	}
	// 对手：只有数量
	if v.Opp.Hand != nil {
		t.Fatalf("opp.hand 应该为 nil，实际 %v —— 泄露了对手手牌", v.Opp.Hand)
	}
	if v.Opp.HandCount != 3 {
		t.Fatalf("opp.hand_count=%d want 3", v.Opp.HandCount)
	}

	// JSON 层面：opp 里不能出现 hand 这个 key
	m := viewJSON(t, v)
	opp := m["opp"].(map[string]any)
	if _, ok := opp["hand"]; ok {
		t.Fatalf("JSON 里 opp 带了 hand 字段: %v", opp["hand"])
	}
	if opp["hand_count"].(float64) != 3 { // JSON 数字解析出来是 float64
		t.Fatalf("JSON opp.hand_count=%v want 3", opp["hand_count"])
	}
	// 牌库：双方都只给数量，任何视角都不出现 deck 内容
	if _, ok := opp["deck"]; ok {
		t.Fatal("JSON 里出现了 deck 字段")
	}
	if int(opp["deck_count"].(float64)) != len(s.Players[0].Deck) {
		t.Fatalf("opp.deck_count=%v want %d", opp["deck_count"], len(s.Players[0].Deck))
	}
}

// ---------- 2. 视图是拷贝：改视图不能影响真实状态 ----------

func TestViewIsCopy(t *testing.T) {
	s := newTestState(t)
	setHand(s, 0, "wolf", "knight")
	setBoard(t, s, 0, false, "wolf")
	origHP := s.Players[0].Board[0].HP

	v := s.ViewFor(0)
	if len(v.Me.Board) != 1 || v.Me.Board[0].InstID == "" || v.Me.Board[0].HP != origHP {
		// ★ 只 make 不 copy 时会得到一只零值随从，这里就会挂
		t.Fatalf("view 里的随从数据不对: %+v", v.Me.Board)
	}

	// 乱改视图
	v.Me.Board[0].HP = -99
	v.Me.Hand[0] = "HACKED"

	if got := s.Players[0].Board[0].HP; got != origHP {
		t.Fatalf("改视图的 Board 影响了 State：HP=%d want %d（Board 没 copy）", got, origHP)
	}
	if got := s.Players[0].Hand[0]; got != "wolf" {
		t.Fatalf("改视图的 Hand 影响了 State：Hand[0]=%q（Hand 没 copy）", got)
	}
}

// ---------- 3. 两个视角互为镜像 ----------

func TestViewMirror(t *testing.T) {
	s := newTestState(t)
	setHand(s, 0, "wolf", "knight")
	setHand(s, 1, "slime")
	setBoard(t, s, 0, true, "wolf")
	setBoard(t, s, 1, false, "slime", "knight")

	v0, v1 := s.ViewFor(0), s.ViewFor(1)

	// 回合：正好一个人 YourTurn
	if v0.YourTurn == v1.YourTurn {
		t.Fatalf("YourTurn 两边都是 %v，应该一真一假", v0.YourTurn)
	}
	if !v0.YourTurn { // 开局 alice 先手
		t.Fatal("开局应该是 alice(0) 的回合")
	}

	// 去掉手牌后，A 眼里的自己 == B 眼里的对手
	strip := func(p PlayerView) PlayerView { p.Hand = nil; return p }
	if !reflect.DeepEqual(strip(v0.Me), strip(v1.Opp)) {
		t.Fatalf("镜像不一致:\n v0.Me =%+v\n v1.Opp=%+v", v0.Me, v1.Opp)
	}
	if !reflect.DeepEqual(strip(v1.Me), strip(v0.Opp)) {
		t.Fatalf("镜像不一致:\n v1.Me =%+v\n v0.Opp=%+v", v1.Me, v0.Opp)
	}

	// 公共字段两边一样
	if v0.Round != v1.Round || v0.Over != v1.Over || v0.Winner != v1.Winner {
		t.Fatalf("公共字段不一致: v0=%+v v1=%+v", v0, v1)
	}
}

// ---------- 4. 空场序列化成 [] 而不是 null ----------

func TestViewEmptyBoardIsArray(t *testing.T) {
	s := newTestState(t) // 开局双方都空场
	m := viewJSON(t, s.ViewFor(0))
	for _, side := range []string{"me", "opp"} {
		pv := m[side].(map[string]any)
		if _, ok := pv["board"].([]any); !ok {
			t.Fatalf("%s.board=%v，want []（前端不用判 null）", side, pv["board"])
		}
	}
}

// ---------- 5. 结束状态透传 ----------

func TestViewOverAndWinner(t *testing.T) {
	s := newTestState(t)
	s.Players[1].HP = 0
	s.checkOver()

	for i := 0; i < 2; i++ {
		v := s.ViewFor(i)
		if !v.Over || v.Winner != "alice" {
			t.Fatalf("ViewFor(%d): over=%v winner=%q want true/alice", i, v.Over, v.Winner)
		}
	}
}

// Seq 由 room 填，battle 层永远是 0 —— 锁住这个分层约定。
func TestViewSeqLeftToRoom(t *testing.T) {
	s := newTestState(t)
	if v := s.ViewFor(0); v.Seq != 0 {
		t.Fatalf("battle 不该填 Seq，got %d", v.Seq)
	}
}
