package main

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DDCcheng/pet/internal/auth"
	"github.com/DDCcheng/pet/internal/battle"
	"github.com/DDCcheng/pet/internal/room"
)

// 进程内端到端：真 auth.Server + 真 WebSocket + 真房间，跳过 MySQL/Redis（直接发 match_found）。
// 两个 bot 打完一整局，双方看到同一个胜者，没有非法动作，战报也交出来了。
func TestBotsPlayFullGame(t *testing.T) {
	cat := loadCat(t)
	s := &auth.Server{
		Tokens:  auth.NewMemTokens(time.Hour, map[string]string{"ta": "bot1", "tb": "bot2"}),
		Conns:   map[string]*auth.WsClient{},
		RoomMgr: room.NewManager(),
		Cat:     cat,
	}
	ts := httptest.NewServer(routesForTest(s))
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")

	newBot := func(name, tok string, seed int64) *client {
		conn, err := dial(addr, tok)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return &client{name: name, conn: conn, cat: cat, rng: rand.New(rand.NewSource(seed)), delay: 0}
	}
	a, b := newBot("bot1", "ta", 1), newBot("bot2", "tb", 2)

	d := []string{"slime", "slime", "wolf", "wolf", "knight", "knight", "dragon", "dragon", "bolt", "bolt", "heal", "heal"}
	st, err := battle.NewState("e2e", cat, battle.PlayerInit{ID: "bot1", Cards: d}, battle.PlayerInit{ID: "bot2", Cards: d}, 7)
	if err != nil {
		t.Fatal(err)
	}
	reps := make(chan room.Report, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s.RoomMgr.Create(ctx, st, "e2e", "bot1", "bot2", s.SendTo, func(r room.Report) { reps <- r })
	s.NotifyMatch("bot1", "bot2", "e2e")

	type out struct {
		res result
		err error
	}
	ch := make(chan out, 2)
	for _, c := range []*client{a, b} {
		go func() { r, err := c.playOne(ctx); ch <- out{r, err} }()
	}
	var rs []result
	for i := 0; i < 2; i++ {
		o := <-ch
		if o.err != nil {
			t.Fatal(o.err)
		}
		rs = append(rs, o.res)
	}
	if rs[0].Winner == "" || rs[0].Winner != rs[1].Winner {
		t.Fatalf("两边看到的胜者不一致: %q vs %q", rs[0].Winner, rs[1].Winner)
	}
	if rs[0].Illegal+rs[1].Illegal != 0 {
		t.Fatalf("有非法动作: %d / %d", rs[0].Illegal, rs[1].Illegal)
	}
	select {
	case rep := <-reps:
		if rep.Reason != "finished" || rep.Winner != rs[0].Winner {
			t.Fatalf("战报 reason=%q winner=%q", rep.Reason, rep.Winner)
		}
		// 战报里的动作数 = 两个 bot 被房间接受的动作数（限速重发的那次只算一次）
		if got, want := len(rep.Actions), rs[0].Actions+rs[1].Actions; got != want {
			t.Fatalf("战报 %d 个动作，bot 发了 %d 个", got, want)
		}
		// 只靠战报重放整局：每一步都要被接受，终局要和房间里的一模一样
		steps, err := room.Steps(rep.Actions)
		if err != nil {
			t.Fatal(err)
		}
		got, err := battle.Replay(cat, rep.RoomID,
			battle.PlayerInit{ID: rep.Players[0], Cards: rep.InitDecks[0]},
			battle.PlayerInit{ID: rep.Players[1], Cards: rep.InitDecks[1]},
			rep.Seed, steps, nil)
		if err != nil {
			t.Fatalf("重放失败: %v", err)
		}
		if !reflect.DeepEqual(got.GodView(), st.GodView()) {
			t.Fatalf("重放终局和房间不一致:\n房间 %+v\n重放 %+v", st.GodView(), got.GodView())
		}
		if got.Winner != rep.Winner || got.DisplayRound() != rep.Rounds {
			t.Fatalf("重放 winner=%q rounds=%d，战报 %q %d", got.Winner, got.DisplayRound(), rep.Winner, rep.Rounds)
		}
	case <-time.After(time.Second):
		t.Fatal("没交战报")
	}
	t.Logf("winner=%s actions=%d/%d limited=%d/%d", rs[0].Winner, rs[0].Actions, rs[1].Actions, rs[0].Limited, rs[1].Limited)
}

func routesForTest(s *auth.Server) *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/ws", s.ServerWs)
	return m
}
