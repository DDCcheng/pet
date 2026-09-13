package room

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

type Cmd struct {
	PlayerId string
	Type     string
	Data     json.RawMessage
}

type Room struct {
	Id      string
	Players [2]string
	inbox   chan Cmd
	quit    chan struct{}
	once    sync.Once
	Out     func(playerId string, payload []byte)
	Grace   time.Duration
}

const emptyGrace = 3 * time.Minute

func New(id string, a, b string, out func(string, []byte)) *Room {
	return &Room{
		Id:      id,
		Players: [2]string{a, b},
		inbox:   make(chan Cmd, 64),
		quit:    make(chan struct{}),
		Out:     out,
		Grace:   emptyGrace,
	}
}

func (r *Room) Send(c Cmd) bool {
	select {
	case r.inbox <- c:
		return true
	case <-r.quit:
		return false
	default:
		return false
	}
}

func (r *Room) handle(st *session, cmd Cmd, startGrace, cancelGrace func()) bool {
	if !st.has(cmd.PlayerId) {
		log.Printf("room %s: %s 不属于本房间，忽略", r.Id, cmd.PlayerId)
		return false
	}
	switch cmd.Type {
	case "join":
		st.joined[cmd.PlayerId] = true
		st.online[cmd.PlayerId] = true
		cancelGrace()
		if countTrue(st.online) == 2 {
			r.status("ready")
		} else {
			r.status("waiting")
		}

	case "leave":
		st.online[cmd.PlayerId] = false
		if countTrue(st.online) == 0 {
			startGrace()
		} else {
			r.status("waiting") // 还剩一个人，告诉他在等对手
		}
	case "game_over":
		st.over = true
		return true
	default:
		log.Printf("room%s,unknown order %s", r.Id, cmd.Type)
	}
	return false
}

func (r *Room) Run(ctx context.Context) {
	state := newSession(r.Players)
	grace := r.Grace
	if grace <= 0 {
		grace = emptyGrace
	}
	var graceTimer *time.Timer
	var graceC <-chan time.Time
	startGrace := func() {
		if graceTimer != nil {
			return // 已经在计时了
		}
		graceTimer = time.NewTimer(grace)
		graceC = graceTimer.C
	}
	cancelGrace := func() {
		if graceTimer == nil {
			return
		}
		graceTimer.Stop()
		graceTimer = nil
		graceC = nil // ← 置回 nil，这个 case 就"消失"了
	}

	defer func() {
		cancelGrace()
		r.Close()
		log.Printf("room %s closed", r.Id)
	}()
	for {
		select {
		case cmd := <-r.inbox:
			if r.handle(state, cmd, startGrace, cancelGrace) {
				return // handle 说该关房间了
			}
		case <-graceC:
			log.Printf("room%s:两人断线超时%s,close", r.Id, emptyGrace)
			return
		case <-ctx.Done():
			return
		case <-r.quit:
			return
		}
	}
}

func (r *Room) Close() { r.once.Do(func() { close(r.quit) }) }
