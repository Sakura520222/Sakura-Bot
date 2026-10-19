-- Sakura Guard v1. Only additive, idempotent MySQL/InnoDB tables.
CREATE TABLE IF NOT EXISTS moderation_cases (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    event_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    action VARCHAR(24) NOT NULL,
    status VARCHAR(32) NOT NULL,
    payload JSON NOT NULL,
    reviewer_id BIGINT NULL,
    reviewer_actor VARCHAR(64) NULL,
    reviewed_at DATETIME NULL,
    error TEXT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_guard_subject (chat_id, user_id, status),
    INDEX idx_guard_work (status, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_whitelist (
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    reviewer_id BIGINT NULL,
    reviewer_actor VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (chat_id, user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_blacklist (
    user_id BIGINT PRIMARY KEY,
    source_chat_id BIGINT NOT NULL,
    case_id CHAR(32) NOT NULL,
    payload JSON NOT NULL,
    reviewer_id BIGINT NULL,
    reviewer_actor VARCHAR(64) NOT NULL,
    first_seen_at DATETIME NOT NULL,
    last_seen_at DATETIME NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'permanent'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_verification_sessions (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    status VARCHAR(24) NOT NULL,
    payload JSON NOT NULL,
    message_id BIGINT NULL,
    attempts INT NOT NULL DEFAULT 0,
    expires_at DATETIME NOT NULL,
    error TEXT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (chat_id, user_id),
    INDEX idx_guard_verification_due (status, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_notifications (
    case_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    target_id BIGINT NOT NULL,
    message_id BIGINT NULL,
    synced_status VARCHAR(32) NULL,
    attempts INT NOT NULL DEFAULT 0,
    error TEXT NULL,
    retry_at DATETIME NOT NULL,
    PRIMARY KEY (case_id, target_id),
    INDEX idx_guard_notification_retry (retry_at),
    CONSTRAINT fk_guard_notification_case FOREIGN KEY (case_id)
        REFERENCES moderation_cases(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
