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
	"time"
)

// PollRegeneration 投票重新生成记录（对齐 mysql.py:2663-2745 与 poll_data.py）。
type PollRegeneration struct {
	ID                     int64     `json:"id"`
	Channel                string    `json:"channel"`
	SummaryMsgID           int64     `json:"summary_msg_id"`
	PollMsgID              *int64    `json:"poll_msg_id"`
	ButtonMsgID            *int64    `json:"button_msg_id"`
	SummaryText            string    `json:"summary_text"`
	ChannelName            string    `json:"channel_name"`
	Timestamp              time.Time `json:"timestamp"`
	SendToChannel          bool      `json:"send_to_channel"`
	VoteCount              int       `json:"vote_count"`
	DiscussionForwardMsgID *int64    `json:"discussion_forward_msg_id"`
	Voters                 []int64   `json:"voters"`
}

// SavePollRegeneration 新增或覆盖投票重新生成记录（mysql.py:2663-2711）。
func (m *Manager) SavePollRegeneration(ctx context.Context, rec PollRegeneration) error {
	var pollArg, btnArg, discArg any
	if rec.PollMsgID != nil {
		pollArg = *rec.PollMsgID
	}
	if rec.ButtonMsgID != nil {
		btnArg = *rec.ButtonMsgID
	}
	if rec.DiscussionForwardMsgID != nil {
		discArg = *rec.DiscussionForwardMsgID
	}

	_, err := m.DB.ExecContext(ctx, `
		INSERT INTO poll_regenerations
		(channel, summary_msg_id, poll_msg_id, button_msg_id,
		 summary_text, channel_name, send_to_channel, discussion_forward_msg_id, vote_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON DUPLICATE KEY UPDATE
			poll_msg_id = VALUES(poll_msg_id),
			button_msg_id = VALUES(button_msg_id),
			summary_text = VALUES(summary_text),
			channel_name = VALUES(channel_name),
			send_to_channel = VALUES(send_to_channel),
			discussion_forward_msg_id = VALUES(discussion_forward_msg_id)
	`, rec.Channel, rec.SummaryMsgID, pollArg, btnArg,
		rec.SummaryText, rec.ChannelName, rec.SendToChannel, discArg)
	if err != nil {
		slog.Error(fmt.Sprintf("添加投票重新生成记录失败: %T: %s", err, err))
		return err
	}
	slog.Info(fmt.Sprintf("已添加投票重新生成记录: channel=%s, summary_id=%d", rec.Channel, rec.SummaryMsgID))
	return nil
}

// GetPollRegeneration 查询投票重新生成记录及全部已投票用户 ID（mysql.py:2713-2745）。
// 不存在返回 (nil, nil)。
func (m *Manager) GetPollRegeneration(ctx context.Context, channel string, summaryMsgID int64) (*PollRegeneration, error) {
	var (
		rec       PollRegeneration
		pollID    sql.NullInt64
		btnID     sql.NullInt64
		sumText   sql.NullString
		chName    sql.NullString
		ts        sql.NullTime
		discFwdID sql.NullInt64
	)
	err := m.DB.QueryRowContext(ctx, `
		SELECT id, channel, summary_msg_id, poll_msg_id, button_msg_id,
		       summary_text, channel_name, timestamp, send_to_channel,
		       vote_count, discussion_forward_msg_id
		FROM poll_regenerations
		WHERE channel = ? AND summary_msg_id = ?
	`, channel, summaryMsgID).Scan(
		&rec.ID, &rec.Channel, &rec.SummaryMsgID, &pollID, &btnID,
		&sumText, &chName, &ts, &rec.SendToChannel,
		&rec.VoteCount, &discFwdID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("获取投票重新生成记录失败: %T: %s", err, err))
		return nil, err
	}
	if pollID.Valid {
		v := pollID.Int64
		rec.PollMsgID = &v
	}
	if btnID.Valid {
		v := btnID.Int64
		rec.ButtonMsgID = &v
	}
	rec.SummaryText = sumText.String
	rec.ChannelName = chName.String
	if ts.Valid {
		rec.Timestamp = ts.Time
	}
	if discFwdID.Valid {
		v := discFwdID.Int64
		rec.DiscussionForwardMsgID = &v
	}

	voterRows, err := m.DB.QueryContext(ctx, `
		SELECT user_id FROM poll_voters
		WHERE channel = ? AND summary_msg_id = ?
	`, channel, summaryMsgID)
	if err != nil {
		slog.Error(fmt.Sprintf("获取投票者列表失败: %T: %s", err, err))
		return nil, err
	}
	defer voterRows.Close() //nolint:errcheck // 只读游标

	voters := make([]int64, 0)
	for voterRows.Next() {
		var uid int64
		if err := voterRows.Scan(&uid); err != nil {
			return nil, err
		}
		voters = append(voters, uid)
	}
	if err := voterRows.Err(); err != nil {
		return nil, err
	}
	rec.Voters = voters
	return &rec, nil
}

// SavePollVoter 幂等记录投票者（返回 true 表示首次记录，false 表示已投过票）。
func (m *Manager) SavePollVoter(ctx context.Context, channel string, summaryMsgID, userID int64) (bool, error) {
	res, err := m.DB.ExecContext(ctx, `
		INSERT IGNORE INTO poll_voters (channel, summary_msg_id, user_id)
		VALUES (?, ?, ?)
	`, channel, summaryMsgID, userID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// IncrementPollRegeneration 原子检查投票资格、记录投票者并递增票数
// （对齐 mysql.py:2792-2868 increment_vote_count -> (added, newCount, alreadyVoted)）。
func (m *Manager) IncrementPollRegeneration(ctx context.Context, channel string, summaryMsgID, userID int64) (bool, int, bool, error) {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, false, err
	}
	defer tx.Rollback() //nolint:errcheck // 提交后无害

	var currentCount int
	err = tx.QueryRowContext(ctx, `
		SELECT vote_count FROM poll_regenerations
		WHERE channel = ? AND summary_msg_id = ?
		FOR UPDATE
	`, channel, summaryMsgID).Scan(&currentCount)
	if errors.Is(err, sql.ErrNoRows) {
		slog.Warn(fmt.Sprintf("投票重新生成记录不存在: channel=%s, summary_id=%d", channel, summaryMsgID))
		return false, 0, false, nil
	}
	if err != nil {
		slog.Error(fmt.Sprintf("增加投票计数失败: %T: %s", err, err))
		return false, 0, false, err
	}

	var one int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM poll_voters
		WHERE channel = ? AND summary_msg_id = ? AND user_id = ?
	`, channel, summaryMsgID, userID).Scan(&one)
	if err == nil {
		slog.Info(fmt.Sprintf("用户 %d 已经投票过了", userID))
		return false, currentCount, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, 0, false, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE poll_regenerations
		SET vote_count = vote_count + 1
		WHERE channel = ? AND summary_msg_id = ?
	`, channel, summaryMsgID); err != nil {
		return false, 0, false, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO poll_voters (channel, summary_msg_id, user_id)
		VALUES (?, ?, ?)
	`, channel, summaryMsgID, userID); err != nil {
		return false, 0, false, err
	}

	newCount := currentCount + 1
	if err := tx.Commit(); err != nil {
		return false, 0, false, err
	}

	slog.Info(fmt.Sprintf("投票计数已更新: channel=%s, summary_id=%d, count=%d, user_id=%d",
		channel, summaryMsgID, newCount, userID))
	return true, newCount, false, nil
}

// UpdatePollMessageIDs 更新重新生成后的投票与按钮消息 ID（mysql.py:2747-2769）。
func (m *Manager) UpdatePollMessageIDs(ctx context.Context, channel string, summaryMsgID, pollMsgID, buttonMsgID int64) error {
	_, err := m.DB.ExecContext(ctx, `
		UPDATE poll_regenerations
		SET poll_msg_id = ?, button_msg_id = ?
		WHERE channel = ? AND summary_msg_id = ?
	`, pollMsgID, buttonMsgID, channel, summaryMsgID)
	return err
}

// DeletePollRegeneration 删除投票重新生成记录（外键 ON DELETE CASCADE 同步清理 poll_voters；mysql.py:2770-2791）。
func (m *Manager) DeletePollRegeneration(ctx context.Context, channel string, summaryMsgID int64) error {
	_, err := m.DB.ExecContext(ctx, `
		DELETE FROM poll_regenerations
		WHERE channel = ? AND summary_msg_id = ?
	`, channel, summaryMsgID)
	return err
}

// ResetPollVoteCount 重置投票计数并清空已投票用户列表（mysql.py:2869-2913）。
func (m *Manager) ResetPollVoteCount(ctx context.Context, channel string, summaryMsgID int64) (bool, error) {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck // 提交后无害

	var one int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM poll_regenerations
		WHERE channel = ? AND summary_msg_id = ?
		FOR UPDATE
	`, channel, summaryMsgID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE poll_regenerations
		SET vote_count = 0
		WHERE channel = ? AND summary_msg_id = ?
	`, channel, summaryMsgID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM poll_voters
		WHERE channel = ? AND summary_msg_id = ?
	`, channel, summaryMsgID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// GetPollVoteCount 获取当前票数（mysql.py:2915-2932）。
func (m *Manager) GetPollVoteCount(ctx context.Context, channel string, summaryMsgID int64) (int, error) {
	var count int
	err := m.DB.QueryRowContext(ctx, `
		SELECT vote_count FROM poll_regenerations
		WHERE channel = ? AND summary_msg_id = ?
	`, channel, summaryMsgID).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return count, err
}

// CleanupPollRegenerations 清理超过 days 天的旧投票重新生成记录（mysql.py:2934-2958）。
func (m *Manager) CleanupPollRegenerations(ctx context.Context, days int) (int64, error) {
	cutoff := MysqlDatetime(NowUTC().Add(-time.Duration(days) * 24 * time.Hour))
	res, err := m.DB.ExecContext(ctx, "DELETE FROM poll_regenerations WHERE timestamp < ?", cutoff)
	if err != nil {
		slog.Error(fmt.Sprintf("清理旧投票重新生成记录失败: %T: %s", err, err))
		return 0, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if deleted > 0 {
		slog.Info(fmt.Sprintf("已清理 %d 条超过 %d 天的投票重新生成记录", deleted, days))
	}
	return deleted, nil
}
