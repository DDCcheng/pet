package store

import "database/sql"

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
