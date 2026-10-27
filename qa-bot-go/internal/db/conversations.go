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

// HistoryItem 对齐 get_conversation_history 返回字典的结构
// （mysql.py:1325-1338）：timestamp 为 Python isoformat 字符串（无值时为空串），
// metadata 为解析后的 JSON 值（NULL 或解析失败时为 nil，对齐 except: pass）。
type HistoryItem struct {
	Role      string
	Content   string
	Timestamp string
	Metadata  any
}

// SaveConversation 保存对话记录（mysql.py:1271-1303）。
// metadata 为 nil 或空 map 时写 NULL（Python if metadata else None）。
func (m *Manager) SaveConversation(ctx context.Context, userID int64, sessionID, role, content string, metadata map[string]any) error {
	var metadataJSON any
	if len(metadata) > 0 {
		s, err := EncodeJSON(metadata)
		if err != nil {
			slog.Error(fmt.Sprintf("保存对话记录失败: %T: %s", err, err))
			return err
		}
		metadataJSON = s
	}

	if _, err := m.DB.ExecContext(ctx, `
                        INSERT INTO conversation_history
                        (user_id, session_id, role, content, metadata)
                        VALUES (?, ?, ?, ?, ?)
                    `, userID, sessionID, role, content, metadataJSON); err != nil {
		slog.Error(fmt.Sprintf("保存对话记录失败: %T: %s", err, err))
		return err
	}

	slog.Debug(fmt.Sprintf("保存对话记录: user_id=%d, session=%s, role=%s", userID, sessionID, role))
	return nil
}

// GetConversationHistory 获取用户的对话历史（mysql.py:1305-1347）：
// ORDER BY timestamp ASC LIMIT ?——引擎取历史在前、保存当前消息在后，
// 因此 LIMIT 20 不含当前消息。
func (m *Manager) GetConversationHistory(ctx context.Context, userID int64, sessionID string, limit int) ([]HistoryItem, error) {
	rows, err := m.DB.QueryContext(ctx, `
                        SELECT role, content, timestamp, metadata
                        FROM conversation_history
                        WHERE user_id = ? AND session_id = ?
                        ORDER BY timestamp ASC
                        LIMIT ?
                    `, userID, sessionID, limit)
	if err != nil {
		slog.Error(fmt.Sprintf("获取对话历史失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	var history []HistoryItem
	for rows.Next() {
		var (
			item      HistoryItem
			timestamp sql.NullTime
			metadata  []byte
		)
		if err := rows.Scan(&item.Role, &item.Content, &timestamp, &metadata); err != nil {
			slog.Error(fmt.Sprintf("获取对话历史失败: %T: %s", err, err))
			return nil, err
		}
		if timestamp.Valid {
			item.Timestamp = PythonISOFormat(timestamp.Time)
		}
		// 解析元数据（mysql.py:1333-1337：NULL/解析失败保持缺省 nil）。
		item.Metadata = ParseJSONColumn(metadata)
		history = append(history, item)
	}
	if err := rows.Err(); err != nil {
		slog.Error(fmt.Sprintf("获取对话历史失败: %T: %s", err, err))
		return nil, err
	}

	slog.Debug(fmt.Sprintf("获取对话历史: user_id=%d, session=%s, 条数=%d", userID, sessionID, len(history)))
	return history, nil
}

// ClearUserConversations 清除用户的对话历史（mysql.py:1372-1404）。
// sessionID 传空串等价 Python 的 session_id=None（清除该用户全部会话）。
// 返回删除行数。
func (m *Manager) ClearUserConversations(ctx context.Context, userID int64, sessionID string) (int64, error) {
	var (
		res  sql.Result
		err  error
		args []any
	)
	query := `
                            DELETE FROM conversation_history
                            WHERE user_id = ?
                        `
	args = append(args, userID)
	if sessionID != "" {
		query = `
                            DELETE FROM conversation_history
                            WHERE user_id = ? AND session_id = ?
                        `
		args = append(args, sessionID)
	}
	if res, err = m.DB.ExecContext(ctx, query, args...); err != nil {
		slog.Error(fmt.Sprintf("清除对话历史失败: %T: %s", err, err))
		return 0, err
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		slog.Error(fmt.Sprintf("清除对话历史失败: %T: %s", err, err))
		return 0, err
	}

	slog.Info(fmt.Sprintf("清除对话历史: user_id=%d, session=%s, 删除%d条", userID, pyStrStr(sessionID), deleted))
	return deleted, nil
}

// DeleteOldConversations 删除旧的对话记录（定期清理，mysql.py:1427-1450）。
// cutoff = 当前 UTC 时间减 days 天；返回删除行数。
func (m *Manager) DeleteOldConversations(ctx context.Context, days int) (int64, error) {
	cutoff := MysqlDatetime(time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour))

	res, err := m.DB.ExecContext(ctx, `
                        DELETE FROM conversation_history
                        WHERE timestamp < ?
                    `, cutoff)
	if err != nil {
		slog.Error(fmt.Sprintf("删除旧对话记录失败: %T: %s", err, err))
		return 0, err
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		slog.Error(fmt.Sprintf("删除旧对话记录失败: %T: %s", err, err))
		return 0, err
	}

	slog.Info(fmt.Sprintf("已删除 %d 条旧对话记录 (超过 %d 天)", deleted, days))
	return deleted, nil
}

// pyStrStr 把空会话 ID 渲染为 Python None 的日志形态（仅日志对齐用）。
func pyStrStr(s string) string {
	if s == "" {
		return "None"
	}
	return s
}
