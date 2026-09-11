package battle

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/DDCcheng/pet/internal/deck"
)

const yml = `
cards:
  - {id: slime,  name: 史莱姆, type: minion, cost: 1, atk: 1, hp: 2}
  - {id: wolf,   name: 幼狼,   type: minion, cost: 2, atk: 2, hp: 2}
  - {id: knight, name: 骑士,   type: minion, cost: 3, atk: 3, hp: 4}
  - {id: dragon, name: 幼龙,   type: minion, cost: 5, atk: 5, hp: 6}
  - {id: bolt,   name: 雷击,   type: spell,  cost: 1, damage: 3}
  - {id: heal,   name: 治疗,   type: spell,  cost: 2, heal: 3}
`

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
	a := NewState("r", c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 42)
	b := NewState("r", c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 42)
	if !reflect.DeepEqual(a.Players[0].Deck, b.Players[0].Deck) {
		t.Fatal("same seed must give same deck")
	}
	if !reflect.DeepEqual(a.Players[0].Hand, b.Players[0].Hand) {
		t.Fatal("same seed must give same hand")
	}
	d := NewState("r", c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 43)
	if reflect.DeepEqual(a.Players[0].Deck, d.Players[0].Deck) {
		t.Fatal("different seed should differ")
	}
}

func TestDrawMovesCard(t *testing.T) {
	c := cat(t)
	s := NewState("r", c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 7)
	p := s.Players[0]
	if len(p.Hand) != HandStart {
		t.Fatalf("hand=%d want %d", len(p.Hand), HandStart)
	}
	if len(p.Deck) != len(cards())-HandStart {
		t.Fatalf("deck=%d want %d", len(p.Deck), len(cards())-HandStart)
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
	s := NewState("r", c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 7)
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
	NewState("r", c, PlayerInit{"alice", in}, PlayerInit{"bob", in}, 1)
	if !reflect.DeepEqual(in, before) {
		t.Fatal("NewState mutated caller's slice")
	}
}

func TestSummonIndependentInstances(t *testing.T) {
	c := cat(t)
	s := NewState("r", c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 1)
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
	s := NewState("r", c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 1)
	if _, err := s.Summon(0, "bolt"); err == nil {
		t.Fatal("spell must not be summonable")
	}
	if _, err := s.Summon(0, "nope"); err == nil {
		t.Fatal("unknown card must error")
	}
	for i := 0; i < BoardLimit; i++ {
		s.Summon(0, "slime")
	}
	if _, err := s.Summon(0, "slime"); err == nil {
		t.Fatal("board full must error")
	}
}

func TestInstIDDeterministic(t *testing.T) {
	c := cat(t)
	mk := func() []string {
		s := NewState("r", c, PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 5)
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
