package auth

import (
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/DDCcheng/pet/internal/room"
)

// 背压计数。只增不减，/metrics 读。
var (
	sendDropped atomic.Int64 // send 缓冲写满被丢掉的消息数
	slowKicked  atomic.Int64 // 因为 send 写满被踢掉的连接数
	rateLimited atomic.Int64 // 被 readLoop 限速拒掉的对局指令数
)

func SendDropped() int64 { return sendDropped.Load() }
func SlowKicked() int64  { return slowKicked.Load() }
func RateLimited() int64 { return rateLimited.Load() }

// Metrics 输出纯文本指标（Prometheus 文本格式的子集，curl 直接能看）。
func (s *Server) Metrics(w http.ResponseWriter, r *http.Request) {
	rooms := 0
	if s.RoomMgr != nil {
		rooms = s.RoomMgr.Len()
	}
	s.mu.RLock()
	conns := len(s.Conns)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "pet_rooms_active %d\n", rooms)
	fmt.Fprintf(w, "pet_ws_conns %d\n", conns)
	fmt.Fprintf(w, "pet_inbox_dropped_total %d\n", room.InboxDropped())
	fmt.Fprintf(w, "pet_send_dropped_total %d\n", SendDropped())
	fmt.Fprintf(w, "pet_slow_kicked_total %d\n", SlowKicked())
	fmt.Fprintf(w, "pet_rate_limited_total %d\n", RateLimited())
}
