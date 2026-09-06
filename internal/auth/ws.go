package auth

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

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
	Type    string `json:"type"`
	Payload string `json:"payload,omitempty"`
}

// 包装一下conn，都写都在这里进行
type WsClient struct {
	conn *websocket.Conn
	send chan []byte
	quit chan struct{}
	once sync.Once
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

func (s *Server) removeConn(user string, c *WsClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.Conns[user]; ok && cur == c {
		delete(s.Conns, user)
	}
}

func (s *Server) ServerWs(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	s.mu.Lock()
	username, ok := s.Tokens[token]
	s.mu.Unlock()
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
		conn: conn,
		send: make(chan []byte, 256),
		quit: make(chan struct{}),
	}
	s.addConn(username, c)
	go c.writeLoop()
	welcome, _ := json.Marshal(wsMsg{Type: "welcome", Payload: username})
	c.Send(welcome)
	c.readLoop(username)
	s.removeConn(username, c)
	c.close()
}

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

		log.Printf("%s says %s", username, data)
		c.Send(data)
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
