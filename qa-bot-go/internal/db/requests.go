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
	"fmt"
	"log/slog"
	"time"
)

// Request 对齐 request_queue 表行（SELECT * 列序）。
// Params/Result 为解析后的 JSON 值：NULL → nil、解析失败 → 原始字符串
// （ParseJSONColumn，对齐 mysql.py 的 try json.loads / except: pass）。
type Request struct {
	ID            int64
	RequestType   string
	RequestedBy   int64
	TargetChannel sql.NullString
	Params        any
	Status        string
	Result        any
	CreatedAt     sql.NullTime
	ProcessedAt   sql.NullTime
}

// CreateRequest 创建总结请求（mysql.py:1846-1878）：写 request_queue
// pending 行，返回自增 request_id。params 为 nil 或空 map 时写 NULL
// （Python if params else None）。targetChannel 传空串写 NULL。
func (m *Manager) CreateRequest(ctx context.Context, requestType string, requestedBy int64, targetChannel string, params map[string]any) (int64, error) {
	var (
		targetArg any
		paramsArg any
	)
	if targetChannel != "" {
		targetArg = targetChannel
	}
	if len(params) > 0 {
		s, err := EncodeJSON(params)
		if err != nil {
			slog.Error(fmt.Sprintf("创建请求失败: %T: %s", err, err))
			return 0, err
		}
		paramsArg = s
	}

	res, err := m.DB.ExecContext(ctx, `
                        INSERT INTO request_queue
                        (request_type, requested_by, target_channel, params)
                        VALUES (?, ?, ?, ?)
                    `, requestType, requestedBy, targetArg, paramsArg)
	if err != nil {
		slog.Error(fmt.Sprintf("创建请求失败: %T: %s", err, err))
		return 0, err
	}

	requestID, err := res.LastInsertId()
	if err != nil {
		slog.Error(fmt.Sprintf("创建请求失败: %T: %s", err, err))
		return 0, err
	}

	slog.Info(fmt.Sprintf("创建请求: id=%d, type=%s, user=%d", requestID, requestType, requestedBy))
	return requestID, nil
}

// GetRequestStatus 获取请求状态（mysql.py:1941-1966）。
// 不存在返回 (nil, nil)（对齐 Python None）。
func (m *Manager) GetRequestStatus(ctx context.Context, requestID int64) (*Request, error) {
	var (
		req               Request
		paramsRaw, resRaw []byte
	)
	err := m.DB.QueryRowContext(ctx,
		"SELECT * FROM request_queue WHERE id = ?", requestID).
		Scan(&req.ID, &req.RequestType, &req.RequestedBy, &req.TargetChannel,
			&paramsRaw, &req.Status, &resRaw, &req.CreatedAt, &req.ProcessedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("获取请求状态失败: %T: %s", err, err))
		return nil, err
	}
	req.Params = ParseJSONColumn(paramsRaw)
	req.Result = ParseJSONColumn(resRaw)
	return &req, nil
}

// CleanupOldRequests 清理旧请求记录（mysql.py:1968-1991）：
// 仅删除 completed/failed 且超过 days 天的行，返回删除行数。
func (m *Manager) CleanupOldRequests(ctx context.Context, days int) (int64, error) {
	cutoff := MysqlDatetime(NowUTC().Add(-time.Duration(days) * 24 * time.Hour))

	res, err := m.DB.ExecContext(ctx, `
                        DELETE FROM request_queue
                        WHERE created_at < ? AND status IN ('completed', 'failed')
                    `, cutoff)
	if err != nil {
		slog.Error(fmt.Sprintf("清理旧请求失败: %T: %s", err, err))
		return 0, err
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		slog.Error(fmt.Sprintf("清理旧请求失败: %T: %s", err, err))
		return 0, err
	}

	slog.Info(fmt.Sprintf("清理旧请求: 删除 %d 条记录", deleted))
	return deleted, nil
}
