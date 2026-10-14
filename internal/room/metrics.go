package room

import "sync/atomic"

// inbox 写满被丢掉的指令数（所有房间合计），/metrics 读。
var inboxDropped atomic.Int64

func InboxDropped() int64 { return inboxDropped.Load() }

// 因为 panic 被 recover 掉的房间数，/metrics 读。不为 0 就说明规则代码有 bug，要去日志里看堆栈。
var roomPanics atomic.Int64

func RoomPanics() int64 { return roomPanics.Load() }
