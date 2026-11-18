// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package submission

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

const aiOptimizeSystemPrompt = "你是一个专业的内容编辑助手。用户会给你一段投稿的标题和正文，请你优化标题和正文的可读性和表达质量，保持原意不变。必须严格使用以下JSON格式返回，不要添加任何其他内容：\n{\"title\": \"优化后的标题\", \"content\": \"优化后的正文\"}"

// SubmissionRepo 抽象投稿审核与通知入库接口（由 *db.Manager 实现）。
type SubmissionRepo interface {
	ListPendingSubmissions(ctx context.Context) ([]db.Submission, error)
	GetSubmissionByID(ctx context.Context, id int64) (*db.Submission, error)
	SetSubmissionReviewMessageID(ctx context.Context, id, reviewMessageID int64) error
	SaveAIOptimization(ctx context.Context, id int64, optimizedTitle, optimizedContent string) error
	ClearAIOptimization(ctx context.Context, id int64) error
	ApproveSubmission(ctx context.Context, id, reviewedBy int64, signatureForced bool) (*db.Submission, error)
	RejectSubmission(ctx context.Context, id, reviewedBy int64) error
	EnqueueNotification(ctx context.Context, in db.NotificationInsert) (int64, error)
}

// Completer 抽象非流式 LLM 调用接口。
type Completer interface {
	CompleteChat(ctx context.Context, req llm.CompleteRequest) (*llm.CompleteResponse, error)
}

// ReviewDeps 配置主 Bot 投稿审核处理器。
type ReviewDeps struct {
	Config    *config.Config
	Repo      SubmissionRepo
	BotClient func() mtproto.Messenger
	Completer func() (Completer, string)
	Logger    *slog.Logger
}

// ReviewService 负责待审投稿轮询通知管理员、AI 优化/恢复原文、同意/署名通过/拒绝与频道发布（对齐 submission_review_handler.py + submission_service.py）。
type ReviewService struct {
	deps ReviewDeps
}

// NewReviewService 创建投稿审核服务。
func NewReviewService(deps ReviewDeps) *ReviewService {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &ReviewService{deps: deps}
}

// CheckPendingSubmissions 扫描尚未推送审核卡片的 pending 投稿并向 ADMIN_LIST 发送审核通知。
func (s *ReviewService) CheckPendingSubmissions(ctx context.Context) {
	if s.deps.Repo == nil || s.deps.Config == nil {
		return
	}
	botCli := s.resolveBotClient()
	if botCli == nil {
		return
	}
	pending, err := s.deps.Repo.ListPendingSubmissions(ctx)
	if err != nil || len(pending) == 0 {
		return
	}
	adminIDs := s.deps.Config.AdminIDs()
	if len(adminIDs) == 0 {
		return
	}

	for _, sub := range pending {
		reviewText := buildReviewText(sub)
		buttons := buildReviewButtons(sub.ID, false, canForceSignature(sub))
		uploads := decodeSubmissionMedia(sub.MediaFiles)

		var firstMsgID int64
		for _, adminID := range adminIDs {
			msgID, serr := botCli.SendMessage(ctx, adminID, reviewText, mtproto.SendOptions{
				Buttons: buttons,
			})
			if serr != nil {
				s.deps.Logger.Error("向管理员发送投稿审核卡片失败", "admin_id", adminID, "submission_id", sub.ID, "error", serr)
				continue
			}
			if firstMsgID == 0 && msgID > 0 {
				firstMsgID = int64(msgID)
			}
			if len(uploads) > 0 {
				_, _ = sendUploadsWithFallback(ctx, botCli, adminID, uploads, "")
			}
		}
		if firstMsgID > 0 {
			_ = s.deps.Repo.SetSubmissionReviewMessageID(ctx, sub.ID, firstMsgID)
		}
	}
}

// HandleCallback 处理 submission_{aiopt|restore|approve|signapprove|reject}_{id} 按钮回调。
func (s *ReviewService) HandleCallback(ctx context.Context, ev mtproto.CallbackEvent) error {
	botCli := s.resolveBotClient()
	if botCli == nil || s.deps.Repo == nil {
		return nil
	}
	if s.deps.Config != nil && !s.deps.Config.IsAdmin(ev.SenderID) {
		return botCli.AnswerCallback(ctx, ev.QueryID, i18n.T("error.permission_denied", nil), true)
	}
	_ = botCli.AnswerCallback(ctx, ev.QueryID, "", false)

	data := ev.DataString()
	if !strings.HasPrefix(data, "submission_") {
		return nil
	}
	parts := strings.Split(data, "_")
	if len(parts) < 3 {
		return nil
	}
	action := parts[1]
	subID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || subID <= 0 {
		return nil
	}

	switch action {
	case "aiopt":
		return s.handleAIOptimize(ctx, botCli, ev, subID)
	case "restore":
		return s.handleRestore(ctx, botCli, ev, subID)
	case "approve":
		return s.handleApprove(ctx, botCli, ev, subID, false)
	case "signapprove":
		return s.handleApprove(ctx, botCli, ev, subID, true)
	case "reject":
		return s.handleReject(ctx, botCli, ev, subID)
	default:
		return nil
	}
}

func (s *ReviewService) handleAIOptimize(ctx context.Context, botCli mtproto.Messenger, ev mtproto.CallbackEvent, subID int64) error {
	sub, err := s.deps.Repo.GetSubmissionByID(ctx, subID)
	if err != nil || sub == nil {
		return botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, "❌ 投稿不存在", mtproto.SendOptions{})
	}
	canForce := canForceSignature(*sub)
	_ = botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, "🤖 正在使用 AI 优化投稿内容，请稍候...", mtproto.SendOptions{})

	optTitle, optContent, oerr := s.aiOptimizeSubmission(ctx, *sub)
	if oerr != nil {
		btns := buildReviewButtons(subID, false, canForce)
		return botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, fmt.Sprintf("❌ AI 优化失败: %v", oerr), mtproto.SendOptions{Buttons: btns})
	}

	titlePreview := ""
	if optTitle != "" {
		titlePreview = fmt.Sprintf("标题：%s\n\n", optTitle)
	}
	contentPreview := truncateRunes(optContent, 500)
	btns := buildReviewButtons(subID, true, canForce)
	msg := fmt.Sprintf("🤖 AI 优化完成\n\n%s正文：\n%s\n\n请使用同意或拒绝按钮继续审核。", titlePreview, contentPreview)
	return botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, msg, mtproto.SendOptions{Buttons: btns})
}

func (s *ReviewService) handleRestore(ctx context.Context, botCli mtproto.Messenger, ev mtproto.CallbackEvent, subID int64) error {
	sub, err := s.deps.Repo.GetSubmissionByID(ctx, subID)
	if err != nil || sub == nil {
		return botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, "❌ 投稿不存在", mtproto.SendOptions{})
	}
	_ = s.deps.Repo.ClearAIOptimization(ctx, subID)
	orig := sub.Content
	if orig == "" {
		orig = "(无)"
	}
	btns := buildReviewButtons(subID, false, canForceSignature(*sub))
	msg := fmt.Sprintf("🔄 已恢复原文\n\n%s\n\n%s\n\n请使用同意或拒绝按钮继续审核。", sub.Title, truncateRunes(orig, 500))
	return botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, msg, mtproto.SendOptions{Buttons: btns})
}

func (s *ReviewService) handleApprove(ctx context.Context, botCli mtproto.Messenger, ev mtproto.CallbackEvent, subID int64, signatureForced bool) error {
	approved, err := s.deps.Repo.ApproveSubmission(ctx, subID, ev.SenderID, signatureForced)
	if err != nil || approved == nil {
		return botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, fmt.Sprintf("❌ 操作失败: %v", err), mtproto.SendOptions{})
	}

	pubChannel, pubURL, pubErr := s.publishToChannel(ctx, botCli, *approved)
	s.notifySubmitter(ctx, *approved, "approved", pubChannel, pubURL, pubErr == nil, approved.SignatureForced)

	statusMsg := fmt.Sprintf("✅ 投稿已通过\n\n投稿ID: %d\n审核人: %d", subID, ev.SenderID)
	if approved.SignatureForced {
		statusMsg += "\n" + i18n.T("submission.review_signature_forced", nil)
	}
	if pubErr == nil {
		statusMsg += fmt.Sprintf("\n\n📢 已发布到频道: %s", pubChannel)
	} else {
		statusMsg += fmt.Sprintf("\n\n⚠️ 发布到频道失败: %v", pubErr)
	}
	return botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, statusMsg, mtproto.SendOptions{})
}

func (s *ReviewService) handleReject(ctx context.Context, botCli mtproto.Messenger, ev mtproto.CallbackEvent, subID int64) error {
	if err := s.deps.Repo.RejectSubmission(ctx, subID, ev.SenderID); err != nil {
		return botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, fmt.Sprintf("❌ 操作失败: %v", err), mtproto.SendOptions{})
	}
	_ = botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, fmt.Sprintf("❌ 投稿已拒绝\n\n投稿ID: %d\n审核人: %d", subID, ev.SenderID), mtproto.SendOptions{})
	if sub, err := s.deps.Repo.GetSubmissionByID(ctx, subID); err == nil && sub != nil {
		s.notifySubmitter(ctx, *sub, "rejected", "", "", false, false)
	}
	return nil
}

func (s *ReviewService) aiOptimizeSubmission(ctx context.Context, sub db.Submission) (string, string, error) {
	comp, model := s.resolveCompleter()
	if comp == nil {
		return "", "", fmt.Errorf("AI 优化客户端未配置")
	}
	userBody := fmt.Sprintf("标题：%s\n\n正文：(无)", sub.Title)
	if strings.TrimSpace(sub.Content) != "" {
		userBody = fmt.Sprintf("标题：%s\n\n正文：\n%s", sub.Title, sub.Content)
	}
	resp, err := comp.CompleteChat(ctx, llm.CompleteRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "system", Content: llm.Str(aiOptimizeSystemPrompt)},
			{Role: "user", Content: llm.Str(userBody)},
		},
	})
	if err != nil {
		return "", "", err
	}
	if resp == nil || strings.TrimSpace(resp.Content) == "" {
		return "", "", fmt.Errorf("AI 优化服务返回了空内容")
	}

	raw := strings.TrimSpace(resp.Content)
	optTitle, optContent := parseAIOptimizeJSON(raw)
	if optTitle == "" {
		optTitle = sub.Title
	}
	if optContent == "" {
		optContent = sub.Content
	}
	if err := s.deps.Repo.SaveAIOptimization(ctx, sub.ID, optTitle, optContent); err != nil {
		return "", "", err
	}
	return optTitle, optContent, nil
}

func (s *ReviewService) publishToChannel(ctx context.Context, botCli mtproto.Messenger, sub db.Submission) (string, string, error) {
	target := strings.TrimSpace(sub.TargetChannel)
	if target == "" {
		return "", "", fmt.Errorf("未指定目标频道")
	}
	caption := BuildPublishCaption(sub)
	uploads := decodeSubmissionMedia(sub.MediaFiles)

	var firstMsgID int
	var err error
	if len(uploads) > 0 {
		firstMsgID, err = sendUploadsWithFallback(ctx, botCli, target, uploads, caption)
	}
	if firstMsgID == 0 {
		ids, serr := botCli.SendLongMessage(ctx, target, caption, mtproto.SendOptions{
			ParseMode: "markdown",
			NoWebpage: true,
		})
		if serr != nil {
			if err != nil {
				return "", "", err
			}
			return "", "", serr
		}
		if len(ids) > 0 {
			firstMsgID = ids[0]
		}
	}

	parts := strings.Split(strings.TrimRight(target, "/"), "/")
	channelName := parts[len(parts)-1]
	msgURL := ""
	if info, rerr := botCli.ResolveChannel(ctx, target); rerr == nil && info != nil && info.Username != "" && firstMsgID > 0 {
		msgURL = fmt.Sprintf("https://t.me/%s/%d", info.Username, firstMsgID)
	}
	return channelName, msgURL, nil
}

func (s *ReviewService) notifySubmitter(
	ctx context.Context,
	sub db.Submission,
	status string,
	channelName string,
	messageURL string,
	publishOK bool,
	signatureForced bool,
) {
	statusText := "拒绝"
	if status == "approved" {
		statusText = "通过"
	}
	sigNotice := ""
	if status == "approved" && signatureForced {
		sigNotice = i18n.T("submission.signature_forced_notice", nil)
	}
	extra := ""
	if status == "approved" && publishOK {
		extra = fmt.Sprintf("\n已发布到频道: %s", channelName)
		if messageURL != "" {
			extra += fmt.Sprintf("\n查看: %s", messageURL)
		}
	}
	_, _ = s.deps.Repo.EnqueueNotification(ctx, db.NotificationInsert{
		UserID:           sub.SubmitterID,
		NotificationType: "submission_" + status,
		Content: map[string]any{
			"submission_id": sub.ID,
			"title":         sub.Title,
			"message":       fmt.Sprintf("您的投稿「%s」已被管理员%s。%s%s", sub.Title, statusText, sigNotice, extra),
		},
	})
}

func (s *ReviewService) resolveBotClient() mtproto.Messenger {
	if s.deps.BotClient != nil {
		return s.deps.BotClient()
	}
	return nil
}

func (s *ReviewService) resolveCompleter() (Completer, string) {
	if s.deps.Completer != nil {
		return s.deps.Completer()
	}
	if s.deps.Config != nil {
		cfg := s.deps.Config.EffectiveAIConfig("summary")
		return llm.NewPurposeClient(s.deps.Config, "summary"), cfg.Model
	}
	return nil, ""
}

// BuildPublishCaption 拼接最终发布文案（优先使用 AI 优化标题/正文与署名策略，对齐 _build_publish_caption）。
func BuildPublishCaption(sub db.Submission) string {
	title := sub.Title
	if sub.AIOptimizedTitle != nil && strings.TrimSpace(*sub.AIOptimizedTitle) != "" {
		title = *sub.AIOptimizedTitle
	}
	content := sub.Content
	if sub.AIOptimizedContent != nil && strings.TrimSpace(*sub.AIOptimizedContent) != "" {
		content = *sub.AIOptimizedContent
	}
	var b strings.Builder
	b.WriteString("**")
	b.WriteString(title)
	b.WriteString("**\n")
	if content != "" {
		b.WriteByte('\n')
		b.WriteString(content)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if (sub.IsAnonymous && !sub.SignatureForced) || strings.TrimSpace(sub.SubmitterName) == "" {
		b.WriteString("—— 投稿者: 匿名")
	} else {
		b.WriteString("—— 投稿者: @" + strings.TrimPrefix(sub.SubmitterName, "@"))
	}
	return b.String()
}

func canForceSignature(sub db.Submission) bool {
	return sub.IsAnonymous && strings.TrimSpace(sub.SubmitterName) != ""
}

func buildReviewText(sub db.Submission) string {
	anon := "否"
	if sub.IsAnonymous {
		anon = "是"
	}
	body := sub.Content
	if body == "" {
		body = "(无)"
	}
	chLine := ""
	if sub.TargetChannel != "" {
		parts := strings.Split(strings.TrimRight(sub.TargetChannel, "/"), "/")
		chLine = "\n目标频道: " + parts[len(parts)-1]
	}
	return fmt.Sprintf(
		"📝 新投稿审核\n\n投稿ID: %d\n投稿者: %s\n匿名投稿: %s\n标题: %s\n正文: %s%s\n\n请审核此投稿：",
		sub.ID, sub.SubmitterName, anon, sub.Title, truncateRunes(body, 300), chLine,
	)
}

func buildReviewButtons(subID int64, showRestore, canForceSig bool) [][]mtproto.InlineButton {
	row1 := []mtproto.InlineButton{{Text: "✅ 同意", Data: fmt.Sprintf("submission_approve_%d", subID)}}
	if canForceSig {
		row1 = append(row1, mtproto.InlineButton{
			Text: i18n.T("submission.review_approve_signed", nil),
			Data: fmt.Sprintf("submission_signapprove_%d", subID),
		})
	}
	row1 = append(row1, mtproto.InlineButton{Text: "❌ 拒绝", Data: fmt.Sprintf("submission_reject_%d", subID)})

	var row2 []mtproto.InlineButton
	if showRestore {
		row2 = []mtproto.InlineButton{{Text: "🔄 恢复原文", Data: fmt.Sprintf("submission_restore_%d", subID)}}
	} else {
		row2 = []mtproto.InlineButton{{Text: "🤖 AI 优化", Data: fmt.Sprintf("submission_aiopt_%d", subID)}}
	}
	return [][]mtproto.InlineButton{row1, row2}
}

func decodeSubmissionMedia(files []map[string]any) []mtproto.MediaUpload {
	var out []mtproto.MediaUpload
	for i, item := range files {
		b64, _ := item["file_data"].(string)
		if b64 == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(raw) == 0 {
			continue
		}
		mType, _ := item["type"].(string)
		fName, _ := item["file_name"].(string)
		mimeType, _ := item["mime_type"].(string)
		if fName == "" {
			switch mType {
			case "photo":
				fName = "photo.jpg"
			case "video":
				fName = "video" + mimeToExt(mimeType, ".mp4")
			case "audio":
				fName = "audio" + mimeToExt(mimeType, ".mp3")
			default:
				fName = fmt.Sprintf("file_%d", i)
			}
		}
		out = append(out, mtproto.MediaUpload{
			Data:          raw,
			FileName:      fName,
			MimeType:      mimeType,
			ForceDocument: mType == "document",
		})
	}
	return out
}

func sendUploadsWithFallback(ctx context.Context, botCli mtproto.Messenger, target any, uploads []mtproto.MediaUpload, caption string) (int, error) {
	if len(uploads) == 0 {
		return 0, nil
	}
	prepared := make([]mtproto.MediaUpload, len(uploads))
	copy(prepared, uploads)
	if caption != "" {
		prepared[0].Caption = caption
		prepared[0].ParseMode = "markdown"
	}
	if len(prepared) == 1 {
		return botCli.SendMedia(ctx, target, prepared[0], mtproto.SendOptions{
			ParseMode: "markdown",
			NoWebpage: true,
		})
	}
	if ids, err := botCli.SendAlbum(ctx, target, prepared, mtproto.SendOptions{
		ParseMode: "markdown",
		NoWebpage: true,
	}); err == nil && len(ids) > 0 {
		return ids[0], nil
	}
	firstID := 0
	var lastErr error
	for _, item := range prepared {
		id, err := botCli.SendMedia(ctx, target, item, mtproto.SendOptions{
			ParseMode: "markdown",
			NoWebpage: true,
		})
		if err != nil {
			lastErr = err
			continue
		}
		if firstID == 0 {
			firstID = id
		}
	}
	return firstID, lastErr
}

func parseAIOptimizeJSON(raw string) (string, string) {
	var obj struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		return strings.TrimSpace(obj.Title), strings.TrimSpace(obj.Content)
	}
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(raw[start:end+1]), &obj); err == nil {
			return strings.TrimSpace(obj.Title), strings.TrimSpace(obj.Content)
		}
	}
	return "", raw
}

func mimeToExt(mimeType, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav":
		return ".wav"
	case "application/pdf":
		return ".pdf"
	default:
		return fallback
	}
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes]) + "..."
}
