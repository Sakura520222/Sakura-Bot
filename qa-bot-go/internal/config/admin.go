// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package config

import (
	"log/slog"
	"strconv"
	"strings"
)

// IsAdmin 严格判定用户是否为管理员（fail-closed），
// 对齐 core/infrastructure/utils/auth.py:30-53：仅当 userID 与
// REPORT_ADMIN_IDS 中显式配置、格式合法的数字 ID 完全一致才返回 true；
// 未配置、非法或类型不符一律 false，绝无“未配置即放行”的通配语义。
func (c *Config) IsAdmin(userID int64) bool {
	for _, id := range c.adminIDList() {
		if id == userID {
			return true
		}
	}
	return false
}

// adminIDList 解析 REPORT_ADMIN_IDS（逗号分隔数字 ID）。
// 空配置或非法项不构成错误：非法项告警后跳过（settings.py:93-111），
// 结果为空即无人是管理员（fail-closed）。
func (c *Config) adminIDList() []int64 {
	raw := c.Env.Get(KeyReportAdminIDs)
	if raw == "" {
		return nil
	}
	var ids []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			slog.Warn("忽略非法的管理员ID配置项", "value", part)
			continue
		}
		ids = append(ids, id)
	}
	return ids
}
