// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package welcome

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/poll"
)

const (
	welcomeCacheTTL   = time.Hour
	maxCallbackBytes  = 64
	defaultQueueLimit = 512
)

// Repo 抽象评论区周报申请所需的数据库接口（由 *db.Manager 实现）。
type Repo interface {
	IsUserRegistered(ctx context.Context, userID int64) (bool, error)
	HasRecentSummaryRequest(ctx context.Context, userID int64, channelID string, within time.Duration) (bool, error)
	EnqueueSummaryRequest(ctx context.Context, userID int64, channelID string, msgID int64, params map[string]any) (int64, error)
}

// AutoPollEnqueuer 抽象讨论群自动趣味投票联动接口（由 *poll.AutoPollHandler 实现）。
type AutoPollEnqueuer interface {
	Enqueue(task poll.AutoPollTask) bool
}

type welcomeTask struct {
	discussionID int64
	forwardMsgID int
	channelID    string
	channelMsgID int
}

// Deps 配置评论区欢迎与周报申请处理器。
type Deps struct {
	Config         *config.Config
	Repo           Repo
	BotClient      func() mtproto.Messenger
	AutoPoll       AutoPollEnqueuer
	MinDelay       time.Duration
	MaxDelay       time.Duration
	RestorePollInt time.Duration
	RestoreChecks  int
	Logger         *slog.Logger
}

// Handler 负责讨论组自动转发欢迎回复、自动趣味投票触发与 req_summary: 按钮回调（对齐 channel_comment_welcome.py）。
type Handler struct {
	deps Deps

	mu       sync.Mutex
	running  bool
	queue    chan welcomeTask
	cancelBg context.CancelFunc
	wg       sync.WaitGroup

	cacheMu sync.Mutex
	cache   map[string]time.Time
}

// NewHandler 创建评论区欢迎消息处理器。
func NewHandler(deps Deps) *Handler {
	if deps.MinDelay == 0 && deps.MaxDelay == 0 {
		deps.MinDelay = time.Second
		deps.MaxDelay = 3 * time.Second
	}
	if deps.RestorePollInt <= 0 {
		deps.RestorePollInt = 3 * time.Second
	}
	if deps.RestoreChecks <= 0 {
		deps.RestoreChecks = 5
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Handler{
		deps:  deps,
		cache: make(map[string]time.Time),
	}
}

// Start 启动后台限流发送 worker 与去重缓存清理协程。
func (h *Handler) Start(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running {
		return
	}
	h.queue = make(chan welcomeTask, defaultQueueLimit)
	bgCtx, cancel := context.WithCancel(ctx)
	h.cancelBg = cancel
	h.running = true

	h.wg.Add(2)
	go func() {
		defer h.wg.Done()
		h.workerLoop(bgCtx)
	}()
	go func() {
		defer h.wg.Done()
		h.cleanupLoop(bgCtx)
	}()
}

// Stop 停止后台协程并等待退出。
func (h *Handler) Stop() {
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		return
	}
	h.running = false
	cancel := h.cancelBg
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	h.wg.Wait()
}

// BuildSummaryCallbackData 构造 req_summary:<channelID>:<msgID> 回调载荷并校验 64 字节上限。
func BuildSummaryCallbackData(channelID string, channelMsgID int) (string, bool) {
	payload := fmt.Sprintf("req_summary:%s:%d", channelID, channelMsgID)
	return payload, len(payload) <= maxCallbackBytes
}

// ParseSummaryCallbackData 安全解析 req_summary:<channelID>:<msgID>（支持含 https:// 的频道 URL）。
func ParseSummaryCallbackData(data string) (channelID string, msgID int, ok bool) {
	action, rest, found := strings.Cut(data, ":")
	if !found || action != "req_summary" {
		return "", 0, false
	}
	lastColon := strings.LastIndexByte(rest, ':')
	if lastColon <= 0 || lastColon >= len(rest)-1 {
		return "", 0, false
	}
	chPart := rest[:lastColon]
	idPart := rest[lastColon+1:]
	parsedID, err := strconv.Atoi(idPart)
	if err != nil || parsedID <= 0 || strings.TrimSpace(chPart) == "" {
		return "", 0, false
	}
	return chPart, parsedID, true
}

// IsChannelWhitelisted 检查频道标识是否命中配置的 channels 白名单（对齐 is_channel_in_whitelist）。
func IsChannelWhitelisted(channelRef string, whitelist []string) (string, bool) {
	key := config.ComparableChannelKey(channelRef)
	if key == "" {
		return "", false
	}
	for _, entry := range whitelist {
		norm := config.ComparableChannelKey(entry)
		if norm == key || strings.HasSuffix(norm, "/"+key) || strings.HasSuffix(key, "/"+norm) {
			return config.NormalizeChannelID(entry), true
		}
	}
	return "", false
}

// HandleDiscussionMessage 处理讨论超级群中的频道自动转发帖子（对齐 handle_discussion_message）。
func (h *Handler) HandleDiscussionMessage(ctx context.Context, msg *mtproto.IncomingMessage) {
	if msg == nil || !msg.IsSupergroup || msg.IsOut || msg.FwdFrom == nil || msg.FwdFrom.ChannelPost <= 0 {
		return
	}
	if h.deps.Config == nil {
		return
	}
	botCli := h.resolveBotClient()
	if botCli == nil {
		return
	}

	whitelist := h.deps.Config.Channels()
	if len(whitelist) == 0 {
		return
	}

	// 解析讨论群绑定的主频道并校验其等于转发源且在白名单内。
	linkedChatID, err := botCli.GetLinkedDiscussionChatID(ctx, strconv.FormatInt(msg.ChatID, 10))
	if err != nil || linkedChatID == 0 {
		return
	}
	if msg.FwdFrom.FromID != 0 && msg.FwdFrom.FromID != linkedChatID {
		return
	}

	var matchedChannelKey string
	var chInfo *mtproto.ChannelInfo
	if info, rerr := botCli.ResolveChannel(ctx, strconv.FormatInt(linkedChatID, 10)); rerr == nil && info != nil {
		chInfo = info
		if info.Username != "" {
			if k, ok := IsChannelWhitelisted(info.Username, whitelist); ok {
				matchedChannelKey = k
			}
		}
		if matchedChannelKey == "" && info.ID > 0 {
			if k, ok := IsChannelWhitelisted(strconv.FormatInt(info.ID, 10), whitelist); ok {
				matchedChannelKey = k
			}
		}
	}
	if matchedChannelKey == "" {
		bare := strings.TrimPrefix(strconv.FormatInt(linkedChatID, 10), "-100")
		if k, ok := IsChannelWhitelisted(bare, whitelist); ok {
			matchedChannelKey = k
		}
	}
	if matchedChannelKey == "" {
		return
	}

	// 去重检查（相册按 grouped_id 合并为单条欢迎，同时按 channel_post 抑制手动转发与自动同步重复触发）。
	postKey := fmt.Sprintf("%d_post_%d", msg.ChatID, msg.FwdFrom.ChannelPost)
	groupKey := ""
	if msg.GroupedID != 0 {
		groupKey = fmt.Sprintf("%d_grp_%d", msg.ChatID, msg.GroupedID)
	}
	now := time.Now().UTC()
	h.cacheMu.Lock()
	if ts, ok := h.cache[postKey]; ok && now.Sub(ts) < welcomeCacheTTL {
		h.cacheMu.Unlock()
		return
	}
	if groupKey != "" {
		if ts, ok := h.cache[groupKey]; ok && now.Sub(ts) < welcomeCacheTTL {
			h.cacheMu.Unlock()
			return
		}
		h.cache[groupKey] = now
	}
	h.cache[postKey] = now
	h.cacheMu.Unlock()

	h.mu.Lock()
	running := h.running
	q := h.queue
	h.mu.Unlock()
	if running && q != nil {
		select {
		case q <- welcomeTask{
			discussionID: msg.ChatID,
			forwardMsgID: msg.ID,
			channelID:    matchedChannelKey,
			channelMsgID: msg.FwdFrom.ChannelPost,
		}:
		default:
			h.deps.Logger.Warn("评论区欢迎队列已满，跳过本条欢迎", "discussion_id", msg.ChatID)
		}
	}

	// 联动触发自动趣味投票（若相册首图无文案则从源频道补充邻近消息文本）。
	if h.deps.AutoPoll != nil && h.deps.Config.IsAutoPollEnabledForChannel(matchedChannelKey) {
		msgText := msg.Text
		if strings.TrimSpace(msgText) == "" && msg.GroupedID != 0 {
			srcRef := matchedChannelKey
			if chInfo != nil && chInfo.MarkedID != 0 {
				srcRef = strconv.FormatInt(chInfo.MarkedID, 10)
			}
			if extra := h.collectSourceAlbumText(ctx, botCli, srcRef, msg.FwdFrom.ChannelPost); extra != "" {
				msgText = extra
			}
		}
		h.deps.AutoPoll.Enqueue(poll.AutoPollTask{
			ChannelID:    matchedChannelKey,
			DiscussionID: msg.ChatID,
			ForwardMsgID: msg.ID,
			MessageText:  msgText,
			MediaItem:    poll.IncomingToPollMediaItem(msg),
		})
	}
}

// HandleSummaryRequestCallback 处理讨论组欢迎卡片上的 req_summary:<channel>:<msg_id> 点击（对齐 handle_summary_request_callback）。
func (h *Handler) HandleSummaryRequestCallback(ctx context.Context, ev mtproto.CallbackEvent) error {
	botCli := h.resolveBotClient()
	if botCli == nil {
		return nil
	}
	rawChannel, msgID, ok := ParseSummaryCallbackData(ev.DataString())
	if !ok {
		return botCli.AnswerCallback(ctx, ev.QueryID, "无效的请求", false)
	}
	normChannel := config.NormalizeChannelID(rawChannel)

	if h.deps.Repo == nil {
		return botCli.AnswerCallback(ctx, ev.QueryID, "系统错误：数据库未就绪", true)
	}

	registered, err := h.deps.Repo.IsUserRegistered(ctx, ev.SenderID)
	if err != nil {
		return botCli.AnswerCallback(ctx, ev.QueryID, "系统错误：无法检查用户状态", true)
	}
	if !registered {
		qaUser := ""
		if h.deps.Config != nil {
			qaUser = h.deps.Config.QABotUsername()
		}
		if qaUser != "" {
			regBtn := [][]mtproto.InlineButton{{{
				Text: i18n.T("comment_welcome.register_button", nil),
				URL:  "https://t.me/" + qaUser,
			}}}
			_ = botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, i18n.T("comment_welcome.not_registered", nil), mtproto.SendOptions{
				Buttons: regBtn,
			})
			_ = botCli.AnswerCallback(ctx, ev.QueryID, "", false)

			h.wg.Add(1)
			go func() {
				defer h.wg.Done()
				h.restoreOriginalButton(botCli, ev.ChatID, ev.MessageID, rawChannel, msgID, ev.SenderID)
			}()
			return nil
		}
		return botCli.AnswerCallback(ctx, ev.QueryID, i18n.T("comment_welcome.not_registered", nil), true)
	}

	hasPending, err := h.deps.Repo.HasRecentSummaryRequest(ctx, 0, normChannel, 10*time.Minute)
	if err == nil && hasPending {
		return botCli.AnswerCallback(ctx, ev.QueryID, i18n.T("comment_welcome.already_requested", nil), true)
	}

	if _, err := h.deps.Repo.EnqueueSummaryRequest(ctx, ev.SenderID, normChannel, int64(msgID), nil); err != nil {
		return botCli.AnswerCallback(ctx, ev.QueryID, "处理请求时出错", true)
	}
	return botCli.AnswerCallback(ctx, ev.QueryID, i18n.T("comment_welcome.request_sent", nil), true)
}

func (h *Handler) restoreOriginalButton(
	botCli mtproto.Messenger,
	chatID int64,
	messageID int,
	rawChannel string,
	channelMsgID int,
	userID int64,
) {
	for i := 0; i < h.deps.RestoreChecks; i++ {
		time.Sleep(h.deps.RestorePollInt)
		if h.deps.Repo != nil {
			if ok, err := h.deps.Repo.IsUserRegistered(context.Background(), userID); err == nil && ok {
				break
			}
		}
	}
	msgText, btnRows := h.buildWelcomeContent(rawChannel, channelMsgID)
	if msgText == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = botCli.EditMessage(ctx, chatID, messageID, msgText, mtproto.SendOptions{Buttons: btnRows})
}

func (h *Handler) workerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-h.queue:
			delay := h.randomDelay()
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			h.sendWelcomeTask(ctx, task)
		}
	}
}

func (h *Handler) sendWelcomeTask(ctx context.Context, task welcomeTask) {
	botCli := h.resolveBotClient()
	if botCli == nil || h.deps.Config == nil {
		return
	}
	cw := h.deps.Config.CommentWelcomeSettings().GetForChannel(task.channelID)
	if !cw.Enabled {
		return
	}
	msgText, btnRows := h.buildWelcomeContent(task.channelID, task.channelMsgID)
	if msgText == "" {
		return
	}

	_, err := botCli.SendMessage(ctx, task.discussionID, msgText, mtproto.SendOptions{
		ReplyToMsgID: task.forwardMsgID,
		Buttons:      btnRows,
	})
	if err != nil {
		var fw *mtproto.FloodWaitError
		if errors.As(err, &fw) && fw.Seconds > 0 {
			timer := time.NewTimer(time.Duration(fw.Seconds) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			_, _ = botCli.SendMessage(ctx, task.discussionID, msgText, mtproto.SendOptions{
				ReplyToMsgID: task.forwardMsgID,
				Buttons:      btnRows,
			})
		}
	}
}

func (h *Handler) buildWelcomeContent(channelID string, channelMsgID int) (string, [][]mtproto.InlineButton) {
	cw := config.DefaultCommentWelcomeConfig()
	if h.deps.Config != nil {
		cw = h.deps.Config.CommentWelcomeSettings().GetForChannel(channelID)
	}
	msgText := cw.WelcomeMessage
	if strings.HasPrefix(msgText, "comment_welcome.") {
		msgText = i18n.T(msgText, nil)
	}
	btnText := cw.ButtonText
	if strings.HasPrefix(btnText, "comment_welcome.") {
		btnText = i18n.T(btnText, nil)
	}

	var rows [][]mtproto.InlineButton
	if cw.ButtonAction == "request_summary" {
		if cb, ok := BuildSummaryCallbackData(channelID, channelMsgID); ok {
			rows = [][]mtproto.InlineButton{{{Text: btnText, Data: cb}}}
		}
	}
	return msgText, rows
}

func (h *Handler) collectSourceAlbumText(ctx context.Context, botCli mtproto.Messenger, srcRef string, postID int) string {
	start := postID - 5
	if start < 1 {
		start = 1
	}
	ids := make([]int, 0, 11)
	for id := start; id <= postID+5; id++ {
		ids = append(ids, id)
	}
	msgs, err := botCli.GetMessages(ctx, srcRef, ids)
	if err != nil || len(msgs) == 0 {
		return ""
	}
	var targetGroup int64
	for _, m := range msgs {
		if m != nil && m.ID == postID {
			targetGroup = m.GroupedID
			break
		}
	}
	if targetGroup == 0 {
		return ""
	}
	var parts []string
	for _, m := range msgs {
		if m != nil && m.GroupedID == targetGroup && strings.TrimSpace(m.Text) != "" {
			parts = append(parts, strings.TrimSpace(m.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func (h *Handler) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			h.cacheMu.Lock()
			for k, ts := range h.cache {
				if now.Sub(ts) >= welcomeCacheTTL {
					delete(h.cache, k)
				}
			}
			h.cacheMu.Unlock()
		}
	}
}

func (h *Handler) randomDelay() time.Duration {
	minD := h.deps.MinDelay
	maxD := h.deps.MaxDelay
	if maxD <= 0 || minD < 0 {
		return 0
	}
	if maxD <= minD {
		return minD
	}
	span := maxD - minD
	return minD + time.Duration(rand.Int64N(int64(span)))
}

func (h *Handler) resolveBotClient() mtproto.Messenger {
	if h.deps.BotClient != nil {
		return h.deps.BotClient()
	}
	return nil
}
