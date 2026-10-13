package auth

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCheckOrigin(t *testing.T) {
	req := func(host, origin string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+"/ws", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	same := &Server{}
	list := &Server{AllowedOrigins: ParseOrigins("http://localhost:5173, https://pet.example.com/")}

	cases := []struct {
		name string
		s    *Server
		r    *http.Request
		want bool
	}{
		{"no origin (bot)", list, req("pet.example.com", ""), true},
		{"same origin, no whitelist", same, req("game.local:8080", "http://game.local:8080"), true},
		{"other origin, no whitelist", same, req("game.local:8080", "http://evil.com"), false},
		{"whitelisted", list, req("api.example.com", "http://localhost:5173"), true},
		{"whitelisted, trailing slash in config", list, req("api.example.com", "https://pet.example.com"), true},
		{"case-insensitive", list, req("api.example.com", "HTTPS://PET.EXAMPLE.COM"), true},
		{"wrong port", list, req("api.example.com", "http://localhost:5174"), false},
		{"wrong scheme", list, req("api.example.com", "http://pet.example.com"), false},
		{"suffix trick", list, req("api.example.com", "https://pet.example.com.evil.com"), false},
		{"null origin", list, req("api.example.com", "null"), false},
	}
	for _, c := range cases {
		if got := c.s.checkOrigin(c.r); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestParseOrigins(t *testing.T) {
	got := ParseOrigins(" http://a.com/ ,, https://b.com:8443 ")
	want := []string{"http://a.com", "https://b.com:8443"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if ParseOrigins("") != nil {
		t.Fatal("empty env should give no whitelist")
	}
}

// 真握手：白名单外的浏览器来源拿不到 WebSocket
func TestWsRejectsForeignOrigin(t *testing.T) {
	s := &Server{
		Tokens:         NewMemTokens(time.Hour, map[string]string{"ta": "alice"}),
		Conns:          map[string]*WsClient{},
		AllowedOrigins: []string{"http://localhost:5173"},
	}
	ts := httptest.NewServer(http.HandlerFunc(s.ServerWs))
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=ta"

	_, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://evil.com"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("evil origin: err=%v resp=%v, want 403", err, resp)
	}

	c, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://localhost:5173"}})
	if err != nil {
		t.Fatalf("allowed origin: %v", err)
	}
	c.Close()

	c, _, err = websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("no origin: %v", err)
	}
	c.Close()
}

// 外站来源在查 token 之前就被拒：Tokens 是 nil，走到 Lookup 会 panic
func TestForeignOriginRejectedBeforeTokenLookup(t *testing.T) {
	s := &Server{AllowedOrigins: []string{"http://localhost:5173"}}
	r := httptest.NewRequest(http.MethodGet, "/ws?token=x", nil)
	r.Header.Set("Origin", "http://evil.com")
	rec := httptest.NewRecorder()
	s.ServerWs(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code %d, want 403", rec.Code)
	}
}
