package room

import "sync/atomic"

// inbox 写满被丢掉的指令数（所有房间合计），/metrics 读。
var inboxDropped atomic.Int64

func InboxDropped() int64 { return inboxDropped.Load() }
