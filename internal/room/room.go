package room

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/DDCcheng/pet/internal/battle"
)

type Cmd struct {
	PlayerId string
	Type     string
	Data     json.RawMessage
}

type Room struct {
	Id        string
	Players   [2]string
	inbox     chan Cmd
	quit      chan struct{}
	once      sync.Once
	Out       func(playerId string, payload []byte)
	Grace     time.Duration
	Battle    *battle.State
	seq       uint64
	TurnLimit time.Duration
}

const defaultTurn = 60 * time.Second
const emptyGrace = 3 * time.Minute

func New(id string, a, b string, out func(string, []byte)) *Room {
	return &Room{
		Id:        id,
		Players:   [2]string{a, b},
		inbox:     make(chan Cmd, 64),
		quit:      make(chan struct{}),
		Out:       out,
		Grace:     emptyGrace,
		TurnLimit: defaultTurn,
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
		if r.Battle != nil {
			if !st.started && countTrue(st.online) == 2 {
				st.started = true
				r.pushState()
			} else if st.started {
				r.sendView(cmd.PlayerId)
			}
		}
	case "play_card", "end_turn", "attack":
		if r.Battle == nil {
			r.sendErr(cmd.PlayerId, errors.New("对局未开始"))
			return false
		}
		var a battle.Action
		if err := json.Unmarshal(cmd.Data, &a); err != nil {
			r.sendErr(cmd.PlayerId, errors.New("action错误"))
			return false
		}
		a.Type = cmd.Type
		if err := r.Battle.Apply(cmd.PlayerId, a); err != nil {
			r.sendErr(cmd.PlayerId, err)
			return false
		}
		r.pushState()
		if r.Battle.Over {
			r.broadcast(map[string]string{
				"type":   "game_over",
				"winner": r.Battle.Winner,
			})
			return true
		}

	case "leave":
		st.online[cmd.PlayerId] = false
		if countTrue(st.online) == 0 {
			startGrace()
		} else {
			r.status("waiting") // 还剩一个人，告诉他在等对手
		}
	default:
		log.Printf("room%s,unknown order %s", r.Id, cmd.Type)
	}
	return false
}

func (r *Room) Run(ctx context.Context) {
	state := newSession(r.Players)
	grace := r.Grace
	turnLimit := r.TurnLimit
	if turnLimit <= 0 {
		turnLimit = defaultTurn
	}
	if grace <= 0 {
		grace = emptyGrace
	}
	var graceTimer *time.Timer
	var graceC <-chan time.Time
	var turnTimer *time.Timer
	var turnC <-chan time.Time
	resetTurn := func() {
		if turnTimer != nil {
			turnTimer.Stop()
		}
		turnTimer = time.NewTimer(turnLimit)
		turnC = turnTimer.C
	}
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
		if turnTimer != nil {
			turnTimer.Stop()
		}
		r.Close()
		log.Printf("room %s closed", r.Id)
	}()
	lastTurn := -1
	checkTurn := func() {
		if r.Battle != nil && state.started && !r.Battle.Over && r.Battle.Turn != lastTurn {
			lastTurn = r.Battle.Turn
			resetTurn()
		}
	}
	for {
		select {
		case cmd := <-r.inbox:
			if r.handle(state, cmd, startGrace, cancelGrace) {
				return // 该关房间了
			}
			checkTurn()
		case <-graceC:
			log.Printf("room%s:两人断线超时%s,close", r.Id, emptyGrace)
			return
		case <-turnC:
			turnC = nil
			nowPlayer := r.Battle.Players[r.Battle.Turn].ID
			if err := r.Battle.Apply(nowPlayer, battle.Action{Type: "end_turn"}); err != nil {
				log.Printf("room %s: 超时自动 end_turn 失败: %v", r.Id, err)
			}
			r.pushState()
			if r.Battle.Over {
				r.broadcast(map[string]string{
					"type":   "game_over",
					"winner": r.Battle.Winner,
				})
				return
			}
			checkTurn()
		case <-ctx.Done():
			return
		case <-r.quit:
			return
		}
	}
}

func (r *Room) Close() { r.once.Do(func() { close(r.quit) }) }

func (r *Room) sendView(pid string) {
	if r.Out == nil || r.Battle == nil {
		return
	}
	for i, p := range r.Players {
		if p == pid {
			v := r.Battle.ViewFor(i)
			v.Seq = r.seq
			msg := struct {
				Type string `json:"type"`
				battle.View
			}{"game_state", v}
			b, _ := json.Marshal(msg)
			r.Out(pid, b)
			return
		}
	}

}

func (r *Room) pushState() {
	if r.Out == nil || r.Battle == nil {
		return
	}
	r.seq++
	for _, pid := range r.Players {
		r.sendView(pid)
	}
}

func (r *Room) sendErr(pid string, err error) {
	var pe *battle.Err
	code := "internal"
	msg := err.Error()
	if errors.As(err, &pe) {
		code = string(pe.Code)
		msg = pe.Msg
	}
	b, _ := json.Marshal(map[string]string{
		"type": "error",
		"code": code,
		"msg":  msg,
	})
	if r.Out != nil {
		r.Out(pid, b)
	}
}
