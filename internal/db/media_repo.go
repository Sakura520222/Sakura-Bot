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
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ImageAsset image_assets 表物理资产与视觉识别缓存记录（core/media/image_store.py:13-18）。
type ImageAsset struct {
	SHA256      string    `json:"sha256"`
	MimeType    string    `json:"mime_type"`
	ByteSize    int64     `json:"byte_size"`
	StoragePath string    `json:"storage_path"`
	OCR         *string   `json:"ocr,omitempty"`
	Description *string   `json:"description,omitempty"`
	Recognized  bool      `json:"recognized"`
	Model       *string   `json:"model,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// MessageImageInput 采集频道消息图片的输入参数（image_store.py:55-94 capture）。
type MessageImageInput struct {
	ChannelID       string
	MessageID       int64
	MediaID         string
	GroupedID       string
	ChannelName     string
	ChannelUsername string
	SourceDate      time.Time
	SourceLink      string
	Caption         string
	SHA256          string
}

// MessageImageJob message_images 表任务行（含租约与 revision 围栏，image_store.py:19-31）。
type MessageImageJob struct {
	ChannelID       string     `json:"channel_id"`
	MessageID       int64      `json:"message_id"`
	MediaID         string     `json:"media_id"`
	GroupedID       *string    `json:"grouped_id,omitempty"`
	ChannelName     string     `json:"channel_name"`
	ChannelUsername *string    `json:"channel_username,omitempty"`
	SourceDate      time.Time  `json:"source_date"`
	SourceLink      string     `json:"source_link"`
	Caption         string     `json:"caption"`
	SHA256          *string    `json:"sha256,omitempty"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	LastError       *string    `json:"last_error,omitempty"`
	NextAttempt     time.Time  `json:"next_attempt"`
	LeaseUntil      *time.Time `json:"lease_until,omitempty"`
	LeaseToken      *string    `json:"-"`
	Revision        string     `json:"-"`
	Deleted         bool       `json:"deleted"`
}

// MessageImageDetail 消息图片任务连同已识别 OCR/描述结果。
type MessageImageDetail struct {
	MessageImageJob
	MimeType    string  `json:"mime_type,omitempty"`
	ByteSize    int64   `json:"byte_size,omitempty"`
	Recognized  bool    `json:"recognized"`
	OCR         *string `json:"ocr,omitempty"`
	Description *string `json:"description,omitempty"`
	Model       *string `json:"model,omitempty"`
}

// ImageStatsItem 图片任务按状态聚合计数（image_store.py:221-229）。
type ImageStatsItem struct {
	Count   int64 `json:"count"`
	Retries int64 `json:"retries"`
}

// ImageListPage WebUI 图片任务分页与统计视图（不暴露内部 lease_token、revision 与 storage_path）。
type ImageListPage struct {
	Rows   []map[string]any          `json:"rows"`
	Total  int                       `json:"total"`
	Limit  int                       `json:"limit"`
	Offset int                       `json:"offset"`
	Stats  map[string]ImageStatsItem `json:"stats"`
}

const messageImageColumnsSQL = `channel_id, message_id, media_id, grouped_id,
	channel_name, channel_username, source_date, source_link, caption, sha256,
	status, attempts, last_error, next_attempt, lease_until, lease_token, revision, deleted`

// UpsertAsset 幂等注册物理图片资产或写入识别结果（对齐 image_store.py:154-160 save_asset）。
// 若资产已存在且本次未携带新识别结果，保留既有 recognized/ocr/description/model 缓存。
func (m *Manager) UpsertAsset(ctx context.Context, asset ImageAsset) error {
	var ocrArg, descArg, modelArg any
	if asset.OCR != nil {
		ocrArg = *asset.OCR
	}
	if asset.Description != nil {
		descArg = *asset.Description
	}
	if asset.Model != nil {
		modelArg = *asset.Model
	}

	_, err := m.DB.ExecContext(ctx, `
		INSERT INTO image_assets
		(sha256, mime_type, byte_size, storage_path, ocr, description, recognized, model, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE
			mime_type = VALUES(mime_type),
			byte_size = VALUES(byte_size),
			storage_path = VALUES(storage_path),
			ocr = COALESCE(VALUES(ocr), ocr),
			description = COALESCE(VALUES(description), description),
			recognized = IF(VALUES(recognized), TRUE, recognized),
			model = COALESCE(VALUES(model), model),
			updated_at = UTC_TIMESTAMP(6)
	`, asset.SHA256, asset.MimeType, asset.ByteSize, asset.StoragePath,
		ocrArg, descArg, asset.Recognized, modelArg)
	return err
}

// LinkMessageImage 采集或更新频道消息与图片任务的绑定关系（对齐 image_store.py:55-94 capture）。
// 同一媒体的重复未变更事件直接忽略（返回 false, nil），不会重新排队已失败任务；
// 编辑说明文字或替换媒体时刷新 revision 并重新排队。
func (m *Manager) LinkMessageImage(ctx context.Context, rec MessageImageInput) (bool, error) {
	current, err := m.GetMessageImage(ctx, rec.ChannelID, rec.MessageID)
	if err != nil {
		return false, err
	}
	if current != nil && !current.Deleted &&
		current.MediaID == rec.MediaID && current.Caption == rec.Caption {
		return false, nil
	}

	sameMedia := current != nil && !current.Deleted && current.MediaID == rec.MediaID
	var shaArg any
	if rec.SHA256 != "" {
		shaArg = rec.SHA256
	} else if sameMedia && current.SHA256 != nil && *current.SHA256 != "" {
		shaArg = *current.SHA256
	}

	sourceDate := rec.SourceDate
	if sourceDate.IsZero() {
		sourceDate = NowUTC()
	}
	revision := newHexToken32()

	_, err = m.DB.ExecContext(ctx, `
		INSERT INTO message_images
		(channel_id, message_id, media_id, grouped_id, channel_name, channel_username,
		 source_date, source_link, caption, sha256, next_attempt, revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6), ?)
		ON DUPLICATE KEY UPDATE
			media_id = VALUES(media_id),
			grouped_id = VALUES(grouped_id),
			channel_name = VALUES(channel_name),
			channel_username = VALUES(channel_username),
			source_date = VALUES(source_date),
			source_link = VALUES(source_link),
			caption = VALUES(caption),
			sha256 = VALUES(sha256),
			status = 'pending',
			attempts = 0,
			last_error = NULL,
			next_attempt = UTC_TIMESTAMP(6),
			lease_until = NULL,
			lease_token = NULL,
			revision = VALUES(revision),
			deleted = FALSE
	`, rec.ChannelID, rec.MessageID, rec.MediaID, nullableString(rec.GroupedID),
		rec.ChannelName, nullableString(rec.ChannelUsername), MysqlDatetime6(sourceDate.UTC()),
		rec.SourceLink, rec.Caption, shaArg, revision)
	if err != nil {
		return false, err
	}
	return true, nil
}

// BindMessageImageSHA256 在下载完成后把内容哈希写入当前 revision 的任务行（image_pipeline.py:170-174）。
func (m *Manager) BindMessageImageSHA256(ctx context.Context, channelID string, messageID int64, revision, sha256 string) error {
	_, err := m.DB.ExecContext(ctx, `
		UPDATE message_images SET sha256 = ?
		WHERE channel_id = ? AND message_id = ? AND revision = ? AND deleted = FALSE
	`, sha256, channelID, messageID, revision)
	return err
}

// BindMediaIDSHA256 为同 media_id 的待识别记录补齐 sha256（image_pipeline.py:239-243）。
func (m *Manager) BindMediaIDSHA256(ctx context.Context, mediaID, sha256 string) error {
	_, err := m.DB.ExecContext(ctx, `
		UPDATE message_images SET sha256 = ?
		WHERE media_id = ? AND deleted = FALSE AND sha256 IS NULL
	`, sha256, mediaID)
	return err
}

// ClaimPendingAssets 带 10 分钟租约认领到期或租约超时的待处理图片任务（image_store.py:103-114）。
func (m *Manager) ClaimPendingAssets(ctx context.Context, limit int) ([]MessageImageJob, error) {
	if limit <= 0 {
		limit = 1
	}
	token := newHexToken32()
	res, err := m.DB.ExecContext(ctx, `
		UPDATE message_images
		SET status = 'processing',
		    lease_token = ?,
		    lease_until = DATE_ADD(UTC_TIMESTAMP(6), INTERVAL 10 MINUTE)
		WHERE deleted = FALSE
		  AND ((status = 'pending' AND next_attempt <= UTC_TIMESTAMP(6))
		    OR (status = 'processing' AND lease_until < UTC_TIMESTAMP(6)))
		ORDER BY next_attempt
		LIMIT ?
	`, token, limit)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil || affected == 0 {
		return []MessageImageJob{}, err
	}

	rows, err := m.DB.QueryContext(ctx,
		"SELECT "+messageImageColumnsSQL+" FROM message_images WHERE lease_token = ? ORDER BY next_attempt",
		token)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标
	return scanMessageImageJobs(rows)
}

// ClaimOnePendingImage 认领单条到期图片任务（不存在返回 nil, nil）。
func (m *Manager) ClaimOnePendingImage(ctx context.Context) (*MessageImageJob, error) {
	jobs, err := m.ClaimPendingAssets(ctx, 1)
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	return &jobs[0], nil
}

// ClaimDigestImage 按已知 sha256 认领一条未删除任务（供投票即时识别复用，image_store.py:116-126）。
func (m *Manager) ClaimDigestImage(ctx context.Context, sha256 string) (*MessageImageJob, error) {
	token := newHexToken32()
	res, err := m.DB.ExecContext(ctx, `
		UPDATE message_images
		SET status = 'processing',
		    lease_token = ?,
		    lease_until = DATE_ADD(UTC_TIMESTAMP(6), INTERVAL 10 MINUTE)
		WHERE sha256 = ? AND deleted = FALSE
		ORDER BY source_date
		LIMIT 1
	`, token, sha256)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil || affected == 0 {
		return nil, err
	}

	rows, err := m.DB.QueryContext(ctx,
		"SELECT "+messageImageColumnsSQL+" FROM message_images WHERE lease_token = ? LIMIT 1",
		token)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标
	jobs, err := scanMessageImageJobs(rows)
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	return &jobs[0], nil
}

// SaveAssetRecognition 在同一条 UPDATE JOIN 中校验 revision/lease_token 并写入 OCR 与描述结果
// （对齐 image_store.py:162-181 recognize，防止强制重识别后旧请求覆写缓存）。
func (m *Manager) SaveAssetRecognition(ctx context.Context, sha256, ocr, description, model string, job MessageImageJob) (bool, error) {
	if job.LeaseToken == nil || *job.LeaseToken == "" {
		return false, nil
	}
	res, err := m.DB.ExecContext(ctx, `
		UPDATE image_assets a
		JOIN message_images m ON m.sha256 = a.sha256
		SET a.ocr = ?,
		    a.description = ?,
		    a.model = ?,
		    a.recognized = TRUE,
		    a.updated_at = UTC_TIMESTAMP(6)
		WHERE a.sha256 = ?
		  AND m.channel_id = ?
		  AND m.message_id = ?
		  AND m.revision = ?
		  AND m.lease_token = ?
		  AND m.deleted = FALSE
	`, ocr, description, model, sha256, job.ChannelID, job.MessageID, job.Revision, *job.LeaseToken)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

// MarkAssetCompleted 标记图片任务识别并索引完成（status='complete'，释放租约）。
func (m *Manager) MarkAssetCompleted(ctx context.Context, job MessageImageJob, sha256, ocr, description, model string) error {
	if sha256 != "" && (ocr != "" || description != "" || model != "") {
		if _, err := m.SaveAssetRecognition(ctx, sha256, ocr, description, model, job); err != nil {
			return err
		}
	}
	return m.finishImageJob(ctx, job, "complete", sha256, "", 3)
}

// MarkAssetFailed 记录图片任务执行失败：递增 attempts 与指数退避，达到上限后置为 'failed'
// （对齐 image_store.py:128-148 update_job）。
func (m *Manager) MarkAssetFailed(ctx context.Context, job MessageImageJob, errMsg string, maxAttempts int) error {
	if errMsg == "" {
		errMsg = "unknown_error"
	}
	return m.finishImageJob(ctx, job, "pending", "", errMsg, maxAttempts)
}

// MarkAssetSkipped 将图片任务标记为跳过或仅存储待识别（如视觉开关关闭时 status='stored'）。
func (m *Manager) MarkAssetSkipped(ctx context.Context, job MessageImageJob, status, sha256 string) error {
	if status == "" {
		status = "stored"
	}
	return m.finishImageJob(ctx, job, status, sha256, "", 3)
}

func (m *Manager) finishImageJob(ctx context.Context, job MessageImageJob, status, sha256, errMsg string, maxAttempts int) error {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if job.LeaseToken == nil || *job.LeaseToken == "" {
		return nil
	}
	attempts := job.Attempts
	var errArg any
	if errMsg != "" {
		attempts++
		if attempts >= maxAttempts {
			status = "failed"
		} else {
			status = "pending"
		}
		if len(errMsg) > 255 {
			errMsg = errMsg[:255]
		}
		errArg = errMsg
	}

	backoffSec := 1
	if attempts > 0 {
		shift := attempts
		if shift > 9 {
			shift = 9
		}
		backoffSec = 1 << shift
		if backoffSec > 300 {
			backoffSec = 300
		}
	}

	var shaArg any
	if sha256 != "" {
		shaArg = sha256
	}

	_, err := m.DB.ExecContext(ctx, `
		UPDATE message_images
		SET status = ?,
		    sha256 = COALESCE(?, sha256),
		    attempts = ?,
		    last_error = ?,
		    next_attempt = DATE_ADD(UTC_TIMESTAMP(6), INTERVAL ? SECOND),
		    lease_until = NULL,
		    lease_token = NULL
		WHERE channel_id = ?
		  AND message_id = ?
		  AND revision = ?
		  AND deleted = FALSE
		  AND lease_token = ?
	`, status, shaArg, attempts, errArg, backoffSec,
		job.ChannelID, job.MessageID, job.Revision, *job.LeaseToken)
	return err
}

// MarkMessageImageDeleted 在消息删除或移除媒体时使图片任务及关联索引失效（image_store.py:193-198）。
func (m *Manager) MarkMessageImageDeleted(ctx context.Context, channelID string, messageID int64) error {
	_, err := m.DB.ExecContext(ctx, `
		UPDATE message_images
		SET deleted = TRUE,
		    status = 'deleted',
		    revision = ?,
		    caption = '',
		    lease_token = NULL
		WHERE channel_id = ? AND message_id = ?
	`, newHexToken32(), channelID, messageID)
	return err
}

// GetAsset 按 sha256 查询物理图片资产记录（image_store.py:150-152；不存在返回 nil, nil）。
func (m *Manager) GetAsset(ctx context.Context, sha256 string) (*ImageAsset, error) {
	var (
		a     ImageAsset
		ocr   sql.NullString
		desc  sql.NullString
		model sql.NullString
	)
	err := m.DB.QueryRowContext(ctx, `
		SELECT sha256, mime_type, byte_size, storage_path,
		       ocr, description, recognized, model, updated_at
		FROM image_assets
		WHERE sha256 = ?
	`, sha256).Scan(
		&a.SHA256, &a.MimeType, &a.ByteSize, &a.StoragePath,
		&ocr, &desc, &a.Recognized, &model, &a.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ocr.Valid {
		v := ocr.String
		a.OCR = &v
	}
	if desc.Valid {
		v := desc.String
		a.Description = &v
	}
	if model.Valid {
		v := model.String
		a.Model = &v
	}
	return &a, nil
}

// GetMessageImage 按 (channel_id, message_id) 查询单条图片任务（image_store.py:95-101）。
func (m *Manager) GetMessageImage(ctx context.Context, channelID string, messageID int64) (*MessageImageJob, error) {
	rows, err := m.DB.QueryContext(ctx,
		"SELECT "+messageImageColumnsSQL+" FROM message_images WHERE channel_id = ? AND message_id = ? LIMIT 1",
		channelID, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	jobs, err := scanMessageImageJobs(rows)
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	return &jobs[0], nil
}

// GetRecognizedImageText 读取指定消息已完成识别的 OCR 与描述文本（image_store.py:183-191）。
func (m *Manager) GetRecognizedImageText(ctx context.Context, channelID string, messageID int64) (string, string, bool, error) {
	var ocr, desc sql.NullString
	err := m.DB.QueryRowContext(ctx, `
		SELECT a.ocr, a.description
		FROM message_images m
		JOIN image_assets a ON a.sha256 = m.sha256
		WHERE m.channel_id = ? AND m.message_id = ?
		  AND m.deleted = FALSE AND a.recognized = TRUE
	`, channelID, messageID).Scan(&ocr, &desc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return ocr.String, desc.String, true, nil
}

// GetMessageImages 批量查询某频道指定消息（为空时取最近 200 条）的图片与识别详情。
func (m *Manager) GetMessageImages(ctx context.Context, channelID string, messageIDs []int64) ([]MessageImageDetail, error) {
	var (
		query string
		args  []any
	)
	selectCols := `
		SELECT m.channel_id, m.message_id, m.media_id, m.grouped_id,
		       m.channel_name, m.channel_username, m.source_date, m.source_link,
		       m.caption, m.sha256, m.status, m.attempts, m.last_error,
		       m.next_attempt, m.lease_until, m.lease_token, m.revision, m.deleted,
		       a.mime_type, a.byte_size, a.recognized, a.ocr, a.description, a.model
		FROM message_images m
		LEFT JOIN image_assets a ON a.sha256 = m.sha256
	`
	if len(messageIDs) > 0 {
		placeholders := strings.Repeat("?,", len(messageIDs))
		placeholders = placeholders[:len(placeholders)-1]
		query = selectCols + " WHERE m.channel_id = ? AND m.message_id IN (" + placeholders + ") ORDER BY m.message_id ASC"
		args = make([]any, 0, 1+len(messageIDs))
		args = append(args, channelID)
		for _, id := range messageIDs {
			args = append(args, id)
		}
	} else {
		query = selectCols + " WHERE m.channel_id = ? AND m.deleted = FALSE ORDER BY m.source_date DESC LIMIT 200"
		args = []any{channelID}
	}

	rows, err := m.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	out := make([]MessageImageDetail, 0)
	for rows.Next() {
		var (
			d          MessageImageDetail
			groupedID  sql.NullString
			chUser     sql.NullString
			sha        sql.NullString
			lastErr    sql.NullString
			leaseUntil sql.NullTime
			leaseToken sql.NullString
			mimeType   sql.NullString
			byteSize   sql.NullInt64
			recognized sql.NullBool
			ocr        sql.NullString
			desc       sql.NullString
			model      sql.NullString
		)
		if err := rows.Scan(
			&d.ChannelID, &d.MessageID, &d.MediaID, &groupedID,
			&d.ChannelName, &chUser, &d.SourceDate, &d.SourceLink,
			&d.Caption, &sha, &d.Status, &d.Attempts, &lastErr,
			&d.NextAttempt, &leaseUntil, &leaseToken, &d.Revision, &d.Deleted,
			&mimeType, &byteSize, &recognized, &ocr, &desc, &model,
		); err != nil {
			return nil, err
		}
		if groupedID.Valid {
			v := groupedID.String
			d.GroupedID = &v
		}
		if chUser.Valid {
			v := chUser.String
			d.ChannelUsername = &v
		}
		if sha.Valid {
			v := sha.String
			d.SHA256 = &v
		}
		if lastErr.Valid {
			v := lastErr.String
			d.LastError = &v
		}
		if leaseUntil.Valid {
			t := leaseUntil.Time
			d.LeaseUntil = &t
		}
		if leaseToken.Valid {
			v := leaseToken.String
			d.LeaseToken = &v
		}
		d.MimeType = mimeType.String
		d.ByteSize = byteSize.Int64
		d.Recognized = recognized.Bool
		if ocr.Valid {
			v := ocr.String
			d.OCR = &v
		}
		if desc.Valid {
			v := desc.String
			d.Description = &v
		}
		if model.Valid {
			v := model.String
			d.Model = &v
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RetryMessageImage 重新排队单条图片任务；force=true 时同步作废同哈希资产识别缓存
// （对齐 image_store.py:200-219 与 web_api/routes/images.py:53-60）。
func (m *Manager) RetryMessageImage(ctx context.Context, channelID string, messageID int64, force bool) (bool, error) {
	job, err := m.GetMessageImage(ctx, channelID, messageID)
	if err != nil {
		return false, err
	}
	if job == nil || job.Deleted {
		return false, nil
	}
	rev := newHexToken32()
	if force && job.SHA256 != nil && *job.SHA256 != "" {
		_, err = m.DB.ExecContext(ctx, `
			UPDATE image_assets a
			JOIN message_images m ON m.sha256 = a.sha256
			SET a.recognized = FALSE,
			    m.status = 'pending',
			    m.attempts = 0,
			    m.next_attempt = UTC_TIMESTAMP(6),
			    m.revision = ?,
			    m.lease_token = NULL
			WHERE a.sha256 = ? AND m.deleted = FALSE
		`, rev, *job.SHA256)
		return err == nil, err
	}
	_, err = m.DB.ExecContext(ctx, `
		UPDATE message_images
		SET status = 'pending',
		    attempts = 0,
		    next_attempt = UTC_TIMESTAMP(6),
		    revision = ?,
		    lease_token = NULL
		WHERE channel_id = ? AND message_id = ? AND deleted = FALSE
	`, rev, channelID, messageID)
	return err == nil, err
}

// GetImageJobStats 按状态统计图片任务数与累计重试次数（对齐 image_store.py:221-229 与 /api/images/stats）。
func (m *Manager) GetImageJobStats(ctx context.Context) (map[string]ImageStatsItem, error) {
	rows, err := m.DB.QueryContext(ctx, `
		SELECT status, COUNT(*) AS count, COALESCE(SUM(attempts), 0) AS retries
		FROM message_images
		GROUP BY status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	stats := make(map[string]ImageStatsItem)
	for rows.Next() {
		var (
			st      string
			cnt     int64
			retries int64
		)
		if err := rows.Scan(&st, &cnt, &retries); err != nil {
			return nil, err
		}
		stats[st] = ImageStatsItem{Count: cnt, Retries: retries}
	}
	return stats, rows.Err()
}

// ListAssetsForWebUI 分页查询 WebUI 图片任务与识别概况，过滤内部 lease_token/revision/storage_path
// （对齐 core/web_api/routes/images.py 的脱敏与状态统计要求）。
func (m *Manager) ListAssetsForWebUI(ctx context.Context, channelID, status string, limit, offset int) (*ImageListPage, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	var (
		conditions []string
		params     []any
	)
	if channelID != "" {
		conditions = append(conditions, "m.channel_id = ?")
		params = append(params, channelID)
	}
	if status != "" {
		conditions = append(conditions, "m.status = ?")
		params = append(params, status)
	}
	whereClause := "1=1"
	if len(conditions) > 0 {
		whereClause = strings.Join(conditions, " AND ")
	}

	var total int
	if err := m.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM message_images m WHERE "+whereClause, params...).Scan(&total); err != nil {
		return nil, err
	}

	queryParams := make([]any, 0, len(params)+2)
	queryParams = append(queryParams, params...)
	queryParams = append(queryParams, limit, offset)

	rows, err := m.DB.QueryContext(ctx, `
		SELECT m.channel_id, m.message_id, m.media_id, m.grouped_id,
		       m.channel_name, m.channel_username, m.source_date, m.source_link,
		       m.caption, m.sha256, m.status, m.attempts, m.last_error,
		       m.next_attempt, m.lease_until, m.deleted,
		       a.mime_type, a.byte_size, a.recognized, a.ocr, a.description, a.model
		FROM message_images m
		LEFT JOIN image_assets a ON a.sha256 = m.sha256
		WHERE `+whereClause+`
		ORDER BY m.source_date DESC, m.message_id DESC
		LIMIT ? OFFSET ?
	`, queryParams...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	items := make([]map[string]any, 0)
	for rows.Next() {
		var (
			chID       string
			msgID      int64
			mediaID    string
			groupedID  sql.NullString
			chName     string
			chUser     sql.NullString
			srcDate    time.Time
			srcLink    string
			caption    string
			sha        sql.NullString
			st         string
			attempts   int
			lastErr    sql.NullString
			nextAt     time.Time
			leaseUntil sql.NullTime
			deleted    bool
			mimeType   sql.NullString
			byteSize   sql.NullInt64
			recognized sql.NullBool
			ocr        sql.NullString
			desc       sql.NullString
			model      sql.NullString
		)
		if err := rows.Scan(
			&chID, &msgID, &mediaID, &groupedID,
			&chName, &chUser, &srcDate, &srcLink,
			&caption, &sha, &st, &attempts, &lastErr,
			&nextAt, &leaseUntil, &deleted,
			&mimeType, &byteSize, &recognized, &ocr, &desc, &model,
		); err != nil {
			return nil, err
		}
		row := map[string]any{
			"channel_id":       chID,
			"message_id":       msgID,
			"media_id":         mediaID,
			"grouped_id":       nullStrAny(groupedID),
			"channel_name":     chName,
			"channel_username": nullStrAny(chUser),
			"source_date":      PythonISOFormat(srcDate),
			"source_link":      srcLink,
			"caption":          caption,
			"sha256":           nullStrAny(sha),
			"status":           st,
			"attempts":         attempts,
			"last_error":       nullStrAny(lastErr),
			"next_attempt":     PythonISOFormat(nextAt),
			"lease_until":      nullTimeISOAny(leaseUntil),
			"deleted":          deleted,
			"mime_type":        nullStrAny(mimeType),
			"byte_size":        nullIntAny(byteSize),
			"recognized":       recognized.Bool,
			"ocr":              nullStrAny(ocr),
			"description":      nullStrAny(desc),
			"model":            nullStrAny(model),
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	stats, err := m.GetImageJobStats(ctx)
	if err != nil {
		return nil, err
	}

	return &ImageListPage{
		Rows:   items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
		Stats:  stats,
	}, nil
}

// PromoteStoredImagesForVision 在视觉能力开启时把 status='stored' 的图片提升回 'pending'（image_pipeline.py:299-301）。
func (m *Manager) PromoteStoredImagesForVision(ctx context.Context) (int64, error) {
	res, err := m.DB.ExecContext(ctx,
		"UPDATE message_images SET status = 'pending' WHERE status = 'stored' AND deleted = FALSE")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanMessageImageJobs(rows *sql.Rows) ([]MessageImageJob, error) {
	out := make([]MessageImageJob, 0)
	for rows.Next() {
		var (
			j          MessageImageJob
			groupedID  sql.NullString
			chUser     sql.NullString
			sha        sql.NullString
			lastErr    sql.NullString
			leaseUntil sql.NullTime
			leaseToken sql.NullString
		)
		if err := rows.Scan(
			&j.ChannelID, &j.MessageID, &j.MediaID, &groupedID,
			&j.ChannelName, &chUser, &j.SourceDate, &j.SourceLink,
			&j.Caption, &sha, &j.Status, &j.Attempts, &lastErr,
			&j.NextAttempt, &leaseUntil, &leaseToken, &j.Revision, &j.Deleted,
		); err != nil {
			return nil, err
		}
		if groupedID.Valid {
			v := groupedID.String
			j.GroupedID = &v
		}
		if chUser.Valid {
			v := chUser.String
			j.ChannelUsername = &v
		}
		if sha.Valid {
			v := sha.String
			j.SHA256 = &v
		}
		if lastErr.Valid {
			v := lastErr.String
			j.LastError = &v
		}
		if leaseUntil.Valid {
			t := leaseUntil.Time
			j.LeaseUntil = &t
		}
		if leaseToken.Valid {
			v := leaseToken.String
			j.LeaseToken = &v
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func newHexToken32() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand 读取失败: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}

func nullStrAny(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func nullIntAny(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func nullTimeISOAny(v sql.NullTime) any {
	if !v.Valid {
		return nil
	}
	return PythonISOFormat(v.Time)
}
