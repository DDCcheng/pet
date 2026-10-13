package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIPLimiterPerIP(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewIPLimiter(1, 10)
	l.now = func() time.Time { return now }

	for i := 0; i < 10; i++ {
		if !l.Allow("1.1.1.1") {
			t.Fatalf("burst request %d rejected", i)
		}
	}
	if l.Allow("1.1.1.1") {
		t.Fatal("11th request in the same second allowed")
	}
	if !l.Allow("2.2.2.2") {
		t.Fatal("another IP should have its own bucket")
	}
	now = now.Add(time.Second)
	if !l.Allow("1.1.1.1") {
		t.Fatal("should refill 1 token per second")
	}
	if l.Allow("1.1.1.1") {
		t.Fatal("only 1 token refilled")
	}
}

func TestIPLimiterSweepsIdle(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewIPLimiter(1, 10)
	l.now = func() time.Time { return now }
	for i := 0; i < 100; i++ {
		l.Allow("10.0.0." + string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}
	if l.size() != 100 {
		t.Fatalf("size %d", l.size())
	}
	now = now.Add(ipIdle + 2*time.Minute)
	l.Allow("9.9.9.9") // 触发清理
	if l.size() != 1 {
		t.Fatalf("idle IPs not swept, size %d", l.size())
	}
}

func TestClientIPIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.RemoteAddr = "203.0.113.7:51234"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("got %q", got)
	}
}

// 超速的登录在查库之前就被拒：Server.DB 是 nil，走到查库会直接 panic
func TestLoginRateLimitedBeforeDB(t *testing.T) {
	s := &Server{LoginLimiter: NewIPLimiter(1, 0)} // burst 0：第一次就拒
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"alice","password":"x"}`))
	rec := httptest.NewRecorder()
	s.Login(rec, r)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("code %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestLoginBodyTooLarge(t *testing.T) {
	s := &Server{}
	big := `{"username":"` + strings.Repeat("a", 100<<10) + `"}`
	rec := httptest.NewRecorder()
	s.Login(rec, httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(big)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code %d, want 400", rec.Code)
	}
}
