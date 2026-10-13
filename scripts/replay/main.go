// replay 从 MySQL 读战报，在本地重放并核对。
//
//	go run ./scripts/replay -room <room_id>   打印这一局的逐帧时间轴并校验
//	go run ./scripts/replay -all              校验库里所有战报，输出汇总；有不一致时退出码为 1
//
// 和 GET /reports/{room_id}/replay 用的是同一个 replay.Build，所以这里校验通过的战报，前端都能回放。
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/DDCcheng/pet/internal/battle"
	"github.com/DDCcheng/pet/internal/deck"
	"github.com/DDCcheng/pet/internal/replay"
	"github.com/DDCcheng/pet/internal/store"
)

func main() {
	dsn := flag.String("dsn", "root:pet@tcp(127.0.0.1:3307)/pet?parseTime=true", "MySQL DSN（要带 parseTime=true）")
	cards := flag.String("cards", "configs/cards.yaml", "卡表路径，哈希要和战报里的 cards_hash 一致")
	roomID := flag.String("room", "", "只看这一局：打印时间轴并校验")
	all := flag.Bool("all", false, "校验全部战报")
	workers := flag.Int("workers", runtime.NumCPU(), "-all 的并发数")
	flag.Parse()
	if (*roomID == "") == !*all {
		fmt.Fprintln(os.Stderr, "用法：-room <room_id> 或 -all（二选一）")
		os.Exit(2)
	}

	cat, err := deck.Load(*cards)
	if err != nil {
		log.Fatal(err)
	}
	raw, err := os.ReadFile(*cards)
	if err != nil {
		log.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	cardsHash := hex.EncodeToString(sum[:])

	db, err := sql.Open("mysql", *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	reports := &store.Reports{DB: db}
	ctx := context.Background()

	if *roomID != "" {
		os.Exit(one(ctx, reports, cat, cardsHash, *roomID))
	}
	os.Exit(verifyAll(ctx, reports, cat, cardsHash, *workers))
}

func one(ctx context.Context, reports *store.Reports, cat *deck.Catalog, cardsHash, roomID string) int {
	r, err := reports.GetReport(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		fmt.Fprintf(os.Stderr, "没有这局：%s\n", roomID)
		return 1
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s  %s(先手) vs %s  seed=%d  %s  胜者=%q  %d 回合\n",
		r.RoomID, r.PlayerA, r.PlayerB, r.Seed, r.EndReason, r.Winner, r.Rounds)
	res, err := replay.Build(cat, cardsHash, r)
	if err != nil {
		fmt.Printf("校验失败：%v\n", err)
		return 1
	}
	for _, f := range res.Frames {
		fmt.Println(frameLine(f))
	}
	kind := "每步校验和一致"
	if !hasChecksums(r) {
		kind = "老战报没有逐步校验和，只核对了终局"
	}
	fmt.Printf("校验通过：%d 帧，%s\n", len(res.Frames), kind)
	return 0
}

func frameLine(f replay.Frame) string {
	var act string
	switch {
	case f.Action == nil:
		act = "开局"
	case f.Action.Type == "play_card":
		act = "出牌 " + f.Action.CardID
	case f.Action.Type == "attack":
		act = fmt.Sprintf("%s 攻击 %s", f.Action.InstID, f.Action.Target)
	case f.Auto:
		act = "超时，自动结束回合"
	default:
		act = "结束回合"
	}
	if f.Player != "" {
		act = f.Player + " " + act
	}
	side := func(p battle.PlayerView) string {
		board := make([]string, len(p.Board))
		for k, m := range p.Board {
			board[k] = fmt.Sprintf("%s:%d/%d", m.CardID, m.Atk, m.HP)
		}
		return fmt.Sprintf("%s hp%-2d 费%d/%d 手%d 库%d [%s]",
			p.ID, p.HP, p.Mana, p.MaxMana, len(p.Hand), p.DeckCount, strings.Join(board, " "))
	}
	s := f.State
	return fmt.Sprintf("#%-3d %6.1fs  %-28s | %s | %s",
		f.Index, float64(f.AtMs)/1000, act, side(s.Players[0]), side(s.Players[1]))
}

func hasChecksums(r store.BattleReport) bool {
	return bytes.Contains(r.ActionsJSON, []byte(`"h":`))
}

type outcome struct {
	room string
	kind string // ok / ok_legacy / cards_changed / mismatch / error
	err  error
}

func verifyAll(ctx context.Context, reports *store.Reports, cat *deck.Catalog, cardsHash string, workers int) int {
	start := time.Now()
	ids, err := reports.AllRoomIDs(ctx)
	if err != nil {
		log.Fatal(err)
	}
	jobs := make(chan string)
	out := make(chan outcome)
	var wg sync.WaitGroup
	for w := 0; w < max(workers, 1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				out <- check(ctx, reports, cat, cardsHash, id)
			}
		}()
	}
	go func() {
		for _, id := range ids {
			jobs <- id
		}
		close(jobs)
		wg.Wait()
		close(out)
	}()

	count := map[string]int{}
	var bad []outcome
	for o := range out {
		count[o.kind]++
		if o.kind != "ok" && o.kind != "ok_legacy" {
			bad = append(bad, o)
		}
	}
	sort.Slice(bad, func(i, j int) bool { return bad[i].room < bad[j].room })

	fmt.Printf("战报 %d 局，用时 %s\n", len(ids), time.Since(start).Round(time.Millisecond))
	fmt.Printf("  通过（逐步校验和）  %d\n", count["ok"])
	fmt.Printf("  通过（老战报，只核对终局）%d\n", count["ok_legacy"])
	fmt.Printf("  卡表已变，跳过      %d\n", count["cards_changed"])
	fmt.Printf("  不一致              %d\n", count["mismatch"])
	fmt.Printf("  读取/解析错误       %d\n", count["error"])
	for k, o := range bad {
		if o.kind == "cards_changed" {
			continue
		}
		if k >= 20 {
			fmt.Printf("  …还有 %d 条\n", len(bad)-k)
			break
		}
		fmt.Printf("  [%s] %s: %v\n", o.kind, o.room, o.err)
	}
	if count["mismatch"]+count["error"] > 0 {
		return 1
	}
	return 0
}

func check(ctx context.Context, reports *store.Reports, cat *deck.Catalog, cardsHash, id string) outcome {
	r, err := reports.GetReport(ctx, id)
	if err != nil {
		return outcome{id, "error", err}
	}
	_, err = replay.Build(cat, cardsHash, r)
	switch {
	case err == nil && hasChecksums(r):
		return outcome{id, "ok", nil}
	case err == nil:
		return outcome{id, "ok_legacy", nil}
	case errors.Is(err, replay.ErrCardsChanged):
		return outcome{id, "cards_changed", err}
	case errors.Is(err, replay.ErrMismatch):
		return outcome{id, "mismatch", err}
	default:
		return outcome{id, "error", err}
	}
}
