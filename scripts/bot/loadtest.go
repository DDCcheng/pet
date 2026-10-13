package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DDCcheng/pet/internal/auth"
	"github.com/DDCcheng/pet/internal/deck"
	_ "github.com/go-sql-driver/mysql"
)

// ---------- 统计工具 ----------

// samples 收集耗时样本，最后算分位数。多协程并发写，所以加锁。
type samples struct {
	mu sync.Mutex
	ds []time.Duration
}

func (s *samples) add(d time.Duration) {
	s.mu.Lock()
	s.ds = append(s.ds, d)
	s.mu.Unlock()
}

// percentile 用最近秩法（nearest-rank）：p 取 0~100
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	k := int(float64(len(sorted))*p/100+0.999999) - 1
	if k < 0 {
		k = 0
	}
	if k >= len(sorted) {
		k = len(sorted) - 1
	}
	return sorted[k]
}

type dist struct {
	N                  int
	P50, P90, P99, Max time.Duration
}

func (s *samples) dist() dist {
	s.mu.Lock()
	ds := append([]time.Duration(nil), s.ds...)
	s.mu.Unlock()
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	d := dist{N: len(ds), P50: percentile(ds, 50), P90: percentile(ds, 90), P99: percentile(ds, 99)}
	if len(ds) > 0 {
		d.Max = ds[len(ds)-1]
	}
	return d
}

func (d dist) String() string {
	r := func(x time.Duration) string { return x.Round(10 * time.Microsecond).String() }
	return fmt.Sprintf("n=%d  p50=%s  p90=%s  p99=%s  max=%s", d.N, r(d.P50), r(d.P90), r(d.P99), r(d.Max))
}

// parseMetrics 解析 /metrics 的 "name value" 文本
func parseMetrics(body string) map[string]int64 {
	out := map[string]int64{}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		if v, err := strconv.ParseInt(f[1], 10, 64); err == nil {
			out[f[0]] = v
		}
	}
	return out
}

func fetchMetrics(addr string) (map[string]int64, error) {
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		sb.WriteString(sc.Text() + "\n")
	}
	return parseMetrics(sb.String()), nil
}

// ---------- 压测 ----------

type loadConfig struct {
	Addr     string
	Players  int
	Prefix   string
	Pass     string
	Games    int
	Delay    time.Duration
	Ramp     time.Duration // 在这段时间内均匀登录，避免瞬间打满
	Timeout  time.Duration
	DSN      string // 非空则先 INSERT IGNORE 压测账号
	JSONOut  string
	CardPath string
}

type loadReport struct {
	Config        loadConfig
	Wall          time.Duration
	Games         int // 完成的对局数（两边各报一次，已除以 2）
	Failed        int
	Actions       int
	ActionsPerSec float64
	Illegal       int
	RateLimited   int
	RTT           dist // 发出动作 → 收到新状态
	MatchWait     dist // enqueue → match_found
	GameLen       dist // match_found → game_over
	PeakRooms     int64
	PeakConns     int64
	ServerDelta   map[string]int64 // 压测期间 /metrics 计数器的增量
}

func ensureUsers(dsn, prefix, pass string, n int) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	// 库里存 bcrypt 哈希。所有压测账号密码相同，只算一次哈希（一次几十毫秒，算 2000 次要好几秒）
	hash, err := auth.HashPassword(pass)
	if err != nil {
		return err
	}
	const batch = 500
	for i := 0; i < n; i += batch {
		var q strings.Builder
		var args []any
		q.WriteString("INSERT IGNORE INTO users (username, password) VALUES ")
		for j := i; j < i+batch && j < n; j++ {
			if j > i {
				q.WriteString(",")
			}
			q.WriteString("(?,?)")
			args = append(args, fmt.Sprintf("%s%d", prefix, j), hash)
		}
		if _, err := db.Exec(q.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

func runLoad(cfg loadConfig) (loadReport, error) {
	rep := loadReport{Config: cfg}
	cat, err := deck.Load(cfg.CardPath)
	if err != nil {
		return rep, err
	}
	if cfg.DSN != "" {
		if err := ensureUsers(cfg.DSN, cfg.Prefix, cfg.Pass, cfg.Players); err != nil {
			return rep, fmt.Errorf("建压测账号: %w", err)
		}
	}
	base, err := fetchMetrics(cfg.Addr)
	if err != nil {
		return rep, fmt.Errorf("读 /metrics: %w", err)
	}

	var rtt, wait, glen samples
	var games, failed, actions, illegal, limited atomic.Int64
	var peakRooms, peakConns atomic.Int64

	stop := make(chan struct{})
	pollDone := make(chan struct{})
	go func() { // 每 200ms 采一次峰值
		defer close(pollDone)
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if m, err := fetchMetrics(cfg.Addr); err == nil {
					if v := m["pet_rooms_active"]; v > peakRooms.Load() {
						peakRooms.Store(v)
					}
					if v := m["pet_ws_conns"]; v > peakConns.Load() {
						peakConns.Store(v)
					}
				}
			}
		}
	}()

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < cfg.Players; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cfg.Ramp > 0 {
				time.Sleep(cfg.Ramp * time.Duration(i) / time.Duration(cfg.Players))
			}
			name := fmt.Sprintf("%s%d", cfg.Prefix, i)
			tok, err := login(cfg.Addr, name, cfg.Pass)
			if err != nil {
				log.Printf("%s login: %v", name, err)
				failed.Add(int64(cfg.Games))
				return
			}
			conn, err := dial(cfg.Addr, tok)
			if err != nil {
				log.Printf("%s dial: %v", name, err)
				failed.Add(int64(cfg.Games))
				return
			}
			defer conn.Close()
			var enq, matched time.Time
			c := &client{
				name: name, conn: conn, cat: cat, delay: cfg.Delay,
				rng:        rand.New(rand.NewSource(int64(i) + 1)),
				enqueuedAt: func() time.Time { return enq },
				onMatch:    func(d time.Duration) { matched = time.Now(); wait.add(d) },
				onRTT:      rtt.add,
			}
			for g := 0; g < cfg.Games; g++ {
				enq = time.Now()
				if err := conn.WriteJSON(map[string]string{"type": "enqueue"}); err != nil {
					failed.Add(1)
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
				res, err := c.playOne(ctx)
				cancel()
				if err != nil {
					log.Printf("%s game %d: %v", name, g+1, err)
					failed.Add(1)
					return
				}
				glen.add(time.Since(matched))
				games.Add(1)
				actions.Add(int64(res.Actions))
				illegal.Add(int64(res.Illegal))
				limited.Add(int64(res.Limited))
			}
		}()
	}
	wg.Wait()
	rep.Wall = time.Since(start)
	close(stop)
	<-pollDone

	final, err := fetchMetrics(cfg.Addr)
	if err != nil {
		return rep, err
	}
	rep.ServerDelta = map[string]int64{}
	for k, v := range final {
		if strings.HasSuffix(k, "_total") {
			rep.ServerDelta[k] = v - base[k]
		}
	}
	rep.Games = int(games.Load()) / 2
	rep.Failed = int(failed.Load())
	rep.Actions = int(actions.Load())
	rep.ActionsPerSec = float64(rep.Actions) / rep.Wall.Seconds()
	rep.Illegal = int(illegal.Load())
	rep.RateLimited = int(limited.Load())
	rep.RTT, rep.MatchWait, rep.GameLen = rtt.dist(), wait.dist(), glen.dist()
	rep.PeakRooms, rep.PeakConns = peakRooms.Load(), peakConns.Load()

	if cfg.JSONOut != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(cfg.JSONOut, b, 0o644); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func (r loadReport) print() {
	fmt.Printf("\n===== 压测结果 =====\n")
	fmt.Printf("玩家 %d，每人 %d 局，动作间隔 %v，用时 %v\n", r.Config.Players, r.Config.Games, r.Config.Delay, r.Wall.Round(time.Millisecond))
	fmt.Printf("完成对局 %d，失败 %d，非法动作 %d，客户端被限速 %d 次\n", r.Games, r.Failed, r.Illegal, r.RateLimited)
	fmt.Printf("峰值同时在线房间 %d，峰值连接 %d\n", r.PeakRooms, r.PeakConns)
	fmt.Printf("房间处理动作 %d 个，吞吐 %.0f 动作/秒\n", r.Actions, r.ActionsPerSec)
	fmt.Printf("动作往返 RTT   %s\n", r.RTT)
	fmt.Printf("匹配等待       %s\n", r.MatchWait)
	fmt.Printf("单局时长       %s\n", r.GameLen)
	fmt.Printf("服务端计数增量 %v\n", r.ServerDelta)
}
