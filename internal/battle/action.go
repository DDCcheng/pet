package battle

type Action struct {
	Type   string `json:"type"`              // "play_card" | "end_turn"
	CardID string `json:"card_id,omitempty"` // play_card 用
	InstID string `json:"inst_id,omitempty"` // Day 10 攻击用
	Target string `json:"target,omitempty"`  // Day 10 用
}
