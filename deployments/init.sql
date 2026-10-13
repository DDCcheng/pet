CREATE TABLE IF NOT EXISTS battle_reports (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    room_id VARCHAR(128) NOT NULL,
    player_a VARCHAR(64) NOT NULL,
    player_b VARCHAR(64) NOT NULL,
    winner VARCHAR(64) NOT NULL DEFAULT '',
    end_reason VARCHAR(16) NOT NULL,
    seed BIGINT NOT NULL,
    rounds INT NOT NULL,
    init_decks JSON NOT NULL,
    actions JSON NOT NULL,
    cards_hash CHAR(64) NOT NULL,
    started_at DATETIME(3) NOT NULL,
    ended_at DATETIME(3) NOT NULL,
    UNIQUE KEY uk_room (room_id),
    KEY idx_a (player_a),
    KEY idx_b (player_b)
);