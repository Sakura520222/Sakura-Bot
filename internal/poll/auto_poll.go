// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package poll

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

const (
	autoPollQueueMaxSize = 500
	autoPollCacheTTL     = time.Hour
	autoPollCacheMaxSize = 10000
)

// AutoPollTask 表示单条入队的讨论组自动趣味投票任务。
type AutoPollTask struct {
	ChannelID    string
	DiscussionID int64
	ForwardMsgID int
	MessageText  string
	MediaItem    *media.PollMediaItem
}

// AutoPollStats 暴露自动趣味投票运行指标。
type AutoPollStats struct {
	Running        bool  `json:"running"`
	QueueSize      int   `json:"queue_size"`
	ProcessedCount int64 `json:"processed_count"`
	FailedCount    int64 `json:"failed_count"`
}

// AutoPollDeps 配置 AutoPollHandler。
type AutoPollDeps struct {
	Config      *config.Config
	BotClient   func() mtproto.Messenger
	MediaClient media.PollMediaClient // 可选测试覆盖；为空时自动封装 BotClient
	Completer   Completer
	Pipeline    *media.ImagePipeline
	SettleDelay time.Duration
	Logger      *slog.Logger
}

type lruEntry struct {
	key string
	at  time.Time
}

// AutoPollHandler 异步队列处理讨论组中的频道帖子并生成趣味投票（对齐 auto_poll_handler.py:AutoPollHandler）。
type AutoPollHandler struct {
	deps AutoPollDeps

	mu        sync.Mutex
	running   bool
	queue     chan AutoPollTask
	cancelBg  context.CancelFunc
	workerWG  sync.WaitGroup
	cleanupWG sync.WaitGroup

	cacheMu sync.Mutex
	lruList *list.List
	lruMap  map[string]*list.Element

	processed atomic.Int64
	failed    atomic.Int64
}

// NewAutoPollHandler 创建自动趣味投票处理器。
func NewAutoPollHandler(deps AutoPollDeps) *AutoPollHandler {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &AutoPollHandler{
		deps:    deps,
		lruList: list.New(),
		lruMap:  make(map[string]*list.Element),
	}
}

// Start 启动后台队列消费与缓存清理协程。
func (h *AutoPollHandler) Start(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running {
		return
	}
	h.queue = make(chan AutoPollTask, autoPollQueueMaxSize)
	bgCtx, cancel := context.WithCancel(ctx)
	h.cancelBg = cancel
	h.running = true

	h.workerWG.Add(1)
	go func() {
		defer h.workerWG.Done()
		h.workerLoop(bgCtx)
	}()

	h.cleanupWG.Add(1)
	go func() {
		defer h.cleanupWG.Done()
		h.cleanupLoop(bgCtx)
	}()
}

// Stop 优雅排空队列（最多等待 30 秒）并停止后台协程。
func (h *AutoPollHandler) Stop() {
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		return
	}
	h.running = false
	ch := h.queue
	cancel := h.cancelBg
	h.mu.Unlock()

	close(ch)
	done := make(chan struct{})
	go func() {
		h.workerWG.Wait()
		close(done)
	}()

	timer := time.NewTimer(30 * time.Second)
	select {
	case <-done:
		timer.Stop()
	case <-timer.C:
		if cancel != nil {
			cancel()
		}
		<-done
	}
	if cancel != nil {
		cancel()
	}
	h.cleanupWG.Wait()
}

// Enqueue 校验开关、去重缓存与容量后将任务入队（对齐 enqueue_poll_task）。
func (h *AutoPollHandler) Enqueue(task AutoPollTask) bool {
	hasMedia := task.MediaItem != nil && task.MediaItem.HasPollMedia()
	if strings.TrimSpace(task.MessageText) == "" && !hasMedia {
		return false
	}

	h.mu.Lock()
	running := h.running
	q := h.queue
	h.mu.Unlock()
	if !running || q == nil {
		return false
	}

	if h.deps.Config != nil && !h.deps.Config.IsAutoPollEnabledForChannel(task.ChannelID) {
		return false
	}

	cacheKey := fmt.Sprintf("%d_%d", task.DiscussionID, task.ForwardMsgID)
	now := time.Now().UTC()
	h.cacheMu.Lock()
	if el, exists := h.lruMap[cacheKey]; exists {
		ent := el.Value.(*lruEntry)
		if now.Sub(ent.at) < autoPollCacheTTL {
			h.cacheMu.Unlock()
			return false
		}
		h.lruList.Remove(el)
		delete(h.lruMap, cacheKey)
	}
	task.MessageText = strings.TrimSpace(task.MessageText)
	select {
	case q <- task:
		el := h.lruList.PushBack(&lruEntry{key: cacheKey, at: now})
		h.lruMap[cacheKey] = el
		for h.lruList.Len() > autoPollCacheMaxSize {
			oldest := h.lruList.Front()
			if oldest == nil {
				break
			}
			h.lruList.Remove(oldest)
			delete(h.lruMap, oldest.Value.(*lruEntry).key)
		}
		h.cacheMu.Unlock()
		return true
	default:
		h.cacheMu.Unlock()
		h.deps.Logger.Warn("趣味投票队列已满，背压丢弃任务", "channel", task.ChannelID, "forward_msg_id", task.ForwardMsgID)
		return false
	}
}

// Stats 返回当前运行统计。
func (h *AutoPollHandler) Stats() AutoPollStats {
	h.mu.Lock()
	running := h.running
	qLen := 0
	if h.queue != nil {
		qLen = len(h.queue)
	}
	h.mu.Unlock()
	return AutoPollStats{
		Running:        running,
		QueueSize:      qLen,
		ProcessedCount: h.processed.Load(),
		FailedCount:    h.failed.Load(),
	}
}

func (h *AutoPollHandler) workerLoop(ctx context.Context) {
	for task := range h.queue {
		if ctx.Err() != nil {
			return
		}
		h.processTask(ctx, task)
	}
}

func (h *AutoPollHandler) processTask(ctx context.Context, task AutoPollTask) {
	if h.deps.Config != nil && !h.deps.Config.IsAutoPollEnabledForChannel(task.ChannelID) {
		return
	}
	botCli := h.resolveBotClient()
	if botCli == nil {
		h.failed.Add(1)
		return
	}
	mediaCli := h.deps.MediaClient
	if mediaCli == nil {
		mediaCli = &mtprotoPollMediaAdapter{bot: botCli}
	}

	text, images, err := media.CollectPollImages(ctx, mediaCli, task.MediaItem, task.DiscussionID, task.MessageText, media.CollectPollOptions{
		SettleDelay: h.deps.SettleDelay,
		Pipeline:    h.deps.Pipeline,
	})
	if err != nil {
		h.failed.Add(1)
		h.deps.Logger.Warn("采集趣味投票图片失败", "channel", task.ChannelID, "error", err)
		return
	}
	if len([]rune(strings.TrimSpace(text))) < 10 && len(images) == 0 {
		return
	}

	spec, gerr := GeneratePollFromSummary(ctx, text, images, GenerateOptions{
		Config:    h.deps.Config,
		Completer: h.deps.Completer,
		Pipeline:  h.deps.Pipeline,
		Logger:    h.deps.Logger,
	})
	if len(images) > 0 && (gerr != nil || spec == nil) {
		h.failed.Add(1)
		h.deps.Logger.Warn("图片趣味投票生成失败，已跳过", "channel", task.ChannelID, "error", gerr)
		return
	}
	if spec == nil {
		spec = DefaultPollSpec()
	}

	// 发送前再次热读校验配置，确保生成期间 WebUI 禁用后立即拦截（对齐 auto_poll_handler.py:301）。
	if h.deps.Config != nil && !h.deps.Config.IsAutoPollEnabledForChannel(task.ChannelID) {
		h.deps.Logger.Info("频道自动趣味投票在生成期间已禁用，取消发送", "channel", task.ChannelID)
		return
	}

	pubVoters := true
	if h.deps.Config != nil {
		pubVoters = h.deps.Config.PollPublicVoters()
	}
	if _, serr := botCli.SendPoll(ctx, task.DiscussionID, mtproto.PollSendOptions{
		Question:     clampRunes(spec.Question, 250, 247),
		Options:      spec.Options,
		PublicVoters: pubVoters,
		ReplyToMsgID: task.ForwardMsgID,
	}); serr != nil {
		h.failed.Add(1)
		h.deps.Logger.Error("发送自动趣味投票失败", "channel", task.ChannelID, "error", serr)
		return
	}
	h.processed.Add(1)
}

func (h *AutoPollHandler) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			h.cacheMu.Lock()
			for el := h.lruList.Front(); el != nil; {
				next := el.Next()
				ent := el.Value.(*lruEntry)
				if now.Sub(ent.at) >= autoPollCacheTTL {
					h.lruList.Remove(el)
					delete(h.lruMap, ent.key)
				}
				el = next
			}
			h.cacheMu.Unlock()
		}
	}
}

func (h *AutoPollHandler) resolveBotClient() mtproto.Messenger {
	if h.deps.BotClient != nil {
		return h.deps.BotClient()
	}
	return nil
}

// IncomingToPollMediaItem 将 IncomingMessage 转换为趣味投票媒体项。
func IncomingToPollMediaItem(msg *mtproto.IncomingMessage) *media.PollMediaItem {
	if msg == nil {
		return nil
	}
	item := &media.PollMediaItem{
		ID:        msg.ID,
		GroupedID: msg.GroupedID,
		Text:      msg.Text,
	}
	if msg.Media != nil {
		item.IsStaticImage = msg.Media.IsStaticImage()
		item.MediaID = msg.Media.MediaID
		item.FileSize = msg.Media.FileSize
		item.RawMedia = msg.Media
	}
	if msg.FwdFrom != nil && msg.FwdFrom.FromID < -1000000000000 && msg.FwdFrom.ChannelPost > 0 {
		item.FwdChannelID = strconv.FormatInt(msg.FwdFrom.FromID, 10)
		item.FwdChannelPostID = msg.FwdFrom.ChannelPost
	}
	return item
}

type mtprotoPollMediaAdapter struct {
	bot mtproto.Messenger
}

func (a *mtprotoPollMediaAdapter) GetPollMessages(ctx context.Context, discussionID int64, ids []int) ([]media.PollMediaItem, error) {
	msgs, err := a.bot.GetMessages(ctx, discussionID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]media.PollMediaItem, 0, len(msgs))
	for _, m := range msgs {
		if item := IncomingToPollMediaItem(m); item != nil {
			out = append(out, *item)
		}
	}
	return out, nil
}

func (a *mtprotoPollMediaAdapter) FetchOriginalPostImage(ctx context.Context, channelRef string, postID int) (*media.RemoteImageMessage, error) {
	msgs, err := a.bot.GetMessages(ctx, channelRef, []int{postID})
	if err != nil || len(msgs) == 0 || msgs[0] == nil {
		return nil, err
	}
	m := msgs[0]
	mediaID := ""
	var fileSize int64
	var raw any
	if m.Media != nil && m.Media.IsStaticImage() {
		mediaID = m.Media.MediaID
		fileSize = m.Media.FileSize
		raw = m.Media.Raw
	}
	grouped := ""
	if m.GroupedID != 0 {
		grouped = strconv.FormatInt(m.GroupedID, 10)
	}
	return &media.RemoteImageMessage{
		ChannelID:       media.ChannelKey(channelRef),
		ChannelTitle:    m.ChatTitle,
		ChannelUsername: m.ChatUsername,
		MessageID:       int64(m.ID),
		GroupedID:       grouped,
		MediaID:         mediaID,
		FileSize:        fileSize,
		Caption:         m.Text,
		Date:            m.Date,
		RawMedia:        raw,
	}, nil
}

func (a *mtprotoPollMediaAdapter) DownloadPollImageBytes(ctx context.Context, item media.PollMediaItem, maxBytes int64) ([]byte, error) {
	att, ok := item.RawMedia.(*mtproto.MediaAttachment)
	if !ok || att == nil {
		return nil, errors.New("missing_media_attachment")
	}
	data, err := a.bot.DownloadMediaBytes(ctx, att, maxBytes)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "exceed") {
		return nil, media.ErrPollImageTooLarge
	}
	return data, err
}
