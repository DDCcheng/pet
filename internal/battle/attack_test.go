package battle

import (
	"testing"

	"github.com/DDCcheng/pet/internal/deck"
)

// ---------- helpers ----------

// setBoard 直接摆场：绕开费用和出牌流程，但仍然走 Summon，
// 这样随从属性永远和卡表一致（改卡表数值，测试跟着变，不用改断言）。
// ready=true 表示这些随从"上回合就在场上"，本回合可以攻击。
// ★ 每条 case 都从 newTestState 的空场开始，InstID 由 nextInstID 递增，
//   所以先给谁摆场，谁的第一只就是 m1。
func setBoard(t *testing.T, s *State, i int, ready bool, cardIDs ...string) []string {
	t.Helper()
	ids := make([]string, 0, len(cardIDs))
	for _, id := range cardIDs {
		m, err := s.Summon(i, id)
		if err != nil {
			t.Fatalf("setBoard: Summon(%d, %q) 失败: %v", i, id, err)
		}
		ids = append(ids, m.InstID)
	}
	if ready {
		p := s.Players[i]
		for k := range p.Board {
			p.Board[k].CanAttack = true // ★ 必须用下标，range 的值是拷贝
		}
	}
	return ids
}

func boardIDs(p *Player) []string {
	out := make([]string, 0, len(p.Board))
	for k := range p.Board {
		out = append(out, p.Board[k].InstID)
	}
	return out
}

func wantBoard(t *testing.T, p *Player, want ...string) {
	t.Helper()
	got := boardIDs(p)
	if len(got) != len(want) {
		t.Fatalf("%s 场上 %v，want %v", p.ID, got, want)
	}
	for k := range want {
		if got[k] != want[k] {
			t.Fatalf("%s 场上 %v，want %v（顺序也要一致）", p.ID, got, want)
		}
	}
}

func wantHP(t *testing.T, p *Player, want int) {
	t.Helper()
	if p.HP != want {
		t.Fatalf("%s HP=%d want %d", p.ID, p.HP, want)
	}
}

// ---------- 表驱动：attack 的所有分支 ----------

func TestAttack(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*testing.T, *State)
		action   Action
		wantCode Code // "" 表示期望成功
		check    func(*testing.T, *State)
	}{
		// ---------- 校验区：不该改任何状态 ----------
		{
			name:     "攻击者不在自己场上",
			action:   Action{Type: "attack", InstID: "m1", Target: "face"},
			wantCode: CodeMinionNotFound,
		},
		{
			name:     "召唤当回合不能攻击",
			setup:    func(t *testing.T, s *State) { setBoard(t, s, 0, false, "wolf") },
			action:   Action{Type: "attack", InstID: "m1", Target: "face"},
			wantCode: CodeCantAttack,
		},
		{
			name: "同一只本回合攻击两次",
			setup: func(t *testing.T, s *State) {
				setBoard(t, s, 0, true, "wolf")
				if err := s.Apply("alice", Action{Type: "attack", InstID: "m1", Target: "face"}); err != nil {
					t.Fatalf("第一次攻击就失败了: %v", err)
				}
			},
			action:   Action{Type: "attack", InstID: "m1", Target: "face"},
			wantCode: CodeCantAttack,
		},
		{
			// 卡表里没有 0 攻的卡，手动造一只。
			name: "0 攻随从不能攻击",
			setup: func(t *testing.T, s *State) {
				setBoard(t, s, 0, true, "wolf")
				s.Players[0].Board[0].Atk = 0
			},
			action:   Action{Type: "attack", InstID: "m1", Target: "face"},
			wantCode: CodeCantAttack,
		},
		{
			name: "不能攻击自己的随从",
			setup: func(t *testing.T, s *State) {
				setBoard(t, s, 0, true, "wolf", "slime") // m1, m2 都是 alice 的
			},
			action:   Action{Type: "attack", InstID: "m1", Target: "m2"},
			wantCode: CodeInvalidTarget,
		},
		{
			name:     "目标不存在",
			setup:    func(t *testing.T, s *State) { setBoard(t, s, 0, true, "wolf") },
			action:   Action{Type: "attack", InstID: "m1", Target: "m99"},
			wantCode: CodeTargetNotFound,
		},

		// ---------- 结算：打脸 ----------
		{
			name:   "打脸扣对手英雄血",
			setup:  func(t *testing.T, s *State) { setBoard(t, s, 0, true, "wolf") }, // wolf 2/2
			action: Action{Type: "attack", InstID: "m1", Target: "face"},
			check: func(t *testing.T, s *State) {
				wantHP(t, s.Players[1], StartHP-2)
				wantHP(t, s.Players[0], StartHP) // ★ 打脸没有反击
				m := s.Players[0].Board[0]
				if m.HP != 2 {
					t.Fatalf("打脸不该掉血: attacker HP=%d want 2", m.HP)
				}
				if m.CanAttack {
					t.Fatal("攻击后 CanAttack 应该置 false")
				}
			},
		},

		// ---------- 结算：随从互殴 ----------
		{
			name: "防守方死亡下场，攻击方受反击伤害",
			setup: func(t *testing.T, s *State) {
				setBoard(t, s, 0, true, "knight") // m1: 3/4
				setBoard(t, s, 1, false, "slime") // m2: 1/2
			},
			action: Action{Type: "attack", InstID: "m1", Target: "m2"},
			check: func(t *testing.T, s *State) {
				wantBoard(t, s.Players[1]) // slime 2 血挨 3 点，死
				wantBoard(t, s.Players[0], "m1")
				if hp := s.Players[0].Board[0].HP; hp != 3 { // 4 - 1 反击
					t.Fatalf("knight HP=%d want 3", hp)
				}
				wantHP(t, s.Players[0], StartHP)
				wantHP(t, s.Players[1], StartHP) // ★ 打随从不该扣英雄血
			},
		},
		{
			name: "攻击方死亡，防守方存活",
			setup: func(t *testing.T, s *State) {
				setBoard(t, s, 0, true, "slime")  // m1: 1/2
				setBoard(t, s, 1, false, "knight") // m2: 3/4
			},
			action: Action{Type: "attack", InstID: "m1", Target: "m2"},
			check: func(t *testing.T, s *State) {
				wantBoard(t, s.Players[0])
				wantBoard(t, s.Players[1], "m2")
				if hp := s.Players[1].Board[0].HP; hp != 3 { // 4 - 1
					t.Fatalf("knight HP=%d want 3", hp)
				}
			},
		},
		{
			name: "同归于尽：两只一起下场",
			setup: func(t *testing.T, s *State) {
				setBoard(t, s, 0, true, "wolf")  // m1: 2/2
				setBoard(t, s, 1, false, "wolf") // m2: 2/2
			},
			action: Action{Type: "attack", InstID: "m1", Target: "m2"},
			check: func(t *testing.T, s *State) {
				wantBoard(t, s.Players[0])
				wantBoard(t, s.Players[1])
				if s.Over {
					t.Fatal("随从死光不等于对局结束")
				}
			},
		},

		// ---------- 胜负 ----------
		{
			name: "致命一击结束对局",
			setup: func(t *testing.T, s *State) {
				setBoard(t, s, 0, true, "wolf")
				s.Players[1].HP = 1
			},
			action: Action{Type: "attack", InstID: "m1", Target: "face"},
			check: func(t *testing.T, s *State) {
				if !s.Over {
					t.Fatal("对手 HP<=0 后 Over 应该是 true")
				}
				if s.Winner != "alice" {
					t.Fatalf("winner=%q want alice", s.Winner)
				}
				// ★ 一条 case 同时验"结束"和"结束后锁死"
				assertCode(t, s.Apply("alice", Action{Type: "end_turn"}), CodeGameOver)
				assertCode(t, s.Apply("bob", Action{Type: "end_turn"}), CodeGameOver)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestState(t) // ★ 每条 case 一个全新 State
			if tc.setup != nil {
				tc.setup(t, s)
			}
			err := s.Apply("alice", tc.action)

			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				if tc.check != nil {
					tc.check(t, s)
				}
				return
			}
			assertCode(t, err, tc.wantCode)
		})
	}
}

// 非法攻击不能留下任何痕迹。
// ★ 这条守的是 attack 里"先全校验、再全结算"的分界线：
//   谁把扣血或 CanAttack=false 挪到校验前面，只有它会红。
func TestAttackFailureLeavesStateUntouched(t *testing.T) {
	s := newTestState(t)
	setBoard(t, s, 0, true, "wolf")
	setBoard(t, s, 1, false, "knight")

	assertCode(t, s.Apply("alice", Action{Type: "attack", InstID: "m1", Target: "m99"}), CodeTargetNotFound)

	if !s.Players[0].Board[0].CanAttack {
		t.Fatal("失败的攻击消耗了攻击次数")
	}
	if s.Players[0].Board[0].HP != 2 || s.Players[1].Board[0].HP != 4 {
		t.Fatal("失败的攻击改了随从血量")
	}
	wantHP(t, s.Players[0], StartHP)
	wantHP(t, s.Players[1], StartHP)
}

// removeDead 专项：死活交错时，活着的顺序和身份都不能乱。
// ★ 双指针过滤最容易错在"尾巴没截断"和"顺序被打乱"，这条盯的就是这两点。
func TestRemoveDeadKeepsOrder(t *testing.T) {
	s := newTestState(t)
	setBoard(t, s, 0, true, "slime", "slime", "slime", "slime", "slime") // m1..m5
	setBoard(t, s, 1, true, "wolf")                                      // m6

	s.Players[0].Board[1].HP = 0  // m2 死
	s.Players[0].Board[3].HP = -3 // m4 死（负数也算死）

	s.removeDead()

	wantBoard(t, s.Players[0], "m1", "m3", "m5")
	wantBoard(t, s.Players[1], "m6") // ★ 另一边不能被误伤
	for k := range s.Players[0].Board {
		if s.Players[0].Board[k].HP <= 0 {
			t.Fatalf("清理后还留着死的: %+v", s.Players[0].Board[k])
		}
	}
}

// 双方同时 HP<=0 判平局，且胜负只定一次。
func TestCheckOverDraw(t *testing.T) {
	s := newTestState(t)
	s.Players[0].HP = 0
	s.Players[1].HP = -1
	s.checkOver()

	if !s.Over {
		t.Fatal("双死也要结束对局")
	}
	if s.Winner != "" {
		t.Fatalf("winner=%q want 空字符串（平局）", s.Winner)
	}

	// 再调一次不能把平局改写成某一方赢
	s.checkOver()
	if s.Winner != "" {
		t.Fatalf("重复 checkOver 改写了结果: winner=%q", s.Winner)
	}
}

// ---------- 冒烟：能打完一局 ----------

// botTurn 一个贪心 bot 的完整回合：能出的最贵随从先出 → 所有能攻击的随从打脸 → 结束回合。
func botTurn(t *testing.T, s *State) {
	t.Helper()
	me := s.Players[s.Turn]

	for len(me.Board) < BoardLimit {
		best, bestCost := "", -1
		for _, cid := range me.Hand {
			c, ok := s.cat.Get(cid)
			if !ok || c.Type != deck.TypeMinion || c.Cost > me.Mana {
				continue // 法术还没实现，跳过
			}
			if c.Cost > bestCost {
				best, bestCost = cid, c.Cost
			}
		}
		if best == "" {
			break
		}
		if err := s.Apply(me.ID, Action{Type: "play_card", CardID: best}); err != nil {
			t.Fatalf("bot 出牌 %q 失败: %v", best, err)
		}
	}

	for {
		inst := ""
		for k := range me.Board {
			if me.Board[k].CanAttack && me.Board[k].Atk > 0 {
				inst = me.Board[k].InstID
				break
			}
		}
		if inst == "" {
			break
		}
		if err := s.Apply(me.ID, Action{Type: "attack", InstID: inst, Target: "face"}); err != nil {
			t.Fatalf("bot 攻击 %q 失败: %v", inst, err)
		}
		if s.Over {
			return // 打死了就别再 end_turn
		}
	}

	if err := s.Apply(me.ID, Action{Type: "end_turn"}); err != nil {
		t.Fatalf("bot end_turn 失败: %v", err)
	}
}

// Day 10 的过关线：两个 bot 互打，必须能打完并打印赢家。
func TestFullGame(t *testing.T) {
	s := newTestState(t)

	const maxTurns = 500 // ★ 必须有上限：没有疲劳规则时牌库空了会无限 end_turn，
	//                      有上限才会"失败"，没上限就是测试挂死。
	turns := 0
	for ; turns < maxTurns && !s.Over; turns++ {
		botTurn(t, s)
	}

	if !s.Over {
		t.Fatalf("%d 个回合还没打完——检查回合推进，或者补上疲劳规则", maxTurns)
	}
	if s.Winner == "" {
		t.Fatal("双方互相打脸不该出现平局")
	}
	if s.Winner != "alice" && s.Winner != "bob" {
		t.Fatalf("winner=%q 不是本局玩家", s.Winner)
	}
	if hp := s.Players[s.indexOf(s.Winner)].HP; hp <= 0 {
		t.Fatalf("赢家 HP=%d，胜负判反了", hp)
	}
	assertCode(t, s.Apply(s.Winner, Action{Type: "end_turn"}), CodeGameOver)

	t.Logf("打完一局：winner=%s，共 %d 个回合，双方 HP=%d/%d",
		s.Winner, turns, s.Players[0].HP, s.Players[1].HP)
}
