package battle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DDCcheng/pet/internal/deck"
)

// fuzzCat 和 cat 一样，只是 *testing.F 也能用
func fuzzCat(f *testing.F) *deck.Catalog {
	p := filepath.Join(f.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o644); err != nil {
		f.Fatal(err)
	}
	c, err := deck.Load(p)
	if err != nil {
		f.Fatal(err)
	}
	return c
}

// 字节流 → 卡组 + 动作序列。同一份字节永远生成同一局，fuzz 找到的反例可以原样复现。
type byteReader struct {
	b []byte
	i int
}

func (r *byteReader) next() int {
	if r.i >= len(r.b) {
		return 0
	}
	v := int(r.b[r.i])
	r.i++
	return v
}

func (r *byteReader) done() bool { return r.i >= len(r.b) }

// fuzzDeck 生成 4–20 张的卡组（NewState 至少要 4 张），卡从卡表里挑，法术也会混进来。
func fuzzDeck(r *byteReader, ids []string) []string {
	n := 4 + r.next()%17
	d := make([]string, n)
	for k := range d {
		d[k] = ids[r.next()%len(ids)]
	}
	return d
}

// fuzzAction 按当前局面生成一个动作，合法和非法的都有：
// 出手里的牌（可能费不够/场满/是法术）、出随便一张卡、攻击脸/对方随从/自己随从/不存在的目标、
// 结束回合、未知动作，以及"不是你的回合"的玩家来发动作。
func fuzzAction(r *byteReader, s *State, ids []string) (string, Action) {
	me, opp := s.Players[s.Turn], s.Players[1-s.Turn]
	pid := me.ID
	pickMinion := func(p *Player) string {
		if len(p.Board) == 0 {
			return fmt.Sprintf("m%d", r.next()%8)
		}
		return p.Board[r.next()%len(p.Board)].InstID
	}
	switch op := r.next() % 10; op {
	case 0, 1, 2:
		if len(me.Hand) == 0 {
			return pid, Action{Type: "play_card", CardID: ids[r.next()%len(ids)]}
		}
		return pid, Action{Type: "play_card", CardID: me.Hand[r.next()%len(me.Hand)]}
	case 3:
		return pid, Action{Type: "play_card", CardID: ids[r.next()%len(ids)]}
	case 4, 5, 6:
		a := Action{Type: "attack", InstID: pickMinion(me)}
		switch r.next() % 4 {
		case 0, 1:
			a.Target = "face"
		case 2:
			a.Target = pickMinion(opp)
		default:
			a.Target = pickMinion(me) // 打自己人，应被拒
		}
		return pid, a
	case 7:
		return pid, Action{Type: "end_turn"}
	case 8:
		return opp.ID, Action{Type: "end_turn"} // 抢对方回合
	default:
		return pid, Action{Type: "surrender"} // 未知动作
	}
}

// checkInvariants 是规则层任何时候都必须成立的性质。played[i] = 玩家 i 被接受的出牌次数。
func checkInvariants(s *State, init [2]int, played [2]int) error {
	if s.Turn != 0 && s.Turn != 1 {
		return fmt.Errorf("turn=%d", s.Turn)
	}
	seen := map[string]bool{}
	for i, p := range s.Players {
		if p.HP > StartHP {
			return fmt.Errorf("p%d hp %d > %d", i, p.HP, StartHP)
		}
		if p.Mana < 0 || p.Mana > p.MaxMana || p.MaxMana > MaxMana {
			return fmt.Errorf("p%d mana %d/%d", i, p.Mana, p.MaxMana)
		}
		if p.Fatigue < 0 {
			return fmt.Errorf("p%d fatigue %d", i, p.Fatigue)
		}
		if p.Fatigue > 0 && len(p.Deck) > 0 {
			return fmt.Errorf("p%d fatigue %d with %d cards left", i, p.Fatigue, len(p.Deck))
		}
		if len(p.Board) > BoardLimit {
			return fmt.Errorf("p%d board %d > %d", i, len(p.Board), BoardLimit)
		}
		for _, m := range p.Board {
			if m.HP <= 0 || m.HP > m.MaxHP {
				return fmt.Errorf("p%d minion %s hp %d/%d", i, m.InstID, m.HP, m.MaxHP)
			}
			if seen[m.InstID] {
				return fmt.Errorf("duplicate inst id %s", m.InstID)
			}
			seen[m.InstID] = true
		}
		// 牌守恒：手牌 + 牌库 + 打出去的 = 开局卡组
		if got := len(p.Hand) + len(p.Deck) + played[i]; got != init[i] {
			return fmt.Errorf("p%d cards hand %d + deck %d + played %d != %d", i, len(p.Hand), len(p.Deck), played[i], init[i])
		}
	}
	if played[0]+played[1] != s.nextInstID {
		return fmt.Errorf("played %d minions but nextInstID=%d", played[0]+played[1], s.nextInstID)
	}
	dead0, dead1 := s.Players[0].HP <= 0, s.Players[1].HP <= 0
	if s.Over != (dead0 || dead1) {
		return fmt.Errorf("over=%v but hp %d/%d", s.Over, s.Players[0].HP, s.Players[1].HP)
	}
	switch {
	case dead0 && dead1 && s.Winner != "":
		return fmt.Errorf("both dead but winner %q", s.Winner)
	case dead0 && !dead1 && s.Winner != s.Players[1].ID, dead1 && !dead0 && s.Winner != s.Players[0].ID:
		return fmt.Errorf("winner %q, hp %d/%d", s.Winner, s.Players[0].HP, s.Players[1].HP)
	}
	return nil
}

// FuzzReplayDeterministic：随机卡组 + 随机动作（合法非法混着）打一局，检查
//  1. 每一步之后规则不变量都成立；
//  2. 被拒的动作不改任何状态（校验和不变）；
//  3. 对局结束后任何动作都被拒；
//  4. 用 seed + 卡组 + 被接受的动作重放，每一步的校验和、终局完整指纹都和线上一致；再放一遍还一致。
func FuzzReplayDeterministic(f *testing.F) {
	f.Add(int64(7), []byte{10, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 0, 1, 2, 3, 4, 5, 6, 7})
	f.Add(int64(42), []byte("play attack end_turn play play attack attack end_turn"))
	f.Add(int64(-1), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7})
	long := make([]byte, 2000)
	for k := range long {
		long[k] = byte(k*37 + k/7)
	}
	f.Add(int64(2026), long)
	// 长局：两边 20 张牌，只结束回合，能打到法力封顶（第 10 回合以后）和疲劳
	idle := []byte{16}
	for k := 0; k < 20; k++ {
		idle = append(idle, byte(k))
	}
	idle = append(idle, 16)
	for k := 0; k < 20; k++ {
		idle = append(idle, byte(k))
	}
	for k := 0; k < 60; k++ {
		idle = append(idle, 7)
	}
	f.Add(int64(3), idle)

	c := fuzzCat(f)
	ids := c.IDs()
	f.Fuzz(func(t *testing.T, seed int64, script []byte) {
		if len(script) > 8192 {
			script = script[:8192]
		}
		r := &byteReader{b: script}
		a := PlayerInit{"alice", fuzzDeck(r, ids)}
		b := PlayerInit{"bob", fuzzDeck(r, ids)}
		live, err := NewState("fz", c, a, b, seed)
		if err != nil {
			t.Fatal(err)
		}
		init := [2]int{len(a.Cards), len(b.Cards)}
		var played [2]int
		var steps []Step
		sums := []string{live.Checksum()}
		if err := checkInvariants(live, init, played); err != nil {
			t.Fatalf("after NewState: %v", err)
		}

		for !r.done() {
			pid, act := fuzzAction(r, live, ids)
			before := live.Checksum()
			if err := live.Apply(pid, act); err != nil {
				var be *Err
				if !errors.As(err, &be) {
					t.Fatalf("step %d %s %+v: non-rule error %v", len(steps), pid, act, err)
				}
				if live.Checksum() != before {
					t.Fatalf("rejected %s %+v (%v) changed the state", pid, act, err)
				}
				continue
			}
			if act.Type == "play_card" {
				played[live.indexOf(pid)]++
			}
			steps = append(steps, Step{Player: pid, Action: act})
			sums = append(sums, live.Checksum())
			if err := checkInvariants(live, init, played); err != nil {
				t.Fatalf("after step %d %s %+v: %v", len(steps)-1, pid, act, err)
			}
		}
		if live.Over {
			for _, p := range live.Players {
				if err := live.Apply(p.ID, Action{Type: "end_turn"}); codeOf(err) != string(CodeGameOver) {
					t.Fatalf("action after game over: %v", err)
				}
			}
		}

		for run := 0; run < 2; run++ {
			k := 0
			got, err := Replay(c, "fz", a, b, seed, steps, func(i int, s *State) {
				if s.Checksum() != sums[k] {
					t.Fatalf("run %d: checksum differs at step %d", run, i)
				}
				k++
			})
			if err != nil {
				t.Fatalf("run %d: replay rejected an accepted action: %v", run, err)
			}
			if k != len(sums) {
				t.Fatalf("run %d: onStep called %d times, want %d", run, k, len(sums))
			}
			if fullFingerprint(got) != fullFingerprint(live) {
				t.Fatalf("run %d: final state differs:\n--- live ---\n%s\n--- replay ---\n%s", run, fullFingerprint(live), fullFingerprint(got))
			}
		}
	})
}
