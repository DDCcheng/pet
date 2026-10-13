package main

import (
	"math/rand"
	"testing"

	"github.com/DDCcheng/pet/internal/battle"
	"github.com/DDCcheng/pet/internal/deck"
)

func loadCat(t *testing.T) *deck.Catalog {
	t.Helper()
	c, err := deck.Load("../../configs/cards.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func board(n int, canAttack bool) []battle.Minion {
	out := make([]battle.Minion, n)
	for i := range out {
		out[i] = battle.Minion{InstID: string(rune('a' + i)), CardID: "slime", Atk: 1, HP: 2, CanAttack: canAttack}
	}
	return out
}

func TestChooseAction(t *testing.T) {
	cat := loadCat(t)
	cases := []struct {
		name  string
		v     battle.View
		allow map[string]bool // 允许出现的动作类型
		check func(t *testing.T, a battle.Action)
	}{
		{
			name:  "没费没随从：只能 end_turn",
			v:     battle.View{Me: battle.PlayerView{Mana: 0, Hand: []string{"slime", "dragon"}}},
			allow: map[string]bool{"end_turn": true},
		},
		{
			name:  "1 费只出得起 slime",
			v:     battle.View{Me: battle.PlayerView{Mana: 1, Hand: []string{"dragon", "slime", "knight"}}},
			allow: map[string]bool{"play_card": true},
			check: func(t *testing.T, a battle.Action) {
				if a.CardID != "slime" {
					t.Fatalf("出了 %s，1 费只够 slime", a.CardID)
				}
			},
		},
		{
			name:  "法术不出（服务端未实现）",
			v:     battle.View{Me: battle.PlayerView{Mana: 10, Hand: []string{"bolt", "heal"}}},
			allow: map[string]bool{"end_turn": true},
		},
		{
			name:  "场上满了不召唤",
			v:     battle.View{Me: battle.PlayerView{Mana: 10, Hand: []string{"slime"}, Board: board(battle.BoardLimit, false)}},
			allow: map[string]bool{"end_turn": true},
		},
		{
			name:  "刚召唤的随从不攻击",
			v:     battle.View{Me: battle.PlayerView{Board: board(2, false)}, Opp: battle.PlayerView{Board: board(1, true)}},
			allow: map[string]bool{"end_turn": true},
		},
		{
			name:  "能攻击：目标是脸或对方随从，不打自己人",
			v:     battle.View{Me: battle.PlayerView{Board: board(1, true)}, Opp: battle.PlayerView{Board: []battle.Minion{{InstID: "x", Atk: 1, HP: 1}}}},
			allow: map[string]bool{"attack": true},
			check: func(t *testing.T, a battle.Action) {
				if a.InstID != "a" || (a.Target != "face" && a.Target != "x") {
					t.Fatalf("攻击不合法: %+v", a)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(1))
			for i := 0; i < 50; i++ { // 随机策略多抽几次
				a := chooseAction(c.v, cat, rng)
				if !c.allow[a.Type] {
					t.Fatalf("选了 %+v，允许的是 %v", a, c.allow)
				}
				if c.check != nil {
					c.check(t, a)
				}
			}
		})
	}
}

// 用真的 battle.State 自己跟自己打：bot 选的每个动作都必须被 Apply 接受，而且对局能结束
func TestChooseActionAlwaysLegalFullGame(t *testing.T) {
	cat := loadCat(t)
	d := []string{"slime", "slime", "wolf", "wolf", "knight", "knight", "dragon", "dragon", "bolt", "bolt", "heal", "heal"}
	for seed := int64(1); seed <= 30; seed++ {
		st, err := battle.NewState("r", cat, battle.PlayerInit{ID: "a", Cards: d}, battle.PlayerInit{ID: "b", Cards: d}, seed)
		if err != nil {
			t.Fatal(err)
		}
		rng := rand.New(rand.NewSource(seed))
		steps := 0
		for !st.Over {
			if steps++; steps > 2000 {
				t.Fatalf("seed=%d：2000 步还没打完", seed)
			}
			pid := st.Players[st.Turn].ID
			a := chooseAction(st.ViewFor(st.Turn), cat, rng)
			if err := st.Apply(pid, a); err != nil {
				t.Fatalf("seed=%d step=%d：bot 选了非法动作 %+v：%v", seed, steps, a, err)
			}
		}
		if st.Winner == "" {
			t.Fatalf("seed=%d：结束了但没有胜者", seed)
		}
	}
}
