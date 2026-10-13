package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"os/signal"
	"syscall"
	"time"

	"github.com/DDCcheng/pet/internal/auth"
	"github.com/DDCcheng/pet/internal/battle"
	"github.com/DDCcheng/pet/internal/deck"
	"github.com/DDCcheng/pet/internal/match"
	"github.com/DDCcheng/pet/internal/room"
	"github.com/DDCcheng/pet/internal/store"
	"github.com/redis/go-redis/v9"
)

const (
	httpAddr  = ":8080"
	redisAddr = "127.0.0.1:6379"
	mysqlDSN  = "root:pet@tcp(127.0.0.1:3307)/pet?parseTime=true&charset=utf8mb4"
)
const cardPath = "configs/cards.yaml"

// envOr 读环境变量，没设就用默认值。本地直接 go run 走上面的常量，容器里由 compose 注入。
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// newLoginLimiter：每个 IP 每秒 rate 次登录，突发 10 次。rate<=0 表示关闭（压测时所有 bot 来自同一个 IP）。
func newLoginLimiter(v string) *auth.IPLimiter {
	rate, err := strconv.ParseFloat(v, 64)
	if err != nil {
		log.Printf("PET_LOGIN_RATE=%q 不是数字，按 1 处理", v)
		rate = 1
	}
	if rate <= 0 {
		log.Printf("登录限速已关闭")
		return nil
	}
	return auth.NewIPLimiter(rate, 10)
}

func openMysql() (*sql.DB, error) {
	db, err := store.Open(envOr("PET_MYSQL_DSN", mysqlDSN))
	if err != nil {
		log.Fatalf("MySQL 连不上：%v\n请先执行 docker compose up -d", err)
	}
	return db, err
}

func openRedis(ctx context.Context) (*redis.Client, error) {
	addr := envOr("PET_REDIS_ADDR", redisAddr)
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("Redis 连不上 %s（先跑 docker compose up -d）：%w", addr, err)
	}
	return rdb, nil
}

func newMatcher(rdb *redis.Client) *match.Manager {
	return &match.Manager{
		RDB:         rdb,
		Tick:        500 * time.Millisecond,
		BaseWindow:  50,
		WidenPerSec: 20,
		MaxWindow:   500,
		Batch:       200, // 压测：20 时每秒最多约 80 人进局，2000 人排队 p99 等 20s
	}
}

func loadDeckOrDefault(db *sql.DB, cat *deck.Catalog, username string) []string {
	cards, err := store.LoadDeck(db, username)
	if err == nil {
		if verr := deck.ValidateDeck(cat, deck.Deck{Owner: username, Cards: cards}); verr == nil {
			return cards
		} else {
			log.Printf("%s 的卡组已失效（%v),使用默认卡组", username, verr)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		log.Printf("读取 %s 的卡组失败（%v),使用默认卡组", username, err)
	}
	return defaultDeck(cat)
}

// defaultDeck 按卡表顺序每张放 2 张，最多 12 张（满足 ValidateDeck 的 8-12 张、单卡 ≤2）。
func defaultDeck(cat *deck.Catalog) []string {
	out := make([]string, 0, 12)
	for _, id := range cat.IDs() {
		for k := 0; k < 2 && len(out) < 12; k++ {
			out = append(out, id)
		}
	}
	return out
}

func routes(s *auth.Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", s.Login)
	mux.HandleFunc("/decks", s.Savedecks)
	mux.HandleFunc("/ws", s.ServerWs)
	mux.HandleFunc("/metrics", s.Metrics)
	mux.HandleFunc("GET /reports", s.ListReports)
	mux.HandleFunc("GET /reports/{room_id}/replay", s.ReplayReport)
	return mux
}

// toBattleReport 把房间交出来的 Report 转成要落库的一行。纯函数，方便单测。
func toBattleReport(cardsHash string, rep room.Report) (store.BattleReport, error) {
	d, err := json.Marshal(rep.InitDecks)
	if err != nil {
		return store.BattleReport{}, err
	}
	actions := rep.Actions
	if actions == nil {
		actions = []room.LogEntry{} // 开局后没人动就中止：存 [] 而不是 null，回放时按数组读
	}
	a, err := json.Marshal(actions)
	if err != nil {
		return store.BattleReport{}, err
	}
	return store.BattleReport{
		RoomID:        rep.RoomID,
		PlayerA:       rep.Players[0],
		PlayerB:       rep.Players[1],
		Winner:        rep.Winner,
		EndReason:     rep.Reason,
		Seed:          rep.Seed,
		Rounds:        rep.Rounds,
		InitDecksJSON: d,
		ActionsJSON:   a,
		CardsHash:     cardsHash,
		StartedAt:     rep.StartedAt,
		EndedAt:       rep.EndedAt,
	}, nil
}

// saveReport 在房间 goroutine 里被调用：出错只打日志，不能 log.Fatal 把整个服务带崩。
func saveReport(db *sql.DB, cardsHash string, rep room.Report) {
	br, err := toBattleReport(cardsHash, rep)
	if err != nil {
		log.Printf("room %s: 战报序列化失败 %v", rep.RoomID, err)
		return
	}
	// 落库用新的 ctx：reason=aborted 时房间 ctx 和 main 的 signal ctx 都已取消
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := store.SaveBattleReport(ctx, db, br); err != nil {
		log.Printf("room %s: 战报落库失败 %v", rep.RoomID, err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	//mysql conn
	db, err := openMysql()
	if err != nil {
		return err
	}
	defer db.Close()
	//redis conn
	rdb, err := openRedis(ctx)
	if err != nil {
		return err
	}
	defer rdb.Close()
	cat, err := deck.Load(cardPath)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(cardPath)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	cardsHash := hex.EncodeToString(sum[:])
	log.Printf("卡表加载完成 %d张", cat.Len())
	//objects in different packages
	s := &auth.Server{
		DB:     db,
		Cat:    cat,
		Tokens: &auth.RedisTokens{RDB: rdb},
		Conns:  map[string]*auth.WsClient{},

		AllowedOrigins: auth.ParseOrigins(os.Getenv("PET_ALLOWED_ORIGINS")),
		LoginLimiter:   newLoginLimiter(envOr("PET_LOGIN_RATE", "1")),

		Reports:   &store.Reports{DB: db},
		CardsHash: cardsHash,
	}
	mm := newMatcher(rdb)
	rooms := room.NewManager()
	s.MM = mm
	s.RoomMgr = rooms
	if s.MM == nil || s.RoomMgr == nil || s.Cat == nil {
		return errors.New("MM / RoomMgr/Cat 未接线")
	}
	mm.OnMatch = func(p match.Pair) {
		// 1. 取双方卡组（没存 / 已失效 → 默认卡组）
		deckA := loadDeckOrDefault(db, cat, p.A)
		deckB := loadDeckOrDefault(db, cat, p.B)
		// 2. 建对局状态。seed 打日志：出了问题可以用同一个 seed 复盘整局
		seed := time.Now().UnixNano()
		st, err := battle.NewState(p.RoomID, cat,
			battle.PlayerInit{ID: p.A, Cards: deckA},
			battle.PlayerInit{ID: p.B, Cards: deckB},
			seed)
		if err != nil {
			log.Printf("room %s: 开局失败 %v", p.RoomID, err)
			msg := []byte(`{"type":"error","code":"start_failed","msg":"开局失败，请重新匹配"}`)
			s.SendTo(p.A, msg)
			s.SendTo(p.B, msg)
			return
		}
		log.Printf("room %s: %s vs %s seed=%d", p.RoomID, p.A, p.B, seed)
		// 3. 建房间。★ A/B 顺序必须和 NewState 一致：Room.Players[i] 与 Battle.Players[i] 是同一个人
		rooms.Create(ctx, st, p.RoomID, p.A, p.B, s.SendTo, func(rep room.Report) {
			saveReport(db, cardsHash, rep)
		})
		s.NotifyMatch(p.A, p.B, p.RoomID)
	}
	go mm.Run(ctx)
	srv := &http.Server{Addr: httpAddr, Handler: routes(s)}
	go func() {
		log.Printf("listen:%s", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Println("server error:", err) // 端口被占等错误会走到这
			stop()                            // 让主流程醒过来，别傻等
		}
	}()

	<-ctx.Done()
	log.Println("shutting down…")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	rooms.Wait() // 等所有房间写完战报，再让 defer 关 DB
	log.Println("bye")
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
