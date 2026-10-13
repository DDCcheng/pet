package auth

import (
	"bufio"
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/DDCcheng/pet/internal/room"
)

// parseMetrics 把 "name value" 每行解析成 map
func parseMetrics(t *testing.T, body string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			t.Fatalf("格式不对: %q", sc.Text())
		}
		v, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			t.Fatalf("值不是整数: %q", sc.Text())
		}
		out[f[0]] = v
	}
	return out
}

func TestMetricsEndpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{Conns: map[string]*WsClient{"alice": fakeClient(1), "bob": fakeClient(1)}, RoomMgr: room.NewManager()}
	s.RoomMgr.Create(ctx, nil, "r1", "alice", "bob", nil, nil)
	s.RoomMgr.Create(ctx, nil, "r2", "carol", "dave", nil, nil)

	// 先制造一次 send 写满：alice 的缓冲只有 1
	before := parseMetrics(t, get(t, s))
	s.SendTo("alice", []byte("1"))
	s.SendTo("alice", []byte("2"))

	rec := httptest.NewRecorder()
	s.Metrics(rec, httptest.NewRequest("GET", "/metrics", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type=%q", ct)
	}
	m := parseMetrics(t, rec.Body.String())
	for _, k := range []string{"pet_rooms_active", "pet_ws_conns", "pet_inbox_dropped_total",
		"pet_send_dropped_total", "pet_slow_kicked_total", "pet_rate_limited_total"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("缺指标 %s\n%s", k, rec.Body.String())
		}
	}
	if m["pet_rooms_active"] != 2 || m["pet_ws_conns"] != 2 {
		t.Fatalf("rooms=%d conns=%d want 2/2", m["pet_rooms_active"], m["pet_ws_conns"])
	}
	if d := m["pet_send_dropped_total"] - before["pet_send_dropped_total"]; d != 1 {
		t.Fatalf("send_dropped 增加 %d want 1", d)
	}
	if d := m["pet_slow_kicked_total"] - before["pet_slow_kicked_total"]; d != 1 {
		t.Fatalf("slow_kicked 增加 %d want 1", d)
	}
}

func get(t *testing.T, s *Server) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Metrics(rec, httptest.NewRequest("GET", "/metrics", nil))
	return rec.Body.String()
}
