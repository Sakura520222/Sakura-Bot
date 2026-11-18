// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package guard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type protectionState int

const (
	protectionUnprotected protectionState = iota
	protectionProtected
	protectionUnknown
)

// Service 是主进程 Sakura Guard 统一协调与运行时入口（对齐 core/moderation/moderator.py:Moderator）。
type Service struct {
	repo   *guarddb.Repository
	appCfg *config.Config
	ops    TelegramOps
	logger *slog.Logger

	cfgPtr atomic.Pointer[GuardConfig]

	directory      *GroupDirectory
	chatCleanup    *ChatCleanup
	historyCleanup *ChatCleanup
	deletedCleanup *DeletedUserCleanup
	verification   *VerificationManager
	observation    *ObservationEngine
	reports        *ReportHandler
	reviews        *ReviewHandler
	notifier       *Notifier
	banHistory     *BanHistoryCleanup
	learning       *AILearning
	aiReports      *AIReportWorker
	appeals        *AppealWorkflow

	mu               sync.Mutex
	running          bool
	observerAttached bool
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	unsubs           []func()
	observerUnsubs   []func()
}

type ServiceOption func(*Service)

// WithVisionCompleter injects the shared client configured for Guard OCR.
func WithVisionCompleter(completer Completer) ServiceOption {
	return func(s *Service) {
		s.observation.visionCompleter = completer
	}
}

// NewService 创建并初始化 Sakura Guard 完整子系统。
func NewService(
	repo *guarddb.Repository,
	appCfg *config.Config,
	ops TelegramOps,
	completer Completer,
	logger *slog.Logger,
	options ...ServiceOption,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{
		repo:   repo,
		appCfg: appCfg,
		ops:    ops,
		logger: logger,
	}
	defCfg := DefaultGuardConfig()
	if appCfg != nil {
		if parsed, err := ParseGuardConfig(appCfg.RawSnapshot()); err == nil {
			defCfg = parsed
		}
	}
	s.cfgPtr.Store(&defCfg)

	s.directory = NewGroupDirectory(s, logger)
	s.chatCleanup = NewChatCleanup(s.directory, "leave")
	s.historyCleanup = NewChatCleanup(s.directory, "history")
	s.deletedCleanup = NewDeletedUserCleanup(s, logger)
	s.verification = NewVerificationManager(s)
	s.observation = NewObservationEngine(s, logger)
	s.observation.textCompleter = completer
	if appCfg == nil {
		s.observation.visionCompleter = completer
	}
	s.reports = NewReportHandler(s)
	s.reviews = NewReviewHandler(s)
	s.notifier = NewNotifier(s)
	s.banHistory = NewBanHistoryCleanup(s)
	s.learning = NewAILearning(repo, appCfg, s.Config, completer, logger)
	s.aiReports = NewAIReportWorker(s, completer, logger)
	s.appeals = NewAppealWorkflow(s, logger)
	for _, option := range options {
		option(s)
	}

	return s
}

// ────────────────────────────── 子组件与配置访问器（供 WebUI 与主进程调用） ──────────────────────────────

func (s *Service) Repository() *guarddb.Repository     { return s.repo }
func (s *Service) Directory() *GroupDirectory          { return s.directory }
func (s *Service) ChatCleanup() *ChatCleanup           { return s.chatCleanup }
func (s *Service) HistoryCleanup() *ChatCleanup        { return s.historyCleanup }
func (s *Service) DeletedCleanup() *DeletedUserCleanup { return s.deletedCleanup }
func (s *Service) Verification() *VerificationManager  { return s.verification }
func (s *Service) Reviews() *ReviewHandler             { return s.reviews }
func (s *Service) Notifier() *Notifier                 { return s.notifier }
func (s *Service) BanHistory() *BanHistoryCleanup      { return s.banHistory }
func (s *Service) Learning() *AILearning               { return s.learning }
func (s *Service) AIReports() *AIReportWorker          { return s.aiReports }
func (s *Service) Appeals() *AppealWorkflow            { return s.appeals }

// IsObserverActive 返回 UserBot 群内普通 Bot 发言补充观察器是否在线。
func (s *Service) IsObserverActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observerAttached
}

// BotID 返回主 Bot 自身数字 ID。
func (s *Service) BotID() int64 {
	if s.ops != nil {
		if id := s.ops.BotID(); id > 0 {
			return id
		}
	}
	if s.appCfg != nil {
		if idStr, _, ok := strings.Cut(strings.TrimSpace(s.appCfg.TelegramBotToken()), ":"); ok {
			if id, err := strconv.ParseInt(idStr, 10, 64); err == nil && id > 0 {
				return id
			}
		}
	}
	return 0
}

// Config 返回当前生效的全局群管配置副本。
func (s *Service) Config() GuardConfig {
	p := s.cfgPtr.Load()
	if p == nil {
		return DefaultGuardConfig()
	}
	return *p
}

// Group 返回指定负整数群组的配置（未配置时返回 DefaultGuardGroup）。
func (s *Service) Group(chatID int64) GuardGroup {
	cfg := s.Config()
	if grp, ok := cfg.Groups[strconv.FormatInt(chatID, 10)]; ok {
		return grp
	}
	return DefaultGuardGroup()
}

// DefaultAdmins 返回系统级管理员数字 ID 列表（来自环境变量 REPORT_ADMIN_IDS / AdminIDs）。
func (s *Service) DefaultAdmins() []int64 {
	if s.appCfg == nil {
		return nil
	}
	return slices.Clone(s.appCfg.AdminIDs())
}

// Reload 从最新完整配置字典原子热重载 GuardConfig。
func (s *Service) Reload(rootDoc map[string]any) error {
	parsed, err := ParseGuardConfig(rootDoc)
	if err != nil {
		return err
	}
	s.cfgPtr.Store(&parsed)
	for k := range parsed.Groups {
		if cid, cerr := strconv.ParseInt(k, 10, 64); cerr == nil && cid < 0 {
			s.directory.Remember(cid, "")
		}
	}
	if s.learning != nil {
		s.learning.Wake()
	}
	if s.appeals != nil {
		s.appeals.MarkRoutingChanged()
	}
	return nil
}

// ReloadFromAppConfig 从绑定的 *config.Config 热重载。
func (s *Service) ReloadFromAppConfig() error {
	if s.appCfg == nil {
		return nil
	}
	return s.Reload(s.appCfg.RawSnapshot())
}

// Authorized 检查数字用户 ID 是否具备指定群组的审核/配置管理权限（全局管理员、群 reviewer_ids 或开启 allow_group_admin_review 时的群管理员）。
func (s *Service) Authorized(ctx context.Context, userID int64, chatID int64) bool {
	if userID <= 0 {
		return false
	}
	if slices.Contains(s.DefaultAdmins(), userID) {
		return true
	}
	grp := s.Group(chatID)
	if slices.Contains(grp.ReviewerIDs, userID) {
		return true
	}
	if grp.AllowGroupAdminReview && s.ops != nil && chatID < 0 {
		if perm, err := s.ops.Permissions(ctx, chatID, userID); err == nil && perm != nil {
			return perm.IsAdmin || perm.IsCreator
		}
	}
	return false
}

// ValidateGroup 校验单群配置字段，并在启用时向 Telegram 确认该会话为超级群且机器人具备封禁与删消息权限（对齐 Moderator.validate_group）。
func (s *Service) ValidateGroup(ctx context.Context, chatID int64, grp GuardGroup) error {
	if chatID >= 0 {
		return GuardError("guard.invalid_group")
	}
	if err := grp.Validate(); err != nil {
		return err
	}
	if !grp.Enabled || s.ops == nil {
		return nil
	}
	info, err := s.ops.ResolveChannel(ctx, strconv.FormatInt(chatID, 10))
	if err != nil || info == nil || !info.Megagroup || info.Monoforum {
		return GuardError("guard.supergroup_required")
	}
	perm, perr := s.ops.Permissions(ctx, chatID, s.BotID())
	if perr != nil || perm == nil {
		return GuardError("guard.permissions_required")
	}
	if !perm.IsCreator {
		if !perm.IsAdmin || !perm.CanBanUsers || !perm.CanDeleteMessages {
			return GuardError("guard.permissions_required")
		}
		if grp.JoinRequests == "approve" && !perm.CanInviteUsers {
			return GuardError("guard.permissions_required")
		}
	}
	return nil
}

// SaveGroup 校验并持久化保存单群配置至 data/config.json，随后原子重载内存状态（对齐 guard_commands.py:save_group）。
func (s *Service) SaveGroup(ctx context.Context, chatID int64, grp GuardGroup) error {
	if err := s.ValidateGroup(ctx, chatID, grp); err != nil {
		return err
	}
	if s.appCfg == nil {
		cfg := s.Config()
		if cfg.Groups == nil {
			cfg.Groups = map[string]GuardGroup{}
		}
		cfg.Groups[strconv.FormatInt(chatID, 10)] = grp
		s.cfgPtr.Store(&cfg)
		return nil
	}
	err := s.appCfg.UpdateConfig(func(doc map[string]any) error {
		MigrateNicknameKeywords(doc)
		sg, ok := doc["sakura_guard"].(map[string]any)
		if !ok || sg == nil {
			sg = StructToMap(DefaultGuardConfig())
			doc["sakura_guard"] = sg
		}
		groups, ok := sg["groups"].(map[string]any)
		if !ok || groups == nil {
			groups = map[string]any{}
			sg["groups"] = groups
		}
		groups[strconv.FormatInt(chatID, 10)] = StructToMap(grp)
		_, perr := ParseGuardConfig(doc)
		return perr
	})
	if err != nil {
		return err
	}
	return s.ReloadFromAppConfig()
}

// ────────────────────────────── 生命周期与事件挂载 ──────────────────────────────

// BindDispatcher 将 Sakura Guard 所需的实时事件处理器注册到主 Bot MTProto Dispatcher。
func (s *Service) BindDispatcher(disp *mtproto.Dispatcher) {
	if disp == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unsubs = append(s.unsubs,
		disp.OnNewMessage(func(ctx context.Context, msg *mtproto.IncomingMessage) {
			s.HandleMessage(ctx, msg)
		}),
		disp.OnMessageEdited(func(ctx context.Context, msg *mtproto.IncomingMessage) {
			s.HandleMessage(ctx, msg)
		}),
		disp.OnParticipant(func(ctx context.Context, ev mtproto.ParticipantEvent) {
			s.HandleParticipant(ctx, ev)
		}),
		disp.OnJoinRequest(func(ctx context.Context, ev mtproto.JoinRequestEvent) {
			s.HandleJoinRequest(ctx, ev)
		}),
		disp.OnUserName(func(ctx context.Context, ev mtproto.UserNameEvent) {
			s.HandleUserName(ctx, ev)
		}),
		disp.OnCallbackQuery(func(ctx context.Context, ev mtproto.CallbackEvent) {
			if strings.HasPrefix(ev.DataString(), "sg:") {
				_ = s.HandleCallback(ctx, &ev)
			}
		}),
	)
}

// AttachUserBotObserver 挂载可选 UserBot 消息流，以补充侦测普通 Bot 成员的群内发言（所有处罚仍严格由主 Bot 执行，对齐 bot_observer.py）。
func (s *Service) AttachUserBotObserver(disp *mtproto.Dispatcher) {
	if disp == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.observerAttached {
		return
	}
	handler := func(ctx context.Context, msg *mtproto.IncomingMessage) {
		if msg == nil || msg.ChatID >= 0 || msg.Sender.ID <= 0 || msg.Sender.ID == s.BotID() {
			return
		}
		grp := s.Group(msg.ChatID)
		if !grp.Enabled {
			return
		}
		if msg.Sender.IsBot || msg.GuestCallerID > 0 {
			s.HandleMessage(ctx, msg)
		}
	}
	s.observerUnsubs = append(s.observerUnsubs,
		disp.OnNewMessage(handler),
		disp.OnMessageEdited(handler),
	)
	s.observerAttached = true
}

// Start 初始化数据库表结构并启动全部后台恢复、学习、AI 复核、销号清理与申诉协程。
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	if s.repo != nil && s.repo.DB != nil {
		if err := s.repo.Initialize(ctx); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.running = true
	s.mu.Unlock()

	s.observation.Start(runCtx)
	s.learning.Start(runCtx)
	s.aiReports.Start(runCtx)
	s.appeals.Start(runCtx)
	s.deletedCleanup.StartWorker(runCtx)
	s.directory.Refresh(true)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.recoveryLoop(runCtx)
	}()
	return nil
}

// Stop 优雅停止全部后台工作协程并解除事件订阅。
func (s *Service) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	cancel := s.cancel
	unsubs := s.unsubs
	s.unsubs = nil
	obsUnsubs := s.observerUnsubs
	s.observerUnsubs = nil
	s.observerAttached = false
	s.mu.Unlock()

	for _, fn := range unsubs {
		fn()
	}
	for _, fn := range obsUnsubs {
		fn()
	}
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()

	s.directory.Stop()
	s.chatCleanup.Stop()
	s.historyCleanup.Stop()
	s.deletedCleanup.Stop()
	s.appeals.Stop()
	s.aiReports.Stop()
	s.learning.Stop()
	s.observation.Stop()
}

func (s *Service) recoveryLoop(ctx context.Context) {
	workTicker := time.NewTicker(5 * time.Second)
	retentionTicker := time.NewTicker(24 * time.Hour)
	defer workTicker.Stop()
	defer retentionTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-workTicker.C:
			s.StepRecovery(ctx)
		case <-retentionTicker.C:
			if s.repo != nil && s.repo.DB != nil {
				_ = s.repo.RetentionCleanup(ctx, s.Config().RetentionDays)
			}
		}
	}
}

// StepRecovery 执行一轮待恢复案件、验证会话及通知投递补偿。
func (s *Service) StepRecovery(ctx context.Context) {
	if s.repo == nil || s.repo.DB == nil {
		return
	}
	cfg := s.Config()
	policy := make(map[string]bool, len(cfg.Groups))
	for k, g := range cfg.Groups {
		policy[k] = g.KickButtonEnabled
	}
	cases, sessions, err := s.repo.GuardWork(ctx, policy)
	if err != nil {
		return
	}
	for _, sess := range sessions {
		if ctx.Err() != nil {
			return
		}
		s.verification.ProcessDueSession(ctx, sess)
	}
	for _, c := range cases {
		if ctx.Err() != nil {
			return
		}
		uid := rowInt64(c, "user_id")
		cid := rowStr(c, "id")
		chatID := rowInt64(c, "chat_id")
		if uid == 0 || cid == "" {
			continue
		}
		_ = s.repo.WithLock(ctx, uid, func(st *guarddb.Store) error {
			cur, _ := st.Case(ctx, cid)
			if cur == nil {
				return nil
			}
			grp := s.Group(chatID)
			if rowStr(cur, "status") == "executing" {
				_ = s.ExecuteCase(ctx, st, cur)
				cur, _ = st.Case(ctx, cid)
			} else if strings.HasPrefix(rowStr(cur, "status"), "reviewing_") {
				_ = s.reviews.resumeReview(ctx, st, cur)
				cur, _ = st.Case(ctx, cid)
			}
			if cur != nil {
				_ = s.reviews.resumeMessageDeletion(ctx, st, cur, false)
				cur, _ = st.Case(ctx, cid)
			}
			if cur != nil {
				s.banHistory.Resume(ctx, st, cur, false)
				cur, _ = st.Case(ctx, cid)
			}
			if cur != nil {
				s.notifier.Sync(ctx, st, cur, grp)
			}
			return nil
		})
		if rowBool(rowPayload(c), "guest_ban_pending") {
			s.resumeGuestMessage(ctx, cid)
		}
	}
}

// ────────────────────────────── 核心匹配与处罚执行 ──────────────────────────────

func (s *Service) checkProtection(ctx context.Context, chatID, userID int64, grp GuardGroup, allowNonMember bool) protectionState {
	if userID <= 0 || userID == s.BotID() || slices.Contains(grp.TrustedBotIDs, userID) {
		return protectionProtected
	}
	if slices.Contains(s.DefaultAdmins(), userID) {
		return protectionProtected
	}
	if s.ops == nil {
		return protectionUnprotected
	}
	perm, err := s.ops.Permissions(ctx, chatID, userID)
	if err != nil {
		if allowNonMember && (strings.Contains(err.Error(), "USER_NOT_PARTICIPANT") || strings.Contains(err.Error(), "UserNotParticipant")) {
			return protectionUnprotected
		}
		return protectionUnknown
	}
	if perm != nil && (perm.IsAdmin || perm.IsCreator) {
		return protectionProtected
	}
	return protectionUnprotected
}

// matchSnapshot 按严格 9 级优先级检测用户资料与消息是否命中风控规则（对齐 Moderator.match）。
func (s *Service) matchSnapshot(
	ctx context.Context,
	st *guarddb.Store,
	chatID, userID int64,
	user mtproto.ProfileSummary,
	msg *mtproto.IncomingMessage,
	grp GuardGroup,
) *Match {
	globalCfg := s.Config()

	// 1. 跨群共享全局黑名单（先于群白名单）
	if grp.ShareGlobalBlacklist && st != nil {
		if source := guestMessageSource(msg); source != nil {
			caller := rowInt64(source, "user_id")
			if black, _ := st.Blacklist(ctx, caller); black != nil {
				_ = st.TouchBlacklist(ctx, caller)
				return &Match{
					Target: "global_blacklist", RuleID: "global-guest-caller-id", Action: "ban",
					Reason: i18n.T("guard.guest_blacklist_reason", nil), Snippet: strconv.FormatInt(caller, 10),
				}
			}
		}
		if black, _ := st.Blacklist(ctx, userID); black != nil {
			_ = st.TouchBlacklist(ctx, userID)
			return &Match{
				Target:  "global_blacklist",
				RuleID:  "global-user-id",
				Action:  "ban",
				Reason:  "global_blacklist",
				Snippet: fmt.Sprintf("user:%d", userID),
			}
		}
	}

	// 2. 群白名单豁免
	if st != nil {
		if whitelisted, _ := st.Whitelisted(ctx, chatID, userID); whitelisted {
			return nil
		}
	}

	matcher := NewMatcher()

	// 3. 昵称与用户名过滤
	if hit := matcher.MatchNickname(user, globalCfg, grp); hit != nil {
		return hit
	}

	// 4. 结构化与普通 Bot 发言拦截
	if hit := matcher.MatchStructural(msg, grp); hit != nil {
		return hit
	}

	// 5. 正文与提取链接过滤
	if hit := matcher.MatchMessageAndLinks(msg, grp); hit != nil {
		return hit
	}

	// 6. 进程内活跃 AI 动态规则快照
	if s.learning != nil && s.learning.Cache() != nil {
		if hit := s.learning.Cache().MatchMessage(chatID, user, msg); hit != nil {
			return hit
		}
	}
	return nil
}

// RecordEvent 在持有用户锁下创建已完成状态的审计/通知案件并触发通知同步（对齐 Moderator.record_event）。
func (s *Service) RecordEvent(
	ctx context.Context,
	st *guarddb.Store,
	chatID, userID int64,
	action, eventKey, category string,
	actor any,
	extraPayload map[string]any,
) error {
	if st == nil {
		return nil
	}
	if len(eventKey) < 64 {
		eventKey = sha256Hex(eventKey)
	}
	if existing, _ := st.CaseForEvent(ctx, eventKey); existing != nil {
		return nil
	}
	payload := map[string]any{
		"notification_event": category,
	}
	for k, v := range extraPayload {
		payload[k] = v
	}
	revIDPtr, revActor := parseReviewerIdentity(actor)
	var revActorPtr *string
	var revAtPtr *time.Time
	if actor != nil && actor != 0 && actor != "" {
		revActorPtr = &revActor
		now := time.Now().UTC()
		revAtPtr = &now
	}
	caseID := newUUIDHex()
	if err := st.CreateCase(ctx, guarddb.CaseInsert{
		ID:            caseID,
		EventKey:      eventKey,
		ChatID:        chatID,
		UserID:        userID,
		Action:        action,
		Status:        "done",
		Payload:       payload,
		ReviewerID:    revIDPtr,
		ReviewerActor: revActorPtr,
		ReviewedAt:    revAtPtr,
	}); err != nil {
		return err
	}
	caseRow, _ := st.Case(ctx, caseID)
	if s.notifier != nil && caseRow != nil {
		s.notifier.Sync(ctx, st, caseRow, s.Group(chatID))
	}
	return nil
}

func (s *Service) punishLocked(
	ctx context.Context,
	st *guarddb.Store,
	chatID, userID int64,
	senderName string,
	messageID int,
	match *Match,
	grp GuardGroup,
	token string,
	message ...*mtproto.IncomingMessage,
) {
	if st == nil || match == nil {
		return
	}
	eventKey := sha256Hex(fmt.Sprintf("%d:%d:%s:%s", chatID, userID, match.Action, token))
	if existing, _ := st.CaseForEvent(ctx, eventKey); existing != nil {
		return
	}
	if strings.TrimSpace(senderName) == "" {
		senderName = strconv.FormatInt(userID, 10)
	}
	caseID := newUUIDHex()
	payload := map[string]any{
		"name":                senderName,
		"controls_version":    2,
		"kick_button_enabled": grp.KickButtonEnabled,
		"notification_event":  "moderation",
		"evidence": map[string]any{
			"target":  match.Target,
			"rule_id": match.RuleID,
			"reason":  match.Reason,
			"snippet": match.Snippet,
		},
	}
	if messageID > 0 {
		payload["message_id"] = messageID
	}
	if len(message) > 0 {
		if source := guestMessageSource(message[0]); source != nil {
			payload["guest_actor"] = source
			if match.Action == "ban" {
				prepareGuestBan(payload)
			}
		}
	}
	if err := st.CreateCase(ctx, guarddb.CaseInsert{
		ID:       caseID,
		EventKey: eventKey,
		ChatID:   chatID,
		UserID:   userID,
		Action:   match.Action,
		Status:   "executing",
		Payload:  payload,
	}); err != nil {
		return
	}

	if match.AIRuleID != "" {
		_ = st.RecordAIRuleHit(ctx, guarddb.Row{
			"id":      match.AIRuleID,
			"version": match.AIRuleVersion,
		}, chatID, userID, &caseID, match.Action, payload["evidence"])
	} else if match.Action == "ban" && s.learning != nil && s.learning.Enabled(chatID) && s.learning.SourceAllowed("rule_auto_ban") {
		sid := newUUIDHex()
		cidCopy := caseID
		if _, created, _ := st.CreateAISample(ctx, guarddb.AISampleInsert{
			ID:             sid,
			IdempotencyKey: sha256Hex("ai-case:" + caseID),
			ChatID:         chatID,
			UserID:         userID,
			Source:         "rule_auto_ban",
			CaseID:         &cidCopy,
			Label:          "positive",
			Status:         "queued",
			Payload:        payload,
		}); created {
			_ = st.EnqueueAILearningJob(ctx, sid, chatID, userID)
			s.learning.Wake()
		}
	}

	caseRow, _ := st.Case(ctx, caseID)
	_ = s.ExecuteCase(ctx, st, caseRow)
}

// ExecuteCase 在持有用户锁下执行单条 executing 案件的 Telegram 处罚动作并推进状态机（对齐 Moderator.execute_case）。
func (s *Service) ExecuteCase(ctx context.Context, st *guarddb.Store, caseRow guarddb.Row) error {
	if st == nil || caseRow == nil {
		return nil
	}
	caseID := rowStr(caseRow, "id")
	chatID := rowInt64(caseRow, "chat_id")
	userID := rowInt64(caseRow, "user_id")
	action := rowStr(caseRow, "action")
	payload := rowPayload(caseRow)
	grp := s.Group(chatID)

	if payload["verification_admin"] != nil {
		return s.verification.executeAdminVerificationCase(ctx, st, caseRow, nil)
	}
	// A kick whose ban already succeeded must finish its unban even if a reload disables Guard.
	if action == "kick" && rowBool(payload, "kick_banned") && s.ops != nil {
		if err := s.executeKickTwoPhase(ctx, st, caseRow); err != nil {
			detail := err.Error()
			if saved := st.UpdateCase(ctx, caseID, "executing", &detail, payload); saved != nil {
				return errors.Join(err, saved)
			}
			return err
		}
		if err := st.UpdateCase(ctx, caseID, "done", nil, payload); err != nil {
			return err
		}
		updated, err := st.Case(ctx, caseID)
		if err != nil {
			return err
		}
		s.notifier.Sync(ctx, st, updated, grp)
		return nil
	}
	if !grp.Enabled {
		_ = st.UpdateCase(ctx, caseID, "cancelled", nil, payload)
		return nil
	}

	prot := s.checkProtection(ctx, chatID, userID, grp, true)
	if prot == protectionProtected {
		_ = st.UpdateCase(ctx, caseID, "cancelled", nil, payload)
		return nil
	}
	if prot == protectionUnknown {
		errCode := "guard.permissions_unknown"
		_ = st.UpdateCase(ctx, caseID, "executing", &errCode, payload)
		return errors.New(errCode)
	}
	if err := s.checkGuestSource(ctx, st, caseRow); err != nil {
		if errors.Is(err, errGuestDecisionChanged) {
			return st.UpdateCase(ctx, caseID, "cancelled", nil, payload)
		}
		return err
	}

	if s.ops != nil {
		if mid := int(rowInt64(payload, "message_id")); mid > 0 && !rowBool(payload, "verification_message") {
			if action == "ban" {
				requestErr := s.reviews.requestMessageDeletion(ctx, st, caseRow, "system:auto_filter", true)
				fresh, readErr := st.Case(ctx, caseID)
				if readErr != nil || fresh == nil {
					return errors.Join(requestErr, readErr)
				}
				payload = rowPayload(fresh)
				deletion, _ := payload["report_message_deletion"].(map[string]any)
				if deletion == nil && requestErr != nil {
					return requestErr
				}
				if rowStr(deletion, "status") == "cancelled" {
					return st.UpdateCase(ctx, caseID, "cancelled", nil, payload)
				}
			} else if err := s.ops.DeleteMessages(ctx, chatID, []int{mid}); err != nil && action == "delete" {
				return err
			}
		}

		var actErr error
		switch action {
		case "delete":
			// 消息删除已在上方执行
		case "warn":
			warnText := fmt.Sprintf("⚠️ 成员 %s 触发群规限制，请注意发言规范。", rowStr(payload, "name"))
			_, actErr = s.ops.SendOnce(ctx, chatID, warnText, mtproto.SendOptions{ParseMode: "plain", NoWebpage: true}, mtproto.StableRandomID("warn:"+caseID))
		case "mute":
			origRights, rerr := s.ops.Rights(ctx, chatID, userID)
			if rerr != nil {
				actErr = rerr
			} else {
				now := time.Now().UTC()
				muteUntil := now.Add(time.Duration(grp.MuteSeconds) * time.Second).Unix()
				payload["original_rights"] = mtproto.BannedRightsToMap(origRights)
				payload["restriction_started_at"] = now.Format("2006-01-02T15:04:05.000000")
				actErr = s.ops.SetRights(ctx, chatID, userID, mtproto.MuteRights(origRights, muteUntil))
			}
		case "kick":
			actErr = s.executeKickTwoPhase(ctx, st, caseRow)
		case "ban":
			if isBasicGroupID(chatID) {
				actErr = s.ops.KickBasicGroup(ctx, chatID, userID)
			} else {
				actErr = s.ops.Ban(ctx, chatID, userID)
				if actErr == nil && s.banHistory != nil {
					s.banHistory.ScheduleOnPayload(ctx, chatID, userID, payload)
				}
			}
		case "approve", "reject", "manual":
			actErr = s.executeJoinRequest(ctx, st, caseRow, grp)
		}

		if actErr != nil {
			errMsg := actErr.Error()
			// 若两阶段踢人已完成第一阶段 ban，保持 executing 等待恢复循环自动补齐 unban，绝不使成员永久被封。
			if action == "kick" && rowBool(payload, "kick_banned") {
				_ = st.UpdateCase(ctx, caseID, "executing", &errMsg, payload)
				return actErr
			}
			_ = st.UpdateCase(ctx, caseID, "execution_failed", &errMsg, payload)
			updated, _ := st.Case(ctx, caseID)
			s.notifier.Sync(ctx, st, updated, grp)
			return actErr
		}
	}

	nextStatus := "done"
	switch action {
	case "ban":
		nextStatus = "pending"
	case "manual":
		nextStatus = "manual"
	}
	if err := st.UpdateCase(ctx, caseID, nextStatus, nil, payload); err != nil {
		return err
	}
	updated, _ := st.Case(ctx, caseID)
	if action == "ban" && s.banHistory != nil {
		s.banHistory.Resume(ctx, st, updated, false)
		updated, _ = st.Case(ctx, caseID)
	}
	if s.notifier != nil && updated != nil {
		s.notifier.Sync(ctx, st, updated, grp)
	}
	return nil
}

func (s *Service) executeKickTwoPhase(ctx context.Context, st *guarddb.Store, caseRow guarddb.Row) error {
	if s.ops == nil {
		return GuardError("guard.unavailable")
	}
	chatID := rowInt64(caseRow, "chat_id")
	userID := rowInt64(caseRow, "user_id")
	caseID := rowStr(caseRow, "id")
	payload := rowPayload(caseRow)
	if rowBool(payload, "kick_unbanned") {
		return s.verification.finishLegacyKickSession(ctx, st, caseRow)
	}
	if blocked, err := st.Blacklist(ctx, userID); err != nil {
		return err
	} else if blocked != nil {
		return GuardError("guard.blacklist_conflict")
	}
	if active, err := st.ActiveBan(ctx, chatID, userID, caseID); err != nil {
		return err
	} else if active != nil {
		return GuardError("guard.other_ban_conflict")
	}
	status := rowStr(caseRow, "status")
	if isBasicGroupID(chatID) {
		if err := s.ops.KickBasicGroup(ctx, chatID, userID); err != nil {
			return err
		}
		payload["kick_unbanned"] = true
		if err := st.UpdateCase(ctx, caseID, status, nil, payload); err != nil {
			return err
		}
		return s.verification.finishLegacyKickSession(ctx, st, caseRow)
	}
	member, err := s.ops.Permissions(ctx, chatID, userID)
	if err != nil && !isMissingParticipant(err) {
		return err
	}
	if err == nil && member == nil {
		return GuardError("guard.permissions_unknown")
	}
	var currentBanned bool
	if member != nil {
		if member.IsAdmin || member.IsCreator || (member.UserID != 0 && member.UserID != userID) {
			return GuardError("guard.permissions_changed")
		}
		currentBanned = member.BannedRights.ViewMessages
		if currentBanned && member.KickedBy != s.BotID() {
			return GuardError("guard.permissions_changed")
		}
	}
	if !rowBool(payload, "kick_banned") {
		if currentBanned && !rowBool(payload, "kick_ban_intended") &&
			rowStr(caseRow, "action") != "ban" && !rowBool(payload, "review_ban_confirmed") {
			return GuardError("guard.other_ban_conflict")
		}
		if !currentBanned {
			payload["kick_ban_intended"] = true
			if err := st.UpdateCase(ctx, caseID, status, nil, payload); err != nil {
				return err
			}
			if err := s.ops.Ban(ctx, chatID, userID); err != nil {
				return err
			}
			currentBanned = true
		}
		payload["kick_banned"] = true
		if err := st.UpdateCase(ctx, caseID, status, nil, payload); err != nil {
			return err
		}
	}
	if currentBanned {
		if err := s.ops.Unban(ctx, chatID, userID); err != nil {
			return err
		}
	}
	payload["kick_unbanned"] = true
	if err := st.UpdateCase(ctx, caseID, status, nil, payload); err != nil {
		return err
	}
	return s.verification.finishLegacyKickSession(ctx, st, caseRow)
}

func isMissingParticipant(err error) bool {
	if err == nil {
		return false
	}
	var telegramErr *mtproto.TelegramActionError
	return errors.As(err, &telegramErr) && (telegramErr.Code == "USER_NOT_PARTICIPANT" || telegramErr.Code == "UserNotParticipantError")
}

// ────────────────────────────── 实时事件处理入口 ──────────────────────────────

// HandleMessage 处理群组新消息与编辑消息。
func (s *Service) HandleMessage(ctx context.Context, msg *mtproto.IncomingMessage) {
	if msg == nil || msg.ChatID >= 0 {
		return
	}
	chatKind := "supergroup"
	if msg.IsBasicGroup {
		chatKind = "group"
	} else if msg.IsBroadcast {
		chatKind = "channel"
	}
	s.directory.Observe(msg.ChatID, msg.ChatTitle, chatKind, false)

	grp := s.Group(msg.ChatID)
	if !grp.Enabled {
		return
	}

	// 优先检查是否为成员回复举报/上报
	if s.reports.Handle(ctx, msg) {
		return
	}

	userID := msg.Sender.ID
	if userID <= 0 || userID == s.BotID() {
		return
	}
	if s.checkProtection(ctx, msg.ChatID, userID, grp, msg.Sender.IsBot || msg.GuestCallerID > 0) != protectionUnprotected {
		return
	}

	if s.repo == nil || s.repo.DB == nil {
		return
	}

	editSuffix := "0"
	if msg.EditDate != nil && !msg.EditDate.IsZero() {
		editSuffix = strconv.FormatInt(msg.EditDate.Unix(), 10)
	}
	token := fmt.Sprintf("message:%d:%d:%s", msg.ChatID, msg.ID, editSuffix)
	fullName := strings.TrimSpace(msg.Sender.FirstName + " " + msg.Sender.LastName)

	_ = s.repo.WithLock(ctx, userID, func(st *guarddb.Store) error {
		freshGrp := s.Group(msg.ChatID)
		if !freshGrp.Enabled {
			return nil
		}
		// 若成员仍在入群验证期，自动删除漏网发言
		if sess, _ := st.VerificationSession(ctx, msg.ChatID, userID); sess != nil && guarddb.IsActiveVerificationStatus(rowStr(sess, "status")) {
			if s.ops != nil && msg.ID > 0 {
				_ = s.ops.DeleteMessages(ctx, msg.ChatID, []int{msg.ID})
			}
			return nil
		}

		if hit := s.matchSnapshot(ctx, st, msg.ChatID, userID, msg.Sender, msg, freshGrp); hit != nil {
			s.punishLocked(ctx, st, msg.ChatID, userID, fullName, msg.ID, hit, freshGrp, token, msg)
			return nil
		}

		if obsHit := s.observation.InspectMessage(ctx, st, msg.ChatID, msg, freshGrp); obsHit != nil {
			s.punishLocked(ctx, st, msg.ChatID, userID, fullName, msg.ID, obsHit, freshGrp, token, msg)
			if freshGrp.ObservationAction == "mute" {
				muteHit := *obsHit
				muteHit.Action = "mute"
				s.punishLocked(ctx, st, msg.ChatID, userID, fullName, msg.ID, &muteHit, freshGrp, token+":mute", msg)
			}
		}
		return nil
	})
}

// HandleParticipant 处理群成员加入、离开、被踢或权限变更事件。
func (s *Service) HandleParticipant(ctx context.Context, ev mtproto.ParticipantEvent) {
	if ev.ChatID >= 0 {
		return
	}
	if ev.UserID == s.BotID() {
		if !ev.IsPresent || ev.IsBanned {
			s.directory.Observe(ev.ChatID, "", "", true)
		} else {
			s.directory.Invalidate(ev.ChatID)
			s.directory.Refresh(true)
		}
		return
	}

	grp := s.Group(ev.ChatID)
	if !grp.Enabled || s.repo == nil || s.repo.DB == nil || ev.UserID <= 0 {
		return
	}

	// 成员退群或被外部管理员封禁时终结活跃验证会话
	if !ev.IsPresent || ev.IsBanned {
		if ev.ActorID == s.BotID() {
			return
		}
		_ = s.repo.WithLock(ctx, ev.UserID, func(st *guarddb.Store) error {
			sess, _ := st.VerificationSession(ctx, ev.ChatID, ev.UserID)
			if sess == nil || !guarddb.IsActiveVerificationStatus(rowStr(sess, "status")) {
				return nil
			}
			p := rowPayload(sess)
			if staleMembershipEvent(ev, rowStr(p, "join_token")) {
				return nil
			}
			nextSt := "left"
			if ev.IsBanned {
				nextSt = "blocked"
			}
			p["left_at"], p["left_qts"] = ev.Date.Unix(), ev.QTS
			p["cleanup_pending"] = true
			sess["status"], sess["error"] = nextSt, nil
			if err := saveVerificationRow(ctx, st, sess); err != nil {
				return err
			}
			return s.verification.finishVerificationLifecycle(ctx, st, sess)
		})
		return
	}

	// 新成员加入（!WasPresent && IsPresent）
	if !ev.WasPresent && ev.IsPresent {
		token := fmt.Sprintf("membership:%d:raw:%d", ev.Date.Unix(), ev.QTS)
		if ev.ActionMessageID > 0 {
			token = fmt.Sprintf("membership:%d:chat:%d", ev.Date.Unix(), ev.ActionMessageID)
		}
		user := ev.User
		user.ID = ev.UserID
		_ = s.repo.WithLock(ctx, ev.UserID, func(st *guarddb.Store) error {
			freshGrp := s.Group(ev.ChatID)
			if !freshGrp.Enabled {
				return nil
			}
			switch s.checkProtection(ctx, ev.ChatID, ev.UserID, freshGrp, false) {
			case protectionProtected:
				return nil
			case protectionUnknown:
				return s.verification.queueVerificationCheck(ctx, st, ev.ChatID, user, freshGrp, token)
			default:
				return s.verification.processJoinedMember(ctx, st, ev.ChatID, user, freshGrp, token)
			}
		})
	}
}

// HandleUserName 处理成员实时改名/改用户名事件，遍历已启用群组执行违规昵称拦截（对齐 Moderator.handle_profile_update）。
func (s *Service) HandleUserName(ctx context.Context, ev mtproto.UserNameEvent) {
	if ev.UserID <= 0 || ev.UserID == s.BotID() || s.repo == nil || s.repo.DB == nil {
		return
	}
	prof := mtproto.ProfileSummary{
		ID:              ev.UserID,
		FirstName:       ev.FirstName,
		LastName:        ev.LastName,
		Username:        ev.Username,
		ActiveUsernames: ev.ActiveUsernames,
	}
	cfg := s.Config()
	fullName := strings.TrimSpace(ev.FirstName + " " + ev.LastName)

	for k, grp := range cfg.Groups {
		if !grp.Enabled || !grp.NicknameFilter {
			continue
		}
		if NewMatcher().MatchNickname(prof, cfg, grp) == nil {
			continue
		}
		chatID, err := strconv.ParseInt(k, 10, 64)
		if err != nil || chatID >= 0 {
			continue
		}
		if s.ops != nil {
			perm, perr := s.ops.Permissions(ctx, chatID, ev.UserID)
			if perr != nil || perm == nil || !perm.IsMember || perm.IsAdmin || perm.IsCreator || perm.BannedRights.ViewMessages {
				continue
			}
		}
		_ = s.repo.WithLock(ctx, ev.UserID, func(st *guarddb.Store) error {
			freshGrp := s.Group(chatID)
			if !freshGrp.Enabled {
				return nil
			}
			if hit := s.matchSnapshot(ctx, st, chatID, ev.UserID, prof, nil, freshGrp); hit != nil {
				s.punishLocked(ctx, st, chatID, ev.UserID, fullName, 0, hit, freshGrp, fmt.Sprintf("profile:%s", newUUIDHex()))
			}
			return nil
		})
	}
}

// HandleCallback 统一分发 sg:* 内联按钮回调。
func (s *Service) HandleCallback(ctx context.Context, cb *mtproto.CallbackEvent) error {
	if cb == nil {
		return nil
	}
	parts := strings.Split(cb.DataString(), ":")
	if len(parts) < 2 || parts[0] != "sg" {
		return nil
	}
	switch parts[1] {
	case "r":
		return s.reviews.HandleCallback(ctx, cb, parts[2:])
	case "v":
		return s.verification.HandleCallback(ctx, cb, parts[2:])
	case "n":
		return s.verification.handleNoticeAction(ctx, cb, parts[2:])
	default:
		if s.ops != nil && cb.QueryID != 0 {
			return s.ops.AnswerCallback(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
		}
		return nil
	}
}

// ────────────────────────────── Telegram /guard* 斜杠命令（实现 botcmd.GuardCommandHandler） ──────────────────────────────

// HandleGuardCommand 处理全部 /guard* 斜杠命令（对齐 core/commands/guard_commands.py:handle_guard）。
func (s *Service) HandleGuardCommand(
	ctx context.Context,
	bot mtproto.Messenger,
	msg *mtproto.IncomingMessage,
	cmd string,
	rawArgs string,
) error {
	if bot == nil || msg == nil {
		return nil
	}
	reply := func(text string) error {
		_, err := bot.SendMessage(ctx, msg.ChatID, text, mtproto.SendOptions{
			ReplyToMsgID: msg.ID,
			ParseMode:    "plain",
			NoWebpage:    true,
		})
		return err
	}

	command := "/" + strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cmd)), "/")
	parts := strings.Fields(rawArgs)
	targetChatID := msg.ChatID
	if len(parts) > 0 && strings.HasPrefix(parts[0], "-") {
		parsedChat, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return reply(i18n.T("guard.help", nil))
		}
		targetChatID = parsedChat
		parts = parts[1:]
	}

	err := s.execGuardCommandInner(ctx, msg.Sender.ID, command, targetChatID, parts, reply)
	if err != nil {
		var ge GuardError
		if errors.As(err, &ge) {
			return reply(i18n.T(string(ge), nil))
		}
		s.logger.Warn("Guard 命令执行失败", "command", command, "chat", targetChatID, "error", err)
		return reply(i18n.T("guard.operation_failed", nil))
	}
	return nil
}

func (s *Service) execGuardCommandInner(
	ctx context.Context,
	senderID int64,
	command string,
	chatID int64,
	parts []string,
	reply func(string) error,
) error {
	if command == "/guard_addword" || command == "/guard_delword" {
		if !slices.Contains(s.DefaultAdmins(), senderID) {
			return GuardError("error.permission_denied")
		}
		word := strings.TrimSpace(strings.Join(parts, " "))
		if word == "" {
			return GuardError("guard.help")
		}
		curCfg := s.Config()
		words := slices.Clone(curCfg.NicknameKeywords)
		if command == "/guard_addword" {
			if !slices.Contains(words, word) {
				words = append(words, word)
			}
		} else {
			filtered := make([]string, 0, len(words))
			for _, w := range words {
				if w != word {
					filtered = append(filtered, w)
				}
			}
			words = filtered
		}
		validWords, verr := ValidateWords(words, 50000)
		if verr != nil {
			return GuardError("guard.help")
		}
		if s.appCfg != nil {
			if err := s.appCfg.UpdateConfig(func(doc map[string]any) error {
				MigrateNicknameKeywords(doc)
				sg, ok := doc["sakura_guard"].(map[string]any)
				if !ok || sg == nil {
					sg = StructToMap(DefaultGuardConfig())
					doc["sakura_guard"] = sg
				}
				sg["nickname_keywords"] = validWords
				_, perr := ParseGuardConfig(doc)
				return perr
			}); err != nil {
				return err
			}
			_ = s.ReloadFromAppConfig()
		} else {
			curCfg.NicknameKeywords = validWords
			s.cfgPtr.Store(&curCfg)
		}
		return reply(i18n.T("guard.global_keywords_saved", map[string]string{"count": strconv.Itoa(len(s.Config().NicknameKeywords))}))
	}

	if chatID >= 0 {
		if !slices.Contains(s.DefaultAdmins(), senderID) {
			return GuardError("error.permission_denied")
		}
		cfg := s.Config()
		var lines []string
		lines = append(lines, i18n.T("guard.help", nil))
		for k, g := range cfg.Groups {
			lines = append(lines, fmt.Sprintf("%s: %v", k, g.Enabled))
		}
		return reply(strings.Join(lines, "\n"))
	}

	if !s.Authorized(ctx, senderID, chatID) {
		return GuardError("error.permission_denied")
	}

	grp := s.Group(chatID)
	switch command {
	case "/guard_on":
		grp.Enabled = true
	case "/guard_off":
		grp.Enabled = false
	case "/guard_verify":
		if len(parts) == 0 || (parts[0] != "on" && parts[0] != "off") {
			return GuardError("guard.help")
		}
		grp.VerifyJoin = parts[0] == "on"
	case "/guard_bots":
		if len(parts) != 1 || (parts[0] != "on" && parts[0] != "off") {
			return GuardError("guard.help")
		}
		grp.BlockBotMessages = parts[0] == "on"
	case "/guard_whitelist":
		if len(parts) != 2 || (parts[0] != "add" && parts[0] != "remove") {
			return GuardError("guard.help")
		}
		uid, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || uid <= 0 {
			return GuardError("guard.help")
		}
		if s.repo == nil || s.repo.DB == nil {
			return GuardError("guard.unavailable")
		}
		if err := s.repo.WithLock(ctx, uid, func(st *guarddb.Store) error {
			if parts[0] == "add" {
				if black, _ := st.Blacklist(ctx, uid); black != nil {
					return GuardError("guard.blacklist_conflict")
				}
				revID := senderID
				if err := st.AddWhitelist(ctx, chatID, uid, &revID, fmt.Sprintf("telegram:%d", senderID)); err != nil {
					return err
				}
				s.learning.RecordWhitelist(ctx, st, chatID, uid, fmt.Sprintf("telegram:%d", senderID))
				return s.RecordEvent(ctx, st, chatID, uid, "whitelist_add", newUUIDHex(), "whitelist", senderID, nil)
			}
			if err := st.RemoveWhitelist(ctx, chatID, uid); err != nil {
				return err
			}
			return s.RecordEvent(ctx, st, chatID, uid, "whitelist_remove", newUUIDHex(), "whitelist", senderID, nil)
		}); err != nil {
			return err
		}
		return reply(i18n.T("guard.saved", nil))

	case "/guard_unban":
		if len(parts) != 1 {
			return GuardError("guard.help")
		}
		res, err := s.reviews.Review(ctx, parts[0], "allow", senderID, false, &chatID, nil)
		if err != nil {
			return err
		}
		return reply(i18n.T("guard.review_result", map[string]string{
			"status": i18n.T("guard.status_"+rowStr(res, "status"), nil),
		}))

	case "/guard_logs":
		if s.repo == nil || s.repo.DB == nil {
			return GuardError("guard.unavailable")
		}
		page, err := s.repo.QueryRecords(ctx, "cases", guarddb.RecordFilter{ChatID: &chatID, Limit: 10})
		if err != nil {
			return err
		}
		if page == nil || len(page.Rows) == 0 {
			return reply(i18n.T("guard.empty", nil))
		}
		var lines []string
		for _, r := range page.Rows {
			lines = append(lines, fmt.Sprintf("%s | %d | %s | %s",
				rowStr(r, "id"), rowInt64(r, "user_id"), rowStr(r, "action"), rowStr(r, "status")))
		}
		return reply(strings.Join(lines, "\n"))

	default:
		statusMsg := i18n.T("guard.group_status", map[string]string{
			"chat_id":    strconv.FormatInt(chatID, 10),
			"enabled":    strconv.FormatBool(grp.Enabled),
			"verify":     strconv.FormatBool(grp.VerifyJoin),
			"nickname":   strconv.FormatBool(grp.NicknameFilter),
			"message":    strconv.FormatBool(grp.MessageFilter),
			"block_bots": strconv.FormatBool(grp.BlockBotMessages),
			"sharing":    strconv.FormatBool(grp.ShareGlobalBlacklist),
		})
		return reply(statusMsg + "\n\n" + i18n.T("guard.help", nil))
	}

	if err := s.SaveGroup(ctx, chatID, grp); err != nil {
		return err
	}
	return reply(i18n.T("guard.saved", nil))
}
