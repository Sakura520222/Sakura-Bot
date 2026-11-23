// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package wiki

import (
	"fmt"
	"strings"
)

// SchemaVersion 是当前 Sakura Wiki 数据库表结构版本（对齐 core/wiki/schema.py:SCHEMA_VERSION）。
const SchemaVersion = 1

const (
	ddlChannelCovers = `CREATE TABLE IF NOT EXISTS wiki_channel_covers (
    channel_id VARCHAR(32) PRIMARY KEY,
    digest CHAR(64) NOT NULL,
    image MEDIUMBLOB NOT NULL,
    FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

	ddlRevisionReviews = `CREATE TABLE IF NOT EXISTS wiki_revision_reviews (
    revision_id CHAR(32) NOT NULL, policy_key CHAR(64) NOT NULL, subject_hash CHAR(64) NOT NULL,
    approved BOOLEAN NOT NULL, result JSON NOT NULL, created_at DATETIME(6) NOT NULL,
    PRIMARY KEY(revision_id,policy_key,subject_hash),
    FOREIGN KEY(revision_id) REFERENCES wiki_page_revisions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

	ddlDirtyPages = `CREATE TABLE IF NOT EXISTS wiki_dirty_pages (
    channel_id VARCHAR(32) NOT NULL, page_id CHAR(32) NOT NULL,
    PRIMARY KEY(channel_id,page_id),
    FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
    FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

	ddlAlbums = `CREATE TABLE IF NOT EXISTS wiki_albums (
    channel_id VARCHAR(32) NOT NULL, media_group VARCHAR(64) NOT NULL,
    page_id CHAR(32), pending BOOLEAN NOT NULL DEFAULT TRUE,
    PRIMARY KEY(channel_id,media_group), INDEX wiki_pending_albums(channel_id,pending,media_group),
    FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
)

// WikiAccountTables 定义 Wiki 读者账户、OIDC 登录流、会话、旧版回放保护与书签表（5 张表）。
var WikiAccountTables = []string{
	`CREATE TABLE IF NOT EXISTS wiki_users (
        id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
        telegram_id BIGINT UNSIGNED NOT NULL UNIQUE,
        telegram_subject VARBINARY(255) NOT NULL UNIQUE,
        display_name VARCHAR(160) NOT NULL, username VARCHAR(64),
        avatar MEDIUMBLOB, avatar_type VARCHAR(16),
        show_avatar BOOLEAN NOT NULL DEFAULT FALSE, show_username BOOLEAN NOT NULL DEFAULT FALSE,
        anonymous_contributions BOOLEAN NOT NULL DEFAULT TRUE, blocked BOOLEAN NOT NULL DEFAULT FALSE,
        profile_at DATETIME(6) NOT NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS wiki_login_flows (
        state_hash BINARY(32) PRIMARY KEY, browser_hash BINARY(32) NOT NULL,
        nonce CHAR(43) NOT NULL, verifier CHAR(43) NOT NULL,
        redirect_path VARCHAR(2048) NOT NULL, refresh_session CHAR(32) CHARACTER SET ascii COLLATE ascii_bin,
        client_id VARCHAR(19) NOT NULL, origin VARCHAR(255) NOT NULL,
        expires_at DATETIME(6) NOT NULL, INDEX wiki_flow_expiry(expires_at), INDEX wiki_flow_browser(browser_hash)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS wiki_sessions (
        id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
        user_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
        access_hash BINARY(32) NOT NULL UNIQUE, refresh_hash BINARY(32) NOT NULL UNIQUE,
        logout_hash BINARY(32) NOT NULL UNIQUE,
        access_until DATETIME(6) NOT NULL, refresh_until DATETIME(6) NOT NULL,
        INDEX wiki_session_expiry(refresh_until),
        FOREIGN KEY(user_id) REFERENCES wiki_users(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS wiki_legacy_login_replays (
        signature BINARY(32) PRIMARY KEY, expires_at DATETIME(6) NOT NULL,
        INDEX wiki_legacy_login_expiry(expires_at)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS wiki_bookmarks (
        user_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
        page_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
        created_at DATETIME(6) NOT NULL,
        PRIMARY KEY(user_id,page_id), INDEX wiki_bookmark_order(user_id,created_at,page_id),
        FOREIGN KEY(user_id) REFERENCES wiki_users(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
}

// WikiCommunityTables 定义联网检索缓存/配额、外部事实核查与社区投稿/贡献表（5 张表）。
var WikiCommunityTables = []string{
	`CREATE TABLE IF NOT EXISTS wiki_search_cache (
        cache_key CHAR(64) PRIMARY KEY, result JSON NOT NULL, expires_at DATETIME(6) NOT NULL,
        INDEX wiki_search_expiry(expires_at)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS wiki_search_usage (
        day DATE NOT NULL, provider VARCHAR(16) NOT NULL, calls INT NOT NULL DEFAULT 0,
        cost_units BIGINT NOT NULL DEFAULT 0, PRIMARY KEY(day,provider)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS wiki_fact_checks (
        cache_key CHAR(64) PRIMARY KEY, result JSON NOT NULL, expires_at DATETIME(6) NOT NULL,
        INDEX wiki_fact_expiry(expires_at)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS wiki_edit_proposals (
        id CHAR(32) PRIMARY KEY, user_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
        channel_id VARCHAR(32) NOT NULL, page_id CHAR(32) NOT NULL, base_revision CHAR(32) NOT NULL,
        revision_id CHAR(32), kind VARCHAR(24) NOT NULL, payload JSON NOT NULL, status VARCHAR(16) NOT NULL,
        actor VARCHAR(128), note VARCHAR(1000), created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
        INDEX wiki_proposal_queue(status,created_at), INDEX wiki_proposal_user(user_id,created_at),
        UNIQUE KEY wiki_proposal_revision(revision_id),
        FOREIGN KEY(user_id) REFERENCES wiki_users(id) ON DELETE CASCADE,
        FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS wiki_contributions (
        proposal_id CHAR(32) PRIMARY KEY, revision_id CHAR(32) NOT NULL, publication_id CHAR(32) NOT NULL,
        created_at DATETIME(6) NOT NULL, INDEX wiki_contribution_revision(revision_id),
        FOREIGN KEY(proposal_id) REFERENCES wiki_edit_proposals(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
}

// SchemaStatements 按外键依赖顺序排列全部 34 张 wiki_* 表的幂等建表语句（严格对齐 core/wiki/schema.py:SCHEMA）。
var SchemaStatements = func() []string {
	core := []string{
		`CREATE TABLE IF NOT EXISTS wiki_schema (
        version INT PRIMARY KEY, applied_at DATETIME(6) NOT NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_channels (
        id VARCHAR(32) PRIMARY KEY, title VARCHAR(255) NOT NULL, username VARCHAR(64),
        enabled BOOLEAN NOT NULL DEFAULT FALSE, public BOOLEAN NOT NULL DEFAULT FALSE,
        access_verified BOOLEAN NOT NULL DEFAULT FALSE, verified_until DATETIME(6),
        config JSON NOT NULL, coverage JSON NOT NULL, published_revision CHAR(32),
        source_generation BIGINT NOT NULL DEFAULT 0, active_job CHAR(32),
        dirty_at DATETIME(6), scheduled_at DATETIME(6), reconciled_at DATETIME(6),
        updated_at DATETIME(6) NOT NULL, INDEX wiki_public(public,enabled,access_verified)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		ddlChannelCovers,
		`CREATE TABLE IF NOT EXISTS wiki_channel_aliases (
        alias VARCHAR(255) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_alias_channel(channel_id)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_sources (
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
        INDEX wiki_source_album(channel_id,media_group,source_type,message_id),
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_chunks (
        id CHAR(64) PRIMARY KEY, source_id CHAR(64) NOT NULL, content_hash CHAR(64) NOT NULL,
        start_offset INT NOT NULL, end_offset INT NOT NULL,
        applied_key CHAR(64) NOT NULL DEFAULT '',
        FOREIGN KEY(source_id) REFERENCES wiki_sources(id) ON DELETE CASCADE,
        INDEX wiki_chunk_source(source_id,content_hash)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_extractions (
        cache_key CHAR(64) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, chunk_id CHAR(64) NOT NULL,
        result JSON NOT NULL, approved BOOLEAN NOT NULL, review JSON NOT NULL,
        input_tokens BIGINT NOT NULL DEFAULT 0, output_tokens BIGINT NOT NULL DEFAULT 0,
        model VARCHAR(255) NOT NULL, created_at DATETIME(6) NOT NULL,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        FOREIGN KEY(chunk_id) REFERENCES wiki_chunks(id) ON DELETE CASCADE,
        INDEX wiki_extraction_channel(channel_id,chunk_id)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_pages (
        id CHAR(32) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL,
        entity_key CHAR(64) NOT NULL, title VARCHAR(160) NOT NULL, slug VARCHAR(180) NOT NULL,
        kind VARCHAR(16) NOT NULL, topic VARCHAR(160) NOT NULL,
        locked BOOLEAN NOT NULL DEFAULT FALSE, hidden BOOLEAN NOT NULL DEFAULT FALSE,
        override_json JSON, updated_at DATETIME(6) NOT NULL,
        UNIQUE KEY wiki_entity(channel_id,entity_key), UNIQUE KEY wiki_slug(channel_id,slug),
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		"CREATE TABLE IF NOT EXISTS wiki_entity_aliases (\n" +
			"        channel_id VARCHAR(32) NOT NULL, alias_key CHAR(64) NOT NULL, name VARCHAR(160) NOT NULL,\n" +
			"        page_id CHAR(32) NOT NULL, `manual` BOOLEAN NOT NULL DEFAULT FALSE,\n" +
			"        PRIMARY KEY(channel_id,alias_key), INDEX wiki_entity_alias(page_id),\n" +
			"        FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE,\n" +
			"        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE\n" +
			"    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		`CREATE TABLE IF NOT EXISTS wiki_slug_aliases (
        channel_id VARCHAR(32) NOT NULL, slug VARCHAR(180) NOT NULL, page_id CHAR(32) NOT NULL,
        PRIMARY KEY(channel_id,slug), FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_facts (
        id CHAR(64) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, page_id CHAR(32) NOT NULL,
        chunk_id CHAR(64) NOT NULL, source_hash CHAR(64) NOT NULL, fact JSON NOT NULL,
        FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(chunk_id) REFERENCES wiki_chunks(id) ON DELETE CASCADE,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_fact_page(page_id)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_page_revisions (
        id CHAR(32) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, page_id CHAR(32) NOT NULL,
        job_id CHAR(32), document JSON NOT NULL, content_hash CHAR(64) NOT NULL,
        search_text LONGTEXT NOT NULL, approved BOOLEAN NOT NULL DEFAULT FALSE,
        rejected BOOLEAN NOT NULL DEFAULT FALSE, created_at DATETIME(6) NOT NULL,
        FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_revision_page(page_id,created_at), INDEX wiki_revision_job(job_id),
        INDEX wiki_feed_latest(created_at DESC,page_id DESC)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		ddlRevisionReviews,
		`CREATE TABLE IF NOT EXISTS wiki_evidence (
        revision_id CHAR(32) NOT NULL, source_id CHAR(64) NOT NULL,
        source_hash CHAR(64) NOT NULL, chunk_id CHAR(64) NOT NULL,
        start_offset INT NOT NULL, end_offset INT NOT NULL,
        PRIMARY KEY(revision_id,chunk_id,start_offset,end_offset),
        FOREIGN KEY(revision_id) REFERENCES wiki_page_revisions(id) ON DELETE CASCADE,
        FOREIGN KEY(source_id) REFERENCES wiki_sources(id) ON DELETE CASCADE,
        FOREIGN KEY(chunk_id) REFERENCES wiki_chunks(id) ON DELETE CASCADE,
        INDEX wiki_evidence_source(source_id,revision_id)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_revision_claims (
        revision_id CHAR(32) NOT NULL, ordinal INT NOT NULL, occurred_at DATETIME(6),
        source_created_at DATETIME(6) NOT NULL, payload JSON NOT NULL,
        PRIMARY KEY(revision_id,ordinal), INDEX wiki_event_time(occurred_at,source_created_at),
        FOREIGN KEY(revision_id) REFERENCES wiki_page_revisions(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_edges (
        id CHAR(64) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, chunk_id CHAR(64) NOT NULL,
        source_hash CHAR(64) NOT NULL, source_page CHAR(32) NOT NULL, target_page CHAR(32) NOT NULL,
        kind VARCHAR(32) NOT NULL, relation_json JSON NOT NULL, active BOOLEAN NOT NULL DEFAULT TRUE,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        FOREIGN KEY(chunk_id) REFERENCES wiki_chunks(id) ON DELETE CASCADE,
        FOREIGN KEY(source_page) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(target_page) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        INDEX wiki_neighbors(source_page,target_page)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_publications (
        id CHAR(32) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, job_id CHAR(32),
        profile JSON NOT NULL, coverage JSON NOT NULL, created_at DATETIME(6) NOT NULL,
        content_hash CHAR(64) NOT NULL DEFAULT '',
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_publication_time(channel_id,created_at)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_publication_pages (
        publication_id CHAR(32) NOT NULL, page_id CHAR(32) NOT NULL, revision_id CHAR(32) NOT NULL,
        PRIMARY KEY(publication_id,page_id),
        FOREIGN KEY(publication_id) REFERENCES wiki_publications(id) ON DELETE CASCADE,
        FOREIGN KEY(page_id) REFERENCES wiki_pages(id) ON DELETE CASCADE,
        FOREIGN KEY(revision_id) REFERENCES wiki_page_revisions(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_publication_edges (
        publication_id CHAR(32) NOT NULL, edge_id CHAR(64) NOT NULL,
        PRIMARY KEY(publication_id,edge_id),
        FOREIGN KEY(publication_id) REFERENCES wiki_publications(id) ON DELETE CASCADE,
        FOREIGN KEY(edge_id) REFERENCES wiki_edges(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		"CREATE TABLE IF NOT EXISTS wiki_jobs (\n" +
			"        id CHAR(32) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL, kind VARCHAR(32) NOT NULL,\n" +
			"        status VARCHAR(16) NOT NULL DEFAULT 'pending', phase VARCHAR(32) NOT NULL DEFAULT 'collect',\n" +
			"        `cursor` JSON NOT NULL, progress JSON NOT NULL, attempts INT NOT NULL DEFAULT 0,\n" +
			"        lease_token CHAR(32), lease_until DATETIME(6), next_attempt DATETIME(6) NOT NULL,\n" +
			"        error_code VARCHAR(128), created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,\n" +
			"        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,\n" +
			"        INDEX wiki_job_queue(status,next_attempt), INDEX wiki_job_channel(channel_id,created_at)\n" +
			"    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		`CREATE TABLE IF NOT EXISTS wiki_review_actions (
        id BIGINT AUTO_INCREMENT PRIMARY KEY, channel_id VARCHAR(32) NOT NULL,
        action VARCHAR(32) NOT NULL, target_id VARCHAR(180), actor VARCHAR(128) NOT NULL,
        details JSON NOT NULL, created_at DATETIME(6) NOT NULL,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE,
        INDEX wiki_audit(channel_id,created_at)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS wiki_profile_cache (
        cache_key CHAR(64) PRIMARY KEY, channel_id VARCHAR(32) NOT NULL,
        narrative JSON NOT NULL, review JSON NOT NULL, created_at DATETIME(6) NOT NULL,
        FOREIGN KEY(channel_id) REFERENCES wiki_channels(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		ddlDirtyPages,
		ddlAlbums,
	}
	all := make([]string, 0, len(core)+len(WikiAccountTables)+len(WikiCommunityTables))
	all = append(all, core...)
	all = append(all, WikiAccountTables...)
	all = append(all, WikiCommunityTables...)
	return all
}()

// AdditiveAlterStatements 是针对已有 v1 库执行的幂等增量列/索引迁移（执行时忽略 MySQL 1060/1061）。
var AdditiveAlterStatements = []string{
	`ALTER TABLE wiki_sources ADD COLUMN exclusion_reason VARCHAR(32) NOT NULL DEFAULT ''`,
	`ALTER TABLE wiki_chunks ADD COLUMN applied_key CHAR(64) NOT NULL DEFAULT ''`,
	`ALTER TABLE wiki_publications ADD COLUMN content_hash CHAR(64) NOT NULL DEFAULT ''`,
	`ALTER TABLE wiki_page_revisions ADD INDEX wiki_feed_latest(created_at DESC,page_id DESC)`,
	`ALTER TABLE wiki_sources ADD INDEX wiki_source_album(channel_id,media_group,source_type,message_id)`,
}

// ChannelVisibleSQL 生成公开频道可见性 SQL 谓词（对齐 core/wiki/schema.py:CHANNEL_VISIBLE 与 internal/db/wiki.go）。
func ChannelVisibleSQL(alias string) string {
	a := strings.TrimSpace(alias)
	if a == "" {
		a = "c"
	}
	return fmt.Sprintf(`%s.enabled=TRUE AND %s.public=TRUE AND %s.access_verified=TRUE
    AND %s.username IS NOT NULL AND %s.verified_until>UTC_TIMESTAMP(6)
    AND %s.published_revision IS NOT NULL`, a, a, a, a, a, a)
}

// RevisionEvidenceSQL 生成修订证据链有效性与未撤销检查 SQL 谓词（对齐 core/wiki/schema.py:REVISION_EVIDENCE）。
func RevisionEvidenceSQL(revAlias, pageAlias string) string {
	r := strings.TrimSpace(revAlias)
	if r == "" {
		r = "r"
	}
	p := strings.TrimSpace(pageAlias)
	if p == "" {
		p = "p"
	}
	return fmt.Sprintf(`%s.rejected=FALSE AND %s.hidden=FALSE
    AND EXISTS (SELECT 1 FROM wiki_evidence ev WHERE ev.revision_id=%s.id)
    AND NOT EXISTS (SELECT 1 FROM wiki_evidence ev JOIN wiki_sources s ON s.id=ev.source_id
        WHERE ev.revision_id=%s.id AND (s.channel_id<>%s.channel_id OR s.deleted=TRUE
        OR s.hidden=TRUE OR s.excluded=TRUE OR s.verified=FALSE OR s.content_hash<>ev.source_hash
        OR (s.source_type='image' AND NOT EXISTS (SELECT 1 FROM wiki_sources parent
            WHERE parent.channel_id=s.channel_id AND parent.source_type='message'
            AND parent.message_id=s.message_id AND parent.media_key=s.media_key
            AND parent.deleted=FALSE AND parent.hidden=FALSE AND parent.excluded=FALSE AND parent.verified=TRUE))))`,
		r, p, r, r, r)
}

// RevisionVisibleSQL 生成公开修订可见性 SQL 谓词（对齐 core/wiki/schema.py:REVISION_VISIBLE 与 internal/db/wiki.go）。
func RevisionVisibleSQL(revAlias, pageAlias string) string {
	r := strings.TrimSpace(revAlias)
	if r == "" {
		r = "r"
	}
	return fmt.Sprintf("%s.approved=TRUE AND %s", r, RevisionEvidenceSQL(r, pageAlias))
}

// imageParentValidSQL 生成“图片来源的父帖仍然有效”谓词（图片来源别名为 s）：父帖须属于同一频道和同一条消息、
// media_key 一致，且未被删除、隐藏、排除或取消核实。RevisionEvidenceSQL 以它的否定形式拒绝孤儿图片。
func imageParentValidSQL() string {
	return `(s.source_type<>'image' OR EXISTS (SELECT 1 FROM wiki_sources parent
        WHERE parent.channel_id=s.channel_id AND parent.source_type='message'
        AND parent.message_id=s.message_id AND parent.media_key=s.media_key
        AND parent.deleted=FALSE AND parent.hidden=FALSE AND parent.excluded=FALSE AND parent.verified=TRUE))`
}

// publishableEvidenceSQL 判定某个分块此刻能否作为公开证据（来源别名为 s，分块别名为 ch）：来源未被删除、
// 隐藏或排除，已核实，内容与引用时一致，且（若为图片）父帖仍然有效。它与 RevisionEvidenceSQL 对单条证据的
// 拒绝条件互为反面，供装配阶段预先剔除发布校验不会放行的引用；来源是否属于本频道由调用方自行限定。
func publishableEvidenceSQL() string {
	return `s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE AND s.verified=TRUE
    AND s.content_hash=ch.content_hash AND ` + imageParentValidSQL()
}

// VisibleSQL 组合频道、修订及隐藏主题过滤的完整公开读取谓词（对齐 core/wiki/reader.py:VISIBLE）。
func VisibleSQL(chanAlias, revAlias, pageAlias string) string {
	c := strings.TrimSpace(chanAlias)
	if c == "" {
		c = "c"
	}
	p := strings.TrimSpace(pageAlias)
	if p == "" {
		p = "p"
	}
	return fmt.Sprintf(`%s AND %s
    AND NOT JSON_CONTAINS(JSON_EXTRACT(%s.config,'$.hidden_topics'),JSON_QUOTE(%s.topic))`,
		ChannelVisibleSQL(c), RevisionVisibleSQL(revAlias, p), c, p)
}

// ExtractTableName 从 CREATE TABLE IF NOT EXISTS <name> 语句提取表名（用于诊断日志）。
func ExtractTableName(stmt string) string {
	fields := strings.Fields(stmt)
	for i := 0; i+1 < len(fields); i++ {
		if strings.EqualFold(fields[i], "EXISTS") {
			return strings.Trim(fields[i+1], "(")
		}
	}
	if len(fields) >= 3 {
		return strings.Trim(fields[2], "(")
	}
	return "wiki_unknown"
}
