package auth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// IPLimiter 给 /login 按来源 IP 限速：每个 IP 一个令牌桶。
// 放在 bcrypt 之前：被拒的请求不查库、不算哈希，攻击者没法用登录接口把 CPU 打满。
type IPLimiter struct {
	mu        sync.Mutex
	rate      float64
	burst     float64
	m         map[string]*limiter
	lastSweep time.Time
	now       func() time.Time
}

// ipIdle 内没有新请求、桶也已经回满的 IP 会被清掉，防止 map 无限长。
const ipIdle = 10 * time.Minute

func NewIPLimiter(rate, burst float64) *IPLimiter {
	return &IPLimiter{rate: rate, burst: burst, m: map[string]*limiter{}, now: time.Now}
}

func (l *IPLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastSweep) > time.Minute {
		l.sweep(now)
	}
	b, ok := l.m[ip]
	if !ok {
		b = &limiter{rate: l.rate, burst: l.burst, tokens: l.burst, last: now, now: l.now}
		l.m[ip] = b
	}
	return b.Allow()
}

func (l *IPLimiter) sweep(now time.Time) {
	l.lastSweep = now
	for ip, b := range l.m {
		if now.Sub(b.last) > ipIdle {
			delete(l.m, ip)
		}
	}
}

func (l *IPLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}

// clientIP 只取 TCP 连接的对端地址，不读 X-Forwarded-For：那个头客户端可以随便填，
// 信了它，攻击者每次换一个值就能绕过限速。真部署在反向代理后面时，要改成只信任代理加的那一段。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
