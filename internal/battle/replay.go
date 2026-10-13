package battle

type Step struct {
	Player string
	Action Action
}

// func Replay(cat, roomID, a, b PlayerInit, seed, steps []Step, onStep func(i int, s *State)) (*State, error)
