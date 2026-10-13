package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// TokenTTL 是登录 token 的有效期。过期后要重新登录；已经建立的 WebSocket 不受影响。
const TokenTTL = 24 * time.Hour

// ErrInvalidToken 表示 token 不存在、伪造或已过期（调用方回 401）。
// 其他错误表示存储不可用（调用方回 503），两者要分开：Redis 挂了不能让用户以为是自己 token 坏了。
var ErrInvalidToken = errors.New("invalid token")

// TokenStore 负责签发和校验登录 token。
type TokenStore interface {
	Issue(ctx context.Context, username string) (string, error)
	Lookup(ctx context.Context, token string) (string, error)
}

// newToken 生成 32 字节随机数（256 bit），base64url 编码成 43 个字符，可以直接放进 ?token= 查询串。
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// RedisTokens 把 token 存在 Redis：key = tok:<sha256(token)>，value = 用户名，TTL 到期自动删除。
// 存哈希而不是 token 本身：Redis 数据被导出时，拿到的 key 不能直接拿来登录。
type RedisTokens struct {
	RDB *redis.Client
	TTL time.Duration // 0 表示 TokenTTL
}

func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "tok:" + hex.EncodeToString(sum[:])
}

func (t *RedisTokens) ttl() time.Duration {
	if t.TTL <= 0 {
		return TokenTTL
	}
	return t.TTL
}

func (t *RedisTokens) Issue(ctx context.Context, username string) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	if err := t.RDB.Set(ctx, tokenKey(token), username, t.ttl()).Err(); err != nil {
		return "", err
	}
	return token, nil
}

func (t *RedisTokens) Lookup(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrInvalidToken
	}
	username, err := t.RDB.Get(ctx, tokenKey(token)).Result()
	if err == redis.Nil {
		return "", ErrInvalidToken
	}
	if err != nil {
		return "", err
	}
	return username, nil
}

// MemTokens 是进程内的 token 表，同样带过期。给不起 Redis 的测试用。
type MemTokens struct {
	mu  sync.Mutex
	ttl time.Duration
	now func() time.Time
	m   map[string]memToken
}

type memToken struct {
	username string
	expires  time.Time
}

// NewMemTokens 建一个内存 token 表；seed 里的 token→用户名 预先放进去，方便测试直接用固定 token。
func NewMemTokens(ttl time.Duration, seed map[string]string) *MemTokens {
	t := &MemTokens{ttl: ttl, now: time.Now, m: map[string]memToken{}}
	for tok, user := range seed {
		t.m[tok] = memToken{username: user, expires: t.now().Add(ttl)}
	}
	return t
}

func (t *MemTokens) Issue(_ context.Context, username string) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	t.m[token] = memToken{username: username, expires: t.now().Add(t.ttl)}
	t.mu.Unlock()
	return token, nil
}

func (t *MemTokens) Lookup(_ context.Context, token string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[token]
	if !ok || token == "" {
		return "", ErrInvalidToken
	}
	if !t.now().Before(e.expires) {
		delete(t.m, token)
		return "", ErrInvalidToken
	}
	return e.username, nil
}

// tokenErr 把校验错误翻译成 HTTP 状态和对外文案：token 无效回 401，存储出错回 503。
// 存储错误的原文（里面有 Redis 地址）只打日志，不回给客户端。
func tokenErr(err error) (int, string) {
	if errors.Is(err, ErrInvalidToken) {
		return http.StatusUnauthorized, "invalid token"
	}
	log.Printf("token store: %v", err)
	return http.StatusServiceUnavailable, "token store unavailable"
}
