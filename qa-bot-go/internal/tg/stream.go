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
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// 流式编辑协议与用户可见文案（与 qa_bot.py:681-930 逐字对齐，含 emoji/空格/标点）。
const (
	// CtrlDone 引擎控制标记：回答完成（R8：与 Python 字符串逐字一致）
	CtrlDone = "__DONE__"
	// CtrlNewSession 引擎控制标记：新会话开始
	CtrlNewSession = "__NEW_SESSION__"
	// CtrlErrorPrefix 引擎控制标记前缀：后随错误信息
	CtrlErrorPrefix = "__ERROR__:"

	// Cursor 流式编辑光标后缀（含前导空格；U+270D + U+FE0F）
	Cursor = " ✍️"

	// NewSessionPrefix 新会话首个文本块的前缀
	NewSessionPrefix = "🍃 _开始新的对话。_\n\n"

	// PlaceholderThinking 普通问答占位文案（qa_bot.py handle_message）
	PlaceholderThinking = "💭 正在思考..."
	// PlaceholderThinkingChannel /ask 限定频道问答占位文案（qa_bot.py:614）
	PlaceholderThinkingChannel = "💭 正在思考（限定频道）..."

	// ErrQueryFailedText 流中断且无内容时的错误文案
	ErrQueryFailedText = "❌ 抱歉，处理查询时出错。请稍后再试。"
	// ErrNoAnswerText 累积为空时的兜底文案
	ErrNoAnswerText = "❌ 未能获取回答，请稍后再试。"

	// QuotaHintFormat 配额提示（非管理员且剩余 <=1 时追加到最终文本）
	QuotaHintFormat = "\n\n_💡 提示：今日剩余查询次数：%d 次_"

	// StreamingTail 流式阶段截断续尾（qa_bot.py:776-779 的 4066+续尾）
	StreamingTail = "\n\n_（内容生成中…）_"
)

// 流式节流参数（qa_bot.py:704-708）。
const (
	// StreamEditThreshold 每积累多少 rune 触发一次编辑（R2：按 rune 计）
	StreamEditThreshold = 80
	// StreamEditInterval 距上次编辑超过该时长也触发一次编辑（monotonic 时钟）
	StreamEditInterval = 1500 * time.Millisecond
	// MaxMsgLen Telegram 单条消息最大字符数
	MaxMsgLen = 4096
	// streamTruncateLen 流式截断保留长度（MAX_MSG_LEN - 30 = 4066）
	streamTruncateLen = MaxMsgLen - 30
)

// MessageRef 标识一条可编辑的 Telegram 消息。
type MessageRef struct {
	ChatID    int64
	MessageID int
}

// EditMode 文本解析模式：流式阶段纯文本（避免不完整 Markdown 报错），
// 最终阶段 legacy Markdown（对齐 Python parse_mode="Markdown"）。
type EditMode int

const (
	// EditPlain 纯文本，不带 parse_mode。
	EditPlain EditMode = iota
	// EditMarkdown legacy Markdown（Telegram "Markdown"，即 MarkdownV1）。
	EditMarkdown
)

// Editor 对 Telegram 消息进行编辑与回复的窄接口（消费者包内定义，Go 惯例）。
// 生产实现为 *Client（api.go）；测试注入 fake。
type Editor interface {
	// EditText 编辑指定消息文本；mode 控制 parse_mode。
	EditText(ctx context.Context, ref MessageRef, text string, mode EditMode) (*MessageRef, error)
	// ReplyText 以指定消息为回复目标发送新消息；mode 控制 parse_mode。
	ReplyText(ctx context.Context, ref MessageRef, text string, mode EditMode) (*MessageRef, error)
}

// QuotaCheck quota_check 结果中被流式消费端读取的字段子集
// （对齐 Python quota_check.get("is_admin")/get("remaining", 99)）。
type QuotaCheck struct {
	IsAdmin   bool
	Remaining int
}

// StreamChunk 引擎输出块；Err 非 nil 表示流中断（对齐 async for 抛异常）。
type StreamChunk struct {
	Text string
	Err  error
}

// StreamAndEdit 流式接收 QA 引擎输出并实时编辑 Telegram 消息
// （qa_bot.py:681-856 _stream_and_edit 的逐行移植）。
//
// 策略：
//   - 流式阶段：以纯文本实时更新（避免不完整 Markdown 报错），每积累
//     StreamEditThreshold 个 rune 或距上次编辑超过 StreamEditInterval 则编辑一次，
//     文本末尾追加光标 Cursor；
//   - 完成阶段：用完整文本做最终编辑并启用 Markdown（失败则修复后重试，再失败转纯文本）；
//   - 超过 MaxMsgLen 时按 "\n\n" 段落贪心分片：首片编辑，其余逐片追加新消息。
//
// 返回值：完整回答成功输出返回 true；内部错误/中断返回 false
// （调用方据此回退本次配额占用，不为失败请求扣费）。
//
// now 注入单调时钟（测试用 fake clock）；生产传 time.Now。
func StreamAndEdit(
	ctx context.Context,
	ed Editor,
	log Logger,
	now func() time.Time,
	placeholder MessageRef,
	chunks <-chan StreamChunk,
	quota QuotaCheck,
) bool {
	accumulated := ""      // 当前消息已累积的完整文本
	lastEditLen := 0       // 上次编辑时的文本长度（rune）
	lastEditTime := now()  // 上次编辑时刻（monotonic）
	isNewSession := false  // 是否为新会话
	current := placeholder // 当前正在编辑的消息
	success := true        // 最终返回值；流中断但已有内容时置 false

	// safeEdit 安全地编辑消息，失败时静默处理（qa_bot.py:718-743 _safe_edit）。
	safeEdit := func(text string, useMarkdown bool) {
		// 空文本跳过（对齐 Python if not text.strip(): return）
		if strings.TrimSpace(text) == "" {
			return
		}
		mode := EditPlain
		if useMarkdown {
			mode = EditMarkdown
		}
		if _, err := ed.EditText(ctx, current, text, mode); err != nil {
			// 内容与当前内容相同时忽略
			if IsNotModified(err) {
				return
			}
			if useMarkdown {
				// Markdown 失败，尝试修复后重试
				if _, fixErr := ed.EditText(ctx, current, FixMarkdown(text), EditMarkdown); fixErr != nil {
					// 修复后仍然失败：转纯文本（静默）
					_, _ = ed.EditText(ctx, current, text, EditPlain)
				}
			} else {
				log.Debugf("编辑消息失败（已忽略）: %v", err)
			}
		}
	}

	// flushEdit 将 accumulated 写入当前消息（或追加新消息）。
	// （qa_bot.py:745-775 _flush_edit；Python 版仅以 final=True 调用，
	// 流式截断分支为保持行为完整一并移植。）
	flushEdit := func(final bool) {
		text := accumulated
		if strings.TrimSpace(text) == "" {
			return
		}

		if utf8.RuneCountInString(text) > MaxMsgLen {
			if final {
				// 超过单条消息长度限制：保留当前消息，超出部分作为新消息逐片追加
				parts := SplitLongMessage(text, MaxMsgLen)
				// 编辑当前消息为第一部分（Markdown 失败链见 safeEdit）
				safeEdit(parts[0], true)
				// 追加其余部分：Markdown 失败后修复重试，再失败放弃
				//（对齐 Python：分片路径无纯文本兜底）
				for _, part := range parts[1:] {
					newRef, err := ed.ReplyText(ctx, current, part, EditMarkdown)
					if err != nil {
						newRef, err = ed.ReplyText(ctx, current, FixMarkdown(part), EditMarkdown)
					}
					if err == nil && newRef != nil {
						current = *newRef
					}
				}
			} else {
				// 流式阶段：截断显示，末尾加续尾（仅显示层）
				truncated := TruncateRunes(text, streamTruncateLen) + StreamingTail
				safeEdit(truncated, false)
			}
		} else {
			safeEdit(text, final)
		}

		lastEditLen = utf8.RuneCountInString(accumulated)
		lastEditTime = now()
	}

loop:
	for {
		var chunk StreamChunk
		var ok bool
		select {
		case chunk, ok = <-chunks:
			if !ok {
				break loop // 流正常结束
			}
		case <-ctx.Done():
			// Go 侧增强：取消时兜底编辑+退款。Python 的 asyncio.CancelledError 继承
			// BaseException，不会被 _stream_and_edit / handle_message 的 except
			// Exception 捕获（qa_bot.py:837-845、:677）——关停路径直接穿透：无错误
			// 文案编辑、无最终 flush、不退款。
			chunk = StreamChunk{Err: ctx.Err()}
		}

		if chunk.Err != nil {
			log.Errorf("流式接收失败: %T: %v", chunk.Err, chunk.Err)
			if accumulated == "" {
				safeEdit(ErrQueryFailedText, false)
				return false
			}
			// 已有部分内容：继续最终编辑保留已生成内容，但仍视为失败以便回退配额
			success = false
			break loop
		}
		text := chunk.Text

		// ── 处理特殊控制标记 ─────────────────────────────────────
		switch {
		case text == CtrlDone:
			break loop
		case text == CtrlNewSession:
			isNewSession = true
			continue
		case strings.HasPrefix(text, CtrlErrorPrefix):
			errorMsg := strings.TrimPrefix(text, CtrlErrorPrefix)
			safeEdit(errorMsg, false)
			return false
		}

		// ── 首个真实文本块：更新占位消息状态 ─────────────────────
		if accumulated == "" {
			if isNewSession {
				accumulated = NewSessionPrefix + text
			} else {
				accumulated += text
			}
			safeEdit(accumulated+Cursor, false)
			lastEditLen = utf8.RuneCountInString(accumulated)
			lastEditTime = now()
			continue
		}

		// ── 累积文本 ─────────────────────────────────────────────
		accumulated += text

		// ── 判断是否需要触发编辑（80 rune / 1.5s 双阈值） ─────────
		charsSinceEdit := utf8.RuneCountInString(accumulated) - lastEditLen
		timeSinceEdit := now().Sub(lastEditTime)
		if charsSinceEdit >= StreamEditThreshold || timeSinceEdit >= StreamEditInterval {
			// 流式阶段：纯文本 + 光标提示
			safeEdit(accumulated+Cursor, false)
			lastEditLen = utf8.RuneCountInString(accumulated)
			lastEditTime = now()
		}
	}

	// ── 生成完成：最终编辑，追加配额提示并启用 Markdown ─────────────
	if strings.TrimSpace(accumulated) == "" {
		safeEdit(ErrNoAnswerText, false)
		return false
	}

	// 追加配额提示（剩余次数不足时）
	if !quota.IsAdmin && quota.Remaining <= 1 {
		accumulated += fmt.Sprintf(QuotaHintFormat, quota.Remaining)
	}

	// 最终写入（启用 Markdown）
	flushEdit(true)
	log.Infof("流式回答完成，总长度: %d 字符", utf8.RuneCountInString(accumulated))
	return success
}

// ReplyWithFallback 发送消息，强制使用 Markdown 格式；AI 生成的 Markdown 有语法
// 错误时先修复重试，仍失败则退回纯文本（qa_bot.py:880-898 _send_with_fallback 逐行移植）。
func ReplyWithFallback(ctx context.Context, ed Editor, log Logger, ref MessageRef, text string) (*MessageRef, error) {
	// 直接尝试发送 Markdown
	newRef, err := ed.ReplyText(ctx, ref, text, EditMarkdown)
	if err == nil {
		return newRef, nil
	}
	log.Warningf("Markdown发送失败: %v, 尝试修复格式", err)
	// 尝试修复常见的 Markdown 格式错误
	newRef, err = ed.ReplyText(ctx, ref, FixMarkdown(text), EditMarkdown)
	if err == nil {
		return newRef, nil
	}
	log.Errorf("Markdown修复后仍然失败: %v, 使用纯文本", err)
	// 最后的保底方案
	return ed.ReplyText(ctx, ref, text, EditPlain)
}
