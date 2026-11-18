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

	db "github.com/Sakura520222/Sakura-Bot/internal/db"
)

// DeletedUserCandidate 待清理已销号成员候选标识（deleted_cleanup_repository.py:107-113）。
type DeletedUserCandidate struct {
	UserID     int64
	AccessHash *int64
}

// IsActiveCleanupStatus 报告已销号清理任务是否仍占用 Bot 活跃位（ACTIVE_CLEANUP = {"running", "waiting"}）。
func IsActiveCleanupStatus(status string) bool {
	return status == "running" || status == "waiting"
}

// ActiveDeletedCleanupJob 查询当前 Bot 正在运行或等待中的清理任务（deleted_cleanup_repository.py:20-23）。
func (s *Store) ActiveDeletedCleanupJob(ctx context.Context, botID int64) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_deleted_cleanup_jobs WHERE active_key=?", botID)
}

// RequestedDeletedCleanupJob 按幂等 request_id 查询已受理的清理任务（deleted_cleanup_repository.py:25-29）。
func (s *Store) RequestedDeletedCleanupJob(ctx context.Context, botID int64, requestID string) (Row, error) {
	return s.One(ctx,
		"SELECT * FROM moderation_deleted_cleanup_jobs WHERE bot_id=? AND request_id=?",
		botID, requestID)
}

// GetDeletedCleanupJob 按 (bot_id, job_id) 查询单条清理任务（deleted_cleanup_repository.py:31-35）。
func (s *Store) GetDeletedCleanupJob(ctx context.Context, botID int64, jobID string) (Row, error) {
	return s.One(ctx,
		"SELECT * FROM moderation_deleted_cleanup_jobs WHERE id=? AND bot_id=?",
		jobID, botID)
}

// LatestScheduledDeletedCleanup 查询最近一次定时触发的清理任务创建时间（deleted_cleanup_repository.py:37-42）。
func (s *Store) LatestScheduledDeletedCleanup(ctx context.Context, botID int64) (Row, error) {
	return s.One(ctx, `
		SELECT created_at FROM moderation_deleted_cleanup_jobs
		WHERE bot_id=? AND trigger_type='scheduled'
		ORDER BY created_at DESC LIMIT 1
	`, botID)
}

// CreateDeletedCleanupJob 创建已销号成员清理任务，并继承历史同群已移除/失败成员以便复核残留消息
// （逐字对齐 deleted_cleanup_repository.py:44-90 create）。
func (s *Store) CreateDeletedCleanupJob(ctx context.Context, botID int64, actor, requestID string, groups []map[string]any, requestedChatIDs []int64, trigger string) (Row, error) {
	if trigger == "" {
		trigger = "manual"
	}
	if groups == nil {
		groups = []map[string]any{}
	}
	now := db.NowUTC()
	nowStr := db.MysqlDatetime6(now)
	jobID := uuid4Hex()

	var reqIDsVal any
	if requestedChatIDs != nil {
		reqIDsVal = requestedChatIDs
	}
	payload := map[string]any{
		"groups":             groups,
		"group_index":        0,
		"requested_chat_ids": reqIDsVal,
	}

	if _, err := s.Exec(ctx, `
		INSERT INTO moderation_deleted_cleanup_jobs
		(id,bot_id,request_id,active_key,actor,trigger_type,status,payload,error,retry_at,created_at,updated_at)
		VALUES (?,?,?,?,?,?,'running',?,NULL,?,?,?)
	`, jobID, botID, requestID, botID, actor, trigger, db.EncodeJSONMust(payload), nowStr, nowStr, nowStr); err != nil {
		return nil, err
	}

	for _, grp := range groups {
		chatID := rowInt(Row(grp), "chat_id")
		if chatID == 0 {
			continue
		}
		if _, err := s.Exec(ctx, `
			INSERT IGNORE INTO moderation_deleted_cleanup_items
			(job_id,chat_id,user_id,access_hash,status,updated_at)
			SELECT ?,i.chat_id,i.user_id,MAX(i.access_hash),'removing',?
			FROM moderation_deleted_cleanup_items i
			JOIN moderation_deleted_cleanup_jobs j ON j.id=i.job_id
			WHERE j.bot_id=? AND i.chat_id=? AND i.status IN ('removed','failed') AND i.job_id<>?
			GROUP BY i.chat_id,i.user_id
		`, jobID, nowStr, botID, chatID, jobID); err != nil {
			return nil, err
		}
	}
	return s.GetDeletedCleanupJob(ctx, botID, jobID)
}

// SaveDeletedCleanupJob 保存清理任务进度、重试时间与终态（终态自动释放 active_key，deleted_cleanup_repository.py:92-105）。
func (s *Store) SaveDeletedCleanupJob(ctx context.Context, job Row) error {
	status := rowString(job, "status")
	botID := rowInt(job, "bot_id")
	var activeKey any
	if IsActiveCleanupStatus(status) {
		activeKey = botID
	}
	var errArg any
	if errMsg := rowString(job, "error"); errMsg != "" {
		errArg = truncateUTF8(errMsg, 256)
	}
	retryTime, ok := rowTime(job, "retry_at")
	if !ok {
		retryTime = db.NowUTC()
	}
	payload := job["payload"]
	if payload == nil {
		payload = map[string]any{}
	}

	_, err := s.Exec(ctx, `
		UPDATE moderation_deleted_cleanup_jobs
		SET status=?,payload=?,error=?,retry_at=?,active_key=?,updated_at=?
		WHERE id=?
	`, status, db.EncodeJSONMust(payload), errArg,
		db.MysqlDatetime6(retryTime), activeKey, db.MysqlDatetime6(db.NowUTC()),
		rowString(job, "id"))
	return err
}

// AddDeletedCleanupCandidates 幂等批量登记某群发现的已销号候选成员（deleted_cleanup_repository.py:107-113）。
func (s *Store) AddDeletedCleanupCandidates(ctx context.Context, jobID string, chatID int64, users []DeletedUserCandidate) error {
	nowStr := db.MysqlDatetime6(db.NowUTC())
	for _, u := range users {
		var hashArg any
		if u.AccessHash != nil {
			hashArg = *u.AccessHash
		}
		if _, err := s.Exec(ctx, `
			INSERT IGNORE INTO moderation_deleted_cleanup_items
			(job_id,chat_id,user_id,access_hash,status,updated_at)
			VALUES (?,?,?,?,'pending',?)
		`, jobID, chatID, u.UserID, hashArg, nowStr); err != nil {
			return err
		}
	}
	return nil
}

// NextPendingDeletedCleanupItem 取出某群下一条待移除成员记录（deleted_cleanup_repository.py:115-120）。
func (s *Store) NextPendingDeletedCleanupItem(ctx context.Context, jobID string, chatID int64) (Row, error) {
	return s.One(ctx, `
		SELECT * FROM moderation_deleted_cleanup_items
		WHERE job_id=? AND chat_id=? AND status IN ('pending','removing')
		ORDER BY user_id LIMIT 1
	`, jobID, chatID)
}

// HistoryPendingDeletedCleanupItems 取出某群待清理遗留历史消息的已销号成员列表（deleted_cleanup_repository.py:122-126）。
func (s *Store) HistoryPendingDeletedCleanupItems(ctx context.Context, jobID string, chatID int64) ([]Row, error) {
	return s.All(ctx, `
		SELECT * FROM moderation_deleted_cleanup_items
		WHERE job_id=? AND chat_id=? AND status='history_pending'
	`, jobID, chatID)
}

// UpdateDeletedCleanupItem 更新单个成员处理状态与说明文案（deleted_cleanup_repository.py:128-133）。
func (s *Store) UpdateDeletedCleanupItem(ctx context.Context, jobID string, chatID, userID int64, status, message string) error {
	_, err := s.Exec(ctx, `
		UPDATE moderation_deleted_cleanup_items
		SET status=?,message=?,updated_at=?
		WHERE job_id=? AND chat_id=? AND user_id=?
	`, status, truncateUTF8(message, 256), db.MysqlDatetime6(db.NowUTC()), jobID, chatID, userID)
	return err
}

// IncrementDeletedCleanupAttempt 调整单个成员尝试次数（保底不小于 0，deleted_cleanup_repository.py:135-140）。
func (s *Store) IncrementDeletedCleanupAttempt(ctx context.Context, jobID string, chatID, userID int64, delta int) error {
	_, err := s.Exec(ctx, `
		UPDATE moderation_deleted_cleanup_items
		SET attempts=GREATEST(0,attempts+?)
		WHERE job_id=? AND chat_id=? AND user_id=?
	`, delta, jobID, chatID, userID)
	return err
}

// DeletedCleanupHistory 读取当前 Bot 最近的已销号成员清理历史任务（deleted_cleanup_repository.py:142-146）。
func (s *Store) DeletedCleanupHistory(ctx context.Context, botID int64, limit int) ([]Row, error) {
	if limit <= 0 {
		limit = 10
	}
	return s.All(ctx,
		"SELECT * FROM moderation_deleted_cleanup_jobs WHERE bot_id=? ORDER BY created_at DESC LIMIT ?",
		botID, limit)
}

// DeletedCleanupJobView 组装 WebUI 任务详情视图（含各状态计数与最新 100 条已完成成员结果，deleted_cleanup_repository.py:148-170）。
func (s *Store) DeletedCleanupJobView(ctx context.Context, job Row) (map[string]any, error) {
	jobID := rowString(job, "id")
	countRows, err := s.All(ctx,
		"SELECT status, COUNT(*) AS total FROM moderation_deleted_cleanup_items WHERE job_id=? GROUP BY status",
		jobID)
	if err != nil {
		return nil, err
	}
	results, err := s.All(ctx, `
		SELECT chat_id,user_id,status,message,updated_at
		FROM moderation_deleted_cleanup_items
		WHERE job_id=? AND status NOT IN ('pending','removing')
		ORDER BY updated_at DESC LIMIT 100
	`, jobID)
	if err != nil {
		return nil, err
	}
	if results == nil {
		results = []Row{}
	}

	counts := make(map[string]int64, len(countRows))
	for _, r := range countRows {
		counts[rowString(r, "status")] = rowInt(r, "total")
	}

	payload, _ := job["payload"].(map[string]any)
	var groups any = []any{}
	var checkedGroups any = int64(0)
	if payload != nil {
		if g, ok := payload["groups"]; ok && g != nil {
			groups = g
		}
		if gi, ok := payload["group_index"]; ok && gi != nil {
			checkedGroups = gi
		}
	}

	view := map[string]any{
		"id":             jobID,
		"actor":          rowString(job, "actor"),
		"trigger_type":   rowString(job, "trigger_type"),
		"status":         rowString(job, "status"),
		"groups":         groups,
		"checked_groups": checkedGroups,
		"error":          job["error"],
		"counts":         counts,
		"results":        results,
	}
	for _, key := range []string{"created_at", "updated_at", "retry_at"} {
		if t, ok := rowTime(job, key); ok {
			view[key] = db.PythonISOFormat(t) + "Z"
		} else if raw := rowString(job, key); raw != "" {
			view[key] = raw + "Z"
		}
	}
	return view, nil
}

// DeletedCleanupJob 免锁读取单条已销号成员清理任务的完整视图（不存在返回 nil, nil）。
func (r *Repository) DeletedCleanupJob(ctx context.Context, botID int64, jobID string) (map[string]any, error) {
	var out map[string]any
	err := r.Reader(ctx, func(s *Store) error {
		job, e := s.GetDeletedCleanupJob(ctx, botID, jobID)
		if e != nil || job == nil {
			return e
		}
		out, e = s.DeletedCleanupJobView(ctx, job)
		return e
	})
	return out, err
}

// DeletedCleanupStatus 免锁读取当前 Bot 的活跃清理任务与历史任务列表。
func (r *Repository) DeletedCleanupStatus(ctx context.Context, botID int64, historyLimit int) (Row, []Row, error) {
	var (
		active  Row
		history []Row
	)
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		active, e = s.ActiveDeletedCleanupJob(ctx, botID)
		if e != nil {
			return e
		}
		history, e = s.DeletedCleanupHistory(ctx, botID, historyLimit)
		return e
	})
	return active, history, err
}

// ────────────────────────────── Cleaned Chat History ──────────────────────────────

// CleanedChatHistory 读取指定 Bot 已执行过退群历史消息清理的 chat_id 集合（repository.py:430-435）。
func (r *Repository) CleanedChatHistory(ctx context.Context, botID int64) (map[int64]bool, error) {
	var rows []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		rows, e = s.All(ctx,
			"SELECT chat_id FROM moderation_cleaned_chat_history WHERE bot_id=?", botID)
		return e
	})
	if err != nil {
		return nil, err
	}
	set := make(map[int64]bool, len(rows))
	for _, r := range rows {
		set[rowInt(r, "chat_id")] = true
	}
	return set, nil
}

// MarkChatHistoryCleaned 标记某群已完成历史消息清理（repository.py:437-443）。
func (r *Repository) MarkChatHistoryCleaned(ctx context.Context, botID, chatID int64, actor string) error {
	now := db.MysqlDatetime(db.NowUTC())
	return r.Reader(ctx, func(s *Store) error {
		_, err := s.Exec(ctx, `
			INSERT INTO moderation_cleaned_chat_history (bot_id,chat_id,actor,cleaned_at)
			VALUES (?,?,?,?)
			ON DUPLICATE KEY UPDATE actor=VALUES(actor),cleaned_at=VALUES(cleaned_at)
		`, botID, chatID, actor, now)
		return err
	})
}

// RestoreChatHistory 在机器人重新入群或授权恢复时清除已清理历史标记（repository.py:445-454）。
func (r *Repository) RestoreChatHistory(ctx context.Context, botID int64, chatIDs []int64) error {
	if len(chatIDs) == 0 {
		return nil
	}
	return r.Reader(ctx, func(s *Store) error {
		for _, cid := range chatIDs {
			if _, err := s.Exec(ctx,
				"DELETE FROM moderation_cleaned_chat_history WHERE bot_id=? AND chat_id=?",
				botID, cid); err != nil {
				return err
			}
		}
		return nil
	})
}
