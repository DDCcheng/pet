package battle

func (s *State) Apply(playerId string, a Action) error {
	if s.Over {
		return fail(CodeGameOver, "对局已结束")
	}
	i := s.indexOf(playerId)
	if i < 0 {
		return fail(CodeNotInGame, "%s不是本局玩家", playerId)
	}
	if i != s.Turn {
		return fail(CodeNotYourTurn, "现在是 %s 的回合", s.Players[s.Turn].ID)
	}
	switch a.Type {
	case "play_card":
		return s.playCard(i, a)
	case "end_turn":
		return s.endTurn(i)
	default:
		return fail(CodeUnknownAction, "未知动作 %q", a.Type)
	}
}

func (s *State) playCard(i int, a Action) error {
	p := s.Players[i]
	hi := indexOfString(p.Hand, a.CardID)
	if hi < 0 {
		return fail(CodeCardNotInHand, "%s 不在手牌中", a.CardID)
	}
	card, ok := s.cat.Get(a.CardID)
	if !ok {
		return fail(CodeUnknownCard, "卡表里没有 %s", a.CardID)
	}
	if p.Mana < card.Cost {
		return fail(CodeNotEnoughMana, "需要 %d 费，当前 %d", card.Cost, p.Mana)
	}

	if _, err := s.Summon(i, a.CardID); err != nil {
		return err
	}
	p.Mana -= card.Cost
	p.Hand = append(p.Hand[:hi], p.Hand[hi+1:]...) //把hi去除，通过拼接列表的前面和后面
	return nil
}

func (s *State) endTurn(i int) error {
	s.Turn = 1 - i // 0↔1 切换的惯用写法
	s.Round++
	s.beginTurn(s.Turn) // 新回合：涨费、回满、抽牌
	return nil
}
