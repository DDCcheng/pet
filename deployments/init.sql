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

-- 以下由一键启动追加：users / decks 原来是手动建的，结构照搬本地库（SHOW CREATE TABLE），新环境 compose up 就能用
CREATE TABLE IF NOT EXISTS users (
    id BIGINT NOT NULL AUTO_INCREMENT,
    username VARCHAR(32) NOT NULL,
    password VARCHAR(64) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY username (username)
) DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS decks (
    id BIGINT NOT NULL AUTO_INCREMENT,
    user_id BIGINT NOT NULL,
    cards_json TEXT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_user (user_id)
) DEFAULT CHARSET=utf8mb4;
