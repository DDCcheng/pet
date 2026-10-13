package room

import (
	"encoding/json"
	"fmt"

	"github.com/DDCcheng/pet/internal/battle"
)

// Steps 把战报的动作日志转成 battle.Replay 要的步骤。
// Data 是客户端发来的原始 JSON（card_id / inst_id / target，room_id 忽略）；
// 回合超时自动结束的那条 Data 为空，只有 Type。
func Steps(log []LogEntry) ([]battle.Step, error) {
	steps := make([]battle.Step, 0, len(log))
	for i, e := range log {
		var a battle.Action
		if len(e.Data) > 0 {
			if err := json.Unmarshal(e.Data, &a); err != nil {
				return nil, fmt.Errorf("action %d (seq %d): %w", i, e.Seq, err)
			}
		}
		a.Type = e.Type // 和房间里一样：类型以指令类型为准，不信 data 里的 type
		steps = append(steps, battle.Step{Player: e.Player, Action: a})
	}
	return steps, nil
}
