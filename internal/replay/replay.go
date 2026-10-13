// Package replay 把一份落库的战报还原成逐帧的对局，给回放接口和批量校验工具共用。
package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DDCcheng/pet/internal/battle"
	"github.com/DDCcheng/pet/internal/deck"
	"github.com/DDCcheng/pet/internal/room"
	"github.com/DDCcheng/pet/internal/store"
)

var (
	// ErrCardsChanged：打这局时的卡表和现在的不一样，重放出来不可信。
	ErrCardsChanged = errors.New("cards changed since this game")
	// ErrMismatch：重放过程中有动作被拒，或终局和战报记录的不一致。
	ErrMismatch = errors.New("replay does not match report")
)

// Frame 是回放的一帧。第 0 帧是开局，第 i 帧是第 i 个动作做完之后的局面。
type Frame struct {
	Index  int            `json:"index"`
	AtMs   int64          `json:"at_ms"` // 距开局毫秒，前端按它排时间轴
	Seq    uint64         `json:"seq,omitempty"`
	Player string         `json:"player,omitempty"`
	Action *battle.Action `json:"action,omitempty"`
	Auto   bool           `json:"auto,omitempty"` // 回合超时自动结束
	State  battle.GodView `json:"state"`
}

type Result struct {
	RoomID    string    `json:"room_id"`
	Players   [2]string `json:"players"` // players[0] 先手
	Winner    string    `json:"winner"`
	EndReason string    `json:"end_reason"`
	Seed      int64     `json:"seed"`
	Rounds    int       `json:"rounds"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Frames    []Frame   `json:"frames"`
}

// Build 校验卡表哈希，按战报重放整局并逐帧记录，最后核对终局和战报是否一致。
func Build(cat *deck.Catalog, cardsHash string, r store.BattleReport) (*Result, error) {
	if r.CardsHash != cardsHash {
		return nil, fmt.Errorf("%w: report %.8s, now %.8s", ErrCardsChanged, r.CardsHash, cardsHash)
	}
	var decks [2][]string
	if err := json.Unmarshal(r.InitDecksJSON, &decks); err != nil {
		return nil, fmt.Errorf("init_decks: %w", err)
	}
	var log []room.LogEntry
	if err := json.Unmarshal(r.ActionsJSON, &log); err != nil {
		return nil, fmt.Errorf("actions: %w", err)
	}
	steps, err := room.Steps(log)
	if err != nil {
		return nil, err
	}

	res := &Result{
		RoomID: r.RoomID, Players: [2]string{r.PlayerA, r.PlayerB},
		Winner: r.Winner, EndReason: r.EndReason, Seed: r.Seed, Rounds: r.Rounds,
		StartedAt: r.StartedAt, EndedAt: r.EndedAt,
		Frames: make([]Frame, 0, len(steps)+1),
	}
	diverged := -1 // 第一个校验和对不上的动作下标
	final, err := battle.Replay(cat, r.RoomID,
		battle.PlayerInit{ID: r.PlayerA, Cards: decks[0]},
		battle.PlayerInit{ID: r.PlayerB, Cards: decks[1]},
		r.Seed, steps,
		func(i int, s *battle.State) {
			if i < 0 {
				res.Frames = append(res.Frames, Frame{Index: 0, State: s.GodView()})
				return
			}
			// 新战报每一步都带局面校验和；老战报没有（h 为空）就只能靠终局核对
			if h := log[i].Hash; h != "" && diverged < 0 && h != s.Checksum() {
				diverged = i
			}
			a := steps[i].Action
			res.Frames = append(res.Frames, Frame{
				Index: i + 1, AtMs: log[i].AtMs, Seq: log[i].Seq, Player: steps[i].Player,
				Action: &a, Auto: log[i].Auto, State: s.GodView(),
			})
		})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMismatch, err)
	}
	if diverged >= 0 {
		return nil, fmt.Errorf("%w: state checksum differs from action %d (seq %d)", ErrMismatch, diverged, log[diverged].Seq)
	}
	if final.Winner != r.Winner || final.DisplayRound() != r.Rounds || final.Over != (r.EndReason == "finished") {
		return nil, fmt.Errorf("%w: replay winner=%q rounds=%d over=%v, report winner=%q rounds=%d reason=%s",
			ErrMismatch, final.Winner, final.DisplayRound(), final.Over, r.Winner, r.Rounds, r.EndReason)
	}
	return res, nil
}
