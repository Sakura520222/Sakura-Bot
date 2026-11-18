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

const (
	// AIReportLeaseDuration 举报 AI 复核单次认领租约时长（对齐 ai_report_repository.py:56 timedelta(minutes=3)）。
	AIReportLeaseDuration = 3 * time.Minute
	// AIReportMaxAttempts 举报 AI 复核最大尝试次数（超过后转人工 manual）。
	AIReportMaxAttempts = 3
)

// EnqueueAIReport 为超时未审举报案件入队 AI 复核任务（对齐 ai_report_repository.py:30-37）。
func (s *Store) EnqueueAIReport(ctx context.Context, caseID string, userID int64, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	now := db.NowUTC()
	nowStr := db.MysqlDatetime6(now)
	retryStr := db.MysqlDatetime6(now.Add(delay))
	_, err := s.Exec(ctx, `
		INSERT INTO moderation_ai_reports
		(case_id,user_id,status,token,attempts,payload,retry_at,created_at,updated_at)
		VALUES (?,?,'queued',?,0,'{}',?,?,?)
		ON DUPLICATE KEY UPDATE case_id=case_id
	`, caseID, userID, uuid4Hex(), retryStr, nowStr, nowStr)
	return err
}

// GetAIReport 按 case_id 读取举报 AI 复核任务（ai_report_repository.py:47-48）。
func (s *Store) GetAIReport(ctx context.Context, caseID string) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_ai_reports WHERE case_id=?", caseID)
}

// ClaimAIReport 带 3 分钟租约与 token 乐观锁认领到期举报 AI 复核任务（ai_report_repository.py:50-58）。
// 若已被其他实例抢占或未到期，返回 (nil, nil)。
func (s *Store) ClaimAIReport(ctx context.Context, caseID, expectedToken string) (Row, error) {
	newToken := uuid4Hex()
	now := db.NowUTC()
	nowStr := db.MysqlDatetime6(now)
	leaseUntilStr := db.MysqlDatetime6(now.Add(AIReportLeaseDuration))

	changed, err := s.Exec(ctx, `
		UPDATE moderation_ai_reports
		SET status='running',token=?,attempts=attempts+1,retry_at=?,updated_at=?
		WHERE case_id=? AND token=?
		  AND status IN ('queued','running') AND retry_at<=?
	`, newToken, leaseUntilStr, nowStr, caseID, expectedToken, nowStr)
	if err != nil || changed == 0 {
		return nil, err
	}
	return s.GetAIReport(ctx, caseID)
}

// SaveAIReport 按当前持有 token 保存举报 AI 复核状态与载荷（ai_report_repository.py:60-74）。
func (s *Store) SaveAIReport(ctx context.Context, caseID, token, state string, payload any, delay time.Duration, errStr *string) (bool, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	if delay < 0 {
		delay = 0
	}
	var errArg any
	if errStr != nil && *errStr != "" {
		errArg = truncateUTF8(*errStr, 128)
	}
	now := db.NowUTC()
	nowStr := db.MysqlDatetime6(now)
	retryStr := db.MysqlDatetime6(now.Add(delay))

	changed, err := s.Exec(ctx, `
		UPDATE moderation_ai_reports
		SET status=?,payload=?,error=?,retry_at=?,updated_at=?
		WHERE case_id=? AND token=?
	`, state, db.EncodeJSONMust(payload), errArg, retryStr, nowStr, caseID, token)
	return changed > 0, err
}

// FinishAIReport 标记举报 AI 复核执行完成（status='done'）。
func (s *Store) FinishAIReport(ctx context.Context, caseID, token string, payload any) (bool, error) {
	return s.SaveAIReport(ctx, caseID, token, "done", payload, 0, nil)
}

// ManualAIReport 将举报 AI 复核任务移交人工审核（status='manual'）。
func (s *Store) ManualAIReport(ctx context.Context, caseID, token string, payload any, errMsg string) (bool, error) {
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	return s.SaveAIReport(ctx, caseID, token, "manual", payload, 0, errPtr)
}

// SkipAIReport 标记举报 AI 复核跳过（如已被人工处理、消息被改或受保护身份，status='skipped'）。
func (s *Store) SkipAIReport(ctx context.Context, caseID, token string, payload any, errMsg string) (bool, error) {
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	return s.SaveAIReport(ctx, caseID, token, "skipped", payload, 0, errPtr)
}

// FailOrRetryAIReport 根据已累计数 attempts（上限 3 次）决定重新排队（queued）或转人工（manual）。
func (s *Store) FailOrRetryAIReport(ctx context.Context, job Row, payload any, retryDelay time.Duration, errMsg string) (string, bool, error) {
	attempts := int(rowInt(job, "attempts"))
	nextState := "queued"
	delay := retryDelay
	if attempts >= AIReportMaxAttempts {
		nextState = "manual"
		delay = 0
	} else if delay <= 0 {
		delay = time.Duration(30*(1<<max(0, attempts-1))) * time.Second
	}
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	saved, err := s.SaveAIReport(ctx, rowString(job, "case_id"), rowString(job, "token"), nextState, payload, delay, errPtr)
	return nextState, saved, err
}

// RescheduleAIReport 退回本次尝试计数并延后到指定时间重试（对齐 ai_report_repository.py:94-99）。
func (s *Store) RescheduleAIReport(ctx context.Context, caseID, token string, deadline time.Time) (bool, error) {
	changed, err := s.Exec(ctx, `
		UPDATE moderation_ai_reports
		SET status='queued',attempts=GREATEST(0,attempts-1),retry_at=?,updated_at=?
		WHERE case_id=? AND token=?
	`, db.MysqlDatetime6(deadline.UTC()), db.MysqlDatetime6(db.NowUTC()), caseID, token)
	return changed > 0, err
}

// DueAIReports 免锁轮询到期的举报 AI 复核候选任务（ai_report_repository.py:39-45）。
func (r *Repository) DueAIReports(ctx context.Context, limit int) ([]Row, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	nowStr := db.MysqlDatetime6(db.NowUTC())
	var out []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		out, e = s.All(ctx, `
			SELECT * FROM moderation_ai_reports
			WHERE status IN ('queued','running','executing')
			  AND retry_at<=?
			ORDER BY retry_at, created_at
			LIMIT ?
		`, nowStr, limit)
		return e
	})
	return out, err
}

func truncateUTF8(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	runes := []rune(s)
	if len(runes) > maxChars {
		runes = runes[:maxChars]
	}
	out := string(runes)
	for len(out) > maxChars && len(runes) > 0 {
		runes = runes[:len(runes)-1]
		out = string(runes)
	}
	return out
}
