package main

import (
	"log"
	"net/http"

	"github.com/DDCcheng/pet/internal/auth"
	"github.com/DDCcheng/pet/internal/store"
)

func main() {
	db, _ := store.Open("root:pet@tcp(127.0.0.1:3307)/pet?parseTime=true&charset=utf8mb4")
	s := &auth.Server{
		DB:     db,
		Tokens: map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/login", s.Login)
	mux.HandleFunc("/decks", s.Savedecks)
	log.Println("listen:8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
