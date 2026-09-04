package deck

import (
	"fmt"
)

type Card struct {
	ID   string
	Name string
	Cost int
	Atk  int
	HP   int
}

type Deck struct {
	Owner string
	Cards []Card
}

func Catalog() map[string]Card {
	return map[string]Card{
		"slime":  {ID: "slime", Name: "史莱姆", Cost: 1, Atk: 1, HP: 2},
		"wolf":   {ID: "wolf", Name: "幼狼", Cost: 2, Atk: 2, HP: 2},
		"knight": {ID: "knight", Name: "骑士", Cost: 3, Atk: 3, HP: 4},
		"dragon": {ID: "dragon", Name: "幼龙", Cost: 5, Atk: 5, HP: 6},
		"heal":   {ID: "heal", Name: "治疗", Cost: 2, Atk: 0, HP: 0},
		"bolt":   {ID: "bolt", Name: "雷击", Cost: 1, Atk: 0, HP: 0},
	}
}

func ValidateDeck(d Deck) error {
	n := len(d.Cards)
	if n < 8 || n > 12 {
		return fmt.Errorf("deck size %d, want 8-12", n)
	}

	cat := Catalog()
	count := make(map[string]int)

	for _, c := range d.Cards {
		_, ok := cat[c.ID]
		if !ok {
			return fmt.Errorf("unknown card %s", c.ID)
		}
		count[c.ID]++
		if count[c.ID] > 2 {
			return fmt.Errorf("too many copies of %s", c.ID)
		}
	}
	return nil
}
