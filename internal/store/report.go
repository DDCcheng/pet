package store

import (
	"context"
	"database/sql"
	"time"
)

type BattleReport struct {
	RoomID        string
	PlayerA       string
	PlayerB       string
	Winner        string
	EndReason     string
	Seed          int64
	Rounds        int
	InitDecksJSON []byte
	ActionsJSON   []byte
	CardsHash     string
	StartedAt     time.Time
	EndedAt       time.Time
}

func SaveBattleReport(ctx context.Context, db *sql.DB, r BattleReport) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO battle_reports
		 (room_id, player_a, player_b, winner, end_reason, seed, rounds,
init_decks, actions, cards_hash, started_at, ended_at)
		 VALUES (?, ?, ?,?, ?, ?,?, ?, ?,?, ?, ?)`,
		r.RoomID, r.PlayerA, r.PlayerB, r.Winner, r.EndReason, r.Seed, r.Rounds, string(r.InitDecksJSON), string(r.ActionsJSON), r.CardsHash, r.StartedAt, r.EndedAt,
	)
	return err

}
