// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package engine 是 QA 问答引擎（qa_engine_v3.py + conversation_manager.py 的移植）：
// 通用 AI 助手 + Agentic RAG + 真实多轮对话。
//
// 设计原则（qa_engine_v3.py:14-22 逐条对齐）：
//  1. 通用助手是默认路径：普通对话直接回答，频道数据能力以 Function Tools
//     形式供模型自主调用（工具执行器经 Tools 窄接口注入）；
//  2. 用户消息原样传递；/ask 的频道约束经系统级可信上下文注入；
//  3. 对话历史使用真实 user/assistant 角色序列；
//  4. 原生流式 tool-call 循环：文本增量即时下发，无工具调用时不二次生成；
//  5. 每个请求使用独立的工具执行上下文，多用户互不污染；
//  6. 失败降级为"无工具直连对话"。
//
// 对外 API 为 ProcessQueryStream：以通道下发 Chunk（文本/控制标记/错误），
// 控制标记协议 __DONE__ / __NEW_SESSION__ / __ERROR__: 与 Python 字符串逐字一致
// （README 硬规则 9，常量取自 internal/contracts）。
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
)

// 引擎行为常量（qa_engine_v3.py:101-108 逐项对齐）。
const (
	// AgentMaxIterations Agentic RAG 最大工具调用迭代次数（AGENT_MAX_ITERATIONS）。
	AgentMaxIterations = 10
	// MaxHistoryMessages 提供给模型的历史消息条数上限（MAX_HISTORY_MESSAGES）。
	MaxHistoryMessages = 20
	// HistoryMessageMaxChars 单条历史消息内容截断长度（HISTORY_MESSAGE_MAX_CHARS，
	// Python len 语义 = Unicode 码点，Go 侧按 rune 计，README 硬规则 3）。
	HistoryMessageMaxChars = 1500
)

// 用户可见文案与提示词片段（与 qa_engine_v3.py 逐字一致，禁止改动措辞）。
const (
	// errQueryFailedText 外层兜底错误文案（qa_engine_v3.py:221，不含 __ERROR__: 前缀）。
	errQueryFailedText = "❌ 处理查询时出错，请稍后重试。"
	// emptyAnswerText 空回答兜底文案（qa_engine_v3.py:207）。
	emptyAnswerText = "⚠️ 未能生成回答，请稍后重试。"
	// interruptedNoticeText 已有部分输出后中断的附加提示（qa_engine_v3.py:201）。
	interruptedNoticeText = "\n\n⚠️ 回答生成中断，请稍后重试。"
	// forcedFinalPrompt 达到最大迭代后注入的强制终答指令（qa_engine_v3.py:384）。
	forcedFinalPrompt = "请基于已收集的信息直接给出最终回答。"
	// newSessionPersistPrefix 新会话时拼入 assistant 持久化内容的前缀
	// （qa_engine_v3.py:212；注意持久化用 *星号*，与 tg 包显示前缀 _下划线_ 不同）。
	newSessionPersistPrefix = "🍃 *开始新的对话。*\n\n"
)

// errConsumerGone 消费端停止接收（ctx 取消/停止读取）。
// 对齐 Python 生成器被关闭时抛出的 GeneratorExit：BaseException 不进
// `except Exception`，因此不触发无工具降级、不产生 __ERROR__ 标记。
var errConsumerGone = errors.New("engine: 消费端已停止接收")

// ─────────────────────────────────────────────────────────────────────
// 系统提示词模板（qa_engine_v3.py:44-99 原文拷贝，逐字节一致；
// engine_prompt_test.go 会在 Python 源文件可用时做逐字节回归校验）
// ─────────────────────────────────────────────────────────────────────

// baseSystemTemplateRaw 即 BASE_SYSTEM_TEMPLATE。{persona_description} /
// {channel_context} / {command_context} / {current_time} 占位符由
// buildSystemPrompt 以 strings.NewReplacer 单趟替换，对齐 str.format 的
// "占位符同时替换、替换结果不再扫描"语义。{bt} 为反引号占位——Go 原始
// 字符串字面量无法直接包含反引号，包初始化时替换。
const baseSystemTemplateRaw = `{persona_description}

---

## 核心任务
你是一个通用智能助手：可以陪用户聊天、回答知识问题、协助编程与写作。
同时你接入了一个 Telegram 频道管理系统的数据（频道历史总结、消息记录、统计信息），
当问题涉及这些频道内容时，按需调用检索工具查询后再回答。

## 核心约束（必须严格遵守）
1. **自主判断**：是否检索由你根据问题性质决定。闲聊、通用知识、编程与创作等
   直接回答，不要调用任何工具；只有涉及频道历史、消息或统计时才检索。
2. **基于事实**：引用频道数据时严格基于工具返回的内容回答，不要编造信息
3. **上下文理解**：优先利用多轮对话历史理解代词指代（如"它"、"那个"、"这个"等）
4. **按需引用**：工具结果包含 post_links 时，在相关要点后保留帖子链接作为证据；
   与频道内容无关的回答无需附加任何来源信息
5. **Markdown规范**：
   - 粗体：使用 **文本** （两边各两个星号）
   - 斜体：使用 *文本* （两边各一个星号）
   - 代码：使用 {bt}代码{bt} （反引号）
   - **禁止使用 # 标题格式**
   - 列表：使用 - 或 • 开头
   - 链接：使用 [文本](URL) 格式
   - **禁止使用未配对的星号、下划线或反引号**

## 当前上下文
{channel_context}{command_context}
当前日期时间：{current_time}
`

// agentToolInstructions 即 AGENT_TOOL_INSTRUCTIONS（首部两个换行与尾部换行
// 均为模板原文；{max_iterations} 由 buildSystemPrompt 替换）。
const agentToolInstructions = `

## 检索工具
你可以调用以下工具查询频道数据（是否使用完全由你自主判断）：
- **semantic_search**: 语义搜索，适合模糊/概念性查询
- **keyword_search**: 关键词搜索，适合精确匹配特定术语
- **rerank_results**: 对搜索结果重排序（结果>5条时考虑使用）
- **list_channels**: 获取所有可查询频道链接
- **resolve_channel**: 将频道名、@username 或链接解析为标准频道链接
- **get_recent_summaries**: 获取最近总结，适合回答最新动态
- **get_channel_stats**: 获取频道统计信息
- **get_source_detail**: 展开之前搜索结果的完整来源内容

### 使用策略
1. 普通聊天、通用知识、编程与创作类问题：直接回答，不要调用任何工具
2. 涉及频道历史/消息/统计时才检索；用户指定频道名、@username 或链接时，
   先调用 resolve_channel，再把 channel_id 传给搜索工具
3. 可以同时调用多个搜索工具（如语义+关键词并行检索）
4. 检索结果充足则回答，不足则调整参数再次检索
5. 引用规则：只有消息类结果（source_type=message）带有后端验证过的
   post_url 帖子链接，在相关要点后引用该链接作为证据；总结类结果
   （source_type=summary）没有帖子链接。严禁根据 channel_id、message_id
   或其他 ID 自行拼接 t.me 链接
6. 最多进行{max_iterations}轮工具调用，之后必须基于已收集的信息给出最终回答
`

// baseSystemTemplate 最终模板：{bt} → 反引号（仅静态模板持有该占位）。
var baseSystemTemplate = strings.ReplaceAll(baseSystemTemplateRaw, "{bt}", "`")

// ─────────────────────────────────────────────────────────────────────
// 注入接口（消费者包内定义窄接口，由 main 装配；测试注入假实现）
// ─────────────────────────────────────────────────────────────────────

// ChatCompleter 流式补全窄接口：生产实现为 *llm.Client。
type ChatCompleter interface {
	StreamChat(ctx context.Context, req llm.ChatRequest) (*llm.Stream, error)
}

// ToolExecutor 单请求工具执行上下文（qa_engine_v3.py:300 每请求独立实例）。
// Python 版 execute 内部捕获一切异常并转为 {"error": ...} JSON 返回，
// 不向上抛；实现方应保持该语义，引擎把 error 视为异常路径触发降级。
type ToolExecutor interface {
	Execute(ctx context.Context, name string, args map[string]any) (string, error)
}

// Tools 工具层窄接口（生产实现为 internal/tools 包）：
// schema 原样透传 + 每请求独立执行器工厂。
type Tools interface {
	// ToolSchemasJSON 返回 TOOL_SCHEMAS 的原始 JSON 数组（透传给 LLM）。
	ToolSchemasJSON() json.RawMessage
	// NewExecutor 创建本请求独立的工具执行上下文（多用户互不污染）。
	NewExecutor() ToolExecutor
}

// PersonaProvider 人格描述来源（生产实现为 *config.Config 的 Persona）。
type PersonaProvider interface {
	Persona() string
}

// ChannelContextProvider 频道上下文来源（memory_manager.get_channel_context 的
// 无参形态；Python 版内部捕获异常返回空串，实现方对齐，引擎对 error 降级为空串）。
type ChannelContextProvider interface {
	GetChannelContext(ctx context.Context) (string, error)
}

// ─────────────────────────────────────────────────────────────────────
// 流协议块
// ─────────────────────────────────────────────────────────────────────

// ChunkKind 区分 Chunk 载荷类型。
type ChunkKind int

const (
	// ChunkText 文本增量（Text 有效）。
	ChunkText ChunkKind = iota
	// ChunkCtrl 控制标记（Ctrl = contracts.CtrlDone / contracts.CtrlNewSession）。
	ChunkCtrl
	// ChunkErr 致命错误（Err 为错误文案，不含 contracts.CtrlErrPrefix 前缀；
	// 消费端映射为 CtrlErrPrefix + Err）。
	ChunkErr
)

// Chunk 是 ProcessQueryStream 通道上的最小输出单元。
// 消费端（_stream_and_edit 等价物）据此还原 Python 的字符串协议：
// Text → 原文下发；Ctrl → 下发 Ctrl 字符串；Err → 下发 CtrlErrPrefix+Err。
type Chunk struct {
	Kind ChunkKind
	// Text ChunkText 时的文本增量。
	Text string
	// Ctrl ChunkCtrl 时的控制标记全文（逐字协议字符串）。
	Ctrl string
	// Err ChunkErr 时的错误文案（不含 "__ERROR__:" 前缀）。
	Err string
}

// TextChunk 构造文本增量块。
func TextChunk(text string) Chunk { return Chunk{Kind: ChunkText, Text: text} }

// CtrlChunk 构造控制标记块（取 contracts.CtrlDone / contracts.CtrlNewSession）。
func CtrlChunk(ctrl string) Chunk { return Chunk{Kind: ChunkCtrl, Ctrl: ctrl} }

// ErrChunk 构造错误块（msg 不含 "__ERROR__:" 前缀）。
func ErrChunk(msg string) Chunk { return Chunk{Kind: ChunkErr, Err: msg} }

// ─────────────────────────────────────────────────────────────────────
// 引擎装配
// ─────────────────────────────────────────────────────────────────────

// Deps 聚合引擎全部依赖（除 Logger 外均不可为 nil，由 main 启动期装配）。
type Deps struct {
	// Chat 流式补全客户端。
	Chat ChatCompleter
	// Tools 工具层。
	Tools Tools
	// Conversations 会话管理器（本包 NewConversations 构造）。
	Conversations *Conversations
	// Persona 人格描述来源。
	Persona PersonaProvider
	// Memory 频道上下文来源。
	Memory ChannelContextProvider
	// Logger 日志器；nil 时用 slog.Default()。
	Logger *slog.Logger
}

// Engine 问答引擎（对齐 QAEngineV3）。
type Engine struct {
	llm           ChatCompleter
	tools         Tools
	conversations *Conversations
	persona       PersonaProvider
	memory        ChannelContextProvider
	log           *slog.Logger
}

// New 构造引擎并记录与 Python 版一致的初始化日志（qa_engine_v3.py:121）。
func New(d Deps) *Engine {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	log.Info("问答引擎v4.0.0初始化完成（通用AI助手 + Agentic RAG + 真实多轮对话）")
	return &Engine{
		llm:           d.Chat,
		tools:         d.Tools,
		conversations: d.Conversations,
		persona:       d.Persona,
		memory:        d.Memory,
		log:           log,
	}
}

// ProcessQueryStream 流式处理用户查询（qa_engine_v3.py:143-221 的移植）。
//
// channelHint 为 /ask 显式指定的频道约束（空串等价 Python None）；
// 返回通道在处理结束后关闭。取消 ctx 等价消费端放弃生成器：
// 立即停止且不产生任何降级/错误块。
func (e *Engine) ProcessQueryStream(ctx context.Context, query string, userID int64, channelHint string) <-chan Chunk {
	ch := make(chan Chunk)
	go func() {
		defer close(ch)
		send := func(c Chunk) bool {
			// 取消优先判定：ctx 已取消时不再投递（等价生成器被关闭）
			if ctx.Err() != nil {
				return false
			}
			select {
			case ch <- c:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if err := e.run(ctx, send, query, userID, channelHint); err != nil {
			if errors.Is(err, errConsumerGone) {
				return // GeneratorExit 等价：静默终止
			}
			// 外层兜底（qa_engine_v3.py:219-221）
			e.log.Error(fmt.Sprintf("[stream] 处理查询失败: %T: %s", err, err))
			_ = send(ErrChunk(errQueryFailedText))
		}
	}()
	return ch
}

// run 执行完整查询管线（qa_engine_v3.py:156-217 逐段对齐）。
// 返回 errConsumerGone 表示消费端已停止；其他非 nil 错误交由外层兜底。
func (e *Engine) run(ctx context.Context, send func(Chunk) bool, query string, userID int64, channelHint string) error {
	e.log.Info(fmt.Sprintf("[stream] 处理查询: user_id=%d, query=%s", userID, query))

	// 1. 获取或创建会话（内部异常自降级）
	sessionID, isNew := e.conversations.GetOrCreateSession(userID)

	// 2. 先取历史（此时还不含当前消息），再保存当前用户消息，
	//    避免当前消息在历史中重复出现
	history := e.conversations.GetHistory(ctx, userID, sessionID)
	e.conversations.SaveMessage(ctx, userID, sessionID, "user", query, nil)

	if isNew {
		if !send(CtrlChunk(contracts.CtrlNewSession)) {
			return errConsumerGone
		}
	}

	// 3. Agentic 处理：模型自主决定是否检索
	fullAnswer := ""
	if err := e.agenticStream(ctx, send, &fullAnswer, query, history, channelHint); err != nil {
		if errors.Is(err, errConsumerGone) {
			return err
		}
		// 降级策略（qa_engine_v3.py:185-203）：一次无工具的直连对话；
		// 已有部分输出则保留内容并附加可观测提示。
		e.log.Error(fmt.Sprintf("[stream] Agentic 处理异常，降级为无工具直连回答: %T: %s", err, err))
		if fullAnswer == "" {
			if err := e.plainChatStream(ctx, send, &fullAnswer, query, history); err != nil {
				return err // 直连仍失败 → 外层 __ERROR__（Python except 内二次抛出）
			}
		} else {
			fullAnswer += interruptedNoticeText
			if !send(TextChunk(interruptedNoticeText)) {
				return errConsumerGone
			}
		}
	}

	// 空回答兜底（可观测，避免占位消息永远停留在"思考中"）
	if strings.TrimSpace(fullAnswer) == "" {
		fullAnswer = emptyAnswerText
		if !send(TextChunk(fullAnswer)) {
			return errConsumerGone
		}
	}

	// 4. 保存完整回答到对话历史（新会话时前缀拼入持久化内容，
	//    qa_engine_v3.py:211-215；保存失败仅记日志不中断）
	if isNew {
		fullAnswer = newSessionPersistPrefix + fullAnswer
	}
	e.conversations.SaveMessage(ctx, userID, sessionID, "assistant", fullAnswer, nil)

	if !send(CtrlChunk(contracts.CtrlDone)) {
		return errConsumerGone
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────
// 系统提示词构建（qa_engine_v3.py:227-260）
// ─────────────────────────────────────────────────────────────────────

// buildSystemPrompt 构建系统提示词。
//
// channelHint 为 /ask 指定的频道约束（空串 = 无），作为可信系统级上下文注入，
// 绝不改写用户消息本身；includeTools 为 false 时（降级直连）不附加工具说明。
func (e *Engine) buildSystemPrompt(ctx context.Context, channelHint string, includeTools bool) string {
	channelContext := ""
	if s, err := e.memory.GetChannelContext(ctx); err != nil {
		// Python 版 memory_manager.get_channel_context 内部捕获异常返回空串；
		// 引擎对接口错误同样降级为空串并保留原日志文案（memory_manager.py:179）。
		e.log.Error(fmt.Sprintf("获取频道上下文失败: %T: %s", err, err))
	} else {
		channelContext = s
	}

	commandContext := ""
	if channelHint != "" {
		commandContext = "\n【命令上下文】\n用户通过 /ask 命令显式限定了查询频道：" + channelHint +
			"。涉及频道内容时请先调用 resolve_channel 解析该频道，" +
			"再以解析出的 channel_id 限定检索。\n"
	}

	// NewReplacer 单趟替换：替换产物不再参与扫描，对齐 str.format 语义。
	r := strings.NewReplacer(
		"{persona_description}", e.persona.Persona(),
		"{channel_context}", channelContext,
		"{command_context}", commandContext,
		"{current_time}", time.Now().UTC().Format("2006-01-02 15:04")+" (UTC)",
	)
	systemPrompt := r.Replace(baseSystemTemplate)

	if includeTools {
		systemPrompt += strings.ReplaceAll(agentToolInstructions,
			"{max_iterations}", strconv.Itoa(AgentMaxIterations))
	}
	return systemPrompt
}
