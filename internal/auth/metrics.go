package auth

import "sync/atomic"

// 背压计数。只增不减，/metrics 读。
var (
	sendDropped atomic.Int64 // send 缓冲写满被丢掉的消息数
	slowKicked  atomic.Int64 // 因为 send 写满被踢掉的连接数
)

func SendDropped() int64 { return sendDropped.Load() }
func SlowKicked() int64  { return slowKicked.Load() }
