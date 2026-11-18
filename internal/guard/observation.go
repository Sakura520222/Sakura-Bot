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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

const (
	observationQueueSize = 32
	observationWorkers   = 2
	observationMaxBytes  = 4 * 1024 * 1024 // 4 MiB
)

var (
	obsMessagingLinkRE = regexp.MustCompile(`(?i)(?:t\.me|wa\.me|telegram\.me)/[a-z0-9_+]+`)
	obsContactTextRE   = regexp.MustCompile(`(?i)(?:微信|vx|wechat|whatsapp|电报|telegram|商务合作)[:：\s]*[a-z0-9_@-]{4,}`)
	obsAdTextRE        = regexp.MustCompile(`(?i)(?:私信我|点击头像|看主页|看简介|免费领取|跑分|代收代付|日赚|兼职招聘|资源群|福利群)`)
	obsPaidRecruitRE   = regexp.MustCompile(`(?i)(?:招聘|诚聘|急招|兼职|居家办公|线上手工)`)
	obsIncomeClaimRE   = regexp.MustCompile(`(?i)(?:日结|日入|月入|一单一结|[1-9]\d{2,}\s*(?:元|u|usdt|/天))`)
	obsRecruitContRE   = regexp.MustCompile(`(?i)(?:联系|私聊|咨询|有意者|加微|飞机)`)
	obsPromotionRE     = regexp.MustCompile(`(?i)(?:加群|私聊|合作|推广|引流|福利|赚钱|u商)`)
)

// ObservationTask 表示投递给异步媒体/AI 复核池的单条观察期消息快照。
type ObservationTask struct {
	ChatID     int64
	UserID     int64
	MessageID  int
	EditUnix   int64
	Text       string
	SenderName string
	Media      *mtproto.MediaAttachment
}

// ObservationEngine 负责入群后观察期的本地启发式同步拦截与异步图片二维码/OCR/语义复核（对齐 observation.py & observation_media.py）。
type ObservationEngine struct {
	svc             *Service
	textCompleter   Completer
	visionCompleter Completer
	queue           chan ObservationTask
	logger          *slog.Logger

	rateMu     sync.Mutex
	nextSlotAt time.Time

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewObservationEngine 创建入群观察期检测引擎。
func NewObservationEngine(svc *Service, logger *slog.Logger) *ObservationEngine {
	if logger == nil {
		logger = slog.Default()
	}
	return &ObservationEngine{
		svc:    svc,
		queue:  make(chan ObservationTask, observationQueueSize),
		logger: logger,
	}
}

// Start 启动 2 个后台媒体/AI 检测工作协程。
func (o *ObservationEngine) Start(ctx context.Context) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.running {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	o.cancel = cancel
	o.running = true
	for i := 0; i < observationWorkers; i++ {
		o.wg.Add(1)
		go func() {
			defer o.wg.Done()
			o.workerLoop(runCtx)
		}()
	}
}

// Stop 优雅关闭后台工作协程。
func (o *ObservationEngine) Stop() {
	o.mu.Lock()
	if !o.running {
		o.mu.Unlock()
		return
	}
	o.running = false
	cancel := o.cancel
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	o.wg.Wait()
}

// EvaluateHeuristics 纯函数：检测单条观察期消息文本/结构是否命中本地启发式特征（导出供单元测试）。
func EvaluateHeuristics(
	msg *mtproto.IncomingMessage,
	sharedPatternHit bool,
	prevSignatures []string,
	prevTimestamps []int64,
	nowUnix int64,
) (reason string, newSignature string) {
	if msg == nil {
		return "", ""
	}
	norm := NormalizeText(msg.Text)
	if utf8.RuneCountInString(norm) >= 24 {
		newSignature = sha256Hex(norm)
	}

	if len(ExtractLinks(msg.Text, msg.Entities)) > 0 || obsMessagingLinkRE.MatchString(msg.Text) {
		return "observation_link", newSignature
	}
	if sharedPatternHit {
		return "observation_shared_pattern", newSignature
	}
	if (msg.Media != nil && msg.Media.Kind == "contact") || obsContactTextRE.MatchString(msg.Text) {
		return "observation_contact", newSignature
	}
	if msg.HasButtons {
		return "observation_buttons", newSignature
	}
	if obsAdTextRE.MatchString(msg.Text) {
		return "observation_ad_text", newSignature
	}
	if obsPaidRecruitRE.MatchString(msg.Text) && obsIncomeClaimRE.MatchString(msg.Text) && obsRecruitContRE.MatchString(msg.Text) {
		return "observation_recruitment_spam", newSignature
	}
	if newSignature != "" && slices.Contains(prevSignatures, newSignature) {
		return "observation_duplicate_message", newSignature
	}

	recentCount := 0
	for _, ts := range prevTimestamps {
		if nowUnix-ts <= 15 {
			recentCount++
		}
	}
	if recentCount >= 3 && obsPromotionRE.MatchString(msg.Text) {
		return "observation_burst_spam", newSignature
	}
	return "", newSignature
}

// InspectMessage 在持有用户锁的前提下检查成员是否处于观察期并执行同步拦截与异步投递。
func (o *ObservationEngine) InspectMessage(
	ctx context.Context,
	st *guarddb.Store,
	chatID int64,
	msg *mtproto.IncomingMessage,
	grp GuardGroup,
) *Match {
	if grp.ObservationMinutes <= 0 || msg == nil || st == nil {
		return nil
	}
	sess, err := st.VerificationSession(ctx, chatID, msg.Sender.ID)
	if err != nil || sess == nil {
		return nil
	}
	stStr := rowStr(sess, "status")
	if stStr != "passed" && stStr != "passing" {
		return nil
	}
	payload := rowPayload(sess)
	obsMap, ok := payload["observation"].(map[string]any)
	if !ok || obsMap == nil {
		return nil
	}
	now := time.Now().UTC()
	expAt := parseRowTimeOrNow(obsMap, "expires_at", time.Time{})
	if expAt.IsZero() || !now.Before(expAt) {
		return nil
	}

	msgIDs := parseAnyIntSlice(obsMap["message_ids"])
	if len(msgIDs) >= grp.ObservationMessages && !slices.Contains(msgIDs, msg.ID) {
		return nil
	}

	norm := NormalizeText(msg.Text)
	sharedHit := false
	if grp.ObservationSharePatterns && utf8.RuneCountInString(norm) >= 24 {
		fp := sha256Hex(norm)
		otherChats := o.sharedPatternGroupIDs(chatID)
		if len(otherChats) > 0 {
			sharedHit, _ = st.SharedPatternSeen(ctx, fp, otherChats)
		}
		_ = st.RecordPattern(ctx, chatID, fp)
	}

	prevSigs := parseAnyStringSlice(obsMap["signatures"])
	prevTS := parseAnyInt64Slice(obsMap["timestamps"])
	reason, sig := EvaluateHeuristics(msg, sharedHit, prevSigs, prevTS, now.Unix())

	if !slices.Contains(msgIDs, msg.ID) {
		msgIDs = append(msgIDs, msg.ID)
	}
	if sig != "" && !slices.Contains(prevSigs, sig) {
		prevSigs = append(prevSigs, sig)
	}
	prevTS = append(prevTS, now.Unix())
	if len(prevTS) > 20 {
		prevTS = prevTS[len(prevTS)-20:]
	}
	obsMap["message_ids"] = msgIDs
	obsMap["signatures"] = prevSigs
	obsMap["timestamps"] = prevTS

	if reason != "" {
		obsMap["hits"] = rowInt64(obsMap, "hits") + 1
		if grp.ObservationAction == "extend" {
			obsMap["expires_at"] = now.Add(time.Duration(grp.ObservationMinutes) * time.Minute).Format(time.RFC3339)
		}
		payload["observation"] = obsMap
		_ = st.SaveVerificationSession(ctx, guarddb.VerificationSessionSave{
			ID:        rowStr(sess, "id"),
			ChatID:    chatID,
			UserID:    msg.Sender.ID,
			Status:    stStr,
			Payload:   payload,
			MessageID: nil,
			Attempts:  int(rowInt64(sess, "attempts")),
			ExpiresAt: parseRowTimeOrNow(sess, "expires_at", now),
		})
		return &Match{
			Target:  "observation",
			RuleID:  "new-member-observation",
			Action:  "delete",
			Reason:  reason,
			Snippet: EvidenceWindow(msg.Text, 0),
		}
	}

	payload["observation"] = obsMap
	_ = st.SaveVerificationSession(ctx, guarddb.VerificationSessionSave{
		ID:        rowStr(sess, "id"),
		ChatID:    chatID,
		UserID:    msg.Sender.ID,
		Status:    stStr,
		Payload:   payload,
		MessageID: nil,
		Attempts:  int(rowInt64(sess, "attempts")),
		ExpiresAt: parseRowTimeOrNow(sess, "expires_at", now),
	})

	// 若包含图片或开启了语义 AI 复核，无锁异步投递至有界队列。
	hasInspectableImage := grp.ObservationImageMode != "off" && isInspectableImage(msg.Media)
	hasSemanticAI := grp.ObservationAIReview && strings.TrimSpace(msg.Text) != ""
	if hasInspectableImage || hasSemanticAI {
		var editUnix int64
		if msg.EditDate != nil && !msg.EditDate.IsZero() {
			editUnix = msg.EditDate.Unix()
		}
		select {
		case o.queue <- ObservationTask{
			ChatID:     chatID,
			UserID:     msg.Sender.ID,
			MessageID:  msg.ID,
			EditUnix:   editUnix,
			Text:       msg.Text,
			SenderName: strings.TrimSpace(msg.Sender.FirstName + " " + msg.Sender.LastName),
			Media:      msg.Media,
		}:
		default:
			// 队列满时优雅降级丢弃异步扩展扫描，绝不阻塞消息主链路。
		}
	}
	return nil
}

func isInspectableImage(m *mtproto.MediaAttachment) bool {
	if m == nil {
		return false
	}
	if m.Kind == "photo" {
		return true
	}
	if m.Kind == "document" && (m.MimeType == "image/png" || m.MimeType == "image/jpeg") {
		return true
	}
	return false
}

func (o *ObservationEngine) sharedPatternGroupIDs(excludeChatID int64) []int64 {
	cfg := o.svc.Config()
	var out []int64
	for k, g := range cfg.Groups {
		if !g.Enabled || !g.ObservationSharePatterns {
			continue
		}
		var cid int64
		_, _ = fmt.Sscanf(k, "%d", &cid)
		if cid != 0 && cid != excludeChatID {
			out = append(out, cid)
		}
	}
	return out
}

func (o *ObservationEngine) waitForModelSlot(ctx context.Context) error {
	o.rateMu.Lock()
	now := time.Now()
	wait := time.Duration(0)
	if now.Before(o.nextSlotAt) {
		wait = o.nextSlotAt.Sub(now)
		o.nextSlotAt = o.nextSlotAt.Add(3 * time.Second)
	} else {
		o.nextSlotAt = now.Add(3 * time.Second)
	}
	o.rateMu.Unlock()
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (o *ObservationEngine) workerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-o.queue:
			o.processAsyncTask(ctx, task)
		}
	}
}

func (o *ObservationEngine) processAsyncTask(ctx context.Context, task ObservationTask) {
	if o.svc == nil || o.svc.ops == nil || o.svc.repo == nil {
		return
	}
	grp := o.svc.Group(task.ChatID)
	if !grp.Enabled || grp.ObservationMinutes <= 0 {
		return
	}

	var hitReason, hitSnippet string

	// 1. 图片二维码与视觉 OCR 检测
	if grp.ObservationImageMode != "off" && isInspectableImage(task.Media) {
		dlCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		rawBytes, err := o.svc.ops.DownloadMediaBytes(dlCtx, task.Media, observationMaxBytes)
		cancel()
		if err == nil && len(rawBytes) > 0 {
			if qrText, qerr := media.ScanQR(rawBytes); qerr == nil && strings.TrimSpace(qrText) != "" {
				hitReason = "observation_qr_code"
				hitSnippet = EvidenceWindow(qrText, 0)
			} else if grp.ObservationImageMode == "ocr" {
				if mime, verr := media.ValidateImageBytes(rawBytes, observationMaxBytes); verr == nil {
					if o.waitForModelSlot(ctx) == nil {
						ocrText := o.recognizeImageText(ctx, mime, rawBytes)
						if ocrText != "" {
							synthetic := &mtproto.IncomingMessage{Text: ocrText}
							if r, _ := EvaluateHeuristics(synthetic, false, nil, nil, time.Now().Unix()); r != "" {
								hitReason = "observation_ocr:" + r
								hitSnippet = EvidenceWindow(ocrText, 0)
							}
						}
					}
				}
			}
		}
	}

	// 2. 文本语义 AI 复核
	if hitReason == "" && grp.ObservationAIReview && strings.TrimSpace(task.Text) != "" {
		if o.waitForModelSlot(ctx) == nil {
			if v, ok := o.classifySemanticText(ctx, task.Text, grp.ObservationAIConfidence); ok {
				hitReason = "observation_ai:" + v.Category
				hitSnippet = EvidenceWindow(task.Text, 0)
			}
		}
	}

	if hitReason == "" {
		return
	}

	// 3. 处罚前重新拉取实时消息确认未被撤回或篡改，再获取用户锁执行处罚。
	msgs, err := o.svc.ops.GetMessages(ctx, task.ChatID, []int{task.MessageID})
	if err != nil || len(msgs) == 0 || msgs[0] == nil {
		return
	}
	live := msgs[0]
	var liveEdit int64
	if live.EditDate != nil && !live.EditDate.IsZero() {
		liveEdit = live.EditDate.Unix()
	}
	if (live.Sender.ID != 0 && live.Sender.ID != task.UserID) || liveEdit != task.EditUnix || live.Text != task.Text {
		return
	}

	_ = o.svc.repo.WithLock(ctx, task.UserID, func(st *guarddb.Store) error {
		freshGrp := o.svc.Group(task.ChatID)
		if !freshGrp.Enabled {
			return nil
		}
		if whitelisted, _ := st.Whitelisted(ctx, task.ChatID, task.UserID); whitelisted {
			return nil
		}
		match := &Match{
			Target:  "observation",
			RuleID:  "new-member-observation-async",
			Action:  "delete",
			Reason:  hitReason,
			Snippet: hitSnippet,
		}
		o.svc.punishLocked(ctx, st, task.ChatID, task.UserID, task.SenderName, task.MessageID, match, freshGrp, fmt.Sprintf("obs_async:%d:%d", task.ChatID, task.MessageID))
		if freshGrp.ObservationAction == "mute" {
			muteMatch := *match
			muteMatch.Action = "mute"
			o.svc.punishLocked(ctx, st, task.ChatID, task.UserID, task.SenderName, task.MessageID, &muteMatch, freshGrp, fmt.Sprintf("obs_async_mute:%d:%d", task.ChatID, task.MessageID))
		}
		return nil
	})
}

func (o *ObservationEngine) recognizeImageText(ctx context.Context, mime string, rawBytes []byte) string {
	comp := o.visionCompleter
	model := "guard-vision-test"
	if o.svc != nil && o.svc.appCfg != nil {
		c := o.svc.appCfg.EffectiveAIConfig("guard_vision", "vision")
		if !c.SupportsVision {
			return ""
		}
		if comp == nil {
			comp = llm.NewPurposeClient(o.svc.appCfg, "guard_vision")
		}
		model = c.Model
	}
	if comp == nil {
		return ""
	}
	dataURL := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(rawBytes))
	resp, err := comp.CompleteChat(ctx, llm.CompleteRequest{
		Model:       model,
		Temperature: llm.Float64(0),
		Messages: []llm.Message{
			{
				Role: "user",
				Parts: []llm.ContentPart{
					{Type: "text", Text: "请准确提取图片中的所有文字、网址、微信号、Telegram 账号或联系方式，仅输出提取到的文本内容。"},
					{Type: "image_url", ImageURL: &llm.ImageURL{URL: dataURL}},
				},
			},
		},
	})
	if err != nil || resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.Content)
}

func (o *ObservationEngine) classifySemanticText(ctx context.Context, text string, threshold float64) (ReportVerdict, bool) {
	comp := o.textCompleter
	model := "guard-test"
	if o.svc != nil && o.svc.appCfg != nil {
		c := o.svc.appCfg.EffectiveAIConfig("guard", "summary")
		if comp == nil {
			comp = llm.NewPurposeClient(o.svc.appCfg, "guard")
		}
		model = c.Model
	}
	if comp == nil {
		return ReportVerdict{}, false
	}
	resp, err := comp.CompleteChat(ctx, llm.CompleteRequest{
		Model:          model,
		Temperature:    llm.Float64(0),
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Messages: []llm.Message{
			{Role: "system", Content: llm.Str(`判断新入群成员发言是否属于广告垃圾(spam)、诈骗引流(scam)或恶意链接(malware)。输出 JSON: {"verdict":"violation|benign|uncertain","category":"spam|scam|malware","confidence":0.99,"reason":"...","evidence":[{"source":"message","quote":"原文精确子串"}]}`)},
			{Role: "user", Content: llm.Str(text)},
		},
	})
	if err != nil || resp == nil {
		return ReportVerdict{}, false
	}
	var v ReportVerdict
	if err := json.Unmarshal([]byte(extractJSONObject(resp.Content)), &v); err != nil {
		return ReportVerdict{}, false
	}
	ok := IsActionableVerdict(v, threshold, []string{"spam", "scam", "malware"}, false, map[string]string{"message": text})
	return v, ok
}

func parseAnyIntSlice(v any) []int {
	switch arr := v.(type) {
	case []int:
		return arr
	case []any:
		out := make([]int, 0, len(arr))
		for _, it := range arr {
			out = append(out, int(rowInt64(map[string]any{"v": it}, "v")))
		}
		return out
	default:
		return nil
	}
}

func parseAnyInt64Slice(v any) []int64 {
	switch arr := v.(type) {
	case []int64:
		return arr
	case []any:
		out := make([]int64, 0, len(arr))
		for _, it := range arr {
			out = append(out, rowInt64(map[string]any{"v": it}, "v"))
		}
		return out
	default:
		return nil
	}
}
