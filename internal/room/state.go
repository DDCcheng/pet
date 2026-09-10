package room

import "encoding/json"

type state struct {
	players [2]string
	joined  map[string]bool
	online  map[string]bool
	over    bool
}

func (r *Room) status(s string) {
	r.broadcast(map[string]string{"type": "state", "status": s, "room_id": r.Id})
}

func newState(players [2]string) *state {
	return &state{
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
func countTrue(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}
func (st *state) has(id string) bool {
	return id == st.players[0] || id == st.players[1]
}
