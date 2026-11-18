// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package guard

import (
	"context"
	"fmt"
	"strings"
	"time"

	db "github.com/Sakura520222/Sakura-Bot/internal/db"
)

// BlacklistUpsert 全局黑名单写入/更新参数（对齐 repository.py:224-240）。
type BlacklistUpsert struct {
	UserID        int64
	SourceChatID  int64
	CaseID        string
	Payload       map[string]any
	ReviewerID    *int64
	ReviewerActor string
	FirstSeenAt   time.Time
}

// Whitelisted 检查用户是否在指定群白名单中（repository.py:133-139）。
func (s *Store) Whitelisted(ctx context.Context, chatID, userID int64) (bool, error) {
	row, err := s.One(ctx,
		"SELECT user_id FROM moderation_whitelist WHERE chat_id=? AND user_id=?",
		chatID, userID)
	return row != nil, err
}

// AddWhitelist 写入或更新群白名单记录（支持 WebUI reviewerID=nil + reviewerActor="webui:admin"，对齐 repository.py:144-154）。
func (s *Store) AddWhitelist(ctx context.Context, chatID, userID int64, reviewerID *int64, reviewerActor string) error {
	actor := reviewerActor
	if actor == "" && reviewerID != nil {
		actor = fmt.Sprintf("telegram:%d", *reviewerID)
	}
	if actor == "" {
		actor = "webui:admin"
	}
	var revIDArg any
	if reviewerID != nil {
		revIDArg = *reviewerID
	}
	_, err := s.Exec(ctx,
		"INSERT INTO moderation_whitelist (chat_id,user_id,reviewer_id,reviewer_actor,created_at) VALUES (?,?,?,?,?) ON DUPLICATE KEY UPDATE reviewer_id=VALUES(reviewer_id),reviewer_actor=VALUES(reviewer_actor)",
		chatID, userID, revIDArg, actor, db.MysqlDatetime(db.NowUTC()))
	return err
}

// RemoveWhitelist 从指定群白名单中移除用户（repository.py:156-159）。
func (s *Store) RemoveWhitelist(ctx context.Context, chatID, userID int64) error {
	_, err := s.Exec(ctx,
		"DELETE FROM moderation_whitelist WHERE chat_id=? AND user_id=?",
		chatID, userID)
	return err
}

// Blacklisted 返回用户是否在全局黑名单中（如需完整行使用 s.Blacklist）。
func (s *Store) Blacklisted(ctx context.Context, userID int64) (bool, error) {
	row, err := s.Blacklist(ctx, userID)
	return row != nil, err
}

// TouchBlacklist 刷新全局黑名单用户的最后命中时间（repository.py:127-131）。
func (s *Store) TouchBlacklist(ctx context.Context, userID int64) error {
	_, err := s.Exec(ctx,
		"UPDATE moderation_blacklist SET last_seen_at=? WHERE user_id=?",
		db.MysqlDatetime(db.NowUTC()), userID)
	return err
}

// UpsertBlacklist 写入或更新全局永久黑名单记录（repository.py:224-240）。
func (s *Store) UpsertBlacklist(ctx context.Context, in BlacklistUpsert) error {
	actor := in.ReviewerActor
	if actor == "" && in.ReviewerID != nil {
		actor = fmt.Sprintf("telegram:%d", *in.ReviewerID)
	}
	if actor == "" {
		actor = "webui:admin"
	}
	var revIDArg any
	if in.ReviewerID != nil {
		revIDArg = *in.ReviewerID
	}
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	now := db.NowUTC()
	firstSeen := in.FirstSeenAt
	if firstSeen.IsZero() {
		firstSeen = now
	}

	_, err := s.Exec(ctx, `
		INSERT INTO moderation_blacklist
		(user_id,source_chat_id,case_id,payload,reviewer_id,reviewer_actor,first_seen_at,last_seen_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE
			last_seen_at=VALUES(last_seen_at),
			reviewer_id=VALUES(reviewer_id),
			reviewer_actor=VALUES(reviewer_actor),
			source_chat_id=VALUES(source_chat_id),
			case_id=VALUES(case_id),
			payload=VALUES(payload)
	`, in.UserID, in.SourceChatID, in.CaseID, db.EncodeJSONMust(payload),
		revIDArg, actor, db.MysqlDatetime(firstSeen.UTC()), db.MysqlDatetime(now))
	return err
}

// RemoveBlacklist 移除全局黑名单记录，返回是否实际删除（guard.py:648）。
func (s *Store) RemoveBlacklist(ctx context.Context, userID int64) (bool, error) {
	n, err := s.Exec(ctx, "DELETE FROM moderation_blacklist WHERE user_id=?", userID)
	return n > 0, err
}

// RecordPattern 记录观察期发言特征指纹（repository.py:315-320 save_observation_pattern）。
func (s *Store) RecordPattern(ctx context.Context, chatID int64, fingerprint string) error {
	_, err := s.Exec(ctx, `
		INSERT INTO moderation_observation_patterns (fingerprint,chat_id,seen_at)
		VALUES (?,?,UTC_TIMESTAMP())
		ON DUPLICATE KEY UPDATE seen_at=VALUES(seen_at)
	`, fingerprint, chatID)
	return err
}

// SharedPatternSeen 检查 7 天内指定其他群组是否出现过相同观察期指纹（repository.py:322-330）。
func (s *Store) SharedPatternSeen(ctx context.Context, fingerprint string, chatIDs []int64) (bool, error) {
	if len(chatIDs) == 0 {
		return false, nil
	}
	placeholders := strings.Repeat("?,", len(chatIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, 1+len(chatIDs))
	args = append(args, fingerprint)
	for _, cid := range chatIDs {
		args = append(args, cid)
	}
	row, err := s.One(ctx, fmt.Sprintf(`
		SELECT 1 FROM moderation_observation_patterns
		WHERE fingerprint=? AND chat_id IN (%s)
		  AND seen_at >= UTC_TIMESTAMP() - INTERVAL 7 DAY
		LIMIT 1
	`, placeholders), args...)
	return row != nil, err
}

// ActiveAssistant 查询 90 秒心跳窗口内在线的验证助手（repository.py:285-293）。
func (s *Store) ActiveAssistant(ctx context.Context, botID int64) (Row, error) {
	if botID == 0 {
		return nil, nil
	}
	return s.One(ctx, `
		SELECT username, bot_id FROM moderation_verification_assistants
		WHERE bot_id=? AND seen_at >= UTC_TIMESTAMP() - INTERVAL 90 SECOND
		ORDER BY seen_at DESC LIMIT 1
	`, botID)
}

// Whitelisted 免锁检查用户是否在指定群白名单中。
func (r *Repository) Whitelisted(ctx context.Context, chatID, userID int64) (bool, error) {
	var ok bool
	err := r.Reader(ctx, func(s *Store) error {
		v, e := s.Whitelisted(ctx, chatID, userID)
		ok = v
		return e
	})
	return ok, err
}

// ListWhitelist 分页查询白名单记录。
func (r *Repository) ListWhitelist(ctx context.Context, chatID, userID *int64, limit, offset int) (*RecordPage, error) {
	return r.QueryRecords(ctx, "whitelist", RecordFilter{
		ChatID: chatID,
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
}

// Blacklisted 免锁查询用户的全局黑名单记录（nil 表示不在黑名单）。
func (r *Repository) Blacklisted(ctx context.Context, userID int64) (Row, error) {
	var out Row
	err := r.Reader(ctx, func(s *Store) error {
		row, e := s.Blacklist(ctx, userID)
		out = row
		return e
	})
	return out, err
}

// ListBlacklist 分页查询全局黑名单记录。
func (r *Repository) ListBlacklist(ctx context.Context, sourceChatID, userID *int64, status string, limit, offset int) (*RecordPage, error) {
	return r.QueryRecords(ctx, "blacklist", RecordFilter{
		ChatID: sourceChatID,
		UserID: userID,
		Status: status,
		Limit:  limit,
		Offset: offset,
	})
}

// RecordPattern 免锁记录观察期发言特征指纹。
func (r *Repository) RecordPattern(ctx context.Context, chatID int64, fingerprint string) error {
	return r.Reader(ctx, func(s *Store) error {
		return s.RecordPattern(ctx, chatID, fingerprint)
	})
}

// SharedPatternSeen 免锁检查跨群共享观察期指纹。
func (r *Repository) SharedPatternSeen(ctx context.Context, fingerprint string, chatIDs []int64) (bool, error) {
	var ok bool
	err := r.Reader(ctx, func(s *Store) error {
		v, e := s.SharedPatternSeen(ctx, fingerprint, chatIDs)
		ok = v
		return e
	})
	return ok, err
}

// ActiveAssistant 免锁查询在线的验证助手。
func (r *Repository) ActiveAssistant(ctx context.Context, botID int64) (Row, error) {
	var out Row
	err := r.Reader(ctx, func(s *Store) error {
		row, e := s.ActiveAssistant(ctx, botID)
		out = row
		return e
	})
	return out, err
}

// RetentionCleanup 执行 Guard 周期性审计数据留存清理（逐字对齐 repository.py:581-604 cleanup）：
// 永久黑白名单、待审核案件、关联申诉案件与有未完成子任务的记录绝不删除。
func (r *Repository) RetentionCleanup(ctx context.Context, retentionDays int) error {
	if retentionDays <= 0 {
		retentionDays = 90
	}
	cutoff := db.NowUTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	cutoffDT := db.MysqlDatetime(cutoff)
	cutoffDT6 := db.MysqlDatetime6(cutoff)

	return r.Reader(ctx, func(s *Store) error {
		if _, err := s.Exec(ctx,
			"DELETE FROM moderation_observation_patterns WHERE seen_at < UTC_TIMESTAMP() - INTERVAL 7 DAY LIMIT 5000"); err != nil {
			return err
		}
		if _, err := s.Exec(ctx,
			"DELETE FROM moderation_verification_assistants WHERE seen_at < UTC_TIMESTAMP() - INTERVAL 1 DAY LIMIT 200"); err != nil {
			return err
		}
		if _, err := s.Exec(ctx,
			"DELETE FROM moderation_cases WHERE created_at<? AND status IN ('done','allowed','unbanned','unmuted','ignored','blacklisted','execution_failed','cancelled','manual','kicked') AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.notification_cleanup_pending')),'false')<>'true' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.report_message_deletion.status')),'')<>'pending' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.report_notice_dirty')),'false')<>'true' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.guest_ban_pending')),'false')<>'true' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.ban_message_cleanup.status')),'')<>'pending' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.ban_message_cleanup_dirty')),'false')<>'true' AND NOT EXISTS (SELECT 1 FROM moderation_appeals a WHERE a.case_id=moderation_cases.id) LIMIT 500",
			cutoffDT); err != nil {
			return err
		}
		if _, err := s.Exec(ctx,
			"DELETE FROM moderation_verification_sessions WHERE updated_at<? AND status NOT IN ('checking','preparing','waiting','passing','kicking','cancelling','admin_pending','admin_kick_pending','held') AND (message_id IS NULL OR JSON_UNQUOTE(JSON_EXTRACT(payload,'$.cleanup_pending'))='false') LIMIT 500",
			cutoffDT); err != nil {
			return err
		}
		_, err := s.Exec(ctx,
			"DELETE FROM moderation_deleted_cleanup_jobs WHERE updated_at<? AND active_key IS NULL AND status IN ('completed','partial','cancelled') LIMIT 100",
			cutoffDT6)
		return err
	})
}
