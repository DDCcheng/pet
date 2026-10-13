package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/DDCcheng/pet/internal/battle"
	"github.com/DDCcheng/pet/internal/deck"
	"github.com/gorilla/websocket"
)

// inbound 是服务端各种消息的并集
type inbound struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	RoomID  string `json:"room_id"`
	Code    string `json:"code"`
	Msg     string `json:"msg"`
	Winner  string `json:"winner"`
	battle.View
}

type client struct {
	name  string
	conn  *websocket.Conn
	cat   *deck.Catalog
	rng   *rand.Rand
	delay time.Duration // 每个动作前等一下，像人一样；也别撞服务端的 10 条/秒限速

	// 压测用的钩子，普通模式下为 nil
	enqueuedAt func() time.Time        // 本局 enqueue 的时间
	onMatch    func(wait time.Duration) // enqueue → match_found
	onRTT      func(rtt time.Duration)  // 发出动作 → 收到下一份 game_state
}

// result 是一局的统计
type result struct {
	RoomID  string
	Winner  string
	Actions int // 发出去的动作数
	Illegal int // 被房间拒掉的动作数（理想是 0）
	Limited int // 被限速拒掉的次数
}

func (c *client) send(typ string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return c.conn.WriteJSON(map[string]any{"type": typ, "data": json.RawMessage(b)})
}

func (c *client) sendAction(roomID string, a battle.Action) error {
	return c.send(a.Type, map[string]string{
		"room_id": roomID, "card_id": a.CardID, "inst_id": a.InstID, "target": a.Target,
	})
}

// playOne 等 match_found → join → 轮到自己就出一个动作 → 直到 game_over。
// 调用前要么已经 enqueue，要么由别处（测试里）直接推 match_found。
func (c *client) playOne(ctx context.Context) (result, error) {
	var res result
	var last battle.Action
	var lastSeq uint64
	var sentAt time.Time // 非零表示有一个动作在等服务端回状态
	for {
		if dl, ok := ctx.Deadline(); ok {
			c.conn.SetReadDeadline(dl)
		}
		var m inbound
		if err := c.conn.ReadJSON(&m); err != nil {
			return res, fmt.Errorf("%s read: %w", c.name, err)
		}
		switch m.Type {
		case "match_found":
			res.RoomID = m.RoomID
			if c.onMatch != nil && c.enqueuedAt != nil {
				c.onMatch(time.Since(c.enqueuedAt()))
			}
			if err := c.send("join", map[string]string{"room_id": m.RoomID}); err != nil {
				return res, err
			}
		case "game_state":
			if !sentAt.IsZero() && m.Seq > lastSeq {
				if c.onRTT != nil {
					c.onRTT(time.Since(sentAt))
				}
				sentAt = time.Time{}
			}
			if m.Seq <= lastSeq || m.Over || !m.YourTurn {
				lastSeq = max(lastSeq, m.Seq)
				continue
			}
			lastSeq = m.Seq
			time.Sleep(c.delay)
			last = chooseAction(m.View, c.cat, c.rng)
			res.Actions++
			sentAt = time.Now()
			if err := c.sendAction(res.RoomID, last); err != nil {
				return res, err
			}
		case "error":
			switch {
			case m.Payload == "rate_limited":
				res.Limited++
				time.Sleep(200 * time.Millisecond)
				sentAt = time.Now() // 限速等待不算进 RTT
				if err := c.sendAction(res.RoomID, last); err != nil {
					return res, err
				}
			case m.Code != "":
				// 房间拒了这个动作：记下来，结束回合免得卡住
				res.Illegal++
				log.Printf("%s: 动作 %+v 被拒: %s %s", c.name, last, m.Code, m.Msg)
				last = battle.Action{Type: "end_turn"}
				sentAt = time.Now()
				if err := c.sendAction(res.RoomID, last); err != nil {
					return res, err
				}
			default:
				return res, fmt.Errorf("%s: 服务端错误 %s", c.name, m.Payload)
			}
		case "game_over":
			res.Winner = m.Winner
			return res, nil
		}
	}
}
