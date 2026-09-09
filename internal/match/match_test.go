package match

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// 测试脚手架
// ---------------------------------------------------------------------------

// newTestManager 连一个真 Redis，用 DB 15 当测试库，每个用例前清空。
// 环境变量 REDIS_ADDR 可覆盖地址；没起 Redis 就跳过（不让测试变红）。
func newTestManager(t *testing.T) (*Manager, context.Context) {
	t.Helper()

	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15}) // ⚠️ DB 15 专供测试

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("跳过：连不上 Redis (%s)：%v", addr, err)
	}
	rdb.FlushDB(ctx) // 每个用例从空队列开始
	t.Cleanup(func() { rdb.FlushDB(ctx); rdb.Close() })

	return &Manager{
		RDB:         rdb,
		Tick:        50 * time.Millisecond,
		BaseWindow:  50,
		WidenPerSec: 20,
		MaxWindow:   500,
	}, ctx
}

// collector 收集 OnMatch 回调，测试里用它断言配对结果。
type collector struct {
	mu    sync.Mutex
	pairs []Pair
}

func (c *collector) fn(p Pair) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pairs = append(c.pairs, p)
}

func (c *collector) all() []Pair {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Pair, len(c.pairs))
	copy(out, c.pairs)
	return out
}

// 断言某人在/不在队列里
func inQueue(t *testing.T, m *Manager, ctx context.Context, id string) bool {
	t.Helper()
	err := m.RDB.ZScore(ctx, keyRating, id).Err()
	return err != redis.Nil
}

func queueLen(t *testing.T, m *Manager, ctx context.Context) int64 {
	t.Helper()
	n, _ := m.RDB.ZCard(ctx, keyRating).Result()
	return n
}

// ---------------------------------------------------------------------------
// 1. 基础：入队 / 出队
// ---------------------------------------------------------------------------

func TestJoinAndLeave(t *testing.T) {
	m, ctx := newTestManager(t)

	if err := m.Join(ctx, "alice", 1500); err != nil {
		t.Fatal(err)
	}
	if queueLen(t, m, ctx) != 1 {
		t.Fatalf("入队后队列长度应为 1")
	}
	// 两个 ZSET 必须同步
	if n, _ := m.RDB.ZCard(ctx, keyTime).Result(); n != 1 {
		t.Fatalf("mm:time 没同步写入")
	}

	if err := m.Leave(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if queueLen(t, m, ctx) != 0 {
		t.Fatalf("出队后队列应为空")
	}
	if n, _ := m.RDB.ZCard(ctx, keyTime).Result(); n != 0 {
		t.Fatalf("mm:time 没被一起清掉")
	}
}

// 2. 幂等：重复 Join 不能刷掉等待时间（ZAddNX 的意义）
func TestJoinIsIdempotent(t *testing.T) {
	m, ctx := newTestManager(t)

	m.Join(ctx, "alice", 1500)
	first, _ := m.RDB.ZScore(ctx, keyTime, "alice").Result()

	time.Sleep(30 * time.Millisecond)
	m.Join(ctx, "alice", 9999) // 分数也故意换一个

	second, _ := m.RDB.ZScore(ctx, keyTime, "alice").Result()
	if first != second {
		t.Fatalf("重复入队把等待时间刷掉了：%v → %v", first, second)
	}
	if s, _ := m.RDB.ZScore(ctx, keyRating, "alice").Result(); s != 1500 {
		t.Fatalf("重复入队把分数改了：%v", s)
	}
	if queueLen(t, m, ctx) != 1 {
		t.Fatalf("重复入队产生了两条记录")
	}
}

// ---------------------------------------------------------------------------
// 3. 核心：分数相近的两人应该被配上
// ---------------------------------------------------------------------------

func TestMatchesNearbyScores(t *testing.T) {
	m, ctx := newTestManager(t)
	c := &collector{}
	m.OnMatch = c.fn

	m.Join(ctx, "alice", 1500)
	m.Join(ctx, "bob", 1520) // 差 20，在 ±50 窗口内

	if err := m.matchOnce(ctx); err != nil {
		t.Fatal(err)
	}

	ps := c.all()
	if len(ps) != 1 {
		t.Fatalf("应该配出 1 对，实际 %d 对", len(ps))
	}
	if ps[0].RoomID == "" {
		t.Fatal("RoomID 不能为空")
	}
	// 配对成功的人必须离开队列
	if queueLen(t, m, ctx) != 0 {
		t.Fatalf("配对后队列应清空，实际还剩 %d 人", queueLen(t, m, ctx))
	}
	if n, _ := m.RDB.ZCard(ctx, keyTime).Result(); n != 0 {
		t.Fatal("mm:time 里残留了已配对的玩家")
	}
}

// 4. 分差太大不该被配上
func TestDoesNotMatchFarScores(t *testing.T) {
	m, ctx := newTestManager(t)
	c := &collector{}
	m.OnMatch = c.fn
	m.WidenPerSec = 0 // 关掉放宽，专测窗口本身

	m.Join(ctx, "alice", 1000)
	m.Join(ctx, "bob", 2000) // 差 1000，远超 ±50

	m.matchOnce(ctx)

	if len(c.all()) != 0 {
		t.Fatal("分差 1000 不该被配上")
	}
	if queueLen(t, m, ctx) != 2 {
		t.Fatal("没配上的人应该留在队列里")
	}
}

// 5. 队列里只有一个人时不能自己跟自己配（b != a 那个判断）
func TestSinglePlayerNotMatchedWithSelf(t *testing.T) {
	m, ctx := newTestManager(t)
	c := &collector{}
	m.OnMatch = c.fn

	m.Join(ctx, "alice", 1500)
	m.matchOnce(ctx)

	if len(c.all()) != 0 {
		t.Fatal("一个人不该被配对")
	}
	if !inQueue(t, m, ctx, "alice") {
		t.Fatal("alice 被莫名其妙摘出队列了")
	}
}

// ---------------------------------------------------------------------------
// 6. 等待越久窗口越宽
// ---------------------------------------------------------------------------

func TestWindowWidensOverTime(t *testing.T) {
	m, ctx := newTestManager(t)
	c := &collector{}
	m.OnMatch = c.fn
	m.BaseWindow = 50
	m.WidenPerSec = 100 // 每秒放宽 100
	m.MaxWindow = 500

	// 分差 300：一开始配不上，等 3 秒后窗口 50+300=350 就够了
	m.Join(ctx, "alice", 1500)
	m.Join(ctx, "bob", 1800)

	m.matchOnce(ctx)
	if len(c.all()) != 0 {
		t.Fatal("刚入队时分差 300 不该被配上")
	}

	// 不真等 3 秒：直接把入队时间往前拨（测试里操纵时间的常用技巧）
	past := float64(time.Now().Add(-3 * time.Second).UnixMilli())
	m.RDB.ZAdd(ctx, keyTime, redis.Z{Score: past, Member: "alice"})
	m.RDB.ZAdd(ctx, keyTime, redis.Z{Score: past, Member: "bob"})

	m.matchOnce(ctx)
	if len(c.all()) != 1 {
		t.Fatal("等待 3 秒后窗口放宽到 ±350，应该能配上")
	}
}

// 7. MaxWindow 封顶
func TestWindowCappedAtMax(t *testing.T) {
	m, ctx := newTestManager(t)
	c := &collector{}
	m.OnMatch = c.fn
	m.BaseWindow = 50
	m.WidenPerSec = 100
	m.MaxWindow = 200 // 上限只有 200

	m.Join(ctx, "alice", 1000)
	m.Join(ctx, "bob", 1500) // 差 500，超过上限

	// 拨到一小时前，窗口理论上早该无限大了
	past := float64(time.Now().Add(-1 * time.Hour).UnixMilli())
	m.RDB.ZAdd(ctx, keyTime, redis.Z{Score: past, Member: "alice"})
	m.RDB.ZAdd(ctx, keyTime, redis.Z{Score: past, Member: "bob"})

	m.matchOnce(ctx)
	if len(c.all()) != 0 {
		t.Fatal("窗口应被 MaxWindow 封顶在 ±200，分差 500 不该配上")
	}
}

// ---------------------------------------------------------------------------
// 8. 公平性：等最久的优先
// ---------------------------------------------------------------------------

func TestOldestPlayerMatchedFirst(t *testing.T) {
	m, ctx := newTestManager(t)
	c := &collector{}
	m.OnMatch = c.fn

	// 三个人分数都在同一窗口内，只能配出一对
	m.Join(ctx, "old", 1500)
	m.Join(ctx, "mid", 1500)
	m.Join(ctx, "new", 1500)

	now := time.Now()
	m.RDB.ZAdd(ctx, keyTime, redis.Z{Score: float64(now.Add(-30 * time.Second).UnixMilli()), Member: "old"})
	m.RDB.ZAdd(ctx, keyTime, redis.Z{Score: float64(now.Add(-20 * time.Second).UnixMilli()), Member: "mid"})
	m.RDB.ZAdd(ctx, keyTime, redis.Z{Score: float64(now.UnixMilli()), Member: "new"})

	m.matchOnce(ctx)

	ps := c.all()
	if len(ps) != 1 {
		t.Fatalf("3 个人应该配出 1 对，实际 %d", len(ps))
	}
	if ps[0].A != "old" {
		t.Fatalf("等最久的 old 应该先被撮合，实际 A=%s", ps[0].A)
	}
	if !inQueue(t, m, ctx, "new") {
		t.Fatal("剩下的应该是最晚入队的 new")
	}
}

// 9. 一个人绝不能被配进两对（take 的 ZREM 锁）
func TestNobodyMatchedTwice(t *testing.T) {
	m, ctx := newTestManager(t)
	c := &collector{}
	m.OnMatch = c.fn

	for i := 0; i < 6; i++ {
		m.Join(ctx, fmt.Sprintf("p%d", i), 1500)
	}

	m.matchOnce(ctx)

	seen := map[string]int{}
	for _, p := range c.all() {
		seen[p.A]++
		seen[p.B]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("%s 被配了 %d 次", id, n)
		}
	}
	if len(c.all()) != 3 {
		t.Fatalf("6 个人应该配出 3 对，实际 %d 对", len(c.all()))
	}
	if queueLen(t, m, ctx) != 0 {
		t.Fatal("6 个人应该全部出队")
	}
}

// 10. 奇数人：剩一个留在队列里
func TestOddPlayerStaysInQueue(t *testing.T) {
	m, ctx := newTestManager(t)
	c := &collector{}
	m.OnMatch = c.fn

	for i := 0; i < 5; i++ {
		m.Join(ctx, fmt.Sprintf("p%d", i), 1500)
	}
	m.matchOnce(ctx)

	if len(c.all()) != 2 {
		t.Fatalf("5 个人应该配出 2 对，实际 %d", len(c.all()))
	}
	if queueLen(t, m, ctx) != 1 {
		t.Fatalf("应该剩 1 人，实际剩 %d", queueLen(t, m, ctx))
	}
	// 两个 ZSET 不能不一致
	if n, _ := m.RDB.ZCard(ctx, keyTime).Result(); n != 1 {
		t.Fatalf("mm:rating 剩 1 但 mm:time 剩 %d，两个 key 不同步", n)
	}
}

// ---------------------------------------------------------------------------
// 11. 回滚：take(b) 失败时 a 必须原样回队列（含原始时间戳）
// ---------------------------------------------------------------------------

func TestRestoreKeepsOriginalTimestamp(t *testing.T) {
	m, ctx := newTestManager(t)

	m.Join(ctx, "alice", 1500)
	orig := float64(time.Now().Add(-30 * time.Second).UnixMilli())
	m.RDB.ZAdd(ctx, keyTime, redis.Z{Score: orig, Member: "alice"})

	// 模拟：alice 被 take 走了，然后回滚
	ok, _ := m.take(ctx, "alice")
	if !ok {
		t.Fatal("take 应该成功")
	}
	if inQueue(t, m, ctx, "alice") {
		t.Fatal("take 之后 alice 应该已离开队列")
	}

	if err := m.restore(ctx, "alice", 1500, orig); err != nil {
		t.Fatal(err)
	}

	if !inQueue(t, m, ctx, "alice") {
		t.Fatal("restore 之后 alice 应该回到队列")
	}
	got, _ := m.RDB.ZScore(ctx, keyTime, "alice").Result()
	if got != orig {
		t.Fatalf("restore 没保住原始入队时间：want %v, got %v（资历被清零了）", orig, got)
	}
}

// 12. take 的互斥性：同一个人只可能被 take 成功一次
func TestTakeIsExclusive(t *testing.T) {
	m, ctx := newTestManager(t)
	m.Join(ctx, "alice", 1500)

	var success int32
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := m.take(ctx, "alice"); ok {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if success != 1 {
		t.Fatalf("20 个 goroutine 抢同一个人，应该只有 1 个成功，实际 %d 个", success)
	}
}

// ---------------------------------------------------------------------------
// 13. 端到端：跑 Run 循环，验证 ticker 真的在撮合
// ---------------------------------------------------------------------------

func TestRunLoopMatches(t *testing.T) {
	m, ctx := newTestManager(t)
	c := &collector{}
	m.OnMatch = c.fn

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go m.Run(runCtx)

	m.Join(ctx, "alice", 1500)
	m.Join(ctx, "bob", 1510)

	deadline := time.After(2 * time.Second)
	for {
		if len(c.all()) == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("2 秒内 Run 循环没把两人配上")
		case <-time.After(20 * time.Millisecond):
		}
	}

	cancel()
	time.Sleep(100 * time.Millisecond) // Run 应该退出，不该 panic
}

// 14. Run 收到 ctx 取消后必须退出
func TestRunStopsOnContextCancel(t *testing.T) {
	m, ctx := newTestManager(t)

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		m.Run(runCtx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ctx 取消后 Run 没有退出（goroutine 泄漏）")
	}
}
