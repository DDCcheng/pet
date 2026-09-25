package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
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

func openMysql() (*sql.DB, error) {
	db, err := store.Open(mysqlDSN)
	if err != nil {
		log.Fatalf("MySQL 连不上：%v\n请先执行 docker compose up -d", err)
	}
	return db, err
}

func openRedis(ctx context.Context) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("Redis 连不上 %s（先跑 docker compose up -d）：%w", redisAddr, err)
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
	}
}

// loadDeckOrDefault 读玩家保存的卡组；没存过、读库失败、或卡表改过导致不合法，都退回默认卡组。
// ★ 保存时校验过不代表现在还合法（卡表可能改了），开局前再校验一次。
func loadDeckOrDefault(db *sql.DB, cat *deck.Catalog, username string) []string {
	cards, err := store.LoadDeck(db, username)
	if err == nil {
		if verr := deck.ValidateDeck(cat, deck.Deck{Owner: username, Cards: cards}); verr == nil {
			return cards
		} else {
			log.Printf("%s 的卡组已失效（%v），使用默认卡组", username, verr)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		log.Printf("读取 %s 的卡组失败（%v），使用默认卡组", username, err)
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
	return mux
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
	log.Printf("卡表加载完成 %d张", cat.Len())
	//objects in different packages
	s := &auth.Server{
		DB:     db,
		Cat:    cat,
		Tokens: map[string]string{},
		Conns:  map[string]*auth.WsClient{},
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
		rooms.Create(ctx, st, p.RoomID, p.A, p.B, s.SendTo)
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
	log.Println("bye")
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
