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
	"time"

	db "github.com/Sakura520222/Sakura-Bot/internal/db"
)

const guardNoticeProtectedExpr = "(synced_status='removed' OR (synced_status='deleting' AND COALESCE(VALUES(synced_status),'') NOT IN ('deleting','removed')))"

// CaseNotices 查询指定案件在 moderation_notifications 中的全部通知投递记录（repository.py:353-354）。
// 注意：方法命名为 CaseNotices 以与申诉表 s.Notices(appealID) 保持区分。
func (s *Store) CaseNotices(ctx context.Context, caseID string) ([]Row, error) {
	return s.All(ctx, "SELECT * FROM moderation_notifications WHERE case_id=?", caseID)
}

// UpsertCaseNotice 写入或更新群管案件通知状态（逐字对齐 repository.py:356-374 notice）：
//   - 已进入 deleting/removed 的通知行受保护，不会被晚到的普通状态同步回退；
//   - retry_at 按 min(3600, 30 * 2^min(attempts, 7)) 秒退避。
func (s *Store) UpsertCaseNotice(ctx context.Context, caseID string, targetID int64, messageID *int64, syncedStatus *string, errStr *string, attempts int) error {
	if attempts < 0 {
		attempts = 0
	}
	shift := attempts
	if shift > 7 {
		shift = 7
	}
	backoffSec := 30 * (1 << shift)
	if backoffSec > 3600 {
		backoffSec = 3600
	}
	retryAt := db.MysqlDatetime(db.NowUTC().Add(time.Duration(backoffSec) * time.Second))

	var msgArg, statusArg, errArg any
	if messageID != nil {
		msgArg = *messageID
	}
	if syncedStatus != nil {
		statusArg = *syncedStatus
	}
	if errStr != nil && *errStr != "" {
		errArg = *errStr
	}

	query := "INSERT INTO moderation_notifications (case_id,target_id,message_id,synced_status,error,attempts,retry_at) VALUES (?,?,?,?,?,?,?) " +
		"ON DUPLICATE KEY UPDATE " +
		"message_id=IF(" + guardNoticeProtectedExpr + ",message_id,VALUES(message_id))," +
		"error=IF(" + guardNoticeProtectedExpr + ",error,VALUES(error))," +
		"attempts=IF(" + guardNoticeProtectedExpr + ",attempts,VALUES(attempts))," +
		"retry_at=IF(" + guardNoticeProtectedExpr + ",retry_at,VALUES(retry_at))," +
		"synced_status=IF(" + guardNoticeProtectedExpr + ",synced_status,VALUES(synced_status))"

	_, err := s.Exec(ctx, query, caseID, targetID, msgArg, statusArg, errArg, attempts, retryAt)
	return err
}

// MarkCaseNoticeSynced 记录案件通知已成功投递或同步到当前案件状态（error=NULL, attempts=0）。
func (s *Store) MarkCaseNoticeSynced(ctx context.Context, caseID string, targetID int64, messageID *int64, syncedStatus string) error {
	return s.UpsertCaseNotice(ctx, caseID, targetID, messageID, &syncedStatus, nil, 0)
}

// MarkCaseNoticeFailed 记录案件通知投递失败并按 attempts 安排指数退避重试。
func (s *Store) MarkCaseNoticeFailed(ctx context.Context, caseID string, targetID int64, messageID *int64, prevSyncedStatus *string, errMsg string, attempts int) error {
	if attempts <= 0 {
		attempts = 1
	}
	return s.UpsertCaseNotice(ctx, caseID, targetID, messageID, prevSyncedStatus, &errMsg, attempts)
}

// MarkCaseNoticeDeleting 将待撤回的审核通知标记为 deleting 阶段。
func (s *Store) MarkCaseNoticeDeleting(ctx context.Context, caseID string, targetID int64, messageID *int64) error {
	st := "deleting"
	return s.UpsertCaseNotice(ctx, caseID, targetID, messageID, &st, nil, 0)
}

// MarkCaseNoticeRemoved 将已物理撤回或确认不存在的审核通知终态置为 removed（message_id=NULL）。
func (s *Store) MarkCaseNoticeRemoved(ctx context.Context, caseID string, targetID int64) error {
	st := "removed"
	return s.UpsertCaseNotice(ctx, caseID, targetID, nil, &st, nil, 0)
}

// DueCaseNotices 轮询需要初次投递、状态同步或到期重试的群管案件通知记录。
func (r *Repository) DueCaseNotices(ctx context.Context, limit int) ([]Row, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	nowDT := db.MysqlDatetime(db.NowUTC())
	var out []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		out, e = s.All(ctx, `
			SELECT n.*, c.chat_id, c.user_id, c.action AS case_action, c.status AS case_status
			FROM moderation_notifications n
			JOIN moderation_cases c ON c.id=n.case_id
			WHERE (n.synced_status IS NULL OR n.synced_status NOT IN ('deleting','removed'))
			  AND (n.synced_status IS NULL OR n.synced_status<>c.status OR n.error IS NOT NULL)
			  AND n.retry_at<=?
			ORDER BY n.retry_at ASC LIMIT ?
		`, nowDT, limit)
		return e
	})
	return out, err
}
