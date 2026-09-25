package battle

type PlayerView struct {
	//公开字段
	ID      string `json:"id"`
	HP      int    `json:"hp"`
	Mana    int    `json:"mana"`     // 法力值
	MaxMana int    `json:"max_mana"` //最大法力值
	Fatigue int    `json:"fatigue"`  //疲劳规则
	//根据玩家视角变化
	Board     []Minion `json:"board"`
	Hand      []string `json:"hand,omitempty"` // 自己的手牌
	HandCount int      `json:"hand_count"`     // 对手的手牌数量
	DeckCount int      `json:"deck_count"`
}
type View struct {
	Seq      uint64     `json:"seq"`
	Me       PlayerView `json:"me"`
	Opp      PlayerView `json:"opp"`
	YourTurn bool       `json:"your_turn"`
	Round    int        `json:"round"` // ★ 单位是 ply（半回合）：每次换手 +1，一个完整回合 = 2
	Over     bool       `json:"over"`
	Winner   string     `json:"winner"`
}

func (s *State) ViewFor(i int) View {
	view := &View{
		Me:       toView(s.Players[i], true),
		Opp:      toView(s.Players[1-i], false),
		Round:    s.Round,
		Over:     s.Over,
		Winner:   s.Winner,
		YourTurn: i == s.Turn,
	}
	return *view
}

func toView(p *Player, showhand bool) PlayerView {
	pv := &PlayerView{
		ID:        p.ID,
		HP:        p.HP,
		Mana:      p.Mana,
		MaxMana:   p.MaxMana,
		Fatigue:   p.Fatigue,
		HandCount: len(p.Hand),
		DeckCount: len(p.Deck),
	}
	pv.Board = make([]Minion, len(p.Board))
	copy(pv.Board, p.Board)
	if showhand {
		pv.Hand = make([]string, len(p.Hand))
		copy(pv.Hand, p.Hand)
	}
	return *pv
}
