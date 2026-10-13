package battle

import (
	"fmt"
	"hash/fnv"
)

// Checksum 是整个局面（含双方手牌、牌库顺序、下一个 InstID）的 64 位指纹，16 位十六进制。
// 房间每接受一个动作就记一次，回放时逐步比对：哪一步开始不一样，就能精确定位到哪一步。
// 只比胜者和回合数不够：换一个 seed，同一串动作有可能照样合法、照样同一个人赢，但中间的局面完全不同。
func (s *State) Checksum() string {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%d|%v|%s|%d", s.Turn, s.Round, s.Over, s.Winner, s.nextInstID)
	for _, p := range s.Players {
		fmt.Fprintf(h, "|%s,%d,%d,%d,%d,%q,%q", p.ID, p.HP, p.Mana, p.MaxMana, p.Fatigue, p.Hand, p.Deck)
		for _, m := range p.Board {
			fmt.Fprintf(h, ";%s,%s,%d,%d,%d,%v", m.InstID, m.CardID, m.Atk, m.HP, m.MaxHP, m.CanAttack)
		}
	}
	return fmt.Sprintf("%016x", h.Sum64())
}
