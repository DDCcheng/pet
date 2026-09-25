package store

import (
	"database/sql"
	"encoding/json"
)

func FindUserByName(db *sql.DB, name string) (id int64, pass string, err error) {
	err = db.QueryRow(
		`SELECT id, password FROM users WHERE username = ?`,
		name,
	).Scan(&id, &pass)
	return
}

func UpsertDeck(db *sql.DB, userID int64, cardsJSON string) error {
	_, err := db.Exec(
		`INSERT INTO decks (user_id, cards_json) VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE cards_json = VALUES(cards_json)`,
		userID, cardsJSON,
	)
	return err
}

// LoadDeck 按用户名取已保存的卡组。没存过返回 (nil, sql.ErrNoRows)，由调用方决定兜底。
func LoadDeck(db *sql.DB, username string) ([]string, error) {
	var raw string
	err := db.QueryRow(
		`SELECT d.cards_json FROM decks d
		 JOIN users u ON u.id = d.user_id
		 WHERE u.username = ?`,
		username,
	).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var cards []string
	if err := json.Unmarshal([]byte(raw), &cards); err != nil {
		return nil, err
	}
	return cards, nil
}
