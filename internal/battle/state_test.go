package battle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DDCcheng/pet/internal/deck"
)

// assertCode 断言 err 是一个带指定错误码的 *Err。
// ★ battle 包里所有面向玩家的出错路径都必须返回 *Err；比字符串会在改文案时误报。
func assertCode(t *testing.T, err error, want Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("want code %q, got nil", want)
	}
	var e *Err
	if !errors.As(err, &e) {
		t.Fatalf("want *battle.Err, got %T: %v", err, err)
	}
	if e.Code != want {
		t.Fatalf("code=%q want %q (msg: %s)", e.Code, want, e.Msg)
	}
}

const yml = `
cards:
  - {id: slime,  name: 史莱姆, type: minion, cost: 1, atk: 1, hp: 2}
  - {id: wolf,   name: 幼狼,   type: minion, cost: 2, atk: 2, hp: 2}
  - {id: knight, name: 骑士,   type: minion, cost: 3, atk: 3, hp: 4}
  - {id: dragon, name: 幼龙,   type: minion, cost: 5, atk: 5, hp: 6}
  - {id: bolt,   name: 雷击,   type: spell,  cost: 1, damage: 3}
  - {id: heal,   name: 治疗,   type: spell,  cost: 2, heal: 3}
`

func mustNewState(t *testing.T, c *deck.Catalog, a, b PlayerInit, seed int64) *State {
	t.Helper()
	s, err := NewState("r", c, a, b, seed)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func cat(t *testing.T) *deck.Catalog {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte(yml), 0o644)
	c, err := deck.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func cards() []string {
	return []string{"slime", "slime", "wolf", "wolf", "knight", "knight", "dragon", "bolt"}
}

func TestDeterministic(t *testing.T) {
	c := cat(t)
	a := mustNewState(t, c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 42)
	b := mustNewState(t, c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 42)
	if !reflect.DeepEqual(a.Players[0].Deck, b.Players[0].Deck) {
		t.Fatal("same seed must give same deck")
	}
	if !reflect.DeepEqual(a.Players[0].Hand, b.Players[0].Hand) {
		t.Fatal("same seed must give same hand")
	}
	d := mustNewState(t, c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 43)
	if reflect.DeepEqual(a.Players[0].Deck, d.Players[0].Deck) {
		t.Fatal("different seed should differ")
	}
}

func TestDrawMovesCard(t *testing.T) {
	c := cat(t)
	s := mustNewState(t, c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 7)
	p := s.Players[0]
	if len(p.Hand) != HandStart+1 { //+1来自 beginTurn(0) 的回合开始抽牌
		t.Fatalf("hand=%d want %d", len(p.Hand), HandStart+1)
	}
	if len(p.Deck) != len(cards())-HandStart-1 {
		t.Fatalf("deck=%d want %d", len(p.Deck), len(cards())-HandStart-1)
	}
	top := p.Deck[0]
	got, ok := s.Draw(0)
	if !ok || got != top {
		t.Fatalf("Draw got %q ok=%v, want %q", got, ok, top)
	}
	if p.Hand[len(p.Hand)-1] != top {
		t.Fatal("drawn card not appended to hand")
	}
}

func TestDrawEmptyDeck(t *testing.T) {
	c := cat(t)
	s := mustNewState(t, c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 7)
	for i := 0; i < 20; i++ {
		s.Draw(0)
	}
	if _, ok := s.Draw(0); ok {
		t.Fatal("empty deck must return false")
	}
}

func TestDeckNotMutated(t *testing.T) {
	c := cat(t)
	in := cards()
	before := append([]string(nil), in...)
	mustNewState(t, c, PlayerInit{"alice", in}, PlayerInit{"bob", in}, 1)
	if !reflect.DeepEqual(in, before) {
		t.Fatal("NewState mutated caller's slice")
	}
}

func TestSummonIndependentInstances(t *testing.T) {
	c := cat(t)
	s := mustNewState(t, c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 1)
	m1, err := s.Summon(0, "wolf")
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := s.Summon(0, "wolf")
	if m1.InstID == m2.InstID {
		t.Fatal("two wolves must have different InstID")
	}
	s.Players[0].Board[0].HP -= 1
	if s.Players[0].Board[1].HP != 2 {
		t.Fatal("hurting one wolf changed the other")
	}
	if cd, _ := c.Get("wolf"); cd.HP != 2 {
		t.Fatal("catalog got mutated!")
	}
}

func TestSummonRejects(t *testing.T) {
	c := cat(t)
	s := mustNewState(t, c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 1)

	_, err := s.Summon(0, "bolt")
	assertCode(t, err, CodeNotImplemented) // 法术不能当随从召唤

	_, err = s.Summon(0, "nope")
	assertCode(t, err, CodeUnknownCard)

	for i := 0; i < BoardLimit; i++ {
		if _, err := s.Summon(0, "slime"); err != nil {
			t.Fatalf("填满场上时第 %d 只就失败了: %v", i+1, err)
		}
	}
	_, err = s.Summon(0, "slime")
	assertCode(t, err, CodeBoardFull)
}

func TestInstIDDeterministic(t *testing.T) {
	c := cat(t)
	mk := func() []string {
		s := mustNewState(t, c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 5)
		var ids []string
		for i := 0; i < 3; i++ {
			m, _ := s.Summon(0, "wolf")
			ids = append(ids, m.InstID)
		}
		return ids
	}
	if !reflect.DeepEqual(mk(), mk()) {
		t.Fatal("InstID must be deterministic")
	}
}

func TestNewStateRejects(t *testing.T) {
	c := cat(t)
	tests := []struct {
		name string
		cat  *deck.Catalog
		a, b PlayerInit
	}{
		{
			name: "卡表为 nil",
			cat:  nil,
			a:    PlayerInit{"alice", cards()},
			b:    PlayerInit{"bob", cards()},
		},
		{
			name: "先手 ID 为空",
			cat:  c,
			a:    PlayerInit{"", cards()},
			b:    PlayerInit{"bob", cards()},
		},
		{
			name: "后手 ID 为空",
			cat:  c,
			a:    PlayerInit{"alice", cards()},
			b:    PlayerInit{"", cards()},
		},
		{
			// ★ bug 本体：indexOf 线性查找只返回第一个匹配，
			// 两人同名时后手永远被判成 not_your_turn，对局死锁。
			name: "两个玩家同名",
			cat:  c,
			a:    PlayerInit{"alice", cards()},
			b:    PlayerInit{"alice", cards()},
		},
		{
			name: "卡组张数不够发起手牌",
			cat:  c,
			a:    PlayerInit{"alice", []string{"slime", "wolf"}},
			b:    PlayerInit{"bob", cards()},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewState("r", tc.cat, tc.a, tc.b, 1)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if s != nil {
				t.Fatalf("出错时必须返回 nil State，got %+v", s)
			}
		})
	}
}

func TestNewStateEmptyIDBeatsDuplicate(t *testing.T) {
	_, err := NewState("r", cat(t), PlayerInit{"", cards()}, PlayerInit{"", cards()}, 1)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if strings.Contains(err.Error(), "同名") || strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("空 ID 应优先报空值错误，got %q", err)
	}
}

// thickCards 一副够厚的卡组：给那些需要很多回合、又不想被疲劳干扰的测试用。
func thickCards(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, "slime")
	}
	return out
}

// codeOf 把 error 压成一个可比较的短字符串。
func codeOf(err error) string {
	if err == nil {
		return "ok"
	}
	var e *Err
	if errors.As(err, &e) {
		return string(e.Code)
	}
	return "other:" + err.Error()
}

// fingerprint 把一局的可见状态压成一行行文本，用来比较两次重放的结果。
// ★ 为什么不直接 reflect.DeepEqual(*State)：那会把 rng 和 cat 的内部结构一起比进去，
//
//	等于把测试耦合到 math/rand 的实现细节上；而且失败时只报 "not equal"，看不出差在哪。
//	压成文本后，失败信息本身就是 diff。
func fingerprint(s *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "turn=%d round=%d over=%v winner=%q\n", s.Turn, s.Round, s.Over, s.Winner)
	for i, p := range s.Players {
		fmt.Fprintf(&b, "p%d=%s hp=%d mana=%d/%d fatigue=%d hand=%v deck=%d\n",
			i, p.ID, p.HP, p.Mana, p.MaxMana, p.Fatigue, p.Hand, len(p.Deck))
		for _, m := range p.Board {
			fmt.Fprintf(&b, "  %s %s %d/%d can=%v\n", m.InstID, m.CardID, m.Atk, m.HP, m.CanAttack)
		}
	}
	return b.String()
}

func TestReplayDeterministic(t *testing.T) {
	acts := []Action{
		{Type: "play_card", CardID: "slime"},           // 1 费，成功 → m1
		{Type: "play_card", CardID: "slime"},           // 费不够，拒
		{Type: "attack", InstID: "m1", Target: "face"}, // 召唤当回合，拒
		{Type: "end_turn"},

		{Type: "play_card", CardID: "slime"}, // bob → m2
		{Type: "end_turn"},

		{Type: "attack", InstID: "m1", Target: "face"}, // 打脸
		{Type: "play_card", CardID: "slime"},           // → m3
		{Type: "play_card", CardID: "slime"},           // → m4
		{Type: "end_turn"},

		{Type: "attack", InstID: "m2", Target: "m1"}, // 互殴，双方各扣 1
		{Type: "end_turn"},

		{Type: "attack", InstID: "m3", Target: "m2"}, // 打死 m2
		{Type: "attack", InstID: "m1", Target: "face"},
		{Type: "attack", InstID: "m4", Target: "face"},
		{Type: "end_turn"},

		{Type: "play_card", CardID: "slime"}, // bob → m5
		{Type: "end_turn"},

		{Type: "attack", InstID: "m1", Target: "m5"}, // m1 换掉自己
		{Type: "end_turn"},
	}

	run := func() (string, []string) {
		s := mustNewState(t, cat(t),
			PlayerInit{"alice", thickCards(30)},
			PlayerInit{"bob", thickCards(30)}, 99)
		codes := make([]string, 0, len(acts))
		for _, a := range acts {
			// ★ 执行者固定取"当前回合方"，所以整个序列和谁先手无关，只和动作顺序有关
			codes = append(codes, codeOf(s.Apply(s.Players[s.Turn].ID, a)))
		}
		return fingerprint(s), codes
	}

	f1, c1 := run()
	f2, c2 := run()

	if !reflect.DeepEqual(c1, c2) {
		t.Fatalf("两次重放的错误序列不一致:\n%v\n%v", c1, c2)
	}
	if f1 != f2 {
		t.Fatalf("两次重放的最终状态不一致:\n--- 第一次 ---\n%s\n--- 第二次 ---\n%s", f1, f2)
	}
	t.Logf("重放结果:\n%s\n错误序列: %v", f1, c1)
}
