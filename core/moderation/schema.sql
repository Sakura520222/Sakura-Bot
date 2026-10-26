-- Sakura Guard v1. Only additive, idempotent MySQL/InnoDB tables.
CREATE TABLE IF NOT EXISTS moderation_deleted_cleanup_jobs (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    bot_id BIGINT NOT NULL,
    request_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    active_key BIGINT NULL UNIQUE,
    actor VARCHAR(64) NOT NULL,
    trigger_type VARCHAR(16) NOT NULL,
    status VARCHAR(24) NOT NULL,
    payload JSON NOT NULL,
    error VARCHAR(256) NULL,
    retry_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    UNIQUE KEY idx_deleted_cleanup_request (bot_id, request_id),
    INDEX idx_deleted_cleanup_history (bot_id, created_at),
    INDEX idx_deleted_cleanup_schedule (bot_id, trigger_type, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_deleted_cleanup_items (
    job_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    access_hash BIGINT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    message VARCHAR(256) NOT NULL DEFAULT '',
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (job_id, chat_id, user_id),
    INDEX idx_deleted_cleanup_pending (job_id, chat_id, status),
    CONSTRAINT fk_deleted_cleanup_job FOREIGN KEY (job_id)
        REFERENCES moderation_deleted_cleanup_jobs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_cleaned_chat_history (
    bot_id BIGINT NOT NULL,
    chat_id BIGINT NOT NULL,
    actor VARCHAR(64) NOT NULL,
    cleaned_at DATETIME NOT NULL,
    PRIMARY KEY (bot_id, chat_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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

-- Ban appeals share Guard's user lock, case history and lists. No new bot identity.
CREATE TABLE IF NOT EXISTS moderation_ai_reports (
    case_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    user_id BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL,
    token CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    payload JSON NOT NULL,
    error VARCHAR(128) NULL,
    retry_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    INDEX idx_ai_report_work (status, retry_at),
    CONSTRAINT fk_ai_report_case FOREIGN KEY (case_id) REFERENCES moderation_cases(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_appeals (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    case_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    active_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL UNIQUE,
    status VARCHAR(24) NOT NULL,
    revision INT NOT NULL DEFAULT 1,
    published_revision INT NOT NULL DEFAULT 0,
    payload JSON NOT NULL,
    error VARCHAR(128) NULL,
    retry_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    INDEX idx_appeal_user (user_id, created_at),
    INDEX idx_appeal_work (status, retry_at),
    INDEX idx_appeal_source (case_id),
    CONSTRAINT fk_appeal_case FOREIGN KEY (case_id) REFERENCES moderation_cases(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_appeal_events (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    appeal_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    actor VARCHAR(64) NOT NULL,
    action VARCHAR(32) NOT NULL,
    from_status VARCHAR(24) NULL,
    to_status VARCHAR(24) NOT NULL,
    payload JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    INDEX idx_appeal_audit (appeal_id, id),
    CONSTRAINT fk_appeal_event FOREIGN KEY (appeal_id) REFERENCES moderation_appeals(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_appeal_inputs (
    actor_id BIGINT PRIMARY KEY,
    token CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    appeal_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    mode VARCHAR(16) NOT NULL,
    payload JSON NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    CONSTRAINT fk_appeal_input FOREIGN KEY (appeal_id) REFERENCES moderation_appeals(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_appeal_requests (
    id CHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    appeal_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    actor_id BIGINT NOT NULL,
    action VARCHAR(16) NOT NULL,
    revision INT NOT NULL,
    origin_chat_id BIGINT NOT NULL,
    origin_message_id BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    payload JSON NOT NULL,
    error VARCHAR(128) NULL,
    notified BOOLEAN NOT NULL DEFAULT FALSE,
    retry_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    INDEX idx_appeal_request_work (status, notified, retry_at),
    CONSTRAINT fk_appeal_request FOREIGN KEY (appeal_id) REFERENCES moderation_appeals(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_appeal_notifications (
    appeal_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    target_id BIGINT NOT NULL,
    kind VARCHAR(16) NOT NULL,
    message_id BIGINT NULL,
    synced_revision INT NOT NULL DEFAULT 0,
    error VARCHAR(128) NULL,
    attempts INT NOT NULL DEFAULT 0,
    retry_at DATETIME(6) NOT NULL,
    PRIMARY KEY (appeal_id, target_id),
    INDEX idx_appeal_delivery (retry_at),
    CONSTRAINT fk_appeal_notice FOREIGN KEY (appeal_id) REFERENCES moderation_appeals(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
