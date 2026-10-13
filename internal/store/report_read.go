package store

import (
	"context"
	"database/sql"
	"time"
)

// ReportSummary 是战报列表里的一行，不带动作日志（列表页用不到，也省流量）。
type ReportSummary struct {
	RoomID    string
	PlayerA   string
	PlayerB   string
	Winner    string
	EndReason string
	Rounds    int
	StartedAt time.Time
	EndedAt   time.Time
}

// Reports 是战报的读接口，查询都走 battle_reports 上已有的索引（uk_room / idx_a / idx_b）。
type Reports struct{ DB *sql.DB }

// ListReports 返回某个玩家最近打过的对局，新的在前。
// 用 UNION ALL 而不是 WHERE player_a=? OR player_b=?：两半各自走 idx_a / idx_b，
// 写成 OR 的话 MySQL 常常放弃索引改全表扫。
func (r *Reports) ListReports(ctx context.Context, player string, limit int) ([]ReportSummary, error) {
	const cols = `room_id, player_a, player_b, winner, end_reason, rounds, started_at, ended_at`
	rows, err := r.DB.QueryContext(ctx,
		`(SELECT `+cols+` FROM battle_reports WHERE player_a = ? ORDER BY started_at DESC LIMIT ?)
		 UNION ALL
		 (SELECT `+cols+` FROM battle_reports WHERE player_b = ? ORDER BY started_at DESC LIMIT ?)
		 ORDER BY started_at DESC LIMIT ?`,
		player, limit, player, limit, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportSummary{}
	for rows.Next() {
		var s ReportSummary
		if err := rows.Scan(&s.RoomID, &s.PlayerA, &s.PlayerB, &s.Winner, &s.EndReason, &s.Rounds, &s.StartedAt, &s.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetReport 按 room_id 取一整份战报。不存在返回 sql.ErrNoRows。
func (r *Reports) GetReport(ctx context.Context, roomID string) (BattleReport, error) {
	var b BattleReport
	var decks, actions string
	err := r.DB.QueryRowContext(ctx,
		`SELECT room_id, player_a, player_b, winner, end_reason, seed, rounds,
		        init_decks, actions, cards_hash, started_at, ended_at
		 FROM battle_reports WHERE room_id = ?`, roomID,
	).Scan(&b.RoomID, &b.PlayerA, &b.PlayerB, &b.Winner, &b.EndReason, &b.Seed, &b.Rounds,
		&decks, &actions, &b.CardsHash, &b.StartedAt, &b.EndedAt)
	b.InitDecksJSON, b.ActionsJSON = []byte(decks), []byte(actions)
	return b, err
}

// AllRoomIDs 按 id 顺序返回全部 room_id，给批量校验工具用。
func (r *Reports) AllRoomIDs(ctx context.Context) ([]string, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT room_id FROM battle_reports ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
