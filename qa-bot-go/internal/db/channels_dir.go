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
	"sort"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
)

// GetAllChannels 获取所有可用频道（mysql.py:1722-1768）：
// summaries 表 GROUP BY 聚合 + 合并 config.CHANNELS（normalize 后补零行），
// 按 last_summary_time 降序（None 记为 datetime.min，即零值时间排最后；
// Python sorted 稳定排序，Go 用 SliceStable 等价）。
//
// Manager 构造时未提供 config（nil，测试场景）则跳过合并并告警，
// 等价 Python 合并分支异常时的 warning 路径。
func (m *Manager) GetAllChannels(ctx context.Context) ([]contracts.ChannelInfo, error) {
	rows, err := m.DB.QueryContext(ctx, `
                        SELECT
                            channel_id,
                            COALESCE(MAX(NULLIF(channel_name, '')), channel_id) AS channel_name,
                            MAX(created_at) AS last_summary_time,
                            COUNT(*) AS summary_count,
                            COALESCE(SUM(message_count), 0) AS message_count
                        FROM summaries
                        GROUP BY channel_id
                        ORDER BY last_summary_time DESC
                    `)
	if err != nil {
		slog.Error(fmt.Sprintf("获取频道列表失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	byID := make(map[string]contracts.ChannelInfo)
	for rows.Next() {
		var (
			info     contracts.ChannelInfo
			name     sql.NullString
			lastTime sql.NullTime
			summaryN sql.NullInt64
			messageN sql.NullInt64
		)
		if err := rows.Scan(&info.ChannelID, &name, &lastTime, &summaryN, &messageN); err != nil {
			slog.Error(fmt.Sprintf("获取频道列表失败: %T: %s", err, err))
			return nil, err
		}
		if info.ChannelID == "" {
			// Python：if row.get("channel_id") 才入表。
			continue
		}
		if name.Valid {
			info.ChannelName = name.String
		}
		if lastTime.Valid {
			info.LastSummaryTime = lastTime.Time
		}
		if summaryN.Valid {
			info.SummaryCount = summaryN.Int64
		}
		if messageN.Valid {
			info.MessageCount = messageN.Int64
		}
		byID[info.ChannelID] = info
	}
	if err := rows.Err(); err != nil {
		slog.Error(fmt.Sprintf("获取频道列表失败: %T: %s", err, err))
		return nil, err
	}

	// 合并配置频道（mysql.py:1743-1758）：normalize 已由 config.Channels() 完成，
	// 已存在于聚合结果的跳过，其余补零行。
	if m.cfg != nil {
		for _, configured := range m.cfg.Channels() {
			if _, ok := byID[configured]; ok {
				continue
			}
			byID[configured] = contracts.ChannelInfo{
				ChannelID:   configured,
				ChannelName: lastPathSegment(configured),
				FromConfig:  true,
			}
		}
	} else {
		slog.Warn("合并配置频道失败: 配置不可用，仅返回 summaries 聚合结果")
	}

	channels := make([]contracts.ChannelInfo, 0, len(byID))
	for _, info := range byID {
		channels = append(channels, info)
	}
	// 稳定降序：LastSummaryTime 零值视为 datetime.min 排最后。
	sort.SliceStable(channels, func(i, j int) bool {
		return channels[i].LastSummaryTime.After(channels[j].LastSummaryTime)
	})
	return channels, nil
}
