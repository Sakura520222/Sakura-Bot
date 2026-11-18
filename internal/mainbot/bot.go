// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mainbot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/botcmd"
	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/embedder"
	"github.com/Sakura520222/Sakura-Bot/internal/forwarding"
	"github.com/Sakura520222/Sakura-Bot/internal/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/logging"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/poll"
	"github.com/Sakura520222/Sakura-Bot/internal/process"
	"github.com/Sakura520222/Sakura-Bot/internal/rag"
	"github.com/Sakura520222/Sakura-Bot/internal/scheduler"
	"github.com/Sakura520222/Sakura-Bot/internal/submission"
	"github.com/Sakura520222/Sakura-Bot/internal/summary"
	"github.com/Sakura520222/Sakura-Bot/internal/tg"
	"github.com/Sakura520222/Sakura-Bot/internal/vectors"
	"github.com/Sakura520222/Sakura-Bot/internal/version"
	"github.com/Sakura520222/Sakura-Bot/internal/webapi"
	"github.com/Sakura520222/Sakura-Bot/internal/welcome"
	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
	"golang.org/x/sync/errgroup"
)

func runBot(
	ctx context.Context, stop context.CancelFunc, restart func(string), releaseRestart func(string, string, string),
	cfg *config.Config, logger *slog.Logger,
	botOpts mtproto.BotOptions, userOpts mtproto.UserBotOptions,
) error {
	startedAt := time.Now()
	vectorLock, err := lockVectorDirectory(ctx, cfg.VectorDBPath())
	if err != nil {
		return err
	}
	defer vectorLock.Close()
	if err := checkLegacyVectorIndex(cfg.VectorDBPath()); err != nil {
		return err
	}
	manager := db.NewMain(cfg)
	if err := manager.OpenMain(ctx); err != nil {
		return fmt.Errorf("连接 MySQL: %w", err)
	}
	defer manager.Close()
	release, err := manager.HoldMainBotLease(ctx, stop)
	if err != nil {
		return err
	}
	defer release()
	if err := manager.InitMainDatabase(ctx); err != nil {
		return fmt.Errorf("初始化 MySQL: %w", err)
	}

	botPeers, err := mtproto.NewPeerCache(botOpts.SessionPath)
	if err != nil {
		return fmt.Errorf("读取主 Bot 会话实体: %w", err)
	}
	botOpts.PeerCache = botPeers
	botOpts.Dispatcher = mtproto.NewDispatcher(botPeers, logger)
	bot := mtproto.NewClient(mtproto.WithDispatcher(botOpts.Dispatcher), mtproto.WithPeerCache(botPeers), mtproto.WithLogger(logger))
	botClient := func() mtproto.Messenger { return bot }

	if userOpts.Enabled {
		peers, err := mtproto.NewPeerCache(userOpts.SessionPath)
		if err != nil {
			return fmt.Errorf("读取 UserBot 会话实体: %w", err)
		}
		userOpts.PeerCache = peers
		userOpts.Dispatcher = mtproto.NewDispatcher(peers, logger)
	}
	userOpts.SessionOnly = true
	user := mtproto.NewUserBotManager(userOpts)
	defer user.Stop()
	userClient := func() mtproto.Messenger {
		if c := user.Client(); c != nil && c.IsConnected() {
			return c
		}
		return nil
	}
	historyClient := func() mtproto.Messenger {
		if c := userClient(); c != nil {
			return c
		}
		if !userOpts.Enabled || userOpts.FallbackToBot {
			return bot
		}
		return nil
	}

	retrieval := vectors.NewReadWrite(cfg.VectorDBPath(), cfg.EmbeddingDimension())
	defer retrieval.Close()
	embedding := embedder.NewGenerator(cfg.Env, embedder.WithLogger(logger))
	embed := func(ctx context.Context, texts []string) ([][]float32, error) {
		if !embedding.IsAvailable() {
			return nil, errors.New("Embedding 服务未配置")
		}
		result := embedding.BatchGenerate(ctx, texts)
		return result, ctx.Err()
	}
	vectorIndex := indexedVectors{Store: retrieval, embed: embed}
	newLLM := func(purpose string) *llm.Client {
		return llm.NewClient(llm.AIConfigFunc(func() llm.AIConfig { return aiConfig(cfg, purpose) }), llm.WithLogger(logger))
	}

	store := wiki.NewStore(manager.DB)
	if err := store.Initialize(ctx); err != nil {
		return fmt.Errorf("初始化 Wiki: %w", err)
	}
	var images *media.ImagePipeline
	wikiRuntime := wiki.NewRuntime(store, cfg, func() (mtproto.Messenger, mtproto.Messenger) {
		return userClient(), bot
	}, wiki.WithImageEnqueuer(func(ctx context.Context, channel *wiki.ChannelRecord, msg *mtproto.IncomingMessage) {
		if !msg.Media.IsStaticImage() {
			return
		}
		_, err := images.Capture(ctx, media.RemoteImageMessage{
			ChannelID: channel.ID, ChannelTitle: channel.Title, ChannelUsername: channel.Username,
			MessageID: int64(msg.ID), Caption: msg.Text, Date: msg.Date,
			MediaID: msg.Media.MediaID, FileSize: msg.Media.FileSize, RawMedia: msg.Media.Raw,
		})
		if err != nil && ctx.Err() == nil {
			logger.Warn("登记 Wiki 频道图片失败", "message_id", msg.ID, "error", err)
		}
	}))
	defer wikiRuntime.Avatar.Wait()

	ragHandler := rag.NewHandler(rag.Deps{
		Config: cfg, VectorStore: retrieval, EmbedFn: embed, ImageText: manager.GetRecognizedImageText, Logger: logger,
	})
	images = media.NewImagePipeline(media.PipelineOptions{
		Store: manager, RootDir: filepath.Join(cfg.DataDirectory(), "images"),
		VisionProvider: func() media.VisionSettings { return visionSettings(cfg) },
		LLMClient:      newLLM("vision"), Publisher: ragHandler, WikiHook: wikiRuntime.Collector, Logger: logger,
	})
	ragHandler.SetImagePipeline(images)
	ragHandler.Start(ctx)
	defer ragHandler.Stop()
	if err := images.Start(ctx, imageSource{client: func() *mtproto.Client {
		c, _ := historyClient().(*mtproto.Client)
		return c
	}}); err != nil {
		return fmt.Errorf("启动图片工作池: %w", err)
	}
	defer images.Stop()

	timeManager := summary.NewTimeManager(filepath.Join(cfg.DataDirectory(), ".last_summary_time.json"))
	polls := poll.NewService(poll.ServiceDeps{
		Config: cfg, Completer: newLLM("poll"), Pipeline: images, SummaryUpdater: timeManager, SummaryRepo: manager,
		RegenStore: poll.NewRegenStore(filepath.Join(cfg.DataDirectory(), ".poll_regenerations.json")), Logger: logger,
	})
	autoPoll := poll.NewAutoPollHandler(poll.AutoPollDeps{
		Config: cfg, BotClient: botClient, Completer: newLLM("poll"), Pipeline: images, Logger: logger,
	})
	autoPoll.Start(ctx)
	defer autoPoll.Stop()
	welcomeHandler := welcome.NewHandler(welcome.Deps{
		Config: cfg, Repo: manager, BotClient: botClient, AutoPoll: autoPoll, Logger: logger,
	})
	welcomeHandler.Start(ctx)
	defer welcomeHandler.Stop()

	summaryLLM := newLLM("summary")
	directBot := tg.NewDirectBotAPI()
	summaries := summary.NewService(summary.ServiceDeps{
		Config: cfg, BotClient: botClient, HistoryClient: historyClient, DB: manager,
		Completer:   func() (summary.Completer, string) { return summaryLLM, cfg.EffectiveAIConfig("summary").Model },
		VectorStore: vectorIndex, ImagePipeline: images, TimeManager: timeManager, PollSender: polls,
		PushHandler: summary.NewPushHandler(manager, directBot, cfg.QABotToken, logger), Logger: logger,
	})
	// Forwarding uses the main Bot for both sides when UserBot is unavailable.
	forward := forwarding.NewService(forwarding.ServiceDeps{
		Config: cfg, Repo: manager, SendingClient: botClient,
		MonitoringClient: func() mtproto.Messenger {
			if c := userClient(); c != nil {
				return c
			}
			return bot
		},
		CacheDir: filepath.Join(cfg.DataDirectory(), "cache", "forwarding"), Logger: logger,
	})
	defer forward.Stop()
	requests := submission.NewRequestService(submission.RequestDeps{
		Config: cfg, Repo: manager, BotClient: botClient, Generator: summaries, Logger: logger,
	})
	submissionLLM := newLLM("summary")
	reviews := submission.NewReviewService(submission.ReviewDeps{
		Config: cfg, Repo: manager, BotClient: botClient,
		Completer: func() (submission.Completer, string) { return submissionLLM, cfg.EffectiveAIConfig("summary").Model },
		Logger:    logger,
	})

	guardRepo := guarddb.New(manager.DB)
	if err := guardRepo.Initialize(ctx); err != nil {
		return fmt.Errorf("初始化 Sakura Guard: %w", err)
	}
	guardService := guard.NewService(guardRepo, cfg, guard.NewMTProtoTelegramOps(bot, directBot), newLLM("guard"), logger,
		guard.WithVisionCompleter(newLLM("guard_vision")),
	)
	defer guardService.Stop()
	guardService.BindDispatcher(botOpts.Dispatcher)
	guardService.AttachUserBotObserver(userOpts.Dispatcher)

	loc, err := time.LoadLocation(cfg.SchedulerTimezone())
	if err != nil {
		return fmt.Errorf("解析调度时区: %w", err)
	}
	tasks := scheduler.New(loc, logger)
	assistant := process.NewQAManager(cfg, ".", logger)
	defer assistant.Stop(5 * time.Second)
	defer func() {
		grace, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tasks.Stop(grace); err != nil {
			logger.Warn("等待调度器停止", "error", err)
		}
	}()
	registerJobs(tasks, cfg, manager, summaries, polls, requests, reviews, assistant, logger)
	reloadJobs := cfg.Subscribe(0, func(_ context.Context, ev config.ConfigChangedEvent) {
		i18n.SetLanguage(cfg.Language())
		if level, ok := ev.Config["log_level"].(string); ok {
			logging.SetDefaultLevel(level)
		}
		if err := guardService.Reload(ev.Config); err != nil {
			logger.Warn("重载 Sakura Guard 配置失败", "error", err)
		}
		if ev.HasChanged("channels") || ev.HasChanged("summary_schedules") {
			registerSummaryJobs(tasks, cfg, summaries, logger)
		}
		if ev.HasChanged("forwarding") || ev.HasChanged("forwarding_rules") {
			tasks.Wake("join_forwarding_sources", time.Second)
		}
	})
	defer reloadJobs()

	router := botcmd.NewRouter(botcmd.Deps{
		Config: cfg, BotClient: botClient, UserBot: func() botcmd.UserBotController { return user },
		SummaryService: summaries, TimeManager: timeManager, Scheduler: schedulerControl{tasks},
		QAManager: assistantControl{assistant}, DB: manager, VectorClearer: vectorIndex, GuardHandler: guardService,
		TriggerRestart: restart, TriggerReleaseRestart: releaseRestart, TriggerShutdown: stop, WorkDir: ".", Logger: logger,
	})

	group, runCtx := errgroup.WithContext(ctx)
	events := newEventQueue(runCtx, group, logger)
	bindMessages(events, botOpts.Dispatcher, userOpts.Dispatcher, userClient, router, ragHandler, forward, polls, welcomeHandler, wikiRuntime)
	bindCallbacks(events, botOpts.Dispatcher, bot, router, welcomeHandler, polls, requests, reviews, wikiRuntime)
	group.Go(func() error {
		wikiRuntime.Run(runCtx)
		return nil
	})
	group.Go(func() error {
		cfg.NewFileWatcher().Run(runCtx, time.Second)
		return nil
	})
	if userOpts.Enabled {
		tasks.AddInterval("join_forwarding_sources", "forwarding", 15*time.Minute, time.Second, false, func(ctx context.Context) {
			if c := user.Client(); c == nil || !c.IsConnected() {
				return
			}
			var sources []string
			for _, rule := range cfg.ForwardingConfig().Rules {
				sources = append(sources, rule.SourceChannel)
			}
			if len(sources) > 0 {
				result := user.JoinAllForwardingChannels(ctx, sources)
				if !result.Success {
					logger.Warn("未能加入所有转发源频道", "failed_count", result.FailedCount)
				}
			}
		})
		group.Go(func() error {
			maintainUserBot(runCtx, user, tasks, logger)
			return nil
		})
	}
	if cfg.WebUIEnabled() {
		server := webapi.New(webapi.Deps{
			Config: cfg, DB: manager, BotClient: bot, UserBot: user, CommandRouter: router,
			Scheduler: tasks, SummaryService: summaries, TimeManager: timeManager, PollRegenerations: polls.RegenStore(),
			Vector: retrieval, Embedding: embedding, Images: images, HistoryClient: historyClient,
			Guard: guardService, QAManager: assistant, TriggerRestart: restart, StartedAt: startedAt, DirectBotAPI: directBot,
			Wiki:   wikiRuntime,
			Logger: logger, StaticDir: webStaticDir(),
		})
		group.Go(func() error { return serveWeb(runCtx, cfg, logger, server) })
	}

	ready := make(chan struct{}, 1)
	botOpts.OnReady = func(context.Context, *mtproto.ProfileSummary) {
		select {
		case ready <- struct{}{}:
		default:
		}
	}
	group.Go(func() error {
		if err := bot.StartBot(runCtx, botOpts); err != nil && runCtx.Err() == nil {
			return fmt.Errorf("主 Bot MTProto 连接: %w", err)
		}
		if runCtx.Err() == nil {
			return errors.New("主 Bot MTProto 连接已停止")
		}
		return nil
	})
	group.Go(func() error {
		select {
		case <-runCtx.Done():
			return nil
		case <-ready:
		}
		if err := guardService.Start(runCtx); err != nil {
			return fmt.Errorf("启动 Sakura Guard: %w", err)
		}
		tasks.Start(runCtx)
		if cfg.QABotEnabled() && cfg.QABotToken() != "" {
			if result := assistant.Start(); !result.Success {
				logger.Warn("启动助手失败", "message", result.Message)
			}
		}
		cmdCtx, cancel := context.WithTimeout(runCtx, 15*time.Second)
		defer cancel()
		if err := bot.SetBotCommands(cmdCtx, "", botcmd.AllBotCommands()); err != nil && runCtx.Err() == nil {
			logger.Warn("注册主 Bot 命令菜单失败", "error", err)
		}
		notifyStartup(cmdCtx, bot, cfg, logger)
		return nil
	})
	return group.Wait()
}

func notifyStartup(ctx context.Context, bot mtproto.Messenger, cfg *config.Config, logger *slog.Logger) {
	message := fmt.Sprintf("🤖 **Sakura-Bot v%s 已启动**\n\n频道总结、定时任务和管理服务已就绪。\n发送 /help 查看命令列表。", version.Version)
	for _, id := range cfg.AdminIDs() {
		if ctx.Err() != nil {
			return
		}
		_, err := bot.SendMessage(ctx, id, message, mtproto.SendOptions{ParseMode: "markdown", NoWebpage: true})
		if err != nil && ctx.Err() == nil {
			logger.Warn("发送主 Bot 启动通知失败", "admin_id", id, "error", err)
		}
	}
}

func maintainUserBot(ctx context.Context, user *mtproto.UserBotManager, tasks *scheduler.Scheduler, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if c := user.Client(); c == nil || !c.IsConnected() {
			authCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := user.Start(authCtx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					logger.Warn("UserBot 暂不可用", "error", err)
				}
			} else {
				tasks.Wake("join_forwarding_sources", time.Second)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
