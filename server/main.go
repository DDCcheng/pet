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
	//objects in different packages
	s := &auth.Server{
		DB:     db,
		Tokens: map[string]string{},
		Conns:  map[string]*auth.WsClient{},
	}
	mm := newMatcher(rdb)
	rooms := room.NewManager()
	s.MM = mm
	s.RoomMgr = rooms
	if s.MM == nil || s.RoomMgr == nil {
		return errors.New("MM / RoomMgr 未接线")
	}
	mm.OnMatch = func(p match.Pair) {
		rooms.Create(ctx, p.RoomID, p.A, p.B, s.SendTo)
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
