// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package forwarding

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

// Repo 抽象转发去重与统计仓储（由 *db.Manager 实现）。
type Repo interface {
	IsMessageKeyForwarded(ctx context.Context, sourceChannel, messageKey, targetChannel string) (bool, error)
	AddForwardedMessage(ctx context.Context, rec db.ForwardedRecord) (bool, error)
	UpdateForwardingStats(ctx context.Context, channelID string, increment int) error
}

// ServiceDeps 配置频道消息转发服务。
type ServiceDeps struct {
	Config           *config.Config
	Repo             Repo
	MonitoringClient func() mtproto.Messenger // 优先 UserBot，否则回退 Bot
	SendingClient    func() mtproto.Messenger // 主 Bot 发送客户端
	DownloadManager  *DownloadManager
	AlbumSettleDelay time.Duration
	CacheDir         string
	Logger           *slog.Logger
}

type pendingAlbum struct {
	msgs  map[int]*mtproto.IncomingMessage
	timer *time.Timer
}

// Service 实现三阶段过滤、相册聚合、双客户端转发降级、底栏渲染与去重统计（对齐 forwarding_handler.py + forwarding_initializer.py）。
type Service struct {
	deps ServiceDeps

	albumMu sync.Mutex
	albums  map[string]*pendingAlbum
	wg      sync.WaitGroup
}

// NewService 创建频道转发服务。
func NewService(deps ServiceDeps) *Service {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.DownloadManager == nil {
		deps.DownloadManager = NewDownloadManager(deps.CacheDir, 2, deps.Logger)
	}
	if deps.AlbumSettleDelay == 0 {
		deps.AlbumSettleDelay = time.Second
	}
	return &Service{
		deps:   deps,
		albums: make(map[string]*pendingAlbum),
	}
}

// Stop 等待正在刷新的相册任务结束。
func (s *Service) Stop() {
	s.albumMu.Lock()
	for k, pa := range s.albums {
		if pa.timer != nil {
			pa.timer.Stop()
		}
		delete(s.albums, k)
	}
	s.albumMu.Unlock()
	s.wg.Wait()
}

// HandleIncomingMessage 处理监听到的频道新消息（单条立即转发，相册按 grouped_id 聚合后一并过滤与转发，修复首张无文案图片漏采问题）。
func (s *Service) HandleIncomingMessage(ctx context.Context, msg *mtproto.IncomingMessage) {
	if msg == nil || !msg.IsBroadcast {
		return
	}
	if s.deps.Config == nil {
		return
	}
	fwdCfg := s.deps.Config.ForwardingConfig()
	if !fwdCfg.Enabled || len(fwdCfg.Rules) == 0 {
		return
	}
	if len(matchForwardingRules(msg, fwdCfg.Rules)) == 0 {
		return
	}

	if msg.GroupedID == 0 || s.deps.AlbumSettleDelay < 0 {
		_ = s.ProcessGroup(ctx, []*mtproto.IncomingMessage{msg})
		return
	}

	albumKey := fmt.Sprintf("%d_%d", msg.ChatID, msg.GroupedID)
	s.albumMu.Lock()
	pa, exists := s.albums[albumKey]
	if !exists {
		pa = &pendingAlbum{msgs: make(map[int]*mtproto.IncomingMessage)}
		s.albums[albumKey] = pa
		pa.msgs[msg.ID] = msg
		s.wg.Add(1)
		pa.timer = time.AfterFunc(s.deps.AlbumSettleDelay, func() {
			defer s.wg.Done()
			s.flushAlbum(albumKey)
		})
	} else {
		pa.msgs[msg.ID] = msg
	}
	s.albumMu.Unlock()
}

func (s *Service) flushAlbum(albumKey string) {
	s.albumMu.Lock()
	pa, ok := s.albums[albumKey]
	if ok {
		delete(s.albums, albumKey)
	}
	s.albumMu.Unlock()
	if !ok || len(pa.msgs) == 0 {
		return
	}

	ordered := make([]*mtproto.IncomingMessage, 0, len(pa.msgs))
	for _, m := range pa.msgs {
		ordered = append(ordered, m)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_ = s.ProcessGroup(ctx, ordered)
}

// ProcessGroup 执行单条消息或已排序媒体组对所有匹配规则的过滤、去重、转发与统计更新。
func (s *Service) ProcessGroup(ctx context.Context, msgs []*mtproto.IncomingMessage) bool {
	if len(msgs) == 0 || s.deps.Config == nil {
		return false
	}
	fwdCfg := s.deps.Config.ForwardingConfig()
	if !fwdCfg.Enabled {
		return false
	}

	first := msgs[0]
	sourceKey := messageChannelKey(first)
	if sourceKey == "" {
		return false
	}

	matched := matchForwardingRules(first, fwdCfg.Rules)
	if len(matched) == 0 {
		return false
	}

	msgDedupKey := strconv.Itoa(first.ID)
	if len(msgs) > 1 && first.GroupedID != 0 {
		msgDedupKey = fmt.Sprintf("group_%d", first.GroupedID)
	}

	anyForwarded := false
	for _, rule := range matched {
		target := strings.TrimSpace(rule.TargetChannel)
		if target == "" {
			continue
		}
		if s.deps.Repo != nil {
			already, err := s.deps.Repo.IsMessageKeyForwarded(ctx, sourceKey, msgDedupKey, target)
			if err == nil && already {
				continue
			}
		}
		if !ShouldForwardGroup(msgs, rule, s.deps.Logger) {
			continue
		}

		if err := s.executeForward(ctx, msgs, sourceKey, target, rule, fwdCfg.ShowDefaultFooter); err != nil {
			s.deps.Logger.Error("执行频道消息转发失败",
				"source", sourceKey,
				"target", target,
				"key", msgDedupKey,
				"error", err,
			)
			continue
		}

		anyForwarded = true
		if s.deps.Repo != nil {
			ts := first.Date.UTC().Unix()
			if first.Date.IsZero() {
				ts = time.Now().UTC().Unix()
			}
			_, _ = s.deps.Repo.AddForwardedMessage(ctx, db.ForwardedRecord{
				MessageKey:    msgDedupKey,
				SourceMsgID:   int64(first.ID),
				GroupedID:     first.GroupedID,
				SourceChannel: sourceKey,
				TargetChannel: target,
				ContentHash:   hashMessagesMD5(msgs),
				Timestamp:     ts,
				ForwardStatus: "success",
				MessageType:   string(DetectMediaType(first)),
			})
			_ = s.deps.Repo.UpdateForwardingStats(ctx, sourceKey, 1)
		}
	}
	return anyForwarded
}

func (s *Service) executeForward(
	ctx context.Context,
	msgs []*mtproto.IncomingMessage,
	sourceKey string,
	targetChannel string,
	rule config.ForwardingRule,
	showDefaultFooter bool,
) error {
	sendCli := s.resolveSendingClient()
	monCli := s.resolveMonitoringClient()
	if sendCli == nil {
		return fmt.Errorf("sending client unavailable")
	}
	if monCli == nil {
		monCli = sendCli
	}

	first := msgs[0]
	footer := s.RenderFooter(ctx, monCli, first, sourceKey, targetChannel, rule, showDefaultFooter)
	caption := joinCaptionAndFooter(extractGroupText(msgs), footer)

	// 非复制模式且无需附加底栏时（或原生转发优先路径）：仅当未开 copy_mode 且未加底栏？
	// 注意：Python 版当 copy_mode=False 且无需改写文案时走 forward_messages，若需加底栏或原生转发因权限/noforwards 失败则自动降级为复制重发！
	if !rule.CopyMode && footer == "" {
		ids := make([]int, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		fromPeer := any(first.ChatID)
		if first.ChatUsername != "" {
			fromPeer = "@" + first.ChatUsername
		}
		if _, err := sendCli.ForwardMessages(ctx, fromPeer, targetChannel, ids); err == nil {
			return nil
		}
		if monCli != sendCli {
			if _, err := monCli.ForwardMessages(ctx, fromPeer, targetChannel, ids); err == nil {
				return nil
			}
		}
	}

	// 纯文本消息直接发送长文本
	hasMedia := false
	for _, m := range msgs {
		if m.Media != nil {
			hasMedia = true
			break
		}
	}
	if !hasMedia {
		if strings.TrimSpace(caption) == "" {
			return nil
		}
		_, err := sendCli.SendLongMessage(ctx, targetChannel, caption, mtproto.SendOptions{
			ParseMode: "markdown",
			NoWebpage: true,
		})
		return err
	}

	decision := DecideForwardStrategy(msgs, DefaultSmallFileThreshold, DefaultMediaGroupThreshold)
	if decision.Strategy == StrategyMemory {
		if err := s.forwardViaMemory(ctx, monCli, sendCli, msgs, targetChannel, caption); err == nil {
			return nil
		}
		s.deps.Logger.Warn("内存转发失败，自动降级为落盘缓存下载转发", "source", sourceKey, "target", targetChannel)
	}

	forwardID := strconv.Itoa(first.ID)
	if first.GroupedID != 0 {
		forwardID = fmt.Sprintf("group_%d", first.GroupedID)
	}
	defer s.deps.DownloadManager.CleanupCache(forwardID)

	paths, err := s.deps.DownloadManager.DownloadGroup(ctx, monCli, msgs, forwardID)
	if err != nil || len(paths) == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("no media files downloaded")
	}

	uploads := make([]mtproto.MediaUpload, 0, len(paths))
	mediaIdx := 0
	for _, m := range msgs {
		if m.Media == nil || mediaIdx >= len(paths) {
			continue
		}
		up := mtproto.MediaUpload{
			FilePath:      paths[mediaIdx],
			FileName:      filepath.Base(paths[mediaIdx]),
			MimeType:      m.Media.MimeType,
			ForceDocument: m.Media.Kind == "document",
		}
		if len(uploads) == 0 {
			up.Caption = caption
			up.ParseMode = "markdown"
		}
		uploads = append(uploads, up)
		mediaIdx++
	}
	if len(uploads) == 1 {
		_, err = sendCli.SendMedia(ctx, targetChannel, uploads[0], mtproto.SendOptions{
			ParseMode: "markdown",
			NoWebpage: true,
		})
		return err
	}
	_, err = sendCli.SendAlbum(ctx, targetChannel, uploads, mtproto.SendOptions{
		ParseMode: "markdown",
		NoWebpage: true,
	})
	return err
}

func (s *Service) forwardViaMemory(
	ctx context.Context,
	monCli, sendCli mtproto.Messenger,
	msgs []*mtproto.IncomingMessage,
	targetChannel string,
	caption string,
) error {
	uploads := make([]mtproto.MediaUpload, 0, len(msgs))
	for i, m := range msgs {
		if m.Media == nil {
			continue
		}
		raw, err := monCli.DownloadMediaBytes(ctx, m.Media, DefaultMediaGroupThreshold)
		if err != nil {
			return err
		}
		up := mtproto.MediaUpload{
			Data:          raw,
			FileName:      buildMediaFilename(m, i),
			MimeType:      m.Media.MimeType,
			ForceDocument: m.Media.Kind == "document",
		}
		if len(uploads) == 0 {
			up.Caption = caption
			up.ParseMode = "markdown"
		}
		uploads = append(uploads, up)
	}
	if len(uploads) == 0 {
		return fmt.Errorf("empty media uploads")
	}
	if len(uploads) == 1 {
		_, err := sendCli.SendMedia(ctx, targetChannel, uploads[0], mtproto.SendOptions{
			ParseMode: "markdown",
			NoWebpage: true,
		})
		return err
	}
	_, err := sendCli.SendAlbum(ctx, targetChannel, uploads, mtproto.SendOptions{
		ParseMode: "markdown",
		NoWebpage: true,
	})
	return err
}

// RenderFooter 生成转发消息底栏（支持自定义模板、默认助推/助手/投稿底栏与隐藏底栏三种模式，对齐 _generate_footer）。
func (s *Service) RenderFooter(
	ctx context.Context,
	monCli mtproto.Messenger,
	msg *mtproto.IncomingMessage,
	sourceChannel string,
	targetChannel string,
	rule config.ForwardingRule,
	showDefaultFooter bool,
) string {
	sourceUsername := msg.ChatUsername
	sourceTitle := msg.ChatTitle
	if (sourceTitle == "" || sourceUsername == "") && monCli != nil {
		if info, err := monCli.ResolveChannel(ctx, strconv.FormatInt(msg.ChatID, 10)); err == nil && info != nil {
			if sourceTitle == "" {
				sourceTitle = info.Title
			}
			if sourceUsername == "" {
				sourceUsername = info.Username
			}
		}
	}
	if sourceTitle == "" {
		sourceTitle = "Unknown"
	}

	var sourceLink string
	if sourceUsername != "" {
		sourceLink = fmt.Sprintf("https://t.me/%s/%d", sourceUsername, msg.ID)
	} else {
		rawID := strings.TrimPrefix(strconv.FormatInt(msg.ChatID, 10), "-100")
		rawID = strings.TrimPrefix(rawID, "-")
		sourceLink = fmt.Sprintf("https://t.me/c/%s/%d", rawID, msg.ID)
	}

	targetTitle := targetChannel
	targetUsername := extractPublicSlug(targetChannel)
	if monCli != nil {
		if tInfo, err := monCli.ResolveChannel(ctx, targetChannel); err == nil && tInfo != nil {
			if tInfo.Title != "" {
				targetTitle = tInfo.Title
			}
			if tInfo.Username != "" {
				targetUsername = tInfo.Username
			}
		}
	}

	qaUser := ""
	if s.deps.Config != nil {
		qaUser = s.deps.Config.QABotUsername()
	}
	assistantLink := ""
	submissionLink := ""
	if qaUser != "" {
		assistantLink = fmt.Sprintf("[助手BOT](https://t.me/%s)", qaUser)
		submissionLink = fmt.Sprintf("[投稿](https://t.me/%s?start=submit)", qaUser)
	}

	custom := strings.TrimSpace(rule.CustomFooter)
	if custom != "" {
		targetVal := targetChannel
		if targetUsername != "" {
			targetVal = targetUsername
		}
		replacer := strings.NewReplacer(
			"{source_link}", sourceLink,
			"{source_title}", sourceTitle,
			"{target_title}", targetTitle,
			"{source_channel}", sourceChannel,
			"{target_channel}", targetVal,
			"{message_id}", strconv.Itoa(msg.ID),
			"{assistant_bot}", assistantLink,
			"{submission}", submissionLink,
		)
		return replacer.Replace(custom)
	}

	if !showDefaultFooter {
		return ""
	}

	parts := []string{fmt.Sprintf("[Source](%s)", sourceLink)}
	if targetUsername != "" {
		parts = append(parts, "@"+targetUsername)
		parts = append(parts, fmt.Sprintf("\n🌸[助推](https://t.me/boost/%s)", targetUsername))
	} else {
		parts = append(parts, targetChannel)
	}
	if assistantLink != "" {
		parts = append(parts, "| "+assistantLink)
		parts = append(parts, "| "+submissionLink)
	}
	return strings.Join(parts, " ")
}

func (s *Service) resolveMonitoringClient() mtproto.Messenger {
	if s.deps.MonitoringClient != nil {
		if cli := s.deps.MonitoringClient(); cli != nil {
			return cli
		}
	}
	return s.resolveSendingClient()
}

func (s *Service) resolveSendingClient() mtproto.Messenger {
	if s.deps.SendingClient != nil {
		return s.deps.SendingClient()
	}
	return nil
}

func matchForwardingRules(msg *mtproto.IncomingMessage, rules []config.ForwardingRule) []config.ForwardingRule {
	var matched []config.ForwardingRule
	chatIDStr := strconv.FormatInt(msg.ChatID, 10)
	bareIDStr := strings.TrimPrefix(chatIDStr, "-100")
	unameLower := strings.ToLower(strings.TrimPrefix(msg.ChatUsername, "@"))

	for _, r := range rules {
		src := strings.TrimSpace(r.SourceChannel)
		if src == "" {
			continue
		}
		slug := strings.ToLower(strings.TrimPrefix(extractLastSegment(src), "@"))
		if slug == "" {
			continue
		}
		if (unameLower != "" && slug == unameLower) || slug == strings.ToLower(chatIDStr) || slug == bareIDStr {
			matched = append(matched, r)
		}
	}
	return matched
}

func messageChannelKey(msg *mtproto.IncomingMessage) string {
	if msg.ChatUsername != "" {
		return msg.ChatUsername
	}
	if msg.ChatID != 0 {
		return strings.TrimPrefix(strconv.FormatInt(msg.ChatID, 10), "-100")
	}
	return ""
}

func extractGroupText(msgs []*mtproto.IncomingMessage) string {
	seen := make(map[string]bool)
	var parts []string
	for _, m := range msgs {
		if m == nil {
			continue
		}
		t := strings.TrimSpace(m.Text)
		if t != "" && !seen[t] {
			seen[t] = true
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}

func joinCaptionAndFooter(caption, footer string) string {
	caption = strings.TrimSpace(caption)
	footer = strings.TrimSpace(footer)
	if caption == "" {
		return footer
	}
	if footer == "" {
		return caption
	}
	return caption + "\n\n" + footer
}

func hashMessagesMD5(msgs []*mtproto.IncomingMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		if m != nil {
			b.WriteString(m.Text)
		}
	}
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:16]
}

func extractLastSegment(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	parts := strings.Split(s, "/")
	return parts[len(parts)-1]
}

func extractPublicSlug(ch string) string {
	seg := strings.TrimPrefix(extractLastSegment(ch), "@")
	if seg == "" || seg == "c" || strings.HasPrefix(seg, "-") || strings.HasPrefix(seg, "+") {
		return ""
	}
	if _, err := strconv.ParseInt(seg, 10, 64); err == nil {
		return ""
	}
	return seg
}
