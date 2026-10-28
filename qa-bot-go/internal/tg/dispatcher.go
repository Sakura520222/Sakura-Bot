// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// dispatcher.go：update 线性分发链（cont-2 设计 §5.2 与对抗评审 B-1/B-4
// 裁决的实现，README 硬规则 1）。
//
// 串行纪律：go-telegram/bot 默认对每个 handler 起 goroutine
// （process_update.go 的 `go r(...)`），main 必须以
// `bot.WithNotAsyncHandlers()` + `bot.WithWorkers(1)` 装配，使本分发器在
// 唯一的 worker goroutine 里同步执行——对齐 PTB 未开 concurrent_updates
// 的默认串行语义（B-1）。本包自身不创建任何 goroutine。
//
// panic 纪律：每次 handleUpdate 顶层 defer recover（B-4），记 error 日志
// （含 update JSON 摘要）后返回，进程存活、后续 update 照常处理。
//
// 链序（复刻 qa_bot.py:981-1048 的组 -1 → 组 0 注册优先级，首个消费即短路）：
//
//  1. 申诉回调：callback_data ^ap:（组 -1 CallbackQueryHandler）
//  2. 申诉消息：filters.ALL，fail-closed——任何"可能属于申诉"的 update
//     或内部异常都终止分发，绝不让申诉证据漏给 AI（appeal_handler.py:125-132）
//  3. 投稿 FSM：入口 /submit、/start submit、^📝 投稿$ 与态内按钮/文本
//  4. 12 命令 → 6 主菜单文本 → 7 游离投稿文本 → 普通文本 AI 问答
//     （三段由 commands.Route 按同组"先匹配先得"顺序一并承担）
//
// 无任何层消费的 update 静默丢弃（与 PTB 组 0 无 fallback handler 时一致）。
package tg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/go-telegram/bot/models"
)

// updateSummaryLimit panic 日志中 update JSON 摘要的最大长度（rune）。
const updateSummaryLimit = 1000

// AppealLayer 申诉层（组 -1）窄接口：生产实现为 *handlers.AppealHandler，
// 测试注入假层。bool 返回值语义 = Python ApplicationHandlerStop（true 终止
// 分发链）；false 时 update 落入下一层。
type AppealLayer interface {
	// HandleCallback 处理 ^ap: 回调（appeal_handler.py callback）。
	HandleCallback(ctx context.Context, upd *models.Update) bool
	// HandleMessage 处理任意消息型 update（filters.ALL，fail-closed）。
	HandleMessage(ctx context.Context, upd *models.Update) bool
}

// SubmissionLayer 投稿 FSM 窄接口：生产实现为 *handlers.SubmissionHandler。
type SubmissionLayer interface {
	// HandleUpdate 入口与态内处理；true = update 被本 FSM 消费。
	HandleUpdate(ctx context.Context, upd *models.Update) bool
}

// CommandLayer 组 0 命令/菜单/游离按钮/普通文本分发窄接口：生产实现为
// *handlers.Commands（Route 内部按 PTB 组 0 注册序依次匹配，无返回值——
// 未匹配即静默丢弃，不再有下层）。
type CommandLayer interface {
	Route(ctx context.Context, upd *models.Update)
}

// Dispatcher 线性分发器。三个层字段由 main 在 bot.Start 之前装配
// （WithDefaultHandler 需要先拿到本分发器，故字段后置赋值）；任一层为 nil
// 时跳过对应步骤（防御装配遗漏，正常装配不应出现）。
type Dispatcher struct {
	// Appeal 申诉层（组 -1）。
	Appeal AppealLayer
	// Submission 投稿 FSM。
	Submission SubmissionLayer
	// Commands 组 0 命令与问答。
	Commands CommandLayer
	// Log 日志器；nil 时静默。
	Log Logger

	inFlight atomic.Int64
}

// NewDispatcher 构造分发器（层字段随后由 main 赋值）。
func NewDispatcher(log Logger) *Dispatcher {
	if log == nil {
		log = NopLogger{}
	}
	return &Dispatcher{Log: log}
}

// InFlight 返回正在处理中的 update 数（main 关停时等待在途 handler，
// 上限 3s——设计 §11 关停序列）。
func (d *Dispatcher) InFlight() int64 { return d.inFlight.Load() }

// IsAppealCallbackData 判定申诉回调过滤器 ^ap:（qa_bot.py:983
// pattern=r"^ap:"，re.match 语义 = 前缀匹配）。
func IsAppealCallbackData(data string) bool {
	return strings.HasPrefix(data, "ap:")
}

// HandleUpdate 单条 update 的分发链入口（注册为 bot 的 default handler）。
// 顶层 recover 保证单个 handler panic 不杀进程（B-4，README 硬规则 1）。
func (d *Dispatcher) HandleUpdate(ctx context.Context, upd *models.Update) {
	d.inFlight.Add(1)
	defer d.inFlight.Add(-1)
	defer func() {
		if r := recover(); r != nil {
			d.Log.Errorf("处理更新失败（已跳过）: %T: %v | update摘要: %s",
				r, r, UpdateJSONSummary(upd, updateSummaryLimit))
		}
	}()

	if upd == nil {
		return
	}

	// 1. 组 -1：申诉回调（^ap:）。守卫早退（无 query/消息/actor）返回 false：
	// Python 侧该 return 位于 try/finally 之前、不触发 ApplicationHandlerStop，
	// update 同样继续落组 0（最终无消费者，静默丢弃）。
	if upd.CallbackQuery != nil && IsAppealCallbackData(upd.CallbackQuery.Data) {
		if d.Appeal != nil && d.Appeal.HandleCallback(ctx, upd) {
			return
		}
	}

	// 2. 组 -1：申诉消息层（对任意 update 调用，层内自判 update.message
	// 并在"可能属于申诉"或异常时短路——fail-closed）。
	if d.Appeal != nil && d.Appeal.HandleMessage(ctx, upd) {
		return
	}

	// 3. 投稿 FSM（入口 + 态内处理器；未命中放行）。
	if d.Submission != nil && d.Submission.HandleUpdate(ctx, upd) {
		return
	}

	// 4. 组 0：命令 → 主菜单 → 游离投稿按钮 → 普通文本 AI 问答。
	if d.Commands != nil {
		d.Commands.Route(ctx, upd)
	}
}

// UpdateJSONSummary 生成 update 的 JSON 摘要（panic 日志用）：序列化失败
// 回退 %+v 形态；超过 limit 个 rune 时按码点截断（README 硬规则 3）。
func UpdateJSONSummary(upd *models.Update, limit int) string {
	if upd == nil {
		return ""
	}
	raw, err := json.Marshal(upd)
	text := string(raw)
	if err != nil {
		// json.Marshal 失败（理论上 models.Update 不会）：回退 %+v 形态。
		text = fmt.Sprintf("%+v", upd)
	}
	runes := []rune(text)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
