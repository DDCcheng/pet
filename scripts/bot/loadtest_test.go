package main

import (
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	cases := []struct {
		p    float64
		want time.Duration
	}{{50, 50 * time.Millisecond}, {90, 90 * time.Millisecond}, {99, 99 * time.Millisecond}, {100, 100 * time.Millisecond}, {0, time.Millisecond}}
	for _, c := range cases {
		if got := percentile(ds, c.p); got != c.want {
			t.Fatalf("p%v=%v want %v", c.p, got, c.want)
		}
	}
	if percentile(nil, 50) != 0 {
		t.Fatal("空样本应返回 0")
	}
	if got := percentile([]time.Duration{7}, 99); got != 7 {
		t.Fatalf("单样本 p99=%v", got)
	}
}

func TestSamplesDist(t *testing.T) {
	var s samples
	for _, ms := range []int{5, 1, 3, 2, 4} {
		s.add(time.Duration(ms) * time.Millisecond)
	}
	d := s.dist()
	if d.N != 5 || d.P50 != 3*time.Millisecond || d.Max != 5*time.Millisecond {
		t.Fatalf("%+v", d)
	}
}

func TestParseMetrics(t *testing.T) {
	m := parseMetrics("pet_rooms_active 3\npet_ws_conns 6\n\nbad line here\npet_x notnum\n")
	if m["pet_rooms_active"] != 3 || m["pet_ws_conns"] != 6 || len(m) != 2 {
		t.Fatalf("%v", m)
	}
}
