// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Submission 对齐 submissions 表完整记录（core/infrastructure/database/submission_repo.py）。
type Submission struct {
	ID                 int64            `json:"id"`
	SubmitterID        int64            `json:"submitter_id"`
	SubmitterName      string           `json:"submitter_name"`
	Title              string           `json:"title"`
	Content            string           `json:"content"`
	MediaFiles         []map[string]any `json:"media_files"`
	TargetChannel      string           `json:"target_channel"`
	IsAnonymous        bool             `json:"is_anonymous"`
	SignatureForced    bool             `json:"signature_forced"`
	Status             string           `json:"status"`
	AIOptimizedContent *string          `json:"ai_optimized_content"`
	AIOptimizedTitle   *string          `json:"ai_optimized_title"`
	ReviewMessageID    *int64           `json:"review_message_id"`
	ReviewedBy         *int64           `json:"reviewed_by"`
	ReviewedAt         *time.Time       `json:"reviewed_at"`
	CreatedAt          time.Time        `json:"created_at"`
}

// SubmissionInsert 创建投稿输入结构（submission_repo.py:48-99）。
type SubmissionInsert struct {
	SubmitterID   int64
	SubmitterName string
	Title         string
	Content       string
	MediaFiles    []map[string]any
	TargetChannel string
	IsAnonymous   bool
}

// SubmissionStatusUpdate 动态更新投稿状态选项（submission_repo.py:188-256）。
type SubmissionStatusUpdate struct {
	Status             string
	ReviewedBy         *int64
	AIOptimizedContent *string
	AIOptimizedTitle   *string
	ReviewMessageID    *int64
	SignatureForced    *bool
	ClearAIContent     bool
}

const submissionColumnsSQL = `id, submitter_id, submitter_name, title, content,
	media_files, target_channel, is_anonymous, signature_forced, status,
	ai_optimized_content, ai_optimized_title, review_message_id,
	reviewed_by, reviewed_at, created_at`

// CreateSubmission 创建一条待审核投稿记录（submission_repo.py:48-99）。
func (m *Manager) CreateSubmission(ctx context.Context, in SubmissionInsert) (int64, error) {
	var mediaArg any
	if len(in.MediaFiles) > 0 {
		encoded, err := EncodeJSON(in.MediaFiles)
		if err != nil {
			return 0, err
		}
		mediaArg = encoded
	}

	res, err := m.DB.ExecContext(ctx, `
		INSERT INTO submissions
		(submitter_id, submitter_name, title, content, media_files,
		 target_channel, is_anonymous, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending')
	`, in.SubmitterID, nullableString(in.SubmitterName), in.Title,
		nullableString(in.Content), mediaArg, nullableString(in.TargetChannel), in.IsAnonymous)
	if err != nil {
		slog.Error(fmt.Sprintf("创建投稿记录失败: %T: %s", err, err))
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	slog.Info(fmt.Sprintf("创建投稿记录成功: ID=%d, 投稿者=%s", id, in.SubmitterName))
	return id, nil
}

// GetSubmissionByID 按 ID 获取单条投稿记录（submission_repo.py:101-125；不存在返回 nil, nil）。
func (m *Manager) GetSubmissionByID(ctx context.Context, id int64) (*Submission, error) {
	rows, err := m.DB.QueryContext(ctx,
		"SELECT "+submissionColumnsSQL+" FROM submissions WHERE id = ? LIMIT 1", id)
	if err != nil {
		slog.Error(fmt.Sprintf("获取投稿记录失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	list, err := scanSubmissions(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

// ListPendingSubmissions 获取尚未发送管理员审核卡片的 pending 投稿列表
// （submission_repo.py:127-154：status='pending' AND review_message_id IS NULL）。
func (m *Manager) ListPendingSubmissions(ctx context.Context) ([]Submission, error) {
	rows, err := m.DB.QueryContext(ctx, `
		SELECT `+submissionColumnsSQL+`
		FROM submissions
		WHERE status = 'pending' AND review_message_id IS NULL
		ORDER BY created_at ASC
	`)
	if err != nil {
		slog.Error(fmt.Sprintf("获取待审核投稿失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标
	return scanSubmissions(rows)
}

// ListUserSubmissions 获取指定用户的投稿历史（submission_repo.py:156-186）。
func (m *Manager) ListUserSubmissions(ctx context.Context, userID int64, limit int) ([]Submission, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := m.DB.QueryContext(ctx, `
		SELECT `+submissionColumnsSQL+`
		FROM submissions
		WHERE submitter_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, userID, limit)
	if err != nil {
		slog.Error(fmt.Sprintf("获取用户投稿历史失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标
	return scanSubmissions(rows)
}

// UpdateSubmissionStatus 通用更新投稿状态及可选审核字段（对齐 submission_repo.py:188-256）。
func (m *Manager) UpdateSubmissionStatus(ctx context.Context, id int64, opts SubmissionStatusUpdate) error {
	status := opts.Status
	if status == "" {
		status = "pending"
	}
	fields := []string{"status = ?"}
	params := []any{status}

	if opts.ReviewedBy != nil {
		fields = append(fields, "reviewed_by = ?", "reviewed_at = ?")
		params = append(params, *opts.ReviewedBy, MysqlDatetime(NowUTC()))
	}
	if opts.AIOptimizedContent != nil {
		fields = append(fields, "ai_optimized_content = ?")
		params = append(params, *opts.AIOptimizedContent)
	}
	if opts.AIOptimizedTitle != nil {
		fields = append(fields, "ai_optimized_title = ?")
		params = append(params, *opts.AIOptimizedTitle)
	}
	if opts.ClearAIContent {
		fields = append(fields, "ai_optimized_content = NULL", "ai_optimized_title = NULL")
	}
	if opts.ReviewMessageID != nil {
		fields = append(fields, "review_message_id = ?")
		params = append(params, *opts.ReviewMessageID)
	}
	if opts.SignatureForced != nil {
		fields = append(fields, "signature_forced = ?")
		params = append(params, *opts.SignatureForced)
	}

	params = append(params, id)
	query := "UPDATE submissions SET " + strings.Join(fields, ", ") + " WHERE id = ?"
	if _, err := m.DB.ExecContext(ctx, query, params...); err != nil {
		slog.Error(fmt.Sprintf("更新投稿状态失败: %T: %s", err, err))
		return err
	}
	slog.Info(fmt.Sprintf("更新投稿状态: ID=%d, status=%s", id, status))
	return nil
}

// SetSubmissionReviewMessageID 记录已通知管理员的首条审核消息 ID（submission_review_handler.py:136-140）。
func (m *Manager) SetSubmissionReviewMessageID(ctx context.Context, id, reviewMessageID int64) error {
	return m.UpdateSubmissionStatus(ctx, id, SubmissionStatusUpdate{
		Status:          "pending",
		ReviewMessageID: &reviewMessageID,
	})
}

// SaveAIOptimization 保存 AI 优化后的标题与正文（submission_service.py:166-171）。
func (m *Manager) SaveAIOptimization(ctx context.Context, id int64, optimizedTitle, optimizedContent string) error {
	return m.UpdateSubmissionStatus(ctx, id, SubmissionStatusUpdate{
		Status:             "pending",
		AIOptimizedTitle:   &optimizedTitle,
		AIOptimizedContent: &optimizedContent,
	})
}

// ClearAIOptimization 清除投稿的 AI 优化内容，恢复原文（submission_review_handler.py:420-424）。
func (m *Manager) ClearAIOptimization(ctx context.Context, id int64) error {
	return m.UpdateSubmissionStatus(ctx, id, SubmissionStatusUpdate{
		Status:         "pending",
		ClearAIContent: true,
	})
}

// ApproveSubmission 审核通过投稿并返回更新后的记录（submission_service.py:187-215）。
func (m *Manager) ApproveSubmission(ctx context.Context, id, reviewedBy int64, signatureForced bool) (*Submission, error) {
	opts := SubmissionStatusUpdate{
		Status:     "approved",
		ReviewedBy: &reviewedBy,
	}
	if signatureForced {
		opts.SignatureForced = &signatureForced
	}
	if err := m.UpdateSubmissionStatus(ctx, id, opts); err != nil {
		return nil, err
	}
	return m.GetSubmissionByID(ctx, id)
}

// RejectSubmission 审核拒绝投稿（submission_service.py:217-231）。
func (m *Manager) RejectSubmission(ctx context.Context, id, reviewedBy int64) error {
	return m.UpdateSubmissionStatus(ctx, id, SubmissionStatusUpdate{
		Status:     "rejected",
		ReviewedBy: &reviewedBy,
	})
}

// MarkSubmissionPublished 标记已通过的投稿完成频道发布。
func (m *Manager) MarkSubmissionPublished(ctx context.Context, id int64) error {
	_, err := m.DB.ExecContext(ctx, `
		UPDATE submissions
		SET status = 'published', reviewed_at = COALESCE(reviewed_at, ?)
		WHERE id = ?
	`, MysqlDatetime(NowUTC()), id)
	if err != nil {
		slog.Error(fmt.Sprintf("标记投稿已发布失败: %T: %s", err, err))
		return err
	}
	return nil
}

// GetSubmissionStats 按状态统计投稿数量（submission_repo.py:258-276）。
func (m *Manager) GetSubmissionStats(ctx context.Context) (map[string]int64, error) {
	rows, err := m.DB.QueryContext(ctx, `
		SELECT status, COUNT(*) AS count
		FROM submissions
		GROUP BY status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	stats := make(map[string]int64)
	for rows.Next() {
		var (
			st  sql.NullString
			cnt int64
		)
		if err := rows.Scan(&st, &cnt); err != nil {
			return nil, err
		}
		stats[st.String] = cnt
	}
	return stats, rows.Err()
}

func scanSubmissions(rows *sql.Rows) ([]Submission, error) {
	out := make([]Submission, 0)
	for rows.Next() {
		var (
			s          Submission
			subName    sql.NullString
			content    sql.NullString
			mediaRaw   []byte
			targetCh   sql.NullString
			isAnon     sql.NullBool
			sigForced  sql.NullBool
			status     sql.NullString
			aiContent  sql.NullString
			aiTitle    sql.NullString
			reviewMsg  sql.NullInt64
			reviewedBy sql.NullInt64
			reviewedAt sql.NullTime
			createdAt  sql.NullTime
		)
		if err := rows.Scan(
			&s.ID, &s.SubmitterID, &subName, &s.Title, &content,
			&mediaRaw, &targetCh, &isAnon, &sigForced, &status,
			&aiContent, &aiTitle, &reviewMsg,
			&reviewedBy, &reviewedAt, &createdAt,
		); err != nil {
			return nil, err
		}
		s.SubmitterName = subName.String
		s.Content = content.String
		s.MediaFiles = parseSubmissionMediaFiles(mediaRaw)
		s.TargetChannel = targetCh.String
		s.IsAnonymous = isAnon.Bool
		s.SignatureForced = sigForced.Bool
		s.Status = status.String
		if s.Status == "" {
			s.Status = "pending"
		}
		if aiContent.Valid {
			v := aiContent.String
			s.AIOptimizedContent = &v
		}
		if aiTitle.Valid {
			v := aiTitle.String
			s.AIOptimizedTitle = &v
		}
		if reviewMsg.Valid {
			v := reviewMsg.Int64
			s.ReviewMessageID = &v
		}
		if reviewedBy.Valid {
			v := reviewedBy.Int64
			s.ReviewedBy = &v
		}
		if reviewedAt.Valid {
			t := reviewedAt.Time
			s.ReviewedAt = &t
		}
		if createdAt.Valid {
			s.CreatedAt = createdAt.Time
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return out, nil
}

func parseSubmissionMediaFiles(raw []byte) []map[string]any {
	if len(raw) == 0 {
		return []map[string]any{}
	}
	var files []map[string]any
	if err := UnmarshalJSONCompat(raw, &files); err != nil || files == nil {
		return []map[string]any{}
	}
	return files
}
