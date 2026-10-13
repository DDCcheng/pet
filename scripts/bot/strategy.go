package main

import (
	"math/rand"

	"github.com/DDCcheng/pet/internal/battle"
	"github.com/DDCcheng/pet/internal/deck"
)

// chooseAction 从当前视角里挑一个合法动作：能出的随从、能打的攻击里随机选一个，都没有就 end_turn。
// 法术服务端还没实现（Summon 会回 not_implemented），所以 bot 不出法术。
func chooseAction(v battle.View, cat *deck.Catalog, rng *rand.Rand) battle.Action {
	var cands []battle.Action
	if len(v.Me.Board) < battle.BoardLimit {
		seen := map[string]bool{}
		for _, id := range v.Me.Hand {
			if seen[id] {
				continue
			}
			seen[id] = true
			c, ok := cat.Get(id)
			if ok && c.Type == deck.TypeMinion && c.Cost <= v.Me.Mana {
				cands = append(cands, battle.Action{Type: "play_card", CardID: id})
			}
		}
	}
	for _, m := range v.Me.Board {
		if !m.CanAttack || m.Atk <= 0 {
			continue
		}
		cands = append(cands, battle.Action{Type: "attack", InstID: m.InstID, Target: "face"})
		for _, o := range v.Opp.Board {
			cands = append(cands, battle.Action{Type: "attack", InstID: m.InstID, Target: o.InstID})
		}
	}
	if len(cands) == 0 {
		return battle.Action{Type: "end_turn"}
	}
	return cands[rng.Intn(len(cands))]
}
