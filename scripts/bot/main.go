// bot：两个机器人账号自己登录、排队、对打 N 局，用来端到端验证服务和压 /metrics。
//
//	go run ./scripts/bot -games 5
//	go run ./scripts/bot -users bot1,bot2,bot3,bot4 -games 10   # 4 个 bot 同时排队
//	go run ./scripts/bot -load -players 1000 -games 2           # 压测：自动建 load0..load999 账号
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/DDCcheng/pet/internal/deck"
	"github.com/gorilla/websocket"
)

func login(addr, user, pass string) (string, error) {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := http.Post("http://"+addr+"/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var r struct{ Token, Error string }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.Token == "" {
		return "", fmt.Errorf("login %s: %s", user, r.Error)
	}
	return r.Token, nil
}

func dial(addr, token string) (*websocket.Conn, error) {
	c, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws?token="+token, nil)
	if err != nil {
		return nil, err
	}
	var m inbound
	if err := c.ReadJSON(&m); err != nil || m.Type != "welcome" {
		c.Close()
		return nil, fmt.Errorf("没收到 welcome: %v %+v", err, m)
	}
	return c, nil
}

func main() {
	addr := flag.String("addr", "localhost:8080", "服务地址")
	users := flag.String("users", "bot1,bot2", "bot 账号，逗号分隔（seed.sql 里有 bot1-bot4）")
	pass := flag.String("pass", "pet", "密码")
	games := flag.Int("games", 3, "每个 bot 打几局")
	delay := flag.Duration("delay", 150*time.Millisecond, "每个动作前的停顿")
	cards := flag.String("cards", "configs/cards.yaml", "卡表路径")
	timeout := flag.Duration("timeout", 3*time.Minute, "单局超时")
	load := flag.Bool("load", false, "压测模式")
	players := flag.Int("players", 200, "压测：并发玩家数（偶数）")
	prefix := flag.String("prefix", "load", "压测：账号前缀")
	ramp := flag.Duration("ramp", 5*time.Second, "压测：在这段时间内均匀登录")
	dsn := flag.String("dsn", "root:pet@tcp(127.0.0.1:3307)/pet", "压测：用来建账号的 MySQL，空串表示不建")
	jsonOut := flag.String("json", "", "压测：结果另存为 JSON")
	flag.Parse()

	if *load {
		rep, err := runLoad(loadConfig{
			Addr: *addr, Players: *players, Prefix: *prefix, Pass: *pass, Games: *games,
			Delay: *delay, Ramp: *ramp, Timeout: *timeout, DSN: *dsn, JSONOut: *jsonOut, CardPath: *cards,
		})
		if err != nil {
			log.Fatal(err)
		}
		rep.print()
		if rep.Failed > 0 {
			os.Exit(1)
		}
		return
	}

	cat, err := deck.Load(*cards)
	if err != nil {
		log.Fatal(err)
	}
	names := strings.Split(*users, ",")
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, fails, illegal := map[string]int{}, 0, 0
	start := time.Now()
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := login(*addr, name, *pass)
			if err != nil {
				log.Printf("%s: %v", name, err)
				mu.Lock()
				fails += *games
				mu.Unlock()
				return
			}
			conn, err := dial(*addr, tok)
			if err != nil {
				log.Printf("%s: %v", name, err)
				mu.Lock()
				fails += *games
				mu.Unlock()
				return
			}
			defer conn.Close()
			c := &client{name: name, conn: conn, cat: cat, rng: rand.New(rand.NewSource(time.Now().UnixNano() + int64(i))), delay: *delay}
			for g := 0; g < *games; g++ {
				if err := c.conn.WriteJSON(map[string]string{"type": "enqueue"}); err != nil {
					log.Printf("%s: enqueue: %v", name, err)
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), *timeout)
				res, err := c.playOne(ctx)
				cancel()
				mu.Lock()
				if err != nil {
					fails++
					log.Printf("%s 第 %d 局失败: %v", name, g+1, err)
				} else {
					wins[res.Winner]++
					illegal += res.Illegal
					log.Printf("%s 第 %d 局 room=%s winner=%s actions=%d illegal=%d limited=%d",
						name, g+1, res.RoomID, res.Winner, res.Actions, res.Illegal, res.Limited)
				}
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}()
	}
	wg.Wait()
	fmt.Printf("\n完成：%d 个 bot × %d 局，用时 %v，失败 %d，非法动作 %d\n", len(names), *games, time.Since(start).Round(time.Millisecond), fails, illegal)
	fmt.Printf("胜场（每局两边各记一次）：%v\n", wins)
	if fails > 0 {
		log.Fatal("有对局失败")
	}
}
