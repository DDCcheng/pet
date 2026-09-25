package battle

import (
	"fmt"
	"math/rand"

	"github.com/DDCcheng/pet/internal/deck"
)

// PlayerInit 是开局输入：谁、带什么卡组。
type PlayerInit struct {
	ID    string
	Cards []string // CardID 列表，已经过 ValidateDeck
}

func NewState(roomID string, cat *deck.Catalog, a, b PlayerInit, seed int64) (*State, error) {
	if cat == nil {
		return nil, fmt.Errorf("cat can not be nil")
	}
	if a.ID == "" || b.ID == "" {
		return nil, fmt.Errorf("players id can not be nil")
	}
	if a.ID == b.ID {
		return nil, fmt.Errorf("a.id is equal to b.id")
	}

	for _, init := range [2]PlayerInit{a, b} {
		if len(init.Cards) <= HandStart {
			return nil, fmt.Errorf("battle.NewState: 玩家 %s 卡组只有 %d 张，不够发 %d 张起手牌",
				init.ID, len(init.Cards), HandStart)
		}
	}
	st := &State{
		RoomID: roomID,
		Seed:   seed,
		cat:    cat,
		rng:    rand.New(rand.NewSource(seed)), // ★ 每局独立的随机源
		Turn:   0,
		Round:  1,
	}
	for i, init := range [2]PlayerInit{a, b} {
		p := &Player{
			ID:      init.ID,
			HP:      StartHP,
			MaxMana: 0,
		}
		p.Deck = append([]string(nil), init.Cards...)
		st.shuffle(p.Deck)
		st.Players[i] = p
	}
	for k := 0; k < HandStart; k++ {
		st.Draw(0)
		st.Draw(1)
	}
	st.beginTurn(0)
	return st, nil
}

func (s *State) shuffle(d []string) {
	s.rng.Shuffle(len(d), func(i, j int) { d[i], d[j] = d[j], d[i] })
}

func (s *State) Draw(i int) (string, bool) {
	p := s.Players[i]
	if len(p.Deck) == 0 {
		return "", false
	}
	card := p.Deck[0]
	p.Deck = p.Deck[1:]
	p.Hand = append(p.Hand, card)
	return card, true
}

// newInstID 生成场上唯一 ID。★ 用递增计数器而不是随机串 —— 随机串会破坏可复现。
func (s *State) newInstID() string {
	s.nextInstID++
	return fmt.Sprintf("m%d", s.nextInstID)
}

// 保证：属性从卡表拷贝，场上不超上限，InstID 全局唯一递增
func (s *State) Summon(playerIdx int, cardID string) (Minion, error) {
	// 查卡表 → 校验是 minion → 校验场上没满 → 拷贝属性建实例
	card, ok := s.cat.Get(cardID)
	if !ok {
		return Minion{}, fail(CodeUnknownCard, "卡表里没有 %s", cardID)
	}
	if card.Type != deck.TypeMinion {
		return Minion{}, fail(CodeNotImplemented, "%s 不是随从", cardID)
	}
	p := s.Players[playerIdx]
	if len(p.Board) >= BoardLimit {
		return Minion{}, fail(CodeBoardFull, "场上已满（上限 %d）", BoardLimit)
	}
	m := Minion{
		InstID:    s.newInstID(),
		CardID:    card.ID,
		Atk:       card.Atk,
		HP:        card.HP, // 当前血量，之后只改这只，不影响卡表
		MaxHP:     card.HP,
		CanAttack: false, // 召唤当回合不能攻击（Day 10 用）
	}
	p.Board = append(p.Board, m)
	return m, nil
}

func indexOfString(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

func (s *State) beginTurn(i int) {
	p := s.Players[i]
	if p.MaxMana < MaxMana {
		p.MaxMana++
	}
	p.Mana = p.MaxMana
	if _, ok := s.Draw(i); !ok {
		p.Fatigue++ // 第 1 次空抽扣 1，第 2 次扣 2…
		p.HP -= p.Fatigue
		s.checkOver() // ★ 疲劳能直接终结对局
	}
	for k := range p.Board {
		p.Board[k].CanAttack = true
	}
}

// 判断用户是否属于这局游戏,ID 唯一性由 NewState 保证，所以这里返回第一个匹配是安全的
func (s *State) indexOf(playerId string) int {
	for i, p := range s.Players {
		if p.ID == playerId {
			return i
		}
	}
	return -1
}

func (s *State) DisplayRound() int { return (s.Round + 1) / 2 }

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

func findMinion(p *Player, instID string) (*Minion, int) {
	for k := range p.Board {
		if p.Board[k].InstID == instID {
			return &p.Board[k], k
		}
	}
	return nil, -1
}
