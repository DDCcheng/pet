package conc

import "testing"

func TestAlternate(t *testing.T) {
	got := Alternate()
	want := "1a2b3c4d"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}