package deck

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	TypeMinion = "minion"
	TypeSpell  = "spell"
)

type Card struct {
	ID     string `yaml:"id"`
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Cost   int    `yaml:"cost"`
	Atk    int    `yaml:"atk"`
	HP     int    `yaml:"hp"`
	Damage int    `yaml:"damage,omitempty"`
	Heal   int    `yaml:"heal,omitempty"`
}

type Deck struct {
	Owner string
	Cards []string
}

type Catalog struct {
	byId  map[string]Card
	order []string
}

func validateCard(i int, card Card) error {
	where := fmt.Sprintf("卡表第 %d 张 (id=%q)", i+1, card.ID)

	if card.ID == "" {
		return fmt.Errorf("%s: id 不能为空", where)
	}
	if card.Name == "" {
		return fmt.Errorf("%s: name 不能为空", where)
	}
	if card.Cost < 0 {
		return fmt.Errorf("%s: cost=%d 不能为负", where, card.Cost)
	}

	switch card.Type {
	case TypeMinion:
		if card.HP <= 0 {
			return fmt.Errorf("%s: minion 的 hp=%d 必须大于 0", where, card.HP)
		}
		if card.Atk < 0 {
			return fmt.Errorf("%s: minion 的 atk=%d 不能为负", where, card.Atk)
		}
	case TypeSpell:
		if card.Damage == 0 && card.Heal == 0 {
			return fmt.Errorf("%s: spell 必须有 damage 或 heal", where)
		}
	default:
		return fmt.Errorf("%s: type=%q 必须是 %q 或 %q", where, card.Type, TypeMinion, TypeSpell)
	}
	return nil
}

func Load(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读卡表 %s: %w", path, err)
	}
	var file struct {
		Cards []Card `yaml:"cards"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("解析卡表: %w", err)
	}
	c := &Catalog{byId: map[string]Card{}}
	for i, card := range file.Cards {
		if err := validateCard(i, card); err != nil {
			return nil, err
		}
		if _, dup := c.byId[card.ID]; dup {
			return nil, fmt.Errorf("卡表第 %d 张: id %q 重复", i+1, card.ID)
		}
		c.byId[card.ID] = card
		c.order = append(c.order, card.ID)
	}
	if len(c.order) == 0 {
		return nil, fmt.Errorf("卡表 %s 是空的", path) // 空文件也是配置错误
	}
	return c, nil

}
func (c *Catalog) Get(id string) (Card, bool) {
	card, ok := c.byId[id]
	return card, ok
}
func (c *Catalog) Has(id string) bool {
	_, ok := c.byId[id]
	return ok
}
func (c *Catalog) IDs() []string {
	return append([]string(nil), c.order...)
}
func (c *Catalog) Len() int { return len(c.order) }

func ValidateDeck(cat *Catalog, d Deck) error {
	if cat == nil {
		return fmt.Errorf("Catalog is nil")
	}
	n := len(d.Cards)
	if n < 8 || n > 12 {
		return fmt.Errorf("deck size %d, want 8-12", n)
	}
	count := make(map[string]int)

	for _, id := range d.Cards {
		if !cat.Has(id) {
			return fmt.Errorf("unknown card %s", id)
		}
		count[id]++
		if count[id] > 2 {
			return fmt.Errorf("too many copies of %s", id)
		}
	}
	return nil
}
