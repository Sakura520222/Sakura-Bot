// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package contracts 定义跨包共享的数据类型与最小契约。
//
// 本包只放类型与常量，不承载任何业务逻辑；各消费者包按 Go 惯例
// 在自己包内定义窄接口，由 main 装配实现。
package contracts

import "time"

// AIConf 是 AI 连接的最终有效配置（config.json 覆盖 env 后的结果），
// 对齐 core/config/ai_runtime.py::AIConnectionSettings。
type AIConf struct {
	// APIKey 为空表示未配置（对齐 Python 侧 None 语义）。
	APIKey string
	// BaseURL 例如 https://api.deepseek.com
	BaseURL string
	// Model 例如 deepseek-chat
	Model string
}

// QuotaLimits 是每日配额限额，对齐 core/config/qa_quota.py::QAQuotaSettings。
type QuotaLimits struct {
	// UserDaily 单用户每日限额（env 默认 3，clamp 下限 1）。
	UserDaily int64
	// TotalDaily 全局每日限额（env 默认 200，clamp 下限 10）。
	TotalDaily int64
}

// 配额 clamp 边界（qa_quota.py:15-16 与 :26）。
const (
	// QuotaUserMin 单用户每日限额下限。
	QuotaUserMin = int64(1)
	// QuotaTotalMin 全局每日限额下限。
	QuotaTotalMin = int64(10)
	// QuotaMax = 2^31-1，与 Python le=2147483647 一致。
	QuotaMax = int64(2147483647)
)

// ChannelInfo 是频道目录条目，字段对齐
// core/infrastructure/database/mysql.py::get_all_channels（:1722-1768）。
type ChannelInfo struct {
	// ChannelID 为 normalize 后的完整 URL 形式。
	ChannelID string
	// ChannelName 取 MAX(NULLIF(channel_name,''))，合并配置频道时取 URL 尾段。
	ChannelName string
	// LastSummaryTime 为最近一次总结时间（UTC）；零值等价 Python None
	// （排序时视为 datetime.min）。
	LastSummaryTime time.Time
	// SummaryCount 该频道总结条数。
	SummaryCount int64
	// MessageCount 该频道已总结消息数合计。
	MessageCount int64
	// FromConfig 表示该条目来自 config.CHANNELS 合并、尚无任何总结。
	FromConfig bool
}

// QuotaCheckResult 是配额检查结果，字段对齐
// core/ai/quota_manager.py::check_quota 的返回结构。
type QuotaCheckResult struct {
	// Allowed 是否允许本次查询。
	Allowed bool
	// Remaining 今日剩余次数；管理员为 -1。
	Remaining int64
	// Used 该用户今日已用次数。
	Used int64
	// IsAdmin 是否管理员（REPORT_ADMIN_IDS 精确匹配）。
	IsAdmin bool
	// Message 拒绝时回复给用户的文案；允许时为空。
	Message string
}

// 引擎流协议控制标记：与 Python qa_bot.py/qa_engine_v3.py 中字符串逐字一致，
// 任何一侧不得改动（README 硬规则 9）。
const (
	// CtrlDone 标记流正常结束。
	CtrlDone = "__DONE__"
	// CtrlNewSession 标记新会话开始（消费端在首个文本块前加前缀）。
	CtrlNewSession = "__NEW_SESSION__"
	// CtrlErrPrefix 是错误控制标记前缀，完整形式为 CtrlErrPrefix + 错误文案。
	CtrlErrPrefix = "__ERROR__:"
)
