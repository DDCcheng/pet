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

func NewState(rooId string, cat *deck.Catalog, a, b PlayerInit, seed int64) *State {
	if cat == nil {
		panic("battle.NewState: catalog is nil")
	}
	for _, init := range [2]PlayerInit{a, b} {
		if len(init.Cards) <= HandStart {
			panic(fmt.Sprintf("battle.NewState: 玩家 %s 卡组只有 %d 张，不够发 %d 张起手牌",
				init.ID, len(init.Cards), HandStart))
		}
	}
	st := &State{
		RoomID: rooId,
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
	return st
}

func (s *State) shuffle(d []string) {
	s.rng.Shuffle(len(d), func(i, j int) { d[i], d[j] = d[j], d[i] })
}

// Draw 从牌库顶抽一张进手牌。牌库空了返回 false（疲劳规则 Day 10 再说）。
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

// Summon 把一张随从卡变成场上的一只。Day 9 的 Apply 会调它。
func (s *State) Summon(playerIdx int, cardID string) (Minion, error) {
	// 查卡表 → 校验是 minion → 校验场上没满 → 拷贝属性建实例
	card, ok := s.cat.Get(cardID)
	if !ok {
		return Minion{}, fmt.Errorf("unknown card %s", cardID)
	}
	if card.Type != deck.TypeMinion {
		return Minion{}, fmt.Errorf("%s 不是随从", cardID)
	}
	p := s.Players[playerIdx]
	if len(p.Board) >= BoardLimit {
		return Minion{}, fmt.Errorf("场上已满（上限 %d）", BoardLimit)
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
	s.Draw(i)
	for k := range p.Board {
		p.Board[k].CanAttack = true
	}
}

// 判断用户是否属于这局游戏
func (s *State) indexOf(playerId string) int {
	for i, p := range s.Players {
		if p.ID == playerId {
			return i
		}
	}
	return -1
}
