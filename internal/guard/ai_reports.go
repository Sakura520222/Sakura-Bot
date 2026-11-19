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
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

// AIReportWorker 负责超时未审成员举报案件的异步 AI 裁决与重校验自动执行（对齐 ai_reports.py）。
type AIReportWorker struct {
	svc       *Service
	completer Completer
	wakeCh    chan struct{}
	logger    *slog.Logger

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewAIReportWorker 创建延迟 AI 举报裁决工作器。
func NewAIReportWorker(svc *Service, completer Completer, logger *slog.Logger) *AIReportWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &AIReportWorker{
		svc:       svc,
		completer: completer,
		wakeCh:    make(chan struct{}, 1),
		logger:    logger,
	}
}

// IsRunning 返回后台裁决协程是否运行中（供 WebUI /api/guard/config runtime 展示）。
func (w *AIReportWorker) IsRunning() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running
}

// Start 启动 5 秒轮询协程。
func (w *AIReportWorker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.running = true
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.loop(runCtx)
	}()
}

// Stop 优雅停止后台裁决协程。
func (w *AIReportWorker) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	cancel := w.cancel
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
}

// Wake 立即唤醒一轮扫描。
func (w *AIReportWorker) Wake() {
	select {
	case w.wakeCh <- struct{}{}:
	default:
	}
}

func (w *AIReportWorker) loop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wakeCh:
			w.Step(ctx)
		case <-ticker.C:
			w.Step(ctx)
		}
	}
}

// Step 单轮扫描并处理到期 AI 举报任务（导出便于单元测试直接调用）。
func (w *AIReportWorker) Step(ctx context.Context) {
	if w == nil || w.svc == nil || w.svc.repo == nil || w.svc.repo.DB == nil {
		return
	}
	due, err := w.svc.repo.DueAIReports(ctx, 10)
	if err != nil || len(due) == 0 {
		return
	}
	for _, job := range due {
		if ctx.Err() != nil {
			return
		}
		w.processOne(ctx, job)
	}
}

type aiReportContext struct {
	CaseID          string
	ChatID          int64
	UserID          int64
	MessageID       int
	PolicyHash      string
	Fingerprint     string
	Sources         map[string]string
	LocalHit        *Match
	Group           GuardGroup
	OriginalPayload map[string]any
}

func isReportUntouched(caseRow guarddb.Row) bool {
	if caseRow == nil {
		return false
	}
	if rowStr(caseRow, "action") != "report" || rowStr(caseRow, "status") != "pending" {
		return false
	}
	if caseRow["reviewer_id"] != nil || rowStr(caseRow, "reviewer_actor") != "" {
		return false
	}
	p := rowPayload(caseRow)
	if hist, ok := p["review_history"].([]any); ok && len(hist) > 0 {
		return false
	}
	if _, ok := p["report_message_deletion"]; ok {
		return false
	}
	return true
}

func computeAIReportPolicyHash(grp GuardGroup) string {
	b, _ := json.Marshal(map[string]any{
		"enabled":    grp.AIReportReviewEnabled,
		"action":     grp.AIReportReviewAction,
		"confidence": grp.AIReportReviewConfidence,
		"categories": grp.AIReportReviewCategories,
		"rules":      grp.Rules,
		"keywords":   grp.MessageKeywords,
	})
	return sha256Hex(string(b))
}

func (w *AIReportWorker) buildReportContext(ctx context.Context, st *guarddb.Store, caseRow guarddb.Row) (*aiReportContext, string) {
	chatID := rowInt64(caseRow, "chat_id")
	userID := rowInt64(caseRow, "user_id")
	grp := w.svc.Group(chatID)
	if !grp.Enabled || !grp.AIReportReviewEnabled {
		return nil, "ai_report_disabled"
	}
	if !isReportUntouched(caseRow) {
		return nil, "already_handled"
	}
	if otherBan, _ := st.ActiveBan(ctx, chatID, userID, rowStr(caseRow, "id")); otherBan != nil {
		return nil, "active_ban_exists"
	}
	prot := w.svc.checkProtection(ctx, chatID, userID, grp, false)
	if prot != protectionUnprotected {
		return nil, "protected_or_unknown"
	}

	payload := rowPayload(caseRow)
	msgSnap, _ := payload["reported_message"].(map[string]any)
	msgID := mtproto.MessageID(rowInt64(msgSnap, "id"))
	if msgID <= 0 {
		msgID = mtproto.MessageID(rowInt64(payload, "message_id"))
	}
	if msgID <= 0 {
		return nil, "missing_message_id"
	}

	var liveMsg *mtproto.IncomingMessage
	if w.svc.ops != nil {
		msgs, err := w.svc.ops.GetMessages(ctx, chatID, []int{msgID})
		if err != nil || len(msgs) == 0 || msgs[0] == nil {
			return nil, "message_deleted_or_unavailable"
		}
		liveMsg = msgs[0]
		if liveMsg.Sender.ID != 0 && liveMsg.Sender.ID != userID {
			return nil, "sender_mismatch"
		}
		if expectedText := rowStr(msgSnap, "text"); expectedText != "" && liveMsg.Text != expectedText {
			return nil, "message_edited"
		}
	}

	var bio string
	var profile mtproto.ProfileSummary
	if liveMsg != nil {
		profile = liveMsg.Sender
	}
	if w.svc.ops != nil {
		if full, err := w.svc.ops.GetFullUserBio(ctx, userID); err == nil && full != nil {
			bio = full.Bio
			if profile.FirstName == "" {
				profile = *full
			}
		}
	}

	localHit := w.svc.matchSnapshot(ctx, st, chatID, userID, profile, liveMsg, grp)
	fullName := strings.TrimSpace(profile.FirstName + " " + profile.LastName)
	if fullName == "" {
		fullName = rowStr(payload, "name")
	}
	msgText := ""
	if liveMsg != nil {
		msgText = liveMsg.Text
	} else {
		msgText = rowStr(msgSnap, "text")
	}

	sources := map[string]string{
		"message":  msgText,
		"nickname": fullName,
		"username": profile.Username,
		"bio":      bio,
	}
	if black, _ := st.Blacklist(ctx, userID); black != nil {
		sources["permanent_blacklist"] = "permanent_blacklist"
	}
	if localHit != nil {
		sources["rule_match"] = localHit.Reason
	}

	fpRaw, _ := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"user_id":    userID,
		"message_id": msgID,
		"sources":    sources,
	})
	return &aiReportContext{
		CaseID:          rowStr(caseRow, "id"),
		ChatID:          chatID,
		UserID:          userID,
		MessageID:       msgID,
		PolicyHash:      computeAIReportPolicyHash(grp),
		Fingerprint:     sha256Hex(string(fpRaw)),
		Sources:         sources,
		LocalHit:        localHit,
		Group:           grp,
		OriginalPayload: payload,
	}, ""
}

func (w *AIReportWorker) processOne(ctx context.Context, rawJob guarddb.Row) {
	caseID := rowStr(rawJob, "case_id")
	userID := rowInt64(rawJob, "user_id")
	expectedToken := rowStr(rawJob, "token")
	if caseID == "" || userID == 0 {
		return
	}

	var claimed guarddb.Row
	var rctx *aiReportContext
	_ = w.svc.repo.WithLock(ctx, userID, func(st *guarddb.Store) error {
		var err error
		claimed, err = st.ClaimAIReport(ctx, caseID, expectedToken)
		if err != nil || claimed == nil {
			return err
		}
		caseRow, cerr := st.Case(ctx, caseID)
		if cerr != nil || caseRow == nil {
			_, _ = st.SkipAIReport(ctx, caseID, rowStr(claimed, "token"), map[string]any{}, "case_missing")
			claimed = nil
			return nil
		}
		var skipReason string
		rctx, skipReason = w.buildReportContext(ctx, st, caseRow)
		if skipReason != "" {
			_, _ = st.SkipAIReport(ctx, caseID, rowStr(claimed, "token"), map[string]any{"reason": skipReason}, skipReason)
			claimed = nil
		}
		return nil
	})
	if claimed == nil || rctx == nil {
		return
	}
	claimToken := rowStr(claimed, "token")

	comp, modelName := w.resolveCompleter(w.svc.appCfg)
	if comp == nil {
		_ = w.svc.repo.WithLock(ctx, userID, func(st *guarddb.Store) error {
			_, _ = st.ManualAIReport(ctx, caseID, claimToken, map[string]any{}, "model_unconfigured")
			return nil
		})
		return
	}

	promptBytes, _ := json.Marshal(map[string]any{
		"chat_id":    rctx.ChatID,
		"user_id":    rctx.UserID,
		"categories": rctx.Group.AIReportReviewCategories,
		"sources":    rctx.Sources,
		"local_hit":  rctx.LocalHit,
	})
	callCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
	resp, llmErr := comp.CompleteChat(callCtx, llm.CompleteRequest{
		Model:          modelName,
		Temperature:    llm.Float64(0),
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Messages: []llm.Message{
			{Role: "system", Content: llm.Str(`你是社群违规举报复核裁判。严格基于提供的 sources 原文判断被举报消息是否违规。输出 JSON: {"verdict":"violation|benign|uncertain","category":"spam|scam|malware|harassment|rule_match","confidence":0.98,"reason":"...","evidence":[{"source":"message|nickname|username|bio","quote":"精确原文子串"}]}`)},
			{Role: "user", Content: llm.Str(string(promptBytes))},
		},
	})
	cancel()

	_ = w.svc.repo.WithLock(ctx, userID, func(st *guarddb.Store) error {
		if llmErr != nil || resp == nil {
			_, _, _ = st.FailOrRetryAIReport(ctx, claimed, map[string]any{}, 30*time.Second, "llm_call_failed")
			return nil
		}
		var verdict ReportVerdict
		if err := json.Unmarshal([]byte(extractJSONObject(resp.Content)), &verdict); err != nil {
			_, _, _ = st.FailOrRetryAIReport(ctx, claimed, map[string]any{}, 30*time.Second, "invalid_verdict_json")
			return nil
		}

		caseRow, cerr := st.Case(ctx, caseID)
		if cerr != nil || caseRow == nil {
			_, _ = st.SkipAIReport(ctx, caseID, claimToken, map[string]any{}, "case_missing")
			return nil
		}
		freshCtx, skipReason := w.buildReportContext(ctx, st, caseRow)
		if skipReason != "" || freshCtx == nil {
			_, _ = st.SkipAIReport(ctx, caseID, claimToken, map[string]any{"reason": skipReason}, skipReason)
			return nil
		}
		if freshCtx.Fingerprint != rctx.Fingerprint || freshCtx.PolicyHash != rctx.PolicyHash {
			_, _ = st.ManualAIReport(ctx, caseID, claimToken, map[string]any{"verdict": verdict}, "context_changed")
			return nil
		}

		if !IsActionableVerdict(
			verdict,
			freshCtx.Group.AIReportReviewConfidence,
			freshCtx.Group.AIReportReviewCategories,
			freshCtx.LocalHit != nil,
			freshCtx.Sources,
		) {
			_, _ = st.ManualAIReport(ctx, caseID, claimToken, map[string]any{"verdict": verdict}, "not_actionable")
			return nil
		}

		chosenAction := freshCtx.Group.AIReportReviewAction
		if freshCtx.LocalHit != nil && validActions[freshCtx.LocalHit.Action] {
			chosenAction = freshCtx.LocalHit.Action
		}
		if chosenAction == "kick" {
			if black, _ := st.Blacklist(ctx, userID); black != nil {
				_, _ = st.ManualAIReport(ctx, caseID, claimToken, map[string]any{"verdict": verdict}, "kick_conflicts_blacklist")
				return nil
			}
		}

		newPayload := deepCloneAnyMap(freshCtx.OriginalPayload)
		newPayload["ai_report_review"] = map[string]any{
			"state":      "applied",
			"verdict":    verdict,
			"action":     chosenAction,
			"applied_at": time.Now().UTC().Format(time.RFC3339),
		}
		newPayload["report_notice_dirty"] = true

		converted, convErr := st.ConvertReportCase(ctx, caseID, chosenAction, newPayload)
		if convErr != nil || !converted {
			_, _ = st.SkipAIReport(ctx, caseID, claimToken, map[string]any{}, "convert_conflict")
			return nil
		}

		updatedCase, _ := st.Case(ctx, caseID)
		if err := w.svc.ExecuteCase(ctx, st, updatedCase); err != nil {
			_, _ = st.RestoreReportCase(ctx, caseID, freshCtx.OriginalPayload)
			_, _ = st.ManualAIReport(ctx, caseID, claimToken, map[string]any{"error": err.Error()}, "execution_failed")
			return nil
		}

		_, _ = st.FinishAIReport(ctx, caseID, claimToken, map[string]any{"verdict": verdict, "action": chosenAction})
		if w.svc.learning != nil && w.svc.learning.Enabled(freshCtx.ChatID) && w.svc.learning.SourceAllowed("ai_review") {
			sid := newUUIDHex()
			cidCopy := caseID
			_, created, _ := st.CreateAISample(ctx, guarddb.AISampleInsert{
				ID:             sid,
				IdempotencyKey: sha256Hex(fmt.Sprintf("ai-review:%s", caseID)),
				ChatID:         freshCtx.ChatID,
				UserID:         userID,
				Source:         "ai_review",
				CaseID:         &cidCopy,
				Label:          "positive",
				Status:         "queued",
				Payload:        newPayload,
			})
			if created {
				_ = st.EnqueueAILearningJob(ctx, sid, freshCtx.ChatID, userID)
				w.svc.learning.Wake()
			}
		}
		return nil
	})
}

func (w *AIReportWorker) resolveCompleter(appCfg *config.Config) (Completer, string) {
	if w.completer != nil {
		return w.completer, "guard-test"
	}
	if appCfg != nil {
		c := appCfg.EffectiveAIConfig("guard", "summary")
		return llm.NewPurposeClient(appCfg, "guard"), c.Model
	}
	return nil, ""
}
