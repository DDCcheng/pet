package auth

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DDCcheng/pet/internal/room"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

const (
	pongWait   = 60 * time.Second
	pingPeriod = pongWait * 9 / 10 // 必须 < pongWait
	writeWait  = 10 * time.Second
	maxMsgSize = 32 << 10
)

type wsMsg struct {
	Type    string          `json:"type"`
	Payload string          `json:"payload,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type WsClient struct {
	srv    *Server
	conn   *websocket.Conn
	send   chan []byte
	quit   chan struct{}
	once   sync.Once
	roomId string
	kicked atomic.Bool // 慢客户端只踢一次、只计一次
}

func (c *WsClient) close() {
	c.once.Do(func() { close(c.quit) })
}

func (c *WsClient) Send(b []byte) bool {
	select {
	case c.send <- b:
		return true
	case <-c.quit:
		return false
	default:
		// 写满说明对端读得太慢：丢掉这条并踢掉连接，房间协程和对手都不受影响
		sendDropped.Add(1)
		if c.kicked.CompareAndSwap(false, true) {
			slowKicked.Add(1)
			c.close()
		}
		return false
	}
}

func (s *Server) addConn(user string, c *WsClient) {
	s.mu.Lock()
	old := s.Conns[user]
	s.Conns[user] = c
	s.mu.Unlock()
	if old != nil {
		old.close() //
	}
}

func (s *Server) removeConn(user string, c *WsClient) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.Conns[user]; ok && cur == c {
		delete(s.Conns, user)
		return true
	}
	return false
}

// transfer to json format
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (c *WsClient) readLoop(username string) {
	lim := newLimiter(cmdRate, cmdBurst)
	c.conn.SetReadLimit(maxMsgSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			log.Println("read:", username, err)
			return
		}
		c.conn.SetReadDeadline(time.Now().Add(pongWait))

		var m wsMsg
		if err := json.Unmarshal(data, &m); err != nil {
			c.Send(mustJSON(wsMsg{Type: "error", Payload: "bad json"}))
			continue
		}

		switch m.Type {
		case "enqueue":
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			score := 1500.0
			if v, err := strconv.ParseFloat(m.Payload, 64); err == nil {
				score = v // 测试时可以带分数：{"type":"enqueue","payload":"1800"}
			}
			if err := c.srv.MM.Join(ctx, username, score); err != nil {
				log.Println("join:", err)
				c.Send(mustJSON(wsMsg{Type: "error", Payload: "enqueue failed"}))
			} else {
				c.Send(mustJSON(wsMsg{Type: "queued", Payload: username}))
			}
			cancel()
		case "join", "play_card", "end_turn", "attack":
			if !lim.Allow() {
				rateLimited.Add(1)
				c.Send(mustJSON(wsMsg{Type: "error", Payload: "rate_limited"}))
				break
			}
			var p struct {
				RoomID string `json:"room_id"`
			}
			_ = json.Unmarshal(m.Data, &p)
			r, ok := c.srv.RoomMgr.Get(p.RoomID)
			if !ok {
				c.Send(mustJSON(wsMsg{Type: "error", Payload: "room_not_found"}))
				break
			}
			if !r.Has(username) {
				log.Printf("ws: %s 尝试进入不属于自己的房间 %s", username, p.RoomID)
				c.Send(mustJSON(wsMsg{Type: "error", Payload: "not_in_room"}))
				break
			}
			c.roomId = p.RoomID
			if !r.Send(room.Cmd{PlayerId: username, Type: m.Type, Data: m.Data}) {
				c.Send(mustJSON(wsMsg{Type: "error", Payload: "room_busy_or_closed"}))
			}
		case "dequeue":
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = c.srv.MM.Leave(ctx, username)
			c.Send(mustJSON(wsMsg{Type: "dequeued"}))
			cancel()
		default:
			c.Send(mustJSON(wsMsg{Type: "error", Payload: "unknown type: " + m.Type}))
		}

	}
}

func (c *WsClient) writeLoop() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			if err := c.conn.WriteControl(websocket.PingMessage, nil,
				time.Now().Add(writeWait)); err != nil {
				return
			}

		case <-c.quit:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			c.conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
	}

}

func (s *Server) ServerWs(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	s.mu.RLock()
	username, ok := s.Tokens[token]
	s.mu.RUnlock()
	if !ok || token == "" {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}
	c := &WsClient{
		srv:  s,
		conn: conn,
		send: make(chan []byte, 256),
		quit: make(chan struct{}),
	}
	s.addConn(username, c)
	go c.writeLoop()
	welcome, _ := json.Marshal(wsMsg{Type: "welcome", Payload: username})
	c.Send(welcome)
	c.readLoop(username)
	if s.removeConn(username, c) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if s.MM != nil {
			_ = s.MM.Leave(ctx, username)
		}
		cancel()
		if c.roomId != "" {
			if r, ok := s.RoomMgr.Get(c.roomId); ok {
				r.Send(room.Cmd{PlayerId: username, Type: "leave"})
			}
		}
	}
	c.close()
}
