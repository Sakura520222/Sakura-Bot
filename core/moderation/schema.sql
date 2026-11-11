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

-- AI 自适应风控：样本与学习任务彼此独立于人工案件，AI 不可用时样本仍可持久化。
CREATE TABLE IF NOT EXISTS moderation_ai_samples (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    idempotency_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    message_id BIGINT NULL,
    source VARCHAR(24) NOT NULL,
    case_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    label VARCHAR(16) NOT NULL DEFAULT 'pending',
    status VARCHAR(16) NOT NULL DEFAULT 'new',
    payload JSON NOT NULL,
    error VARCHAR(128) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    INDEX idx_ai_sample_chat (chat_id, created_at),
    INDEX idx_ai_sample_user (user_id, created_at),
    INDEX idx_ai_sample_case (case_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_ai_learning_jobs (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    sample_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL,
    token CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    payload JSON NOT NULL,
    error VARCHAR(128) NULL,
    retry_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    INDEX idx_ai_learning_work (status, retry_at),
    CONSTRAINT fk_ai_learning_sample FOREIGN KEY (sample_id)
        REFERENCES moderation_ai_samples(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- AI 动态规则与人工关键词/正则、全局黑名单、白名单完全分离，仅在此表内管理。
CREATE TABLE IF NOT EXISTS moderation_ai_rules (
    id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    rule_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    chat_id BIGINT NOT NULL,
    rule_type VARCHAR(16) NOT NULL,
    match_kind VARCHAR(16) NOT NULL,
    value VARCHAR(512) NOT NULL,
    normalized_value VARCHAR(512) NOT NULL,
    action VARCHAR(16) NOT NULL DEFAULT 'delete',
    status VARCHAR(16) NOT NULL DEFAULT 'candidate',
    confidence FLOAT NOT NULL DEFAULT 0,
    model VARCHAR(64) NULL,
    actor VARCHAR(64) NULL,
    explanation VARCHAR(1024) NOT NULL DEFAULT '',
    sample_ids JSON NOT NULL,
    positives INT NOT NULL DEFAULT 0,
    negatives INT NOT NULL DEFAULT 0,
    reporters INT NOT NULL DEFAULT 0,
    target_users INT NOT NULL DEFAULT 0,
    corrections INT NOT NULL DEFAULT 0,
    hits INT NOT NULL DEFAULT 0,
    last_hit_at DATETIME(6) NULL,
    version INT NOT NULL DEFAULT 1,
    expires_at DATETIME(6) NULL,
    error VARCHAR(128) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    INDEX idx_ai_rule_status (status, chat_id, updated_at),
    INDEX idx_ai_rule_expiry (status, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 命中审计与计数分离，避免高频消息检测直接写主规则表。
CREATE TABLE IF NOT EXISTS moderation_ai_rule_hits (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    rule_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    case_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    version INT NOT NULL,
    action VARCHAR(16) NOT NULL,
    evidence JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    INDEX idx_ai_rule_hit_rule (rule_id, id),
    INDEX idx_ai_rule_hit_chat (chat_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS moderation_ai_rule_feedback (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    rule_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    sample_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    case_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    kind VARCHAR(32) NOT NULL,
    polarity VARCHAR(16) NOT NULL,
    actor VARCHAR(64) NOT NULL,
    payload JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    INDEX idx_ai_feedback_rule (rule_id, id),
    INDEX idx_ai_feedback_case (case_id),
    INDEX idx_ai_feedback_sample (sample_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 跨群共享作用域：管理员显式授权的群才生效，绝无默认共享。
CREATE TABLE IF NOT EXISTS moderation_ai_rule_scopes (
    rule_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    chat_id BIGINT NOT NULL,
    actor VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (rule_id, chat_id),
    INDEX idx_ai_rule_scope_chat (chat_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
