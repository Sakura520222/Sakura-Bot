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

// BotRequestRow 面向主 Bot 请求轮询与审批的 request_queue 行结构
// （对齐 core/handlers/mainbot_request_handler.py 与 mysql.py:1880-1966）。
type BotRequestRow struct {
	ID            int64          `json:"id"`
	RequestType   string         `json:"request_type"`
	RequestedBy   int64          `json:"requested_by"`
	TargetChannel string         `json:"target_channel"`
	Params        map[string]any `json:"params,omitempty"`
	Status        string         `json:"status"`
	Result        map[string]any `json:"result,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	ProcessedAt   *time.Time     `json:"processed_at,omitempty"`
}

// NotificationInsert 跨进程通知入队参数（严格对齐 notification_queue 表与 push_job.go）。
type NotificationInsert struct {
	UserID           int64
	NotificationType string
	Content          map[string]any
}

// UserRecord users 表完整用户信息（对齐 mysql.py:1499-1589 get_user / get_registered_users）。
type UserRecord struct {
	UserID       int64          `json:"user_id"`
	Username     string         `json:"username"`
	FirstName    string         `json:"first_name"`
	RegisteredAt *time.Time     `json:"registered_at"`
	LastActive   *time.Time     `json:"last_active"`
	IsAdmin      bool           `json:"is_admin"`
	Preferences  map[string]any `json:"preferences,omitempty"`
}

const requestColumnsSQL = `id, request_type, requested_by, target_channel, params, status, result, created_at, processed_at`

// HasRecentSummaryRequest 检查在指定时间窗口内是否已有未完结的总结申请
// （对齐 mysql.py:2376-2398 check_pending_summary_request 与 channel_comment_welcome.py:788）。
//
// 过滤规则：
//   - within <= 0 时默认 10 分钟；
//   - channelID 非空时按 target_channel 过滤；
//   - userID > 0 时追加 requested_by 过滤，userID <= 0 时按频道全局去重。
func (m *Manager) HasRecentSummaryRequest(ctx context.Context, userID int64, channelID string, within time.Duration) (bool, error) {
	if within <= 0 {
		within = 10 * time.Minute
	}
	cutoff := MysqlDatetime(NowUTC().Add(-within))

	conditions := []string{
		"request_type IN ('summary_request', 'summary')",
		"status IN ('pending', 'processing')",
		"created_at > ?",
	}
	params := []any{cutoff}

	if channelID != "" {
		conditions = append(conditions, "target_channel = ?")
		params = append(params, channelID)
	}
	if userID > 0 {
		conditions = append(conditions, "requested_by = ?")
		params = append(params, userID)
	}

	var count int64
	query := "SELECT COUNT(*) FROM request_queue WHERE " + strings.Join(conditions, " AND ")
	if err := m.DB.QueryRowContext(ctx, query, params...).Scan(&count); err != nil {
		slog.Error(fmt.Sprintf("检查待处理周报请求失败: %T: %s", err, err))
		return false, err
	}
	return count > 0, nil
}

// EnqueueSummaryRequest 将讨论组欢迎按钮或主 Bot 侧的周报申请写入 request_queue
// （对齐 mysql.py:2336-2374 add_summary_request）。
func (m *Manager) EnqueueSummaryRequest(ctx context.Context, userID int64, channelID string, msgID int64, params map[string]any) (int64, error) {
	merged := map[string]any{
		"message_id":   msgID,
		"request_type": "manual",
	}
	for k, v := range params {
		merged[k] = v
	}
	encoded, err := EncodeJSON(merged)
	if err != nil {
		return 0, err
	}

	res, err := m.DB.ExecContext(ctx, `
		INSERT INTO request_queue
		(request_type, requested_by, target_channel, params, status)
		VALUES ('summary_request', ?, ?, ?, 'pending')
	`, userID, nullableString(channelID), encoded)
	if err != nil {
		slog.Error(fmt.Sprintf("添加周报请求失败: %T: %s", err, err))
		return 0, err
	}
	reqID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	slog.Info(fmt.Sprintf("添加周报请求: id=%d, channel=%s, msg_id=%d, user=%d", reqID, channelID, msgID, userID))
	return reqID, nil
}

// FetchPendingRequests 获取待处理的跨进程请求列表（FIFO，对齐 mysql.py:1880-1914 get_pending_requests）。
func (m *Manager) FetchPendingRequests(ctx context.Context, limit int) ([]BotRequestRow, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := m.DB.QueryContext(ctx, `
		SELECT `+requestColumnsSQL+`
		FROM request_queue
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		slog.Error(fmt.Sprintf("获取待处理请求失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标
	return scanBotRequestRows(rows)
}

// GetRequestByID 按 ID 获取单条跨进程请求行（不存在返回 nil, nil）。
func (m *Manager) GetRequestByID(ctx context.Context, id int64) (*BotRequestRow, error) {
	rows, err := m.DB.QueryContext(ctx,
		"SELECT "+requestColumnsSQL+" FROM request_queue WHERE id = ? LIMIT 1", id)
	if err != nil {
		slog.Error(fmt.Sprintf("获取请求状态失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	list, err := scanBotRequestRows(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

// UpdateRequestStatus 更新请求状态及结果载荷（对齐 mysql.py:1916-1939 与 mainbot_request_handler.py）。
// 若 errMsg 非空且 result 未提供 "error"，自动注入 {"error": errMsg}。
func (m *Manager) UpdateRequestStatus(ctx context.Context, id int64, status string, result map[string]any, errMsg string) error {
	var merged map[string]any
	if len(result) > 0 || errMsg != "" {
		merged = make(map[string]any, len(result)+1)
		for k, v := range result {
			merged[k] = v
		}
		if errMsg != "" {
			if _, exists := merged["error"]; !exists {
				merged["error"] = errMsg
			}
		}
	}

	var resultArg any
	if len(merged) > 0 {
		encoded, err := EncodeJSON(merged)
		if err != nil {
			return err
		}
		resultArg = encoded
	}

	if _, err := m.DB.ExecContext(ctx, `
		UPDATE request_queue
		SET status = ?, result = ?, processed_at = NOW()
		WHERE id = ?
	`, status, resultArg, id); err != nil {
		slog.Error(fmt.Sprintf("更新请求状态失败: %T: %s", err, err))
		return err
	}
	slog.Info(fmt.Sprintf("更新请求状态: id=%d, status=%s", id, status))
	return nil
}

// EnqueueNotification 将通知写入 notification_queue 供 QA Bot 轮询投递
// （对齐 mysql.py:1995-2023 create_notification 与 internal/handlers/push_job.go）。
func (m *Manager) EnqueueNotification(ctx context.Context, in NotificationInsert) (int64, error) {
	var contentArg any
	if len(in.Content) > 0 {
		encoded, err := EncodeJSON(in.Content)
		if err != nil {
			slog.Error(fmt.Sprintf("创建通知失败: %T: %s", err, err))
			return 0, err
		}
		contentArg = encoded
	}

	res, err := m.DB.ExecContext(ctx, `
		INSERT INTO notification_queue
		(user_id, notification_type, content)
		VALUES (?, ?, ?)
	`, in.UserID, in.NotificationType, contentArg)
	if err != nil {
		slog.Error(fmt.Sprintf("创建通知失败: %T: %s", err, err))
		return 0, err
	}
	notifID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	slog.Info(fmt.Sprintf("创建通知: id=%d, type=%s, user=%d", notifID, in.NotificationType, in.UserID))
	return notifID, nil
}

// ListChannelSummarySubscribers 查询订阅了指定频道总结推送的全部启用用户 ID
// （对齐 mysql.py:1700-1720 get_channel_subscribers(channel_id, "summary")）。
func (m *Manager) ListChannelSummarySubscribers(ctx context.Context, channelID string) ([]int64, error) {
	rows, err := m.DB.QueryContext(ctx, `
		SELECT user_id FROM subscriptions
		WHERE channel_id = ? AND sub_type = 'summary' AND enabled = TRUE
	`, channelID)
	if err != nil {
		slog.Error(fmt.Sprintf("获取频道订阅者失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	subscribers := make([]int64, 0)
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		subscribers = append(subscribers, uid)
	}
	return subscribers, rows.Err()
}

// RemoveBlockedSubscriber 在推送遇到用户拉黑或停用账号时移除对应频道订阅
// （对齐 mainbot_push_handler.py:110-118）。
func (m *Manager) RemoveBlockedSubscriber(ctx context.Context, userID int64, channelID string) error {
	_, err := m.RemoveSubscription(ctx, userID, channelID, "")
	return err
}

// GetUser 按 user_id 获取完整用户信息（mysql.py:1499-1519；不存在返回 nil, nil）。
func (m *Manager) GetUser(ctx context.Context, userID int64) (*UserRecord, error) {
	var (
		u        UserRecord
		uname    sql.NullString
		fname    sql.NullString
		regAt    sql.NullTime
		activeAt sql.NullTime
		isAdmin  sql.NullBool
		prefsRaw []byte
	)
	err := m.DB.QueryRowContext(ctx, `
		SELECT user_id, username, first_name, registered_at, last_active, is_admin, preferences
		FROM users
		WHERE user_id = ?
	`, userID).Scan(&u.UserID, &uname, &fname, &regAt, &activeAt, &isAdmin, &prefsRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("获取用户信息失败: %T: %s", err, err))
		return nil, err
	}
	u.Username = uname.String
	u.FirstName = fname.String
	if regAt.Valid {
		t := regAt.Time
		u.RegisteredAt = &t
	}
	if activeAt.Valid {
		t := activeAt.Time
		u.LastActive = &t
	}
	u.IsAdmin = isAdmin.Bool
	if len(prefsRaw) > 0 {
		if parsed, ok := ParseJSONColumn(prefsRaw).(map[string]any); ok {
			u.Preferences = parsed
		}
	}
	return &u, nil
}

// GetUserInfo GetUser 的别名（对齐 mysql.py:2161-2163 get_user_info）。
func (m *Manager) GetUserInfo(ctx context.Context, userID int64) (*UserRecord, error) {
	return m.GetUser(ctx, userID)
}

// SetUserAdmin 设置用户的管理员标志（mysql.py:1543-1563）。
func (m *Manager) SetUserAdmin(ctx context.Context, userID int64, isAdmin bool) error {
	flag := 0
	if isAdmin {
		flag = 1
	}
	_, err := m.DB.ExecContext(ctx, "UPDATE users SET is_admin = ? WHERE user_id = ?", flag, userID)
	return err
}

// ListRegisteredUsers 获取最近 activeDays 天内活跃的注册用户列表（mysql.py:1565-1588）。
func (m *Manager) ListRegisteredUsers(ctx context.Context, activeDays, limit int) ([]UserRecord, error) {
	if activeDays <= 0 {
		activeDays = 30
	}
	if limit <= 0 {
		limit = 100
	}
	cutoff := MysqlDatetime(NowUTC().Add(-time.Duration(activeDays) * 24 * time.Hour))
	rows, err := m.DB.QueryContext(ctx, `
		SELECT user_id, username, first_name, registered_at, last_active, is_admin, preferences
		FROM users
		WHERE last_active >= ?
		ORDER BY last_active DESC
		LIMIT ?
	`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	out := make([]UserRecord, 0)
	for rows.Next() {
		var (
			u        UserRecord
			uname    sql.NullString
			fname    sql.NullString
			regAt    sql.NullTime
			activeAt sql.NullTime
			isAdmin  sql.NullBool
			prefsRaw []byte
		)
		if err := rows.Scan(&u.UserID, &uname, &fname, &regAt, &activeAt, &isAdmin, &prefsRaw); err != nil {
			return nil, err
		}
		u.Username = uname.String
		u.FirstName = fname.String
		if regAt.Valid {
			t := regAt.Time
			u.RegisteredAt = &t
		}
		if activeAt.Valid {
			t := activeAt.Time
			u.LastActive = &t
		}
		u.IsAdmin = isAdmin.Bool
		if len(prefsRaw) > 0 {
			if parsed, ok := ParseJSONColumn(prefsRaw).(map[string]any); ok {
				u.Preferences = parsed
			}
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func scanBotRequestRows(rows *sql.Rows) ([]BotRequestRow, error) {
	out := make([]BotRequestRow, 0)
	for rows.Next() {
		var (
			r         BotRequestRow
			targetCh  sql.NullString
			paramsRaw []byte
			status    sql.NullString
			resRaw    []byte
			createdAt sql.NullTime
			procAt    sql.NullTime
		)
		if err := rows.Scan(
			&r.ID, &r.RequestType, &r.RequestedBy, &targetCh,
			&paramsRaw, &status, &resRaw, &createdAt, &procAt,
		); err != nil {
			return nil, err
		}
		r.TargetChannel = targetCh.String
		r.Status = status.String
		if createdAt.Valid {
			r.CreatedAt = createdAt.Time
		}
		if procAt.Valid {
			t := procAt.Time
			r.ProcessedAt = &t
		}
		if mVal, ok := ParseJSONColumn(paramsRaw).(map[string]any); ok {
			r.Params = mVal
		}
		if mVal, ok := ParseJSONColumn(resRaw).(map[string]any); ok {
			r.Result = mVal
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
