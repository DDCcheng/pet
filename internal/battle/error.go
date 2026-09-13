package battle

import "fmt"

type Code string

const (
	CodeGameOver       Code = "game_over"
	CodeNotYourTurn    Code = "not_your_turn"
	CodeCardNotInHand  Code = "card_not_in_hand"
	CodeNotEnoughMana  Code = "not_enough_mana"
	CodeBoardFull      Code = "board_full"
	CodeUnknownAction  Code = "unknown_action"
	CodeUnknownCard    Code = "unknown_card"
	CodeNotImplemented Code = "not_implemented"
	CodeNotInGame      Code = "not_in_game"
)

type Err struct {
	Code Code
	Msg  string
}

func (e *Err) Error() string {
	return string(e.Code) + ": " + e.Msg
}

func fail(c Code, format string, a ...any) error {
	return &Err{Code: c, Msg: fmt.Sprintf(format, a...)}
}
