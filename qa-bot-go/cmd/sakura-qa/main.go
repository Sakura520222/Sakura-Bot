// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// sakura-qa：qa_bot.py（Python QA 子进程）的单二进制 Go 替代。
//
// 启动序列逐项对齐 qa_bot.py 与设计 cont-2 §11：
//
//  1. 配置快照（env + data/.env override + config.json 首读，import 期语义）；
//  2. 组件日志（stdout + logs/components/qa-bot.log 双写）；
//  3. 语言设置与 QA_MENU_APPEAL 文案求值（core/config.py:438 的 import 期语义）；
//  4. QA_BOT_TOKEN 缺失 → error + exit 1；QA_BOT_ENABLED 非 true → exit 0
//     （qa_bot.py:96-103）；
//  5. GOMEMLIMIT 默认 96MiB（env 可调）经 debug.SetMemoryLimit（README 硬规则 5）；
//  6. 单例组（quota → 会话 → 引擎 → 用户系统，qa_bot.py:109-117 构造顺序）；
//  7. MySQL 池 + 业务 DDL + guard schema（post_init，qa_bot.py:943-947；
//     失败非零退出）；
//  8. set_my_commands 16 条（qa_bot.py:950-967；失败仅记日志不退出，R18）；
//  9. 分发链注册 + 两个 ticker goroutine（申诉 5s/first 5s；通知 30s/first 10s）；
//  10. b.Start(ctx) 长轮询。
//
// 信号：SIGTERM/SIGINT → cancel → 停轮询与 ticker → 等在途 handler ≤3s →
// 关 MySQL → exit 0，全程 ≤5s（process_manager.py:103-115 硬约束）。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/handlers"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/logging"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/qauser"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/vectors"
)

// shutdownGrace 等待在途 handler 的上限（设计 §11：全程 ≤5s 预算内的 3s）。
const shutdownGrace = 3 * time.Second

// defaultMemoryLimit GOMEMLIMIT 未配置时的默认值（96MiB，README 硬规则 5）。
const defaultMemoryLimit = 96 << 20

func main() {
	os.Exit(run())
}

// run 执行启动序列并返回进程退出码（对齐 qa_bot.py main() 的 exit 语义）。
func run() int {
	// 信号：SIGTERM（主进程 stop）/ SIGINT（Ctrl-C）→ ctx 取消。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// ── 1. 配置快照（Python import 期：core/config.py 加载 .env + config.json）。
	cfg := config.Default()
	if err := cfg.Init(); err != nil {
		// 日志系统尚未就绪，走默认 stderr logger；失败即启动失败
		//（对齐 Python import 崩溃 → 健康拉起重试）。
		slog.Error(fmt.Sprintf("配置初始化失败: %v", err))
		return 1
	}

	// ── 2. 组件日志（qa_bot.py:62-89 _setup_qa_logging）。
	logger, _, err := logging.Setup(logging.SetupOptionsFromEnv(cfg.Env))
	if err != nil {
		slog.Error(fmt.Sprintf("日志系统初始化失败: %v", err))
		return 1
	}
	slog.SetDefault(logger)
	defer func() {
		// 进程退出前落盘/关闭组件日志文件（Handler.Close 幂等）。
		if h, ok := logger.Handler().(*logging.Handler); ok {
			_ = h.Close()
		}
	}()

	// ── 3. 语言与申诉菜单文案（core/config.py:438 的 import 期求值语义；
	// QA_MENU_APPEAL = t("appeal.menu") 每进程只求值一次）。
	i18n.SetLanguage(cfg.Language())
	tg.MenuAppeal = i18n.T("appeal.menu", nil)

	// ── 4. 令牌与开关检查（qa_bot.py:96-103）。
	token := cfg.QABotToken()
	if token == "" {
		logger.Error("未设置QA_BOT_TOKEN环境变量")
		logger.Error("请在.env文件中配置: QA_BOT_TOKEN=your_bot_token")
		return 1
	}
	if !cfg.QABotEnabled() {
		logger.Warn("问答Bot未启用 (QA_BOT_ENABLED=False)")
		return 0
	}

	// ── 5. Go 运行时内存上限（README 硬规则 5；Python 无此步骤）。
	applyMemoryLimit(cfg, logger)

	// ── 6. 单例组（qa_bot.py:109-117 构造顺序：quota → 引擎 → 会话 → 用户系统；
	// Go 侧会话管理器先于引擎构造——引擎 Deps 需要其指针，行为等价）。
	dbMgr := db.New(cfg)
	quota, qErr := newQuotaManager(cfg, dbMgr, logger)
	if qErr != nil {
		// __init__ 期限额刷新失败 = Python ValidationError 崩溃语义。
		logger.Error(fmt.Sprintf("Bot运行出错: %v", qErr))
		return 1
	}

	// 向量存储（QA 侧纯读者；构造不触盘，首次 Search 惰性加载）。
	vecStore := vectors.New(cfg.Env.Get(config.KeyVectorDBPath))
	defer func() { _ = vecStore.Close() }()

	conversations := engine.NewConversations(dbMgr)
	qaEngine := assembleEngine(cfg, dbMgr, conversations, vecStore, logger)
	users := qauser.New(dbMgr)

	logger.Info("问答Bot初始化完成（v3.0.0向量搜索版本 + 多轮对话支持 + 用户系统）")

	// ── 7. MySQL 池 + 业务 DDL + guard schema（post_init，qa_bot.py:943-947；
	// 失败 DatabaseError 上抛 → 进程启动失败非零退出）。
	if err := dbMgr.Open(ctx); err != nil {
		logger.Error(fmt.Sprintf("MySQL初始化失败: %v", err))
		return 1
	}
	defer func() { _ = dbMgr.Close() }()
	if err := dbMgr.InitDatabase(ctx); err != nil {
		logger.Error(fmt.Sprintf("数据库初始化失败: %v", err))
		return 1
	}
	guardRepo := guard.New(dbMgr.DB)
	if err := guardRepo.Initialize(ctx); err != nil {
		logger.Error(fmt.Sprintf("Guard schema 初始化失败: %v", err))
		return 1
	}

	// ── 8/9. Bot 构造 + 分发链注册。
	disp := tg.NewDispatcher(slogBridge{logger})
	qaBot, err := bot.New(
		token,
		// 全 update 类型（对齐 run_polling(allowed_updates=Update.ALL_TYPES)）。
		bot.WithAllowedUpdates(bot.AllowedUpdates(allUpdateTypes)),
		// README 硬规则 1：单 worker + 同步 handler = 串行分发（对齐 PTB 默认，
		// 对抗评审 B-1；缺 WithNotAsyncHandlers 时库对每条 update 起 goroutine）。
		bot.WithWorkers(1),
		bot.WithNotAsyncHandlers(),
		// 长轮询错误经 slog（库默认 log.Printf 与组件日志格式不一致）。
		bot.WithErrorsHandler(func(err error) {
			logger.Error(fmt.Sprintf("TG 长轮询错误: %v", err))
		}),
		bot.WithDefaultHandler(func(ctx context.Context, _ *bot.Bot, upd *models.Update) {
			disp.HandleUpdate(ctx, upd)
		}),
	)
	if err != nil {
		logger.Error(fmt.Sprintf("Bot 初始化失败: %v", err))
		return 1
	}
	tgClient := tg.NewClient(qaBot, slogBridge{logger}, tg.DefaultAPITimeout)

	// 申诉层（组 -1）。
	appealHandler := handlers.NewAppealHandler(guardRepo, tgClient, qaBot, slogBridge{logger})
	appealHandler.IsAdmin = cfg.IsAdmin

	// 投稿 FSM（ConversationHandler 移植）。
	submissionHandler := handlers.NewSubmissionHandler(handlers.Deps{
		API:       handlers.TGClientAdapter{Client: tgClient},
		Store:     &submissionStore{db: dbMgr},
		Registrar: dbMgr,
		Channels:  cfg.Channels,
		Log:       logger,
	})
	// Python submission_active 回调：申诉层据此拦截进入投稿会话的用户
	//（qa_bot.py:979-981）。
	appealHandler.SubmissionActive = submissionHandler.GetUserState

	// 组 0：命令 / 菜单 / 游离按钮 / 普通文本 AI 问答。
	commands := handlers.NewCommands(handlers.CommandsDeps{
		TG:          tgClient,
		Users:       users,
		Quota:       quota,
		Sessions:    conversations,
		Engine:      qaEngine,
		Persona:     cfg,
		Submissions: submissionHandler,
		Log:         slogBridge{logger},
	})

	// 装配分发链（字段在 Start 前赋值；WithDefaultHandler 已引用 disp）。
	disp.Appeal = appealHandler
	disp.Submission = submissionHandler
	disp.Commands = commands

	// set_my_commands 16 条（qa_bot.py:950-967；失败仅记日志不退出，R18）。
	registerBotCommands(ctx, qaBot, logger)

	// 两个周期 job（PTB job_queue 对齐：申诉 interval=5, first=5；通知
	// interval=30, first=10）。独立 goroutine，ctx 取消即退出（README 硬规则 1）。
	go appealHandler.RunAppealDeliver(ctx)
	go handlers.NewPushJob(dbMgr, tgClient, token, slogBridge{logger}).Run(ctx)

	// ── 10. 长轮询主循环（阻塞至 ctx 取消；workers=1 保证在途 handler
	// 完成或随 ctx 中止后 Start 才返回）。
	logger.Info("问答Bot已启动，等待消息...")
	botDone := make(chan struct{})
	go func() {
		qaBot.Start(ctx)
		close(botDone)
	}()
	<-botDone

	// ── 关停序列（设计 §11）：等在途 handler ≤3s → 关 MySQL（defer）→ exit 0。
	if ctx.Err() != nil {
		logger.Info("收到中断信号，正在关闭...")
	}
	waitInFlight(disp, logger)
	logger.Info("问答Bot已关闭")
	return 0
}

// allUpdateTypes 对齐 PTB Update.ALL_TYPES 的全量 update 类型
// （run_polling(allowed_updates=Update.ALL_TYPES)，qa_bot.py:1076）。
var allUpdateTypes = []string{
	"message", "edited_message", "channel_post", "edited_channel_post",
	"message_reaction", "message_reaction_count", "inline_query",
	"chosen_inline_result", "callback_query", "shipping_query",
	"pre_checkout_query", "poll", "poll_answer", "my_chat_member",
	"chat_member", "chat_join_request", "chat_boost", "removed_chat_boost",
	"business_connection", "business_message", "edited_business_message",
	"deleted_business_messages",
}

// waitInFlight 等待在途 handler 结束，上限 shutdownGrace；超时记警告
// （进程即将退出，残留 handler 随之终止）。
func waitInFlight(disp *tg.Dispatcher, logger *slog.Logger) {
	deadline := time.Now().Add(shutdownGrace)
	for disp.InFlight() > 0 {
		if !time.Now().Before(deadline) {
			logger.Warn(fmt.Sprintf("关停等待超时，仍有 %d 个在途 handler", disp.InFlight()))
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// applyMemoryLimit 设置 Go 运行时软内存上限（README 硬规则 5）：
// GOMEMLIMIT 缺省 96MiB；配置非法时告警回退默认。
func applyMemoryLimit(cfg *config.Config, logger *slog.Logger) {
	raw := cfg.Env.Get(config.KeyGoMemLimit)
	if raw == "" {
		debug.SetMemoryLimit(defaultMemoryLimit)
		return
	}
	limit, err := parseMemLimit(raw)
	if err != nil {
		logger.Warn(fmt.Sprintf("GOMEMLIMIT 配置非法，回退默认 96MiB: %q (%v)", raw, err))
		debug.SetMemoryLimit(defaultMemoryLimit)
		return
	}
	debug.SetMemoryLimit(limit)
}

// registerBotCommands 注册命令菜单（qa_bot.py:950-967 清单与文案逐字对齐；
// appeal 三条走 i18n）。失败仅记日志不退出（qa_bot.py:969-973，R18）。
// 调用带 5s 超时（对齐 PTB 默认请求超时，避免启动期挂死）。
func registerBotCommands(ctx context.Context, b *bot.Bot, logger *slog.Logger) {
	cmds := botCommandsList()
	callCtx, cancel := context.WithTimeout(ctx, tg.DefaultAPITimeout)
	defer cancel()
	if _, err := b.SetMyCommands(callCtx, &bot.SetMyCommandsParams{Commands: cmds}); err != nil {
		logger.Error(fmt.Sprintf("注册命令菜单失败: %T: %v", err, err))
		return
	}
	logger.Info(fmt.Sprintf("问答Bot命令菜单注册完成，共 %d 个命令", len(cmds)))
}

// botCommandsList 构造 16 条命令清单（qa_bot.py:951-967 顺序与文案逐字一致；
// appeal 三条描述经 i18n 取 appeal.command*）。
func botCommandsList() []models.BotCommand {
	return []models.BotCommand{
		{Command: "start", Description: "查看欢迎信息"},
		{Command: "ask", Description: "限定指定频道查询"},
		{Command: "status", Description: "查看使用配额和会话状态"},
		{Command: "clear", Description: "清除对话记忆"},
		{Command: "help", Description: "显示使用帮助"},
		{Command: "listchannels", Description: "列出可订阅的频道"},
		{Command: "mysubscriptions", Description: "查看我的订阅列表"},
		{Command: "subscribe", Description: "订阅频道总结推送"},
		{Command: "unsubscribe", Description: "取消频道订阅"},
		{Command: "request_summary", Description: "请求生成频道总结"},
		{Command: "submit", Description: "投稿"},
		{Command: "cancel_submit", Description: "取消投稿"},
		{Command: "view_persona", Description: "查看当前助手人格设定"},
		{Command: "appeal", Description: i18n.T("appeal.command", nil)},
		{Command: "appeals", Description: i18n.T("appeal.command_history", nil)},
		{Command: "cancel_appeal", Description: i18n.T("appeal.command_cancel", nil)},
	}
}
