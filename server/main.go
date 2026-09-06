package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/DDCcheng/pet/internal/auth"
	"github.com/DDCcheng/pet/internal/match"
	"github.com/DDCcheng/pet/internal/store"
	"github.com/redis/go-redis/v9"
)

func main() {
	db, _ := store.Open("root:pet@tcp(127.0.0.1:3307)/pet?parseTime=true&charset=utf8mb4")
	s := &auth.Server{
		DB:     db,
		Tokens: map[string]string{},
		Conns:  map[string]*auth.WsClient{},
	}
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatal(err)
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
	mux := http.NewServeMux()
	mux.HandleFunc("/login", s.Login)
	mux.HandleFunc("/decks", s.Savedecks)
	mux.HandleFunc("/ws", s.ServerWs)
	log.Println("listen:8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
