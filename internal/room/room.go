package room

import (
	"context"
	"encoding/json"
	"errors"
	"log"
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
	ctx       context.Context
	cancel    context.CancelFunc
	Out       func(playerId string, payload []byte)
	Grace     time.Duration
	Battle    *battle.State
	seq       uint64
	TurnLimit time.Duration
	OnFinish  func(Report) // 由 main 注入：结束时怎么落库
	log       []LogEntry   // 动作日志，只有房间 goroutine 读写
	startedAt time.Time    // 开局时间，用来算 AtMs
}

const defaultTurn = 60 * time.Second
const emptyGrace = 3 * time.Minute

func New(id string, a, b string, out func(string, []byte)) *Room {
	room := &Room{
		Id:        id,
		Players:   [2]string{a, b},
		inbox:     make(chan Cmd, 64),
		Out:       out,
		Grace:     emptyGrace,
		TurnLimit: defaultTurn,
	}
	ctx, cancel := context.WithCancel(context.Background())
	room.ctx = ctx
	room.cancel = cancel
	return room

}

func (r *Room) Send(c Cmd) bool {
	if r.ctx.Err() != nil {
		return false
	}
	select {
	case r.inbox <- c:
		return true
	case <-r.ctx.Done():
		return false
	default:
		inboxDropped.Add(1) // inbox 满：两人共享，丢的可能是任何一方的指令
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
				r.startedAt = time.Now()
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
		r.record(cmd.PlayerId, cmd.Type, cmd.Data, false)
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

func (r *Room) Run(parent context.Context) {
	stop := context.AfterFunc(parent, r.cancel)
	reason := "finished"
	defer stop()
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
		r.finish(state, reason)
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
			log.Printf("room%s:两人断线超时%s,close", r.Id, grace)
			reason = "abandoned"
			return
		case <-turnC:
			turnC = nil
			nowPlayer := r.Battle.Players[r.Battle.Turn].ID
			if err := r.Battle.Apply(nowPlayer, battle.Action{Type: "end_turn"}); err != nil {
				log.Printf("room %s: 超时自动 end_turn 失败: %v", r.Id, err)
			} else {
				r.record(nowPlayer, "end_turn", nil, true)
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
		case <-r.ctx.Done():
			reason = "aborted"
			return

		}
	}
}

func (r *Room) Close() { r.cancel() }
func (r *Room) Done() <-chan struct{} {
	return r.ctx.Done()
}

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

func (r *Room) Has(pid string) bool {
	return pid == r.Players[0] || pid == r.Players[1]
}
