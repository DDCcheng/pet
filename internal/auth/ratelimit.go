package auth

import "time"

// 每个连接的对局指令限速：人手操作每秒几条，10 条/秒、突发 10 条足够，
// 再多就是刷指令，会挤占两人共享的房间 inbox。
const (
	cmdRate  = 10.0
	cmdBurst = 10.0
)

// limiter 是最简单的令牌桶。只在该连接的 readLoop 里用，不需要锁。
type limiter struct {
	rate, burst float64
	tokens      float64
	last        time.Time
	now         func() time.Time // 测试里换成假时钟
}

func newLimiter(rate, burst float64) *limiter {
	l := &limiter{rate: rate, burst: burst, tokens: burst, now: time.Now}
	l.last = l.now()
	return l
}

func (l *limiter) Allow() bool {
	now := l.now()
	l.tokens += now.Sub(l.last).Seconds() * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
