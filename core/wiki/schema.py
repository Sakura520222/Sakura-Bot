# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Additive, idempotent MySQL migration. Wiki owns all wiki_* tables."""

SCHEMA_VERSION = 1
# 页面失效队列：来源删除/隐藏、策略翻转与人工修订把受影响页面登记到这里，
# 增量装配只重建这些页面而不是遍历全部历史页面。
DIRTY_PAGES_TABLE = """CREATE TABLE IF NOT EXISTS wiki_dirty_pages (
    channel_id VARCHAR(32) NOT NULL, page_id CHAR(32) NOT NULL,
    PRIMARY KEY(channel_id,page_id),
    FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
    FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"""
SCHEMA = (
    """CREATE TABLE IF NOT EXISTS wiki_schema (
        version INT PRIMARY KEY, applied_at DATETIME(6) NOT NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_channels (
        id VARCHAR(32) PRIMARY KEY, title VARCHAR(255) NOT NULL, username VARCHAR(64),
        enabled BOOLEAN NOT NULL DEFAULT FALSE, public BOOLEAN NOT NULL DEFAULT FALSE,
        access_verified BOOLEAN NOT NULL DEFAULT FALSE, verified_until DATETIME(6),
        config JSON NOT NULL, coverage JSON NOT NULL, published_revision CHAR(32),
        source_generation BIGINT NOT NULL DEFAULT 0, active_job CHAR(32),
        dirty_at DATETIME(6), scheduled_at DATETIME(6), reconciled_at DATETIME(6),
        updated_at DATETIME(6) NOT NULL, INDEX wiki_public(public,enabled,access_verified)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_channel_aliases (
        alias VARCHAR(255) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_alias_channel(channel_id)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_sources (
        id CHAR(64) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL,
        source_type VARCHAR(16) NOT NULL, external_id VARCHAR(128) NOT NULL,
        category VARCHAR(16) NOT NULL DEFAULT 'text',
        message_id BIGINT, body LONGTEXT NOT NULL, original_length INT NOT NULL DEFAULT 0,
        content_hash CHAR(64) NOT NULL, revision BIGINT NOT NULL DEFAULT 1,
        created_at DATETIME(6) NOT NULL, edited_at DATETIME(6) NOT NULL,
        observed_at DATETIME(6) NOT NULL, verified BOOLEAN NOT NULL DEFAULT FALSE,
        deleted BOOLEAN NOT NULL DEFAULT FALSE, hidden BOOLEAN NOT NULL DEFAULT FALSE,
        excluded BOOLEAN NOT NULL DEFAULT FALSE, media_group VARCHAR(64), media_key VARCHAR(128),
        exclusion_reason VARCHAR(32) NOT NULL DEFAULT '',
        UNIQUE KEY wiki_source_identity(channel_id,source_type,external_id),
        INDEX wiki_source_message(channel_id,source_type,message_id),
        INDEX wiki_source_scan(channel_id,id), INDEX wiki_source_time(channel_id,created_at),
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_chunks (
        id CHAR(64) PRIMARY KEY, source_id CHAR(64) NOT NULL, content_hash CHAR(64) NOT NULL,
        start_offset INT NOT NULL, end_offset INT NOT NULL,
        applied_key CHAR(64) NOT NULL DEFAULT '',
        FOREIGN KEY(source_id) REFERENCES wiki_sources(id) ON DELETE CASCADE,
        INDEX wiki_chunk_source(source_id,content_hash)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_extractions (
        cache_key CHAR(64) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, chunk_id CHAR(64) NOT NULL,
        result JSON NOT NULL, approved BOOLEAN NOT NULL, review JSON NOT NULL,
        input_tokens BIGINT NOT NULL DEFAULT 0, output_tokens BIGINT NOT NULL DEFAULT 0,
        model VARCHAR(255) NOT NULL, created_at DATETIME(6) NOT NULL,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        FOREIGN KEY(chunk_id) REFERENCES wiki_chunks(id) ON DELETE CASCADE,
        INDEX wiki_extraction_channel(channel_id,chunk_id)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_pages (
        id CHAR(32) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL,
        entity_key CHAR(64) NOT NULL, title VARCHAR(160) NOT NULL, slug VARCHAR(180) NOT NULL,
        kind VARCHAR(16) NOT NULL, topic VARCHAR(160) NOT NULL,
        locked BOOLEAN NOT NULL DEFAULT FALSE, hidden BOOLEAN NOT NULL DEFAULT FALSE,
        override_json JSON, updated_at DATETIME(6) NOT NULL,
        UNIQUE KEY wiki_entity(channel_id,entity_key), UNIQUE KEY wiki_slug(channel_id,slug),
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_entity_aliases (
        channel_id VARCHAR(32) NOT NULL, alias_key CHAR(64) NOT NULL, name VARCHAR(160) NOT NULL,
        page_id CHAR(32) NOT NULL, `manual` BOOLEAN NOT NULL DEFAULT FALSE,
        PRIMARY KEY(channel_id,alias_key), INDEX wiki_entity_alias(page_id),
        FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_slug_aliases (
        channel_id VARCHAR(32) NOT NULL, slug VARCHAR(180) NOT NULL, page_id CHAR(32) NOT NULL,
        PRIMARY KEY(channel_id,slug), FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_facts (
        id CHAR(64) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, page_id CHAR(32) NOT NULL,
        chunk_id CHAR(64) NOT NULL, source_hash CHAR(64) NOT NULL, fact JSON NOT NULL,
        FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(chunk_id) REFERENCES wiki_chunks(id) ON DELETE CASCADE,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_fact_page(page_id)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_page_revisions (
        id CHAR(32) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, page_id CHAR(32) NOT NULL,
        job_id CHAR(32), document JSON NOT NULL, content_hash CHAR(64) NOT NULL,
        search_text LONGTEXT NOT NULL, approved BOOLEAN NOT NULL DEFAULT FALSE,
        rejected BOOLEAN NOT NULL DEFAULT FALSE, created_at DATETIME(6) NOT NULL,
        FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_revision_page(page_id,created_at), INDEX wiki_revision_job(job_id),
        INDEX wiki_feed_latest(created_at DESC,page_id DESC)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_evidence (
        revision_id CHAR(32) NOT NULL, source_id CHAR(64) NOT NULL,
        source_hash CHAR(64) NOT NULL, chunk_id CHAR(64) NOT NULL,
        start_offset INT NOT NULL, end_offset INT NOT NULL,
        PRIMARY KEY(revision_id,chunk_id,start_offset,end_offset),
        FOREIGN KEY(revision_id) REFERENCES wiki_page_revisions(id) ON DELETE CASCADE,
        FOREIGN KEY(source_id) REFERENCES wiki_sources(id) ON DELETE CASCADE,
        FOREIGN KEY(chunk_id) REFERENCES wiki_chunks(id) ON DELETE CASCADE,
        INDEX wiki_evidence_source(source_id,revision_id)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_revision_claims (
        revision_id CHAR(32) NOT NULL, ordinal INT NOT NULL, occurred_at DATETIME(6),
        source_created_at DATETIME(6) NOT NULL, payload JSON NOT NULL,
        PRIMARY KEY(revision_id,ordinal), INDEX wiki_event_time(occurred_at,source_created_at),
        FOREIGN KEY(revision_id) REFERENCES wiki_page_revisions(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_edges (
        id CHAR(64) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, chunk_id CHAR(64) NOT NULL,
        source_hash CHAR(64) NOT NULL, source_page CHAR(32) NOT NULL, target_page CHAR(32) NOT NULL,
        kind VARCHAR(32) NOT NULL, relation_json JSON NOT NULL, active BOOLEAN NOT NULL DEFAULT TRUE,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        FOREIGN KEY(chunk_id) REFERENCES wiki_chunks(id) ON DELETE CASCADE,
        FOREIGN KEY(source_page) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(target_page) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        INDEX wiki_neighbors(source_page,target_page)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_publications (
        id CHAR(32) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, job_id CHAR(32),
        profile JSON NOT NULL, coverage JSON NOT NULL, created_at DATETIME(6) NOT NULL,
        content_hash CHAR(64) NOT NULL DEFAULT '',
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_publication_time(channel_id,created_at)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_publication_pages (
        publication_id CHAR(32) NOT NULL, page_id CHAR(32) NOT NULL, revision_id CHAR(32) NOT NULL,
        PRIMARY KEY(publication_id,page_id),
        FOREIGN KEY(publication_id) REFERENCES wiki_publications(id) ON DELETE CASCADE,
        FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(revision_id) REFERENCES wiki_page_revisions(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_publication_edges (
        publication_id CHAR(32) NOT NULL, edge_id CHAR(64) NOT NULL,
        PRIMARY KEY(publication_id,edge_id),
        FOREIGN KEY(publication_id) REFERENCES wiki_publications(id) ON DELETE CASCADE,
        FOREIGN KEY(edge_id) REFERENCES wiki_edges(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_jobs (
        id CHAR(32) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, kind VARCHAR(32) NOT NULL,
        status VARCHAR(16) NOT NULL DEFAULT 'pending', phase VARCHAR(32) NOT NULL DEFAULT 'collect',
        `cursor` JSON NOT NULL, progress JSON NOT NULL, attempts INT NOT NULL DEFAULT 0,
        lease_token CHAR(32), lease_until DATETIME(6), next_attempt DATETIME(6) NOT NULL,
        error_code VARCHAR(128), created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_job_queue(status,next_attempt), INDEX wiki_job_channel(channel_id,created_at)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_review_actions (
        id BIGINT AUTO_INCREMENT PRIMARY KEY, channel_id VARCHAR(32) NOT NULL,
        action VARCHAR(32) NOT NULL, target_id VARCHAR(180), actor VARCHAR(128) NOT NULL,
        details JSON NOT NULL, created_at DATETIME(6) NOT NULL,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_audit(channel_id,created_at)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    """CREATE TABLE IF NOT EXISTS wiki_profile_cache (
        cache_key CHAR(64) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL,
        narrative JSON NOT NULL, review JSON NOT NULL, created_at DATETIME(6) NOT NULL,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4""",
    # 页面失效队列：来源删除/隐藏、策略翻转与人工修订把受影响页面登记到这里，
    # 增量装配只重建这些页面而不是遍历全部历史页面。
    DIRTY_PAGES_TABLE,
)

# Shared with Go (contract-tested). No snapshot, rollback or index bypasses this gate.
CHANNEL_VISIBLE = """c.enabled=TRUE AND c.public=TRUE AND c.access_verified=TRUE
    AND c.username IS NOT NULL AND c.verified_until>UTC_TIMESTAMP(6)
    AND c.published_revision IS NOT NULL"""
REVISION_VISIBLE = """r.approved=TRUE AND r.rejected=FALSE AND p.hidden=FALSE
    AND EXISTS (SELECT 1 FROM wiki_evidence ev WHERE ev.revision_id=r.id)
    AND NOT EXISTS (SELECT 1 FROM wiki_evidence ev JOIN wiki_sources s ON s.id=ev.source_id
        WHERE ev.revision_id=r.id AND (s.channel_id<>r.channel_id OR s.deleted=TRUE
        OR s.hidden=TRUE OR s.excluded=TRUE OR s.verified=FALSE OR s.content_hash<>ev.source_hash
        OR (s.source_type='image' AND NOT EXISTS (SELECT 1 FROM wiki_sources parent
            WHERE parent.channel_id=s.channel_id AND parent.source_type='message'
            AND parent.message_id=s.message_id AND parent.media_key=s.media_key
            AND parent.deleted=FALSE AND parent.hidden=FALSE AND parent.excluded=FALSE AND parent.verified=TRUE))))"""
