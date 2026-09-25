package battle

import (
	"reflect"
	"testing"
)

// newTestState 起一局标准开局：alice 先手（HandStart+1 张手牌、1 费），bob 后手。
func newTestState(t *testing.T) *State {
	t.Helper()
	return mustNewState(t, cat(t), PlayerInit{"alice", cards()}, PlayerInit{"bob", cards()}, 7)
}

// setHand 直接改私有手牌。★ 测试与被测代码同包，用这个绕开洗牌的随机性，
// 否则每条 case 都得去猜 seed 发了哪几张牌。
func setHand(s *State, i int, hand ...string) {
	s.Players[i].Hand = hand
}

func TestApply(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*State)
		player   string
		action   Action
		wantCode Code // "" 表示期望成功
		check    func(*testing.T, *State)
	}{
		// ---------- 非法动作 ----------
		{
			name:     "不是本局玩家",
			player:   "carol",
			action:   Action{Type: "end_turn"},
			wantCode: CodeNotInGame,
		},
		{
			name:     "不是你的回合",
			player:   "bob",
			action:   Action{Type: "end_turn"},
			wantCode: CodeNotYourTurn,
		},
		{
			name:     "未知动作",
			player:   "alice",
			action:   Action{Type: "fly"},
			wantCode: CodeUnknownAction,
		},
		{
			name:     "牌不在手里",
			setup:    func(s *State) { setHand(s, 0, "slime") },
			player:   "alice",
			action:   Action{Type: "play_card", CardID: "wolf"},
			wantCode: CodeCardNotInHand,
		},
		{
			name:     "卡表里没有这张卡",
			setup:    func(s *State) { setHand(s, 0, "nope") },
			player:   "alice",
			action:   Action{Type: "play_card", CardID: "nope"},
			wantCode: CodeUnknownCard,
		},
		{
			name:     "法术暂未实现",
			setup:    func(s *State) { setHand(s, 0, "bolt") },
			player:   "alice",
			action:   Action{Type: "play_card", CardID: "bolt"},
			wantCode: CodeNotImplemented,
		},
		{
			name:     "费不够",
			setup:    func(s *State) { setHand(s, 0, "dragon") }, // dragon 5 费，开局只有 1 费
			player:   "alice",
			action:   Action{Type: "play_card", CardID: "dragon"},
			wantCode: CodeNotEnoughMana,
		},
		{
			// slime 正好 1 费、开局也正好 1 费，这里显式给 5 是为了让这条 case
			// 不依赖"恰好付得起"这个巧合——换成贵卡时测的仍然是 board_full。
			name: "场上已满",
			setup: func(s *State) {
				for k := 0; k < BoardLimit; k++ {
					s.Summon(0, "slime")
				}
				setHand(s, 0, "slime")
				s.Players[0].Mana = 5
			},
			player:   "alice",
			action:   Action{Type: "play_card", CardID: "slime"},
			wantCode: CodeBoardFull,
		},
		{
			// ★ 用一个本来完全合法的动作，才能证明 Over 的检查排在 indexOf 之前。
			name:     "对局已结束",
			setup:    func(s *State) { s.Over = true },
			player:   "alice",
			action:   Action{Type: "end_turn"},
			wantCode: CodeGameOver,
		},

		// ---------- 合法路径 ----------
		{
			// ★ 手牌顺序是 wolf 在前，所以出 slime 时 hi=1 而不是 0。
			// hi=0 的话，"删 hi"和"恒删第一张"这两种实现结果一样，这条 case 就区分不出来。
			name: "出牌成功且只消耗命中的那一张",
			setup: func(s *State) {
				setHand(s, 0, "wolf", "slime", "slime")
				s.Players[0].Mana = 1
			},
			player: "alice",
			action: Action{Type: "play_card", CardID: "slime"},
			check: func(t *testing.T, s *State) {
				p := s.Players[0]
				if p.Mana != 0 {
					t.Fatalf("mana=%d want 0", p.Mana)
				}
				// ★ 只移除命中的那一张，不是按 CardID 全删。这条盯的是 playCard 里的拼接删除。
				if want := []string{"wolf", "slime"}; !reflect.DeepEqual(p.Hand, want) {
					t.Fatalf("hand=%v want %v", p.Hand, want)
				}
				if len(p.Board) != 1 {
					t.Fatalf("board=%d want 1", len(p.Board))
				}
				m := p.Board[0]
				if m.CardID != "slime" || m.Atk != 1 || m.HP != 2 || m.MaxHP != 2 {
					t.Fatalf("minion=%+v", m)
				}
				if m.CanAttack {
					t.Fatal("召唤当回合不应该能攻击")
				}
			},
		},
		{
			name:   "结束回合交给对手",
			player: "alice",
			action: Action{Type: "end_turn"},
			check: func(t *testing.T, s *State) {
				if s.Turn != 1 {
					t.Fatalf("turn=%d want 1", s.Turn)
				}
				// ★ beginTurn 作用在新回合方（bob）身上，别断言成 alice。
				b := s.Players[1]
				if b.MaxMana != 1 || b.Mana != 1 {
					t.Fatalf("bob mana=%d/%d want 1/1", b.Mana, b.MaxMana)
				}
				if len(b.Hand) != HandStart+1 {
					t.Fatalf("bob hand=%d want %d", len(b.Hand), HandStart+1)
				}
				// 当前 Round 每次结束回合都 +1，实际是半回合计数。
				// 若按 review 改名 TurnNo、或改成轮回先手才 +1，这条断言要跟着改。
				if s.Round != 2 {
					t.Fatalf("round=%d want 2", s.Round)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestState(t) // ★ 每条 case 一个全新 State，不共享
			if tc.setup != nil {
				tc.setup(s)
			}
			err := s.Apply(tc.player, tc.action)

			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				if tc.check != nil {
					tc.check(t, s)
				}
				return
			}
			// ★ 不比字符串，比错误码。assertCode 同时是"所有出错路径都必须返回 *Err"的探测器。
			assertCode(t, err, tc.wantCode)
		})
	}
}

// 回合来回切 + 费用封顶。多次 Apply，放不进上面的表里。
func TestApplyEndTurnAlternatesAndCapsMana(t *testing.T) {
	// ★ 用厚牌库：8 张牌库在第 ~20 手就抽空，疲劳会把人打死，end_turn 会被 game_over 拒绝。
	//   这条测只关心换手和费用封顶，不该被疲劳规则干扰。
	s := mustNewState(t, cat(t), PlayerInit{"alice", thickCards(40)}, PlayerInit{"bob", thickCards(40)}, 7)
	for k := 0; k < 25; k++ {
		cur := s.Players[s.Turn].ID
		want := 1 - s.Turn
		if err := s.Apply(cur, Action{Type: "end_turn"}); err != nil {
			t.Fatalf("第 %d 次 end_turn: %v", k+1, err)
		}
		if s.Turn != want {
			t.Fatalf("第 %d 次 end_turn 后 turn=%d want %d", k+1, s.Turn, want)
		}
	}
	for i, p := range s.Players {
		// ★ 常量 MaxMana(=10) 和字段 p.MaxMana 同名，看花眼就会写出恒真断言。
		if p.MaxMana != MaxMana {
			t.Fatalf("players[%d].MaxMana=%d want %d", i, p.MaxMana, MaxMana)
		}
	}
}

// 出牌被拒时，不能留下任何痕迹。
// ★ 这条守的是 state.go / apply.go 里的**顺序**：Summon 的三个检查全在 append 之前，
// 扣费和弃牌又都在 Summon 成功之后。谁把 Mana -= cost 挪到 Summon 前面，只有它会红。
func TestPlayCardFailureLeavesStateUntouched(t *testing.T) {
	s := newTestState(t)
	p := s.Players[0]

	// 先把场上填满（Summon 不扣费，所以这一步不影响 Mana）
	for k := 0; k < BoardLimit; k++ {
		if _, err := s.Summon(0, "slime"); err != nil {
			t.Fatalf("填满场上时第 %d 只就失败了: %v", k+1, err)
		}
	}
	setHand(s, 0, "slime", "wolf")
	p.Mana = 5

	handBefore := append([]string(nil), p.Hand...)

	err := s.Apply("alice", Action{Type: "play_card", CardID: "slime"})
	assertCode(t, err, CodeBoardFull)

	if p.Mana != 5 {
		t.Fatalf("失败的出牌扣了费: mana=%d want 5", p.Mana)
	}
	if !reflect.DeepEqual(p.Hand, handBefore) {
		t.Fatalf("失败的出牌动了手牌: %v want %v", p.Hand, handBefore)
	}
	if len(p.Board) != BoardLimit {
		t.Fatalf("失败的出牌动了场上: board=%d want %d", len(p.Board), BoardLimit)
	}
}
