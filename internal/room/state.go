package room

import "encoding/json"

//存储的是会话状态，两名玩家，是否都加入游戏，是否在线，这一个对局session是否over
type session struct {
	players [2]string
	joined  map[string]bool
	online  map[string]bool
	over    bool
	started bool
}

func (r *Room) status(s string) {
	r.broadcast(map[string]string{"type": "state", "status": s, "room_id": r.Id})
}

func newSession(players [2]string) *session {
	return &session{
		players: players,
		joined:  map[string]bool{},
		online:  map[string]bool{},
	}
}

func (r *Room) broadcast(v any) {
	if r.Out == nil {
		return
	}
	b, _ := json.Marshal(v)
	for _, p := range r.Players {
		r.Out(p, b)
	}
}

//统计用户在线数量
func countTrue(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}
func (st *session) has(id string) bool {
	return id == st.players[0] || id == st.players[1]
}
