package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DDCcheng/pet/internal/auth"
	"github.com/DDCcheng/pet/internal/match"
	"github.com/DDCcheng/pet/internal/store"
	"github.com/redis/go-redis/v9"
)

func main() {
	db, err := store.Open("root:pet@tcp(127.0.0.1:3307)/pet?parseTime=true&charset=utf8mb4")
	if err != nil {
		log.Fatalf("MySQL 连不上：%v\n请先执行 docker compose up -d", err)
	}
	s := &auth.Server{
		DB:     db,
		Tokens: map[string]string{},
		Conns:  map[string]*auth.WsClient{},
	}
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Redis 连不上 (%s)：%v\n请先执行 docker compose up -d", "127.0.0.1:6379", err)
	}
	mm := &match.Manager{
		RDB:         rdb,
		Tick:        500 * time.Millisecond,
		BaseWindow:  50,
		WidenPerSec: 20,
		MaxWindow:   500,
	}
	mm.OnMatch = func(p match.Pair) {
		s.NotifyMatch(p.A, p.B, p.RoomID) // 在 auth 包里加这个方法
	}
	s.MM = mm
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go mm.Run(ctx)
	mux := http.NewServeMux()
	mux.HandleFunc("/login", s.Login)
	mux.HandleFunc("/decks", s.Savedecks)
	mux.HandleFunc("/ws", s.ServerWs)
	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		log.Println("listen:8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Println("server error:", err) // 端口被占等错误会走到这
			stop()                            // 让主流程醒过来，别傻等
		}
	}()

	<-ctx.Done() // 阻塞在这里等 Ctrl+C
	log.Println("shutting down…")

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	log.Println("bye")
}
