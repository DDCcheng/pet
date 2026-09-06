package match

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

type Manager struct {
	RDB         *redis.Client
	Tick        time.Duration // 撮合频率，500ms
	BaseWindow  float64       // 初始分差窗口，±50
	WidenPerSec float64       // 每等 1 秒放宽多少，20
	MaxWindow   float64       // 上限，±500
	OnMatch     func(p Pair)
}

const (
	keyRating = "mm:rating"
	keyTime   = "mm:time"
)

type Player struct {
	PlayerId string
	Score    float64
}

type Pair struct {
	A, B   string
	RoomID string
}

func (m *Manager) Join(ctx context.Context, playerId string, score float64) error {
	now := float64(time.Now().UnixMilli())
	if err := m.RDB.ZAddNX(ctx, keyRating, redis.Z{Score: score, Member: playerId}).Err(); err != nil {
		return err
	}
	return m.RDB.ZAddNX(ctx, keyTime, redis.Z{Score: now, Member: playerId}).Err()
}

func (m *Manager) restore(ctx context.Context, playerId string, score, enqueuedAt float64) error {
	// 用 ZAdd（不带 NX），把原来的分数和原来的时间戳写回去
	if err := m.RDB.ZAdd(ctx, keyRating, redis.Z{Score: score, Member: playerId}).Err(); err != nil {
		return err
	}
	return m.RDB.ZAdd(ctx, keyTime, redis.Z{Score: enqueuedAt, Member: playerId}).Err()
}

func (m *Manager) Leave(ctx context.Context, playerId string) error {
	pipe := m.RDB.TxPipeline()
	pipe.ZRem(ctx, keyRating, playerId)
	pipe.ZRem(ctx, keyTime, playerId)
	_, err := pipe.Exec(ctx)
	return err
}

func (m *Manager) take(ctx context.Context, playerId string) (bool, error) {
	n, err := m.RDB.ZRem(ctx, keyRating, playerId).Result()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	m.RDB.ZRem(ctx, keyTime, playerId)
	return true, nil
}

func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(m.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.matchOnce(ctx); err != nil {
				log.Println("Match:", err)
			}
		}
	}
}

func (m *Manager) matchOnce(ctx context.Context) error {
	now := float64(time.Now().UnixMilli())
	paired := map[string]bool{}
	zs, err := m.RDB.ZRangeWithScores(ctx, keyTime, 0, 19).Result()
	if err != nil {
		return err
	}
	for _, z := range zs {
		a := z.Member.(string)
		if paired[a] {
			continue
		}
		waited := (now - z.Score) / 1000
		w := m.BaseWindow + m.WidenPerSec*waited
		if w > m.MaxWindow {
			w = m.MaxWindow
		}

		aScore, err := m.RDB.ZScore(ctx, keyRating, a).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return err
		}
		ids, err := m.RDB.ZRangeArgs(ctx, redis.ZRangeArgs{
			Key:     keyRating,
			Start:   aScore - w,
			Stop:    aScore + w,
			ByScore: true,
			Count:   10,
		}).Result()
		if err != nil {
			return err
		}
		b := ""
		for _, tem := range ids {
			if tem != a && !paired[tem] {
				b = tem
				break
			}
		}
		if b == "" {
			continue
		}

		oka, err := m.take(ctx, a)
		if err != nil {
			return err
		}
		if !oka {
			continue
		}
		okb, _ := m.take(ctx, b)
		if !okb {
			m.restore(ctx, a, aScore, z.Score)
			continue
		}

		paired[a], paired[b] = true, true
		roomId := fmt.Sprintf("%s-%s-%d", a, b, time.Now().UnixNano())
		if m.OnMatch != nil {
			m.OnMatch(Pair{A: a, B: b, RoomID: roomId})
		}
	}
	return nil
}
