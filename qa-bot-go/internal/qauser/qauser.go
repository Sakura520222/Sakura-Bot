// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package qauser 是 core/qa_user_system.py（问答Bot用户系统管理）的移植：
// 用户注册（upsert）、summary 订阅管理与跨 Bot 总结请求创建，
// 以及频道目录/订阅列表的用户可见格式化文案（硬编码中文逐字一致）。
package qauser

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
)

// subTypeSummary 订阅类型恒为 "summary"（qa_user_system.py:104,130,152）。
const subTypeSummary = "summary"

// Store 用户系统对 MySQL 层的窄接口（消费者包内定义，生产实现为 *db.Manager，
// 方法签名与其逐字对应；测试注入 fake）。
//
// 错误语义约定：Python 侧这些 db 方法内部自捕获异常并回退零值
// （False/0/[]/None，mysql.py 各方法 except 分支），调用方据此继续走
// 回退路径；因此实现方返回 error 时，本包一律按 Python 的零值语义处理
// （不把 error 上抛为"系统错误"文案）。
type Store interface {
	// IsUserRegistered 检查用户是否已注册（mysql.py:1590-1601；异常回退 false）。
	IsUserRegistered(ctx context.Context, userID int64) (bool, error)
	// UpdateUserActivity 更新最后活跃时间（mysql.py:1521-1541；Python 调用方不检查返回值）。
	UpdateUserActivity(ctx context.Context, userID int64) error
	// RegisterUser 注册新用户 upsert（mysql.py:1454-1497；Python 自捕获异常回退 false）。
	// QA 侧恒以 isAdmin=false 调用（用户表 is_admin=0）。
	RegisterUser(ctx context.Context, userID int64, username, firstName *string, isAdmin bool) error
	// GetAllChannels 频道目录（mysql.py:1722-1768；异常回退空列表）。
	GetAllChannels(ctx context.Context) ([]contracts.ChannelInfo, error)
	// IsSubscribed 检查订阅（mysql.py:1823-1842；异常回退 false）。
	IsSubscribed(ctx context.Context, userID int64, channelID, subType string) (bool, error)
	// AddSubscription 添加订阅 INSERT IGNORE（mysql.py:1605-1635）。
	// false 表示已存在（受影响行数 0）或失败——Python 两分支同文案。
	AddSubscription(ctx context.Context, userID int64, channelID, channelName, subType string) (bool, error)
	// RemoveSubscription 移除订阅，返回删除行数（mysql.py:1637-1666；异常回退 0）。
	RemoveSubscription(ctx context.Context, userID int64, channelID, subType string) (int64, error)
	// GetUserSubscriptions 用户订阅列表（mysql.py:1668-1698；异常回退空列表）。
	GetUserSubscriptions(ctx context.Context, userID int64, subType string) ([]db.Subscription, error)
	// CreateRequest 创建 request_queue 请求，返回自增 id（mysql.py:1846-1878；异常回退 None→0）。
	CreateRequest(ctx context.Context, requestType string, requestedBy int64, targetChannel string, params map[string]any) (int64, error)
	// GetRequestStatus 获取请求状态，不存在/异常回退 nil（mysql.py:1941-1966）。
	GetRequestStatus(ctx context.Context, requestID int64) (*db.Request, error)
}

// Result 注册/订阅/请求操作的返回结构（对齐 Python 返回 dict 的字段子集；
// RequestID 仅在 CreateSummaryRequest 结果中携带，对齐 "request_id" 键）。
type Result struct {
	Success   bool
	NewUser   bool // 仅注册结果携带（Python "new_user" 键）
	Message   string
	RequestID int64
}

// System 问答Bot用户系统管理类（对齐 QAUserSystem）。
type System struct {
	store Store
	log   *slog.Logger
}

// New 构造用户系统并记录与 Python 版一致的初始化日志（qa_user_system.py:31）。
func New(store Store) *System {
	log := slog.Default()
	log.Info("问答Bot用户系统初始化完成")
	return &System{store: store, log: log}
}

// RegisterUser 注册用户（qa_user_system.py:33-68 逐分支移植）。
//
// 已注册 → 仅更新活跃时间（Python 不检查更新返回值）→ "欢迎回来！"；
// 未注册 → 插入新行（QA 侧恒 is_admin=0）。
// username/firstName 传 nil 表示 Telegram 侧未提供（SQL NULL，对齐 Python None）。
func (s *System) RegisterUser(ctx context.Context, userID int64, username, firstName *string) (res Result) {
	// Python 整个方法体包 try/except，异常返回 "注册时发生错误。"；
	// Go 以 recover 复刻该兜底（正常 db 层错误已按零值语义走下方分支）。
	defer func() {
		if r := recover(); r != nil {
			s.log.Error(fmt.Sprintf("用户注册失败: %T: %v", r, r))
			res = Result{Success: false, Message: "注册时发生错误。"}
		}
	}()

	// is_user_registered 异常回退 False（mysql.py:1590-1601 except）→ 继续注册路径。
	registered, _ := s.store.IsUserRegistered(ctx, userID)
	if registered {
		// Python 不检查 update_user_activity 的返回值（qa_user_system.py:51）。
		_ = s.store.UpdateUserActivity(ctx, userID)
		return Result{Success: true, NewUser: false, Message: "欢迎回来！"}
	}

	// db 层 register_user 自捕获异常回退 False → "注册失败，请稍后重试。"。
	if err := s.store.RegisterUser(ctx, userID, username, firstName, false); err != nil {
		return Result{Success: false, Message: "注册失败，请稍后重试。"}
	}
	return Result{Success: true, NewUser: true, Message: "注册成功！欢迎使用智能资讯助手。"}
}

// GetAvailableChannels 获取可订阅频道列表（qa_user_system.py:70-82）。
// 失败记日志并返回空（Python get_all_channels 异常回退 []）。
func (s *System) GetAvailableChannels(ctx context.Context) []contracts.ChannelInfo {
	channels, err := s.store.GetAllChannels(ctx)
	if err != nil {
		s.log.Error(fmt.Sprintf("获取频道列表失败: %T: %s", err, err))
		return nil
	}
	return channels
}

// AddSubscription 添加订阅（qa_user_system.py:84-116 逐分支移植）。
func (s *System) AddSubscription(ctx context.Context, userID int64, channelID, channelName string) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error(fmt.Sprintf("添加订阅失败: %T: %v", r, r))
			res = Result{Success: false, Message: "订阅时发生错误。"}
		}
	}()

	// is_subscribed 异常回退 False（mysql.py:1823-1842 except）→ 继续添加路径。
	subscribed, _ := s.store.IsSubscribed(ctx, userID, channelID, subTypeSummary)
	if subscribed {
		return Result{Success: false, Message: "您已经订阅了此频道的总结推送。"}
	}

	// add_subscription 自捕获异常回退 False；INSERT IGNORE 撞唯一键同样 False——
	// Python 两个分支同文案（mysql.py:1605-1635 except / qa_user_system.py:112）。
	ok, err := s.store.AddSubscription(ctx, userID, channelID, channelName, subTypeSummary)
	if err != nil || !ok {
		return Result{Success: false, Message: "订阅失败，请稍后重试。"}
	}
	return Result{
		Success: true,
		Message: fmt.Sprintf("✅ 已订阅 `%s` 的总结推送\n\n当该频道有新总结时，我会及时通知您！", channelName),
	}
}

// RemoveSubscription 移除订阅（qa_user_system.py:118-139 逐分支移植）。
func (s *System) RemoveSubscription(ctx context.Context, userID int64, channelID string) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error(fmt.Sprintf("移除订阅失败: %T: %v", r, r))
			res = Result{Success: false, Message: "取消订阅时发生错误。"}
		}
	}()

	// remove_subscription 自捕获异常回退 0（mysql.py:1637-1666 except）。
	deleted, err := s.store.RemoveSubscription(ctx, userID, channelID, subTypeSummary)
	if err != nil {
		deleted = 0
	}
	if deleted > 0 {
		return Result{Success: true, Message: "✅ 已取消该频道的订阅"}
	}
	return Result{Success: false, Message: "未找到该频道的订阅记录。"}
}

// GetUserSubscriptions 获取用户订阅列表（qa_user_system.py:141-156）。
// 失败记日志并返回空（Python get_user_subscriptions 异常回退 []）。
func (s *System) GetUserSubscriptions(ctx context.Context, userID int64) []db.Subscription {
	subs, err := s.store.GetUserSubscriptions(ctx, userID, subTypeSummary)
	if err != nil {
		s.log.Error(fmt.Sprintf("获取用户订阅失败: %T: %s", err, err))
		return nil
	}
	return subs
}

// CreateSummaryRequest 创建总结请求（发送给主Bot）（qa_user_system.py:158-193）。
// 写 request_queue pending 行（params={"channel_name": ...}），回执含 request_id。
func (s *System) CreateSummaryRequest(ctx context.Context, userID int64, channelID, channelName string) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error(fmt.Sprintf("创建总结请求失败: %T: %v", r, r))
			res = Result{Success: false, Message: "创建请求时发生错误。"}
		}
	}()

	// create_request 自捕获异常回退 None（mysql.py:1846-1878 except）→ falsy 分支。
	requestID, err := s.store.CreateRequest(ctx, "summary", userID, channelID,
		map[string]any{"channel_name": channelName})
	if err != nil {
		requestID = 0
	}
	if requestID == 0 {
		return Result{Success: false, Message: "创建请求失败，请稍后重试。"}
	}
	// 使用 HTML 格式避免 Markdown 解析错误（Python 注释，qa_user_system.py:182）。
	return Result{
		Success:   true,
		RequestID: requestID,
		Message: fmt.Sprintf("📝 已向管理员提交总结请求\n\n频道: %s\n请求ID: %d\n\n请等待管理员确认并生成总结...",
			channelName, requestID),
	}
}

// GetRequestStatus 获取请求状态（qa_user_system.py:195-209）。
// 不存在或失败返回 nil（Python None）。
func (s *System) GetRequestStatus(ctx context.Context, requestID int64) (req *db.Request) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error(fmt.Sprintf("获取请求状态失败: %T: %v", r, r))
			req = nil
		}
	}()

	status, err := s.store.GetRequestStatus(ctx, requestID)
	if err != nil {
		s.log.Error(fmt.Sprintf("获取请求状态失败: %T: %s", err, err))
		return nil
	}
	return status
}

// formatDateField 安全地把日期格式化为 YYYY-MM-DD（qa_user_system.py:211-245）。
// 零值时间等价 Python None/无值 → 空字符串；数据源均经 parseTime=true & loc=UTC
// 解析，等价 Python datetime 对象的 strftime("%Y-%m-%d") 分支。
func formatDateField(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// formatDateFieldNull sql.NullTime 变体：NULL 等价 Python None → 空字符串。
func formatDateFieldNull(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return formatDateField(t.Time)
}

// orEmptyFallback 复刻 Python `{x or '默认'}`：空字符串取默认。
func orEmptyFallback(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// FormatChannelsList 格式化频道列表为可读文本（qa_user_system.py:247-280 逐行移植，
// 输出为 Markdown 文案）。
func (s *System) FormatChannelsList(channels []contracts.ChannelInfo) string {
	if len(channels) == 0 {
		return "暂无可订阅的频道。"
	}

	// Python 首元素自带尾部 "\n"（join 后为空行）。
	lines := []string{"📋 **可订阅频道列表**\n"}
	for i, ch := range channels {
		// Python channel.get("channel_name", "未知频道")：键恒存在，空串原样输出。
		lines = append(lines,
			fmt.Sprintf("%d. **%s**", i+1, ch.ChannelName),
			fmt.Sprintf("   链接: `%s`", ch.ChannelID),
			fmt.Sprintf("   总结数: %d，消息数: %d", ch.SummaryCount, ch.MessageCount),
			fmt.Sprintf("   最后更新: %s", orEmptyFallback(formatDateField(ch.LastSummaryTime), "暂无总结")),
			"",
		)
	}
	lines = append(lines,
		"💡 使用方法:",
		"`/subscribe <频道链接>`",
		"",
		"示例:",
		fmt.Sprintf("`/subscribe %s`", channels[0].ChannelID),
	)
	return strings.Join(lines, "\n")
}

// FormatSubscriptionsList 格式化订阅列表为可读文本（qa_user_system.py:282-315 逐行移植）。
func (s *System) FormatSubscriptionsList(subscriptions []db.Subscription) string {
	if len(subscriptions) == 0 {
		return "您还没有订阅任何频道。\n\n💡 使用 `/listchannels` 查看可订阅频道"
	}

	// Python 首元素自带尾部 "\n"（join 后为空行）。
	lines := []string{"📚 **我的订阅**\n"}
	for i, sub := range subscriptions {
		// sub.get("channel_name", ...)：键恒存在；NULL 列 → Python None → 字面 "None"。
		channelName := "None"
		if sub.ChannelName.Valid {
			channelName = sub.ChannelName.String
		}
		lines = append(lines,
			fmt.Sprintf("%d. `%s`", i+1, channelName),
			fmt.Sprintf("   链接: `%s`", sub.ChannelID),
			fmt.Sprintf("   订阅时间: %s", formatDateFieldNull(sub.CreatedAt)),
			"",
		)
	}
	lines = append(lines,
		"💡 使用方法:",
		"`/unsubscribe <频道链接>`",
		"",
		"示例:",
		fmt.Sprintf("`/unsubscribe %s`", subscriptions[0].ChannelID),
	)
	return strings.Join(lines, "\n")
}
