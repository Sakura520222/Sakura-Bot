// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：长衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// push_job.go：跨进程通知轮询任务（core/handlers/mainbot_push_handler.py 的移植）。
//
// 调度对齐 qa_bot.py:1076 run_repeating(interval=30, first=10)：由 main 起
// goroutine，先 sleep 10s，之后每 30s 执行一轮；发送走 QA token 的 tg 客户端
// （5s 趞时 + 429 单次重试，README 硬规则 4）。
//
// 运维红线（行为规格 §3.3）：通知队列保持单消费者——QA 只允许单实例轮询。
package handlers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

// 轮询与清理参数（qa_bot.py:1076 的 interval/first；清理 7d 为任务指令附载）。
const (
	// pushFirstDelay 首轮执行延迟（first=10）。
	pushFirstDelay = 10 * time.Second
	// pushInterval 轮询间隔（interval=30）。
	pushInterval = 30 * time.Second
	// pushBatchLimit 单轮取 pending 上限（get_pending_notifications(limit=50)）。
	pushBatchLimit = 50
	// pushRetentionDays sent/failed 通知与 completed/failed 请求的保留天数。
	pushRetentionDays = 7
	// pushCleanupInterval 清理执行频率（Python QA 进程未调用清理，Go 版附载
	// 于推送 job；每小时一次足以满足 7d 语义）。
	pushCleanupInterval = time.Hour
)

// plainTextTypes 需使用纯文本的通知类型（mainbot_push_handler.py:180-183 的
// _PLAIN_TEXT_TYPES）：含动态 URL/用户名，其中 _ * 等字符会被 Markdown 误解析。
// 其余类型默认 Markdown；Markdown 发送失败不降级（R17）。
var plainTextTypes = map[string]bool{
	"submission_approved": true,
	"submission_rejected": true,
}

// errNullMessage 显式 null 的 content.message（键存在但值为 None）哨兵：
// Python text=None → Bot API 400 "message text is empty" → BadRequestError
// （BotAPIError 子类）→ except BotAPIError 分支标 failed（审计 B-4）。
var errNullMessage = errors.New("message text is empty")

// PushStore 推送任务对 MySQL 层的窄接口（生产实现 *db.Manager）。
type PushStore interface {
	// GetPendingNotifications pending、FIFO、LIMIT（mysql.py:2025-2054）。
	GetPendingNotifications(ctx context.Context, limit int) ([]db.Notification, error)
	// UpdateNotificationStatus SET status=?, sent_at=NOW()（mysql.py:2056-2077）。
	UpdateNotificationStatus(ctx context.Context, notificationID int64, status string) error
	// CleanupOldNotifications 7d 清理（mysql.py:2079-2098）。
	CleanupOldNotifications(ctx context.Context, days int) (int64, error)
	// CleanupOldRequests 7d 清理（mysql.py:1968-1991）。
	CleanupOldRequests(ctx context.Context, days int) (int64, error)
}

// PushSender 推送任务对 Telegram 层的窄接口（生产实现 *tg.Client）。
type PushSender interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
}

// PushJob 跨进程通知轮询任务（对齐 MainBotPushHandler.process_pending_notifications）。
type PushJob struct {
	store  PushStore
	sender PushSender
	// token 为 QA_BOT_TOKEN；空串等价 Python 未配置（warning 后跳过）。
	token string
	log   tg.Logger
	now   func() time.Time
	// lastCleanup / cleanupInterval 为 Go 版附载的 7d 清理节流状态。
	lastCleanup     time.Time
	cleanupInterval time.Duration
}

// NewPushJob 装配推送任务。token 取 config 的 QABotToken()。
func NewPushJob(store PushStore, sender PushSender, token string, log tg.Logger) *PushJob {
	if log == nil {
		log = tg.NopLogger{}
	}
	return &PushJob{
		store:           store,
		sender:          sender,
		token:           token,
		log:             log,
		now:             time.Now,
		cleanupInterval: pushCleanupInterval,
	}
}

// Run 推送任务主循环（由 main 起 goroutine）：先 sleep 10s，之后每 30s 一轮
// （对齐 run_repeating(interval=30, first=10) 的 10s/40s/70s… 节拍）。
func (j *PushJob) Run(ctx context.Context) {
	timer := time.NewTimer(pushFirstDelay)
	select {
	case <-timer.C:
	case <-ctx.Done():
		timer.Stop()
		return
	}
	j.Tick(ctx)

	ticker := time.NewTicker(pushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.Tick(ctx)
		}
	}
}

// Tick 执行一轮通知检查（qa_bot.py:1052-1073 check_notifications_job 的移植；
// Python 原实现的 `push_handler.qa_bot is None` 预检引用了不存在的属性而恒抛
// AttributeError——该 bug 不移植，Go 版直接执行轮询本体）。
func (j *PushJob) Tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			j.log.Errorf("❌ 检查通知任务失败: %T: %v", r, r)
		}
	}()

	count := j.ProcessPendingNotifications(ctx)
	if count > 0 {
		j.log.Infof("✅ 通知检查任务完成：已处理 %d 条通知", count)
	} else {
		j.log.Debugf("📭 通知检查任务完成：无待处理通知")
	}

	j.maybeCleanup(ctx)
}

// maybeCleanup 附载的 7d 清理：notification+request 两表，按 cleanupInterval 节流。
func (j *PushJob) maybeCleanup(ctx context.Context) {
	now := j.now()
	if !j.lastCleanup.IsZero() && now.Sub(j.lastCleanup) < j.cleanupInterval {
		return
	}
	j.lastCleanup = now

	if _, err := j.store.CleanupOldNotifications(ctx, pushRetentionDays); err != nil {
		j.log.Errorf("清理旧通知失败: %T: %s", err, err)
	}
	if _, err := j.store.CleanupOldRequests(ctx, pushRetentionDays); err != nil {
		j.log.Errorf("清理旧请求失败: %T: %s", err, err)
	}
}

// ProcessPendingNotifications 处理待发送的通知队列
// （mainbot_push_handler.py:130-231 process_pending_notifications 的移植）。
// 返回成功处理的通知数。
func (j *PushJob) ProcessPendingNotifications(ctx context.Context) (count int) {
	// Python 整个方法体包 try/except 返回 0（mainbot_push_handler.py:229-231）。
	defer func() {
		if r := recover(); r != nil {
			j.log.Errorf("❌ 处理通知队列失败: %T: %v", r, r)
			count = 0
		}
	}()

	if j.token == "" {
		j.log.Warningf("❌ 问答Bot未初始化，无法处理通知")
		return 0
	}

	// 获取待发送通知（pending，FIFO，LIMIT 50）。
	notifications, err := j.store.GetPendingNotifications(ctx, pushBatchLimit)
	if err != nil {
		j.log.Errorf("❌ 处理通知队列失败: %T: %s", err, err)
		return 0
	}

	if len(notifications) == 0 {
		j.log.Debugf("📭 无待处理通知")
		return 0
	}

	j.log.Infof("🔍 发现 %d 个待发送通知", len(notifications))

	successCount := 0
	failedCount := 0

	// Python 逐条 try/except（mainbot_push_handler.py:162-216）：渲染/发送/
	// 状态更新任一失败都不中止本轮，继续处理余下通知（审计 B-3）。
	for _, notification := range notifications {
		if j.processOne(ctx, notification) {
			successCount++
		} else {
			failedCount++
		}
	}

	// 总结日志（mainbot_push_handler.py:218-225）。
	total := len(notifications)
	if failedCount > 0 {
		j.log.Warningf("⚠️ 通知处理完成: 成功 %d/%d, 失败 %d/%d", successCount, total, failedCount, total)
	} else {
		j.log.Infof("✅ 通知处理完成: 全部成功 (%d/%d)", successCount, total)
	}

	return successCount
}

// processOne 渲染并发送单条通知，随后更新状态。
// 返回 true 表示发送成功（Python：update_notification_status 内部吞异常返回
// False 且调用点不检查返回值——状态更新失败仍计成功、本轮继续，审计 B-3）；
// 返回 false 表示已按 Python 语义标记 failed。
func (j *PushJob) processOne(ctx context.Context, notification db.Notification) (sent bool) {
	notificationID := notification.ID
	userID := notification.UserID
	notificationType := notification.NotificationType

	// 根据类型构建消息（mainbot_push_handler.py:169-178）。
	var (
		message   string
		renderErr error
	)
	switch {
	case notificationType == "request_result":
		var content map[string]any
		content, renderErr = contentDict(notification.Content)
		if renderErr == nil {
			message = formatRequestResult(content)
		}
	case notificationType == "summary_push":
		var content map[string]any
		content, renderErr = contentDict(notification.Content)
		if renderErr == nil {
			message = formatSummaryPush(content)
		}
	case strings.HasPrefix(notificationType, "submission_"):
		// 投稿审核结果通知：content.message 原文。显式 null（键存在但值为
		// None）时 Python 得到 message=None → Bot API 400 → BadRequestError →
		// 标 failed（mainbot_push_handler.py:176、196-208，审计 B-4）；Go 无法
		// 投递 nil 文本，按同一 API 错误文案收敛。
		var content map[string]any
		content, renderErr = contentDict(notification.Content)
		if renderErr == nil {
			if v := dictGet(content, "message", "您有新的投稿通知"); v == nil {
				renderErr = errNullMessage
			} else {
				message = pyValueString(v)
			}
		}
	default:
		// 未知类型 fallback（README 硬规则 8，mainbot_push_handler.py:177-178）。
		message = "您有新的通知"
	}
	if renderErr != nil {
		if renderErr == errNullMessage {
			// BotAPIError 分支日志（mainbot_push_handler.py:205-206；str(e) 已去
			// "Bad Request: " 前缀）。
			j.log.Errorf("❌ Telegram API 错误: %s", renderErr.Error())
			return j.failNotification(ctx, notification, "")
		}
		// Python：content 非 dict 时 .get 抛 AttributeError → 通用 except → 标 failed。
		return j.failNotification(ctx, notification,
			fmt.Sprintf("❌ 处理通知 %d 时发生异常: %s", notificationID, renderErr.Error()))
	}

	// parse_mode 分支（mainbot_push_handler.py:180-189）：纯文本类型不带
	// parse_mode；其余 Markdown，失败不降级直接标 failed（R17）。
	parseMode := models.ParseMode("")
	if !plainTextTypes[notificationType] {
		parseMode = models.ParseModeMarkdownV1
	}

	params := &bot.SendMessageParams{
		ChatID:    userID,
		Text:      message,
		ParseMode: parseMode,
	}
	if _, err := j.sender.SendMessage(ctx, params); err != nil {
		// BotAPIError 分支（mainbot_push_handler.py:196-208）：
		// blocked / deactivated / chat not found / 其他，均标 failed 不重试。
		errorMsg := err.Error()
		switch {
		case strings.Contains(errorMsg, "bot was blocked by the user"):
			j.log.Warningf("⚠️ 用户 %d 已阻止Bot，标记通知为失败", userID)
		case strings.Contains(errorMsg, "user is deactivated"):
			j.log.Warningf("⚠️ 用户 %d 账号已停用，标记通知为失败", userID)
		case strings.Contains(errorMsg, "chat not found"):
			j.log.Warningf("⚠️ 未找到与用户 %d 的聊天，标记通知为失败", userID)
		default:
			j.log.Errorf("❌ Telegram API 错误: %s", errorMsg)
		}
		return j.failNotification(ctx, notification, "")
	}

	// 发送成功 → status='sent'（附 sent_at=NOW()）。Python 状态更新内部吞异常
	// （mysql.py:2075-2077，日志"更新通知状态失败: ..."）返回 False 且调用点
	// 不检查返回值：仍计成功、✅ 照记，状态留待下轮（审计 B-3）。
	if err := j.store.UpdateNotificationStatus(ctx, notificationID, "sent"); err != nil {
		j.log.Errorf("更新通知状态失败: Exception: %v", err)
	}
	j.log.Infof("✅ 成功发送通知给用户 %d, 通知ID: %d", userID, notificationID)
	return true
}

// failNotification 标记通知 failed；状态更新失败仅记日志（Python
// update_notification_status 吞异常返回 False，逐条 try/except 保证本轮
// 继续处理余下通知，审计 B-3）。
func (j *PushJob) failNotification(ctx context.Context, notification db.Notification, exceptionLog string) bool {
	if exceptionLog != "" {
		j.log.Errorf("%s", exceptionLog)
	}
	if err := j.store.UpdateNotificationStatus(ctx, notification.ID, "failed"); err != nil {
		j.log.Errorf("更新通知状态失败: Exception: %v", err)
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────
// 通知渲染（mainbot_push_handler.py:233-264 逐字移植，Markdown 模板）
// ─────────────────────────────────────────────────────────────────────

// contentDict 校验 content 为 JSON 对象；否则复刻 Python `.get` 在
// None/str/list 等类型上的 AttributeError（→ 通用 except → 标 failed）。
func contentDict(content any) (map[string]any, error) {
	if m, ok := content.(map[string]any); ok {
		return m, nil
	}
	return nil, fmt.Errorf("AttributeError: '%s' object has no attribute 'get'", pyJSONTypeName(content))
}

// dictGet 复刻 Python dict.get(key, default)：键存在时返回原值（含 None）。
func dictGet(content map[string]any, key, defaultValue string) any {
	if v, ok := content[key]; ok {
		return v
	}
	return defaultValue
}

// formatRequestResult 格式化请求结果通知（mainbot_push_handler.py:233-249）。
func formatRequestResult(content map[string]any) string {
	requestID := pyValueString(dictGet(content, "request_id", "未知"))
	channelID := pyValueString(dictGet(content, "channel_id", "未知频道"))
	message := pyValueString(dictGet(content, "message", ""))

	// 使用代码块显示频道ID，避免转义问题。
	return fmt.Sprintf("📝 **请求结果通知**\n\n您的总结请求已处理完成！\n\n"+
		"**请求ID**: %s\n**频道**: `%s`\n\n"+
		"**结果**: %s\n\n💡 感谢使用！", requestID, channelID, message)
}

// formatSummaryPush 格式化总结推送通知（mainbot_push_handler.py:251-264）。
func formatSummaryPush(content map[string]any) string {
	channelName := pyValueString(dictGet(content, "channel_name", "未知频道"))
	summaryPreview := pyValueString(dictGet(content, "summary_preview", ""))

	// 使用代码块显示频道名称，避免转义问题。
	return fmt.Sprintf("📬 **新总结通知**\n\n频道 `%s` 有新的总结发布了！\n\n"+
		"**总结预览**:\n%s\n\n💡 查看完整总结，请访问频道。", channelName, summaryPreview)
}

// ─────────────────────────────────────────────────────────────────────
// Python 值渲染辅助（f-string 对 JSON 解析值的 str() 语义）
// ─────────────────────────────────────────────────────────────────────

// pyJSONTypeName 返回 JSON 解析值在 Python 侧的类型名
// （json.loads 的 int/float 区分按"整数值 → int"近似；float64 已丢失原始字面）。
func pyJSONTypeName(v any) string {
	switch x := v.(type) {
	case nil:
		return "NoneType"
	case string:
		return "str"
	case bool:
		return "bool"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return "int"
		}
		return "float"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// pyValueString 复刻 Python f-string 对 JSON 值的 str() 渲染。
// （map/slice 分支生产端不会产生，按 %v 兜底并注明。）
func pyValueString(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case float64:
		// json.loads("123") → Python int → "123"；
		// json.loads("1.5") → float → "1.5"（'g' 最短表示）。
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatFloat(x, 'f', -1, 64)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		// list/dict：Python str() 语义复杂且生产端不产生；保守 %v 兜底。
		return fmt.Sprintf("%v", x)
	}
}
