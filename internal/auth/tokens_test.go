package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestNewTokenRandomAndURLSafe(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		tok, err := newToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 43 {
			t.Fatalf("len %d, want 43 (32 bytes base64url)", len(tok))
		}
		if strings.ContainsAny(tok, "+/=&?%") {
			t.Fatalf("token not URL-safe: %q", tok)
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
	}
}

func TestMemTokensExpire(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1000, 0)
	m := NewMemTokens(time.Hour, nil)
	m.now = func() time.Time { return now }

	tok, _ := m.Issue(ctx, "alice")
	if u, err := m.Lookup(ctx, tok); err != nil || u != "alice" {
		t.Fatalf("fresh token: %q %v", u, err)
	}
	now = now.Add(time.Hour)
	if _, err := m.Lookup(ctx, tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token: err=%v", err)
	}
	if _, err := m.Lookup(ctx, "forged"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("forged token: err=%v", err)
	}
	if _, err := m.Lookup(ctx, ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("empty token: err=%v", err)
	}
}

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 14}) // DB 14 专供 auth 测试；match 用 15，go test ./... 会并行跑两个包，共用一个库会互相 FlushDB
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("跳过：连不上 Redis (%s)：%v", addr, err)
	}
	rdb.FlushDB(ctx)
	t.Cleanup(func() { rdb.FlushDB(ctx); rdb.Close() })
	return rdb
}

func TestRedisTokens(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	ts := &RedisTokens{RDB: rdb}

	tok, err := ts.Issue(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if u, err := ts.Lookup(ctx, tok); err != nil || u != "alice" {
		t.Fatalf("lookup: %q %v", u, err)
	}

	// Redis 里只有哈希过的 key，原始 token 不落盘
	keys, _ := rdb.Keys(ctx, "*").Result()
	if len(keys) != 1 || strings.Contains(keys[0], tok) || keys[0] != tokenKey(tok) {
		t.Fatalf("keys = %v", keys)
	}
	ttl := rdb.TTL(ctx, keys[0]).Val()
	if ttl <= TokenTTL-time.Minute || ttl > TokenTTL {
		t.Fatalf("ttl = %v, want about %v", ttl, TokenTTL)
	}

	if _, err := ts.Lookup(ctx, "forged"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("forged: err=%v", err)
	}

	// 过期：把 TTL 改成 1ms，等它被删掉
	rdb.PExpire(ctx, tokenKey(tok), time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if _, err := ts.Lookup(ctx, tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired: err=%v", err)
	}
}

// Redis 挂了要回 503，不能当成 token 无效回 401
func TestTokenStoreDownIs503(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer rdb.Close()
	s := &Server{Tokens: &RedisTokens{RDB: rdb}, Conns: map[string]*WsClient{}}

	rec := httptest.NewRecorder()
	s.ServerWs(rec, httptest.NewRequest(http.MethodGet, "/ws?token=abc", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ws with redis down: %d, want 503", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/decks", strings.NewReader(`{"cards":[]}`))
	req.Header.Set("Authorization", "Bearer abc")
	rec = httptest.NewRecorder()
	s.Savedecks(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("decks with redis down: %d, want 503", rec.Code)
	}
}

func TestInvalidTokenIs401(t *testing.T) {
	s := &Server{Tokens: NewMemTokens(time.Hour, map[string]string{"good": "alice"}), Conns: map[string]*WsClient{}}

	for _, q := range []string{"/ws", "/ws?token=", "/ws?token=forged", "/ws?token=alice-1791897325924832431"} {
		rec := httptest.NewRecorder()
		s.ServerWs(rec, httptest.NewRequest(http.MethodGet, q, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", q, rec.Code)
		}
	}

	for _, h := range []string{"", "Bearer ", "Bearer forged", "Basic good"} {
		req := httptest.NewRequest(http.MethodPost, "/decks", strings.NewReader(`{"cards":[]}`))
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		rec := httptest.NewRecorder()
		s.Savedecks(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q: %d, want 401", h, rec.Code)
		}
	}
}

func TestTokenStoreErrorNotLeaked(t *testing.T) {
	code, msg := tokenErr(errors.New("dial tcp 10.0.0.5:6379: connect: connection refused"))
	if code != http.StatusServiceUnavailable || strings.Contains(msg, "6379") {
		t.Fatalf("got %d %q", code, msg)
	}
	code, _ = tokenErr(fmt.Errorf("missing token: %w", ErrInvalidToken))
	if code != http.StatusUnauthorized {
		t.Fatalf("wrapped invalid: %d", code)
	}
}
