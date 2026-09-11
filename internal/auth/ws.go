package auth

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
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
	pingPeriod = pongWait * 9 / 10 // 54s，必须 < pongWait
	writeWait  = 10 * time.Second
	maxMsgSize = 32 << 10
)

type wsMsg struct {
	Type    string          `json:"type"`
	Payload string          `json:"payload,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// 包装一下conn，都写都在这里进行
type WsClient struct {
	srv    *Server
	conn   *websocket.Conn
	send   chan []byte
	quit   chan struct{}
	once   sync.Once
	roomId string
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
		case "join", "play_card", "end_turn":
			var p struct {
				RoomID string `json:"room_id"`
			}
			_ = json.Unmarshal(m.Data, &p)

			r, ok := c.srv.RoomMgr.Get(p.RoomID)
			if !ok {
				c.Send(mustJSON(wsMsg{Type: "error", Payload: "room not found"}))
				break
			}
			c.roomId = p.RoomID // 记下来，断线时要用
			r.Send(room.Cmd{PlayerId: username, Type: m.Type, Data: m.Data})
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
