// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package tg

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// TG API 调用超时与限流对策（README 硬规则4 / 评审建议8）。
const (
	// DefaultAPITimeout 所有 TG API 调用统一 5s 超时。
	// PTB 默认 read/write/connect 超时即 5s（评审建议7：编辑类调用无超时会导致
	// 串行分发模型下 QA 整体冻结）。
	DefaultAPITimeout = 5 * time.Second
	// AppealCardTimeout 申诉卡片 edit_or_send 的 25s 例外（appeal_messages.py:16-41）。
	AppealCardTimeout = 25 * time.Second
	// floodSleepThreshold 429 仅当 RetryAfter <= 60s 才睡眠重试一次，否则按错误返回
	// （对齐 PTB flood_sleep_threshold 默认 60s；3600s 的 RetryAfter 不得挂起 handler）。
	floodSleepThreshold = 60
)

// IsNotModified 判断 err 是否为 "message is not modified"（编辑内容未变化）。
//
// PTB 会把错误描述首字母大写（"Message is not modified"），qa_bot.py:730-731
// 据此静默；go-telegram/bot 透传 Telegram 原始小写描述，因此这里不区分大小写
// 匹配，两种运行时行为等价。
func IsNotModified(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "message is not modified")
}

// Client go-telegram/bot 薄封装：统一 5s 超时（申诉卡片 25s 例外）与
// 429 RetryAfter<=60s 单次睡眠重试；实现 Editor 供 StreamAndEdit 消费。
type Client struct {
	b       *bot.Bot
	log     Logger
	timeout time.Duration
}

// 编译期断言：*Client 满足 Editor。
var _ Editor = (*Client)(nil)

// NewClient 包装 *bot.Bot。timeout 传 0 或负值时使用 DefaultAPITimeout；
// log 传 nil 时使用 NopLogger。
func NewClient(b *bot.Bot, log Logger, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultAPITimeout
	}
	if log == nil {
		log = NopLogger{}
	}
	return &Client{b: b, log: log, timeout: timeout}
}

// Bot 返回底层 *bot.Bot（供 main 启动长轮询/注册 handler）。
func (c *Client) Bot() *bot.Bot { return c.b }

// call 统一执行一次 TG API 调用：
//   - 请求上下文截断到 timeout（外部 ctx 更早取消/超限时以外部为准）；
//   - 429 且 0 <= RetryAfter <= 60s 时睡眠 RetryAfter+1s 后仅重试一次
//     （对齐 PTB 对 flood 内错误的单次自动重试，cont-2 §5.4）；
//   - 其余错误原样返回，由调用方决定静默或降级。
func call[T any](ctx context.Context, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	res, err := callOnce(ctx, timeout, fn)
	if err == nil {
		return res, nil
	}

	var flood *bot.TooManyRequestsError
	if !errors.As(err, &flood) || flood.RetryAfter < 0 || flood.RetryAfter > floodSleepThreshold {
		return res, err
	}

	// 睡眠 RetryAfter + 1s，期间响应外部取消
	select {
	case <-ctx.Done():
		return res, ctx.Err()
	case <-time.After(time.Duration(flood.RetryAfter)*time.Second + time.Second):
	}

	return callOnce(ctx, timeout, fn)
}

// callOnce 带单次调用超时执行 fn。
func callOnce[T any](ctx context.Context, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fn(callCtx)
}

// parseModeOf 把 EditMode 映射为 Telegram parse_mode；
// legacy "Markdown" 对齐 Python 的 parse_mode="Markdown"。
func parseModeOf(mode EditMode) models.ParseMode {
	if mode == EditMarkdown {
		return models.ParseModeMarkdownV1
	}
	return ""
}

// SendMessage 发送消息（统一 5s 超时 + 429 单次重试）。
func (c *Client) SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	return call(ctx, c.timeout, func(ctx context.Context) (*models.Message, error) {
		return c.b.SendMessage(ctx, params)
	})
}

// SendMessageWithTimeout 以显式超时发送消息（申诉卡片 25s 例外入口）。
func (c *Client) SendMessageWithTimeout(ctx context.Context, timeout time.Duration, params *bot.SendMessageParams) (*models.Message, error) {
	return call(ctx, timeout, func(ctx context.Context) (*models.Message, error) {
		return c.b.SendMessage(ctx, params)
	})
}

// EditMessageText 编辑消息文本（统一 5s 超时 + 429 单次重试）。
// 调用方通过 IsNotModified 决定是否静默（qa_bot.py:730-731）。
func (c *Client) EditMessageText(ctx context.Context, params *bot.EditMessageTextParams) (*models.Message, error) {
	return call(ctx, c.timeout, func(ctx context.Context) (*models.Message, error) {
		return c.b.EditMessageText(ctx, params)
	})
}

// EditMessageTextWithTimeout 以显式超时编辑消息文本（申诉卡片 25s 例外入口）。
func (c *Client) EditMessageTextWithTimeout(ctx context.Context, timeout time.Duration, params *bot.EditMessageTextParams) (*models.Message, error) {
	return call(ctx, timeout, func(ctx context.Context) (*models.Message, error) {
		return c.b.EditMessageText(ctx, params)
	})
}

// AnswerCallbackQuery 应答回调查询（统一 5s 超时 + 429 单次重试）。
func (c *Client) AnswerCallbackQuery(ctx context.Context, params *bot.AnswerCallbackQueryParams) (bool, error) {
	return call(ctx, c.timeout, func(ctx context.Context) (bool, error) {
		return c.b.AnswerCallbackQuery(ctx, params)
	})
}

// EditText 实现 Editor：按 MessageRef 编辑指定消息文本。
func (c *Client) EditText(ctx context.Context, ref MessageRef, text string, mode EditMode) (*MessageRef, error) {
	params := &bot.EditMessageTextParams{
		ChatID:    ref.ChatID,
		MessageID: ref.MessageID,
		Text:      text,
		ParseMode: parseModeOf(mode),
	}
	msg, err := c.EditMessageText(ctx, params)
	if err != nil {
		return nil, err
	}
	return &MessageRef{ChatID: ref.ChatID, MessageID: msg.ID}, nil
}

// EditTextWithTimeout 以显式超时编辑指定消息文本（申诉卡片 HTML 25s）。
func (c *Client) EditTextWithTimeout(ctx context.Context, timeout time.Duration, ref MessageRef, text string, mode EditMode) (*MessageRef, error) {
	params := &bot.EditMessageTextParams{
		ChatID:    ref.ChatID,
		MessageID: ref.MessageID,
		Text:      text,
		ParseMode: parseModeOf(mode),
	}
	msg, err := c.EditMessageTextWithTimeout(ctx, timeout, params)
	if err != nil {
		return nil, err
	}
	return &MessageRef{ChatID: ref.ChatID, MessageID: msg.ID}, nil
}

// ReplyText 实现 Editor：以 ref 为回复目标发送新消息
// （对齐 Python message.reply_text：设置 reply_to_message_id，
// 不带 allow_sending_without_reply）。
func (c *Client) ReplyText(ctx context.Context, ref MessageRef, text string, mode EditMode) (*MessageRef, error) {
	params := &bot.SendMessageParams{
		ChatID: ref.ChatID,
		Text:   text,
		ReplyParameters: &models.ReplyParameters{
			ChatID:    ref.ChatID,
			MessageID: ref.MessageID,
		},
		ParseMode: parseModeOf(mode),
	}
	msg, err := c.SendMessage(ctx, params)
	if err != nil {
		return nil, err
	}
	return &MessageRef{ChatID: msg.Chat.ID, MessageID: msg.ID}, nil
}
