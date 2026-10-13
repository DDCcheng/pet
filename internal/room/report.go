package room

import (
	"encoding/json"
	"log"
	"time"
)

type LogEntry struct {
	Seq    uint64          `json:"seq"`
	Player string          `json:"player"`
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data,omitempty"`
	Auto   bool            `json:"auto,omitempty"` // 回合超时自动 end_turn
	AtMs   int64           `json:"at_ms"`          // 距开局毫秒
	Hash   string          `json:"h,omitempty"`    // 这一步做完后的局面校验和（battle.State.Checksum），回放时逐步比对
}

type Report struct {
	RoomID             string
	Players            [2]string
	Winner, Reason     string
	Seed               int64
	Rounds             int
	Actions            []LogEntry
	StartedAt, EndedAt time.Time
	InitDecks          [2][]string
}

func (r *Room) record(pid, typ string, data json.RawMessage, auto bool) {
	r.log = append(r.log, LogEntry{
		Seq:    r.seq + 1,
		Player: pid,
		Type:   typ,
		Data:   data,
		Auto:   auto,
		AtMs:   time.Since(r.startedAt).Milliseconds(),
		Hash:   r.Battle.Checksum(),
	})
}
func (r *Room) finish(st *session, reason string) {
	if r.OnFinish == nil || r.Battle == nil || !st.started {
		return
	}
	if r.Battle.Over == true {
		reason = "finished"
	}
	rep := Report{
		RoomID:    r.Id,
		Players:   r.Players,
		Winner:    r.Battle.Winner,
		Reason:    reason,
		Seed:      r.Battle.Seed,
		Rounds:    r.Battle.DisplayRound(),
		InitDecks: r.Battle.InitDecks,
		Actions:   r.log,
		StartedAt: r.startedAt,
		EndedAt:   time.Now(),
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("room %s: OnFinish panic: %v", r.Id, p)
		}
	}()
	r.OnFinish(rep)
}
