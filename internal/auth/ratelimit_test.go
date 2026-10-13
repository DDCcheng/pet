package auth

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time      { return f.t }
func (f *fakeClock) add(d time.Duration) { f.t = f.t.Add(d) }
func newTestLimiter(rate, burst float64) (*limiter, *fakeClock) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	l := &limiter{rate: rate, burst: burst, tokens: burst, now: clk.now, last: clk.t}
	return l, clk
}

func TestLimiter(t *testing.T) {
	cases := []struct {
		name  string
		steps []struct {
			wait time.Duration
			n    int // 这一刻连发几条
		}
		wantOK int
	}{
		{"突发上限", []struct {
			wait time.Duration
			n    int
		}{{0, 15}}, 10},
		{"100ms 回 1 个", []struct {
			wait time.Duration
			n    int
		}{{0, 10}, {100 * time.Millisecond, 3}}, 11},
		{"空闲很久也只攒到 burst", []struct {
			wait time.Duration
			n    int
		}{{0, 10}, {time.Hour, 20}}, 20},
		{"人手速度每 200ms 一条，永不被限", []struct {
			wait time.Duration
			n    int
		}{{0, 1}, {200 * time.Millisecond, 1}, {200 * time.Millisecond, 1}, {200 * time.Millisecond, 1}, {200 * time.Millisecond, 1}}, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, clk := newTestLimiter(10, 10)
			ok := 0
			for _, s := range c.steps {
				clk.add(s.wait)
				for i := 0; i < s.n; i++ {
					if l.Allow() {
						ok++
					}
				}
			}
			if ok != c.wantOK {
				t.Fatalf("放行 %d 条 want %d", ok, c.wantOK)
			}
		})
	}
}
