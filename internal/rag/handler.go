// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package rag

import (
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
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/vectors"
)

const (
	DefaultBatchSize     = 5
	DefaultBatchInterval = 5 * time.Second
	DefaultQueueMaxSize  = 1000
)

// VectorWriter 抽象实时消息向量写入与删除接口（由 *vectors.Store 实现）。
type VectorWriter interface {
	PrepareMessageUpsert(ctx context.Context, embedFn vectors.EmbedFunc, items []vectors.MessageItem) (vectors.MessageUpsert, error)
	DeleteMessages(ctx context.Context, channelID string, messageIDs []int64) (int, error)
}

type ragQueueItem struct {
	vectorID        string
	generation      uint64
	channelID       string
	channelName     string
	channelUsername string
	messageID       int64
	senderID        int64
	text            string
	createdAt       time.Time
	imageJob        *db.MessageImageJob
}

// Stats 暴露实时 RAG 队列运行统计。
type Stats struct {
	Running        bool  `json:"running"`
	QueueSize      int   `json:"queue_size"`
	ProcessedCount int64 `json:"processed_count"`
	FailedCount    int64 `json:"failed_count"`
}

// Deps 配置实时 RAG 处理器。
type Deps struct {
	Config        *config.Config
	VectorStore   VectorWriter
	EmbedFn       vectors.EmbedFunc
	ImagePipeline *media.ImagePipeline
	ImageText     func(context.Context, string, int64) (string, string, bool, error)
	SourceArchDir string
	BatchSize     int
	BatchInterval time.Duration
	QueueMaxSize  int
	Logger        *slog.Logger
}

// Handler 监听白名单频道的新消息、编辑与删除事件，维护原文归档并批量异步更新向量库（对齐 realtime_rag_handler.py + realtime_rag_initializer.py）。
// 同时实现 media.IndexPublisher 供图片后台识别完成后同步写入向量。
type Handler struct {
	deps Deps

	runMu    sync.Mutex
	mu       sync.Mutex
	running  bool
	queue    chan ragQueueItem
	cancelBg context.CancelFunc
	wg       sync.WaitGroup

	// Serializes message revisions, archive changes and vector commits.
	// Embedding runs outside this lock.
	genMu       sync.Mutex
	generations map[string]uint64
	genSeq      uint64

	processed atomic.Int64
	failed    atomic.Int64
}

// NewHandler 创建实时 RAG 处理器。
func NewHandler(deps Deps) *Handler {
	if deps.BatchSize <= 0 {
		deps.BatchSize = DefaultBatchSize
	}
	if deps.BatchInterval <= 0 {
		deps.BatchInterval = DefaultBatchInterval
	}
	if deps.QueueMaxSize <= 0 {
		deps.QueueMaxSize = DefaultQueueMaxSize
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Handler{
		deps:        deps,
		generations: make(map[string]uint64),
	}
}

// SetImagePipeline binds the capture service during assembly, before Start.
func (h *Handler) SetImagePipeline(pipeline *media.ImagePipeline) {
	h.deps.ImagePipeline = pipeline
}

// Start 启动批量消费协程。
func (h *Handler) Start(ctx context.Context) {
	h.runMu.Lock()
	defer h.runMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running {
		return
	}
	h.queue = make(chan ragQueueItem, h.deps.QueueMaxSize)
	bgCtx, cancel := context.WithCancel(ctx)
	h.cancelBg = cancel
	h.running = true

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.workerLoop(bgCtx)
	}()
}

// Stop 优雅排空队列（最多等待 30 秒）并停止后台协程。
func (h *Handler) Stop() {
	h.runMu.Lock()
	defer h.runMu.Unlock()
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		return
	}
	h.running = false
	ch := h.queue
	cancel := h.cancelBg
	close(ch)
	h.mu.Unlock()
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
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
	for item := range ch {
		h.clearGenIfMatch(item.vectorID, item.generation)
	}
}

// Stats 返回当前队列深度与成功/失败计数。
func (h *Handler) Stats() Stats {
	h.mu.Lock()
	running := h.running
	qLen := 0
	if h.queue != nil {
		qLen = len(h.queue)
	}
	h.mu.Unlock()
	return Stats{
		Running:        running,
		QueueSize:      qLen,
		ProcessedCount: h.processed.Load(),
		FailedCount:    h.failed.Load(),
	}
}

// HandleIncomingMessage 处理实时监听到的频道新消息或编辑消息（校验白名单、登记图片识别并将文本入队）。
func (h *Handler) HandleIncomingMessage(ctx context.Context, msg *mtproto.IncomingMessage) bool {
	if msg == nil || !msg.IsBroadcast {
		return false
	}
	if !h.isWhitelistedChannel(msg) {
		return false
	}

	chID := media.ChannelKey(strconv.FormatInt(msg.ChatID, 10))
	if chID == "" || chID == "0" {
		return false
	}
	chName := msg.ChatTitle
	if chName == "" {
		chName = chID
	}

	h.genMu.Lock()
	defer h.genMu.Unlock()

	// 先登记或刷新图片流水线（无图编辑会自动使旧图片识别失效）。
	textToEnqueue := msg.Text
	if h.deps.ImagePipeline != nil {
		mediaID := ""
		var fileSize int64
		var raw any
		if msg.Media != nil && msg.Media.IsStaticImage() {
			mediaID = msg.Media.MediaID
			fileSize = msg.Media.FileSize
			raw = msg.Media.Raw
		}
		grouped := ""
		if msg.GroupedID != 0 {
			grouped = strconv.FormatInt(msg.GroupedID, 10)
		}
		_, err := h.deps.ImagePipeline.Capture(ctx, media.RemoteImageMessage{
			ChannelID:       chID,
			ChannelTitle:    chName,
			ChannelUsername: msg.ChatUsername,
			MessageID:       int64(msg.ID),
			GroupedID:       grouped,
			MediaID:         mediaID,
			FileSize:        fileSize,
			Caption:         msg.Text,
			Date:            msg.Date,
			RawMedia:        raw,
		})
		if err != nil {
			h.deps.Logger.Error("登记频道图片任务失败", "channel_id", chID, "message_id", msg.ID, "error", err)
		} else if mediaID != "" {
			if ocr, desc, ok, err := h.lookupRecognizedText(ctx, chID, int64(msg.ID)); err == nil && ok {
				textToEnqueue = media.CombinedText(msg.Text, ocr, desc)
			}
		}
	}

	if strings.TrimSpace(textToEnqueue) == "" {
		// 编辑移除了全部文字且无有效图片识别时，清理旧向量。
		if msg.EditDate != nil {
			_, _ = h.deleteIndexLocked(ctx, chID, int64(msg.ID))
		}
		return false
	}
	return h.enqueueMessageLocked(chID, chName, msg.ChatUsername, int64(msg.ID), msg.SenderID, textToEnqueue, msg.Date)
}

// HandleDeletedEvent 处理频道消息删除事件。
func (h *Handler) HandleDeletedEvent(ctx context.Context, ev mtproto.DeletedMessagesEvent) {
	chID := ""
	if ev.ChannelID > 0 {
		chID = strconv.FormatInt(ev.ChannelID, 10)
	} else if ev.ChatID < -1000000000000 {
		chID = media.ChannelKey(strconv.FormatInt(ev.ChatID, 10))
	}
	if chID == "" {
		return
	}
	for _, mid := range ev.MessageIDs {
		_, _ = h.HandleMessageDelete(ctx, chID, int64(mid))
	}
}

// EnqueueMessage 原子写入原文归档并将消息推入异步向量化队列（对齐 enqueue_message / enqueue_message_update）。
func (h *Handler) EnqueueMessage(
	channelID, channelName, channelUsername string,
	messageID, senderID int64,
	text string,
	createdAt time.Time,
) bool {
	h.genMu.Lock()
	defer h.genMu.Unlock()
	return h.enqueueMessageLocked(channelID, channelName, channelUsername, messageID, senderID, text, createdAt)
}

func (h *Handler) enqueueMessageLocked(
	channelID, channelName, channelUsername string,
	messageID, senderID int64,
	text string,
	createdAt time.Time,
) bool {
	if strings.TrimSpace(text) == "" || messageID <= 0 {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	q := h.queue
	if !h.running || q == nil {
		return false
	}

	chKey := media.ChannelKey(channelID)
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	} else {
		createdAt = createdAt.UTC()
	}
	vecID := fmt.Sprintf("%s:%d", chKey, messageID)
	gen := h.nextGenLocked(vecID)
	h.archiveMessage(chKey, channelName, channelUsername, messageID, senderID, text, createdAt)

	item := ragQueueItem{
		vectorID:        vecID,
		generation:      gen,
		channelID:       chKey,
		channelName:     channelName,
		channelUsername: channelUsername,
		messageID:       messageID,
		senderID:        senderID,
		text:            text,
		createdAt:       createdAt,
	}
	select {
	case q <- item:
		return true
	default:
		delete(h.generations, vecID)
		h.deps.Logger.Warn("实时 RAG 队列已满，背压丢弃消息", "channel_id", chKey, "message_id", messageID)
		return false
	}
}

// IndexImageText 实现 media.IndexPublisher，同步等待分块向量写入完成（供图片识别后台任务持久化调用）。
func (h *Handler) IndexImageText(ctx context.Context, job db.MessageImageJob, combinedText string) (bool, error) {
	chKey := media.ChannelKey(job.ChannelID)
	h.genMu.Lock()
	if pipeline := h.deps.ImagePipeline; pipeline != nil {
		current, err := pipeline.IsCurrentJob(ctx, job)
		if err != nil || !current {
			h.genMu.Unlock()
			return err == nil, err
		}
	}
	if strings.TrimSpace(combinedText) == "" {
		_, err := h.deleteIndexLocked(ctx, chKey, job.MessageID)
		h.genMu.Unlock()
		return err == nil, err
	}
	created := job.SourceDate
	if created.IsZero() {
		created = time.Now().UTC()
	}
	chUser := ""
	if job.ChannelUsername != nil {
		chUser = *job.ChannelUsername
	}
	vecID := fmt.Sprintf("%s:%d", chKey, job.MessageID)
	gen := h.nextGenLocked(vecID)
	h.archiveMessage(chKey, job.ChannelName, chUser, job.MessageID, 0, combinedText, created)
	h.genMu.Unlock()

	ok, err := h.processBatch(ctx, []ragQueueItem{{
		vectorID:        vecID,
		generation:      gen,
		channelID:       chKey,
		channelName:     job.ChannelName,
		channelUsername: chUser,
		messageID:       job.MessageID,
		text:            combinedText,
		createdAt:       created,
		imageJob:        &job,
	}})
	return ok, err
}

// HandleMessageDelete invalidates image work and removes the message from RAG.
func (h *Handler) HandleMessageDelete(ctx context.Context, channelID string, messageID int64) (bool, error) {
	chKey := media.ChannelKey(channelID)
	h.genMu.Lock()
	defer h.genMu.Unlock()

	if pipeline := h.deps.ImagePipeline; pipeline != nil {
		if err := pipeline.DeleteMessage(ctx, chKey, messageID); err != nil {
			h.deps.Logger.Error("撤销频道图片任务失败", "channel_id", chKey, "message_id", messageID, "error", err)
			return false, err
		}
	}
	return h.deleteIndexLocked(ctx, chKey, messageID)
}

func (h *Handler) deleteIndexLocked(ctx context.Context, chKey string, messageID int64) (bool, error) {
	vecID := fmt.Sprintf("%s:%d", chKey, messageID)
	delete(h.generations, vecID)
	sourceErr := vectors.DeleteSource(h.resolveArchiveDir(), chKey, messageID)
	if h.deps.VectorStore == nil {
		return false, sourceErr
	}
	n, err := h.deps.VectorStore.DeleteMessages(ctx, chKey, []int64{messageID})
	if err != nil {
		h.deps.Logger.Error("删除实时消息向量失败", "vector_id", vecID, "error", err)
		return false, errors.Join(sourceErr, err)
	}
	return n > 0, sourceErr
}

func (h *Handler) workerLoop(ctx context.Context) {
	ticker := time.NewTicker(h.deps.BatchInterval)
	defer ticker.Stop()

	batch := make([]ragQueueItem, 0, h.deps.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		flushCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		_, _ = h.processBatch(flushCtx, batch)
		cancel()
		batch = batch[:0]
	}

	for {
		select {
		case item, ok := <-h.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, item)
			if len(batch) >= h.deps.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			flush()
			return
		}
	}
}

func (h *Handler) processBatch(ctx context.Context, rawBatch []ragQueueItem) (bool, error) {
	defer func() {
		for _, item := range rawBatch {
			h.clearGenIfMatch(item.vectorID, item.generation)
		}
	}()
	if len(rawBatch) == 0 || h.deps.VectorStore == nil || h.deps.EmbedFn == nil {
		return false, nil
	}

	// 按代数去重，丢弃已被新编辑或删除覆盖的陈旧入队项（对齐 realtime_rag_handler.py:_generations）。
	active := make([]ragQueueItem, 0, len(rawBatch))
	for _, item := range rawBatch {
		if h.isCurrentGen(item.vectorID, item.generation) {
			active = append(active, item)
		}
	}
	if len(active) == 0 {
		return true, nil
	}

	msgItems := make([]vectors.MessageItem, 0, len(active))
	for _, it := range active {
		msgItems = append(msgItems, vectors.MessageItem{
			ChannelID:       it.channelID,
			ChannelName:     it.channelName,
			ChannelUsername: it.channelUsername,
			MessageID:       it.messageID,
			SenderID:        it.senderID,
			Text:            it.text,
			CreatedAt:       it.createdAt,
		})
	}

	commit, err := h.deps.VectorStore.PrepareMessageUpsert(ctx, h.deps.EmbedFn, msgItems)
	if err == nil && commit == nil {
		return true, nil
	}
	committed := 0
	if err == nil {
		committed, err = h.commitCurrentMessages(ctx, active, commit)
	}
	if err != nil {
		h.failed.Add(int64(len(active)))
		h.deps.Logger.Error("批量写入实时 RAG 消息向量失败", "count", len(active), "error", err)
		return false, err
	}

	h.processed.Add(int64(committed))
	return true, nil
}

func (h *Handler) commitCurrentMessages(ctx context.Context, active []ragQueueItem, commit vectors.MessageUpsert) (int, error) {
	h.genMu.Lock()
	defer h.genMu.Unlock()

	current := make(map[string]bool, len(active))
	for _, item := range active {
		if h.generations[item.vectorID] != item.generation {
			continue
		}
		if pipeline := h.deps.ImagePipeline; pipeline != nil && item.imageJob != nil {
			valid, err := pipeline.IsCurrentJob(ctx, *item.imageJob)
			if err != nil {
				return 0, err
			}
			if !valid {
				continue
			}
		}
		current[item.vectorID] = true
	}
	_, err := commit(func(channelID string, messageID int64) bool {
		return current[fmt.Sprintf("%s:%d", channelID, messageID)]
	})
	return len(current), err
}

func (h *Handler) isWhitelistedChannel(msg *mtproto.IncomingMessage) bool {
	if h.deps.Config == nil {
		return false
	}
	channels := h.deps.Config.Channels()
	if len(channels) == 0 {
		return false
	}
	candidates := []string{
		strconv.FormatInt(msg.ChatID, 10),
		media.ChannelKey(strconv.FormatInt(msg.ChatID, 10)),
	}
	if msg.ChatUsername != "" {
		candidates = append(candidates, msg.ChatUsername)
	}
	for _, cand := range candidates {
		key := config.ComparableChannelKey(cand)
		if key == "" {
			continue
		}
		for _, entry := range channels {
			norm := config.ComparableChannelKey(entry)
			if norm == key || strings.HasSuffix(norm, "/"+key) || strings.HasSuffix(key, "/"+norm) {
				return true
			}
		}
	}
	return false
}

func (h *Handler) archiveMessage(chKey, chName, chUser string, msgID, senderID int64, text string, createdAt time.Time) {
	slug := chUser
	if slug == "" {
		slug = "c/" + chKey
	}
	_ = vectors.ArchiveSource(h.resolveArchiveDir(), vectors.SourceRecord{
		ChannelID:       chKey,
		MessageID:       msgID,
		Text:            text,
		CreatedAt:       createdAt.UTC().Format(time.RFC3339),
		ChannelName:     chName,
		ChannelUsername: chUser,
		PostURL:         fmt.Sprintf("https://t.me/%s/%d", slug, msgID),
		SenderID:        senderID,
	})
}

func (h *Handler) lookupRecognizedText(ctx context.Context, channelID string, messageID int64) (string, string, bool, error) {
	if h.deps.ImageText == nil {
		return "", "", false, nil
	}
	return h.deps.ImageText(ctx, channelID, messageID)
}

func (h *Handler) resolveArchiveDir() string {
	if h.deps.SourceArchDir != "" {
		return h.deps.SourceArchDir
	}
	if h.deps.Config != nil {
		return h.deps.Config.MessageSourceDir()
	}
	return config.DefaultMessageSourceDir
}

func (h *Handler) nextGenLocked(vecID string) uint64 {
	h.genSeq++
	h.generations[vecID] = h.genSeq
	return h.genSeq
}

func (h *Handler) clearGenIfMatch(vecID string, gen uint64) {
	h.genMu.Lock()
	if h.generations[vecID] == gen {
		delete(h.generations, vecID)
	}
	h.genMu.Unlock()
}

func (h *Handler) isCurrentGen(vecID string, gen uint64) bool {
	h.genMu.Lock()
	cur := h.generations[vecID]
	h.genMu.Unlock()
	return cur == gen && cur > 0
}
