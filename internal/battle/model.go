package battle

import (
	"math/rand"

	"github.com/DDCcheng/pet/internal/deck"
)

const (
	StartHP    = 20
	HandStart  = 3
	MaxMana    = 10
	BoardLimit = 5
)

type Minion struct {
	InstID    string `json:"inst_id"` // 场上唯一，attack 靠它指定目标
	CardID    string `json:"card_id"` // 指回卡表
	Atk       int    `json:"atk"`
	HP        int    `json:"hp"` // 当前血量，会变
	MaxHP     int    `json:"max_hp"`
	CanAttack bool   `json:"can_attack"` // Day 10：召唤当回合不能攻击
}

type Player struct {
	ID      string   `json:"id"`
	HP      int      `json:"hp"`
	Mana    int      `json:"mana"`     // 本回合剩余
	MaxMana int      `json:"max_mana"` // 本回合上限
	Deck    []string `json:"-"`        // 牌库（CardID），对手不可见
	Hand    []string `json:"-"`        // 手牌，对手只知道数量
	Board   []Minion `json:"board"`
	Fatigue int      `json:"fatigue"` //疲劳规则
}

type State struct {
	RoomID  string     `json:"room_id"`
	Players [2]*Player `json:"players"`
	Turn    int        `json:"turn"`  // 当前该谁行动：0 或 1
	Round   int        `json:"round"` // ★ 单位是 ply（半回合）：每次换手 +1，一个完整回合 = 2
	Seed    int64      `json:"seed"`
	Over    bool       `json:"over"`
	Winner  string     `json:"winner"`

	// ---- 以下小写，不导出、不序列化、只有房间 goroutine 能碰 ----
	cat        *deck.Catalog
	rng        *rand.Rand
	nextInstID int
}
