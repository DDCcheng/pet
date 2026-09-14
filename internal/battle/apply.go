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
	case "attack":
		return s.attack(i, a)
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
func findMinion(p *Player, instID string) (*Minion, int) {
	for k := range p.Board {
		if p.Board[k].InstID == instID {
			return &p.Board[k], k
		}
	}
	return nil, -1
}

func (s *State) attack(i int, a Action) error {
	me := s.Players[i]
	opposite := s.Players[1-i]
	attacker, _ := findMinion(me, a.InstID)
	if attacker == nil {
		return fail(CodeMinionNotFound, "场上没有 %s", a.InstID)
	}
	if !attacker.CanAttack {
		return fail(CodeCantAttack, "%s 本回合不能攻击", a.InstID)
	}
	if attacker.Atk <= 0 {
		return fail(CodeCantAttack, "%s 随从没有攻击力", a.InstID)
	}

	var def *Minion // nil 表示打脸
	if a.Target != "face" {
		if _, k := findMinion(me, a.Target); k >= 0 {
			return fail(CodeInvalidTarget, "不能攻击自方阵营")
		}
		def, _ = findMinion(opposite, a.Target)
		if def == nil {
			return fail(CodeTargetNotFound, "攻击目标没找到 %s", a.Target)
		}
	}
	if def == nil {
		opposite.HP -= attacker.Atk
	} else {
		def.HP -= attacker.Atk
		attacker.HP -= def.Atk
	}
	attacker.CanAttack = false

	s.removeDead()
	s.checkOver()
	return nil
}

func (s *State) removeDead() {
	for _, p := range s.Players {
		j := 0
		for k := range p.Board {
			if p.Board[k].HP > 0 {
				p.Board[j] = p.Board[k]
				j++
			}
		}
		p.Board = p.Board[:j]
	}

}

func (s *State) checkOver() { // HP<=0 → Over=true, Winner=对方ID
	if s.Over {
		return
	}
	d0, d1 := s.Players[0].HP <= 0, s.Players[1].HP <= 0
	switch {
	case d0 && d1:
		s.Over, s.Winner = true, "" // 平局
	case d0:
		s.Winner = s.Players[1].ID
		s.Over = true
	case d1:
		s.Winner = s.Players[0].ID
		s.Over = true
	}
}
