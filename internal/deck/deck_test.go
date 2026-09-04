package deck

import "testing"

func cards(ids ...string) []Card {
	cat := Catalog()
	out := make([]Card, 0, len(ids))
	for _, id := range ids {
		out = append(out, cat[id])
	}
	return out
}

func TestValidateDeck_OK(t *testing.T) {
	ids := []string{"slime", "wolf", "knight", "dragon", "heal", "bolt"}
	var all []string
	for _, id := range ids {
		all = append(all, id, id) // 6 种 × 2 = 12 张
	}
	if err := ValidateDeck(Deck{Cards: cards(all...)}); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestValidateDeck_TooSmall(t *testing.T) {
	if err := ValidateDeck(Deck{Cards: cards("slime", "wolf")}); err == nil {
		t.Fatal("want error")
	}
}

func TestValidateDeck_TooManyCopies(t *testing.T) {
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = "slime"
	}
	if err := ValidateDeck(Deck{Cards: cards(ids...)}); err == nil {
		t.Fatal("want error")
	}
}

func TestValidateDeck_Unknown(t *testing.T) {
	d := Deck{Cards: cards(
		"slime", "slime",
		"wolf", "wolf",
		"knight", "knight",
		"dragon", "dragon",
	)}
	d.Cards[0].ID = "not-exist"
	if err := ValidateDeck(d); err == nil {
		t.Fatal("want error")
	}
}