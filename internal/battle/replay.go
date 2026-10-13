package battle

import (
	"fmt"

	"github.com/DDCcheng/pet/internal/deck"
)

type Step struct {
	Player string
	Action Action
}

// ReplayError 表示第 Step 步（从 0 开始）在重放时被规则拒绝了。
// 战报里只记被线上接受的动作，所以重放时被拒 = 状态分叉：卡表、规则代码或随机数和线上不一致。
type ReplayError struct {
	Step int
	Err  error
}

func (e *ReplayError) Error() string { return fmt.Sprintf("replay step %d: %v", e.Step, e.Err) }
func (e *ReplayError) Unwrap() error { return e.Err }

// Replay 用同一个 seed 和初始卡组重建对局，再按顺序 Apply 每一步，返回最终状态。
// onStep 可以为 nil；不为 nil 时，开局后先调一次 onStep(-1, s)，之后每成功一步调一次 onStep(i, s)。
// ★ onStep 拿到的是同一个 *State，下一步会被改掉；要留帧必须当场拷贝（比如 GodView）。
func Replay(cat *deck.Catalog, roomID string, a, b PlayerInit, seed int64, steps []Step, onStep func(i int, s *State)) (*State, error) {
	s, err := NewState(roomID, cat, a, b, seed)
	if err != nil {
		return nil, err
	}
	if onStep != nil {
		onStep(-1, s)
	}
	for i, st := range steps {
		if err := s.Apply(st.Player, st.Action); err != nil {
			return s, &ReplayError{Step: i, Err: err}
		}
		if onStep != nil {
			onStep(i, s)
		}
	}
	return s, nil
}
