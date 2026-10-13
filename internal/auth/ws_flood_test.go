package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DDCcheng/pet/internal/room"
	"github.com/gorilla/websocket"
)

// 真 WebSocket：alice 狂刷指令被限速，bob 的 join 照常进房间
func TestFloodingPlayerLimitedOpponentUnaffected(t *testing.T) {
	s := &Server{
		Tokens:  map[string]string{"ta": "alice", "tb": "bob"},
		Conns:   map[string]*WsClient{},
		RoomMgr: room.NewManager(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.RoomMgr.Create(ctx, nil, "r1", "alice", "bob", s.SendTo, nil) // Battle=nil：对局指令会回"对局未开始"

	ts := httptest.NewServer(http.HandlerFunc(s.ServerWs))
	defer ts.Close()
	dial := func(token string) *websocket.Conn {
		u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/?token=" + token
		c, _, err := websocket.DefaultDialer.Dial(u, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		var m wsMsg
		if err := c.ReadJSON(&m); err != nil || m.Type != "welcome" {
			t.Fatalf("没收到 welcome: %v %+v", err, m)
		}
		return c
	}
	alice, bob := dial("ta"), dial("tb")

	r0 := RateLimited()
	const flood = 100
	cmd := []byte(`{"type":"end_turn","data":{"room_id":"r1"}}`)
	for i := 0; i < flood; i++ {
		if err := alice.WriteMessage(websocket.TextMessage, cmd); err != nil {
			t.Fatal(err)
		}
	}
	// alice 每条都会收到一个回复：要么 rate_limited，要么房间回的错误
	limited, inRoom := 0, 0
	for i := 0; i < flood; i++ {
		_, b, err := alice.ReadMessage()
		if err != nil {
			t.Fatalf("第 %d 条回复读失败: %v", i, err)
		}
		var m struct{ Type, Payload, Code string }
		json.Unmarshal(b, &m)
		switch {
		case m.Type == "error" && m.Payload == "rate_limited":
			limited++
		case m.Type == "error" && m.Code != "":
			inRoom++
		default:
			t.Fatalf("意外消息 %s", b)
		}
	}
	if inRoom > int(cmdBurst)+2 || limited < flood-int(cmdBurst)-2 {
		t.Fatalf("进房间 %d 条、被限 %d 条，限速没生效", inRoom, limited)
	}
	if d := RateLimited() - r0; d != int64(limited) {
		t.Fatalf("计数 %d 和 alice 收到的 rate_limited %d 对不上", d, limited)
	}

	// bob 的指令不受 alice 影响
	bob.WriteMessage(websocket.TextMessage, []byte(`{"type":"join","data":{"room_id":"r1"}}`))
	var st struct{ Type, Status string }
	if err := bob.ReadJSON(&st); err != nil || st.Type != "state" {
		t.Fatalf("bob join 后应收到房间状态: %v %+v", err, st)
	}
}
