package match

import (
	"fmt"
	"testing"
)

// 压测发现的瓶颈：每轮只扫等待最久的 Batch 人。100 人同分排队，跑一轮：
// Batch=0（默认 20）最多配 20 对；Batch=100 一轮全部配完。
func TestMatchOnceBatch(t *testing.T) {
	cases := []struct {
		batch    int64
		maxPairs int
		minPairs int
	}{
		{0, 20, 1},
		{100, 50, 50},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("batch=%d", c.batch), func(t *testing.T) {
			m, ctx := newTestManager(t)
			m.Batch = c.batch
			col := &collector{}
			m.OnMatch = col.fn
			for i := 0; i < 100; i++ {
				if err := m.Join(ctx, fmt.Sprintf("p%03d", i), 1500); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.matchOnce(ctx); err != nil {
				t.Fatal(err)
			}
			n := len(col.all())
			if n < c.minPairs || n > c.maxPairs {
				t.Fatalf("一轮配了 %d 对，want [%d, %d]", n, c.minPairs, c.maxPairs)
			}
			if left := queueLen(t, m, ctx); left != int64(100-2*n) {
				t.Fatalf("队列剩 %d 人，配了 %d 对，对不上", left, n)
			}
			seen := map[string]bool{}
			for _, p := range col.all() {
				if seen[p.A] || seen[p.B] || p.A == p.B {
					t.Fatalf("有人被配了两次: %+v", p)
				}
				seen[p.A], seen[p.B] = true, true
			}
		})
	}
}
