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
	"errors"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

var (
	reportTriggerRE    = regexp.MustCompile(`(?i)(?:^|\s)(?:@admins?|/report(?:@[a-z0-9_]+)?|举报|上报)(?:\s|$)`)
	adminOnlyReportRE  = regexp.MustCompile(`(?i)(?:^|\s)上报(?:\s|$)`)
	reviewCallbackCode = map[string]string{
		"a": "allow",
		"b": "blacklist",
		"u": "unban",
		"k": "ban",
		"d": "delete",
		"j": "kick",
	}
)

// ────────────────────────────── ReportHandler ──────────────────────────────

// ReportHandler 处理群内 @admin / /report / 举报 / 上报 回复举报（对齐 report_handler.py）。
type ReportHandler struct {
	svc *Service
}

// NewReportHandler 创建举报处理器。
func NewReportHandler(svc *Service) *ReportHandler {
	return &ReportHandler{svc: svc}
}

// IsReportText 判断文本是否包含举报或上报触发词。
func IsReportText(text string) (isReport bool, isAdminSignalOnly bool) {
	t := strings.TrimSpace(text)
	if t == "" || !reportTriggerRE.MatchString(t) {
		return false, false
	}
	return true, adminOnlyReportRE.MatchString(t)
}

// Handle 检查并处理成员回复举报或管理员上报。返回 true 表示已作为举报消息消费。
func (rh *ReportHandler) Handle(ctx context.Context, msg *mtproto.IncomingMessage) bool {
	if rh == nil || rh.svc == nil || msg == nil || msg.ReplyToMsgID <= 0 {
		return false
	}
	isRep, adminOnly := IsReportText(msg.Text)
	if !isRep {
		return false
	}
	grp := rh.svc.Group(msg.ChatID)
	if !grp.Enabled || rh.svc.ops == nil || rh.svc.repo == nil || rh.svc.repo.DB == nil {
		return false
	}

	isAnonymousAdmin := msg.Sender.ID == 0 && grp.AllowAnonymousAdminReport
	if msg.Sender.ID <= 0 && !isAnonymousAdmin {
		return false
	}

	repliedList, err := rh.svc.ops.GetMessages(ctx, msg.ChatID, []int{msg.ReplyToMsgID})
	if err != nil || len(repliedList) == 0 || repliedList[0] == nil {
		return false
	}
	targetMsg := repliedList[0]
	targetUserID := targetMsg.Sender.ID
	if targetUserID <= 0 || targetUserID == rh.svc.BotID() {
		return false
	}

	if rh.svc.checkProtection(ctx, msg.ChatID, targetUserID, grp, targetMsg.Sender.IsBot || guestMessageSource(targetMsg) != nil) != protectionUnprotected {
		return true
	}

	senderIsAdmin := isAnonymousAdmin
	if !senderIsAdmin && msg.Sender.ID > 0 {
		senderIsAdmin = rh.svc.Authorized(ctx, msg.Sender.ID, msg.ChatID)
	}
	if adminOnly && !senderIsAdmin {
		return false
	}

	reporterKey := fmt.Sprintf("user:%d", msg.Sender.ID)
	if isAnonymousAdmin {
		reporterKey = fmt.Sprintf("anon_admin:%d", msg.ChatID)
	}

	// 管理员「上报」：只进入 AI 样本采集，不额外生成待审举报单。
	if adminOnly {
		if rh.svc.learning != nil && rh.svc.learning.Enabled(msg.ChatID) && rh.svc.learning.SourceAllowed("admin_report") {
			_ = rh.svc.repo.WithLock(ctx, targetUserID, func(st *guarddb.Store) error {
				rh.recordLearningSample(ctx, st, msg.ChatID, targetUserID, targetMsg, "admin_report", reporterKey, "")
				return nil
			})
		}
		return true
	}

	_ = rh.svc.repo.WithLock(ctx, targetUserID, func(st *guarddb.Store) error {
		eventKey := sha256Hex(fmt.Sprintf("report:%d:%d", msg.ChatID, targetMsg.ID))
		existing, _ := st.CaseForEvent(ctx, eventKey)
		var caseID string
		if existing == nil {
			caseID = newUUIDHex()
			fullName := strings.TrimSpace(targetMsg.Sender.FirstName + " " + targetMsg.Sender.LastName)
			if fullName == "" {
				fullName = strconv.FormatInt(targetUserID, 10)
			}
			payload := map[string]any{
				"name":                    fullName,
				"username":                targetMsg.Sender.Username,
				"message_id":              targetMsg.ID,
				"reporters":               []string{reporterKey},
				"report_controls_version": 2,
				"controls_version":        2,
				"kick_button_enabled":     grp.KickButtonEnabled,
				"notification_event":      "moderation",
				"reported_message": map[string]any{
					"id":   targetMsg.ID,
					"text": targetMsg.Text,
				},
				"evidence": map[string]any{
					"target":  "report",
					"rule_id": "member-report",
					"reason":  strings.TrimSpace(msg.Text),
					"snippet": EvidenceWindow(targetMsg.Text, 0),
				},
			}
			if guest := guestMessageSource(targetMsg); guest != nil {
				payload["guest_actor"] = guest
			}
			if cerr := st.CreateCase(ctx, guarddb.CaseInsert{
				ID:       caseID,
				EventKey: eventKey,
				ChatID:   msg.ChatID,
				UserID:   targetUserID,
				Action:   "report",
				Status:   "pending",
				Payload:  payload,
			}); cerr != nil {
				return cerr
			}
			if grp.AIReportReviewEnabled {
				_ = st.EnqueueAIReport(ctx, caseID, targetUserID, time.Duration(grp.AIReportReviewDelayMinutes)*time.Minute)
			}
		} else {
			caseID = rowStr(existing, "id")
			p := rowPayload(existing)
			reps := parseAnyStringSlice(p["reporters"])
			if !slices.Contains(reps, reporterKey) {
				reps = append(reps, reporterKey)
				p["reporters"] = reps
				p["report_notice_dirty"] = true
				_ = st.UpdateCase(ctx, caseID, rowStr(existing, "status"), nil, p)
			}
		}

		if rh.svc.learning != nil && rh.svc.learning.Enabled(msg.ChatID) && rh.svc.learning.SourceAllowed("member_report") {
			rh.recordLearningSample(ctx, st, msg.ChatID, targetUserID, targetMsg, "member_report", reporterKey, caseID)
		}

		caseRow, _ := st.Case(ctx, caseID)
		if rh.svc.notifier != nil && caseRow != nil {
			rh.svc.notifier.Sync(ctx, st, caseRow, grp)
		}
		return nil
	})
	return true
}

func (rh *ReportHandler) recordLearningSample(
	ctx context.Context,
	st *guarddb.Store,
	chatID, targetUserID int64,
	targetMsg *mtproto.IncomingMessage,
	source, reporterKey, caseID string,
) {
	mid := int64(targetMsg.ID)
	var cidPtr *string
	if caseID != "" {
		cidPtr = &caseID
	}
	sid := newUUIDHex()
	label := "pending"
	stStr := "new"
	if source == "admin_report" {
		label = "positive"
		stStr = "queued"
	}
	sample, created, err := st.CreateAISample(ctx, guarddb.AISampleInsert{
		ID:             sid,
		IdempotencyKey: sha256Hex(fmt.Sprintf("ai-sample:%d:%d", chatID, targetMsg.ID)),
		ChatID:         chatID,
		UserID:         targetUserID,
		MessageID:      &mid,
		Source:         source,
		CaseID:         cidPtr,
		Label:          label,
		Status:         stStr,
		Payload: map[string]any{
			"text":      targetMsg.Text,
			"name":      strings.TrimSpace(targetMsg.Sender.FirstName + " " + targetMsg.Sender.LastName),
			"username":  targetMsg.Sender.Username,
			"reporters": []string{reporterKey},
		},
	})
	if err != nil {
		return
	}
	if created && stStr == "queued" {
		_ = st.EnqueueAILearningJob(ctx, sid, chatID, targetUserID)
		rh.svc.learning.Wake()
		return
	}
	if !created && sample != nil {
		p := rowPayload(sample)
		reps := parseAnyStringSlice(p["reporters"])
		if !slices.Contains(reps, reporterKey) {
			reps = append(reps, reporterKey)
			p["reporters"] = reps
			_ = st.SaveAISample(ctx, sample, guarddb.AISampleUpdate{Payload: p})
		}
	}
}

// ────────────────────────────── Notifier ──────────────────────────────

// Notifier 持久化投递并原地更新管理员/复核群案件卡片（对齐 notification.py）。
type Notifier struct {
	svc *Service
}

// NewNotifier 创建通知同步器。
func NewNotifier(svc *Service) *Notifier {
	return &Notifier{svc: svc}
}

// Targets 返回指定群与事件类别的目标接收者列表。
func (n *Notifier) Targets(grp GuardGroup, eventCat string) []int64 {
	if !grp.NotificationEnabled {
		return nil
	}
	if eventCat != "" && !slices.Contains(grp.NotificationEvents, eventCat) {
		return nil
	}
	base := grp.NotificationIDs
	if len(base) == 0 {
		base = n.svc.DefaultAdmins()
	}
	var out []int64
	for _, id := range base {
		if id != 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// RenderCaseCardHTML 渲染 HTML 格式的群管案件审核卡片。
func RenderCaseCardHTML(caseRow guarddb.Row) string {
	payload := rowPayload(caseRow)
	name := html.EscapeString(rowStr(payload, "name"))
	if name == "" {
		name = strconv.FormatInt(rowInt64(caseRow, "user_id"), 10)
	}
	ev, _ := payload["evidence"].(map[string]any)
	reason := html.EscapeString(rowStr(ev, "reason"))
	snippet := html.EscapeString(rowStr(ev, "snippet"))

	var b strings.Builder
	fmt.Fprintf(&b, "🛡 <b>Sakura Guard 风控通知</b>\n")
	fmt.Fprintf(&b, "• 案件：<code>%s</code>\n", html.EscapeString(rowStr(caseRow, "id")))
	fmt.Fprintf(&b, "• 群组：<code>%d</code>\n", rowInt64(caseRow, "chat_id"))
	fmt.Fprintf(&b, "• 用户：%s (<code>%d</code>)\n", name, rowInt64(caseRow, "user_id"))
	fmt.Fprintf(&b, "• 动作：<code>%s</code> | 状态：<b>%s</b>\n",
		html.EscapeString(rowStr(caseRow, "action")),
		html.EscapeString(rowStr(caseRow, "status")))
	if reason != "" {
		fmt.Fprintf(&b, "• 原因：%s\n", reason)
	}
	if snippet != "" {
		if len([]rune(snippet)) > 300 {
			snippet = string([]rune(snippet)[:300]) + "..."
		}
		fmt.Fprintf(&b, "• 摘要：<code>%s</code>\n", snippet)
	}
	if source, _ := payload["guest_actor"].(map[string]any); source != nil {
		task, _ := payload["guest_ban"].(map[string]any)
		status := rowStr(task, "status")
		if status == "" {
			status = "recorded"
		} else if status == "pending" && rowStr(task, "action") == "blacklist" {
			status = "blacklist_pending"
		}
		notice := i18n.T("guard.guest_ban_notice", map[string]string{
			"user_id": strconv.FormatInt(rowInt64(source, "user_id"), 10),
			"state":   i18n.T("guard.guest_ban_"+status, nil),
		})
		fmt.Fprintf(&b, "• %s\n", html.EscapeString(notice))
	}
	return b.String()
}

// BuildReviewButtons 构建与 Python notification.py:review_buttons 完全兼容的 sg:r:{id}:{code}:{gen} 审核按钮矩阵。
func BuildReviewButtons(caseRow guarddb.Row, grp GuardGroup) [][]mtproto.InlineButton {
	payload := rowPayload(caseRow)
	if payload["notification_replaced_by"] != nil {
		return nil
	}
	caseID := rowStr(caseRow, "id")
	action := rowStr(caseRow, "action")
	status := rowStr(caseRow, "status")
	if caseButtons, match := verificationNoticeButtons(caseRow, grp); match {
		return caseButtons
	}
	gen := rowInt64(payload, "review_generation")
	isBan := action == "ban" || rowBool(payload, "review_ban_confirmed")
	prefix := fmt.Sprintf("sg:r:%s:", caseID)

	var rows [][]mtproto.InlineButton
	if action == "report" && status == "pending" && !isBan {
		rows = append(rows,
			[]mtproto.InlineButton{{Text: "🙈 忽略举报", Data: fmt.Sprintf("%su:%d", prefix, gen)}},
			[]mtproto.InlineButton{{Text: "🚫 封禁成员", Data: fmt.Sprintf("%sk:%d", prefix, gen)}},
			[]mtproto.InlineButton{{Text: "⛔ 全局拉黑", Data: fmt.Sprintf("%sb:%d", prefix, gen)}},
		)
	} else if isBan && status == "pending" {
		rows = append(rows,
			[]mtproto.InlineButton{{Text: "✅ 解封并加白", Data: fmt.Sprintf("%sa:%d", prefix, gen)}},
			[]mtproto.InlineButton{{Text: "🔓 仅解封", Data: fmt.Sprintf("%su:%d", prefix, gen)}},
			[]mtproto.InlineButton{{Text: "⛔ 全局拉黑", Data: fmt.Sprintf("%sb:%d", prefix, gen)}},
		)
	} else if (action == "mute" || action == "warn") && (status == "pending" || status == "done") {
		rows = append(rows,
			[]mtproto.InlineButton{{Text: "✅ 解除并加白", Data: fmt.Sprintf("%sa:%d", prefix, gen)}},
			[]mtproto.InlineButton{{Text: "🔓 解除限制", Data: fmt.Sprintf("%su:%d", prefix, gen)}},
			[]mtproto.InlineButton{{Text: "🚫 升级封禁", Data: fmt.Sprintf("%sk:%d", prefix, gen)}},
		)
	}

	if grp.KickButtonEnabled && status != "kicked" && status != "executing" && !strings.HasPrefix(status, "reviewing_") {
		if action == "report" || action == "mute" || action == "warn" {
			rows = append(rows, []mtproto.InlineButton{{Text: "👢 踢出群聊", Data: fmt.Sprintf("%sj:%d", prefix, gen)}})
		}
	}
	if action == "report" && rowInt64(payload, "message_id") > 0 {
		delMap, _ := payload["report_message_deletion"].(map[string]any)
		if rowStr(delMap, "status") != "deleted" {
			rows = append(rows, []mtproto.InlineButton{{Text: "🗑 删除被举报消息", Data: fmt.Sprintf("%sd:%d", prefix, gen)}})
		}
	}
	return rows
}

func verificationNoticeButtons(row guarddb.Row, grp GuardGroup) ([][]mtproto.InlineButton, bool) {
	p := rowPayload(row)
	action := rowStr(row, "action")
	if !slices.Contains([]string{"verify_start", "verify_pass", "verify_skip"}, action) &&
		(p["verification_failure"] == nil || rowStr(p, "verification_result") != "held") {
		return nil, false
	}
	state := rowStr(p, "verification_state")
	if state == "" {
		if action == "verify_skip" && rowStr(row, "status") == "done" {
			state = "admin_skipped"
		} else if p["verification_failure"] != nil && rowStr(p, "verification_result") == "held" {
			state = "held"
		}
	}
	active := slices.Contains([]string{"waiting", "passing", "cancelling", "held"}, state)
	if !active && state != "passed" && state != "admin_skipped" {
		return nil, true
	}
	var buttons [][]mtproto.InlineButton
	for _, code := range []string{"b", "x", "j", "s"} {
		action := verificationAdminCodes[code]
		if (action == "kick" && !grp.KickButtonEnabled) || (action == "skip" && !active) ||
			(action != "kick" && !slices.Contains(grp.VerificationAdminActions, action)) {
			continue
		}
		buttons = append(buttons, []mtproto.InlineButton{{
			Text: i18n.T("guard.verify_"+action+"_button", nil), Data: "sg:n:" + rowStr(row, "id") + ":" + code,
		}})
	}
	return buttons, true
}

// ────────────────────────────── ReviewHandler ──────────────────────────────

// ReviewHandler 处理 Telegram 内联按钮 sg:r:* 与 WebUI POST /api/guard/cases/{id}/review 人工审核（对齐 review_handler.py）。
type ReviewHandler struct {
	svc *Service
}

// NewReviewHandler 创建案件审核处理器。
func NewReviewHandler(svc *Service) *ReviewHandler {
	return &ReviewHandler{svc: svc}
}

// HandleCallback 解析并执行 sg:r:{case_id}:{code}[:{gen}] 回调。
func (rh *ReviewHandler) HandleCallback(ctx context.Context, cb *mtproto.CallbackEvent, subParts []string) error {
	if len(subParts) < 2 || len(subParts[0]) != 32 {
		return rh.answer(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
	}
	act, ok := reviewCallbackCode[subParts[1]]
	if !ok {
		return rh.answer(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
	}
	var expectedGen *int64
	if len(subParts) >= 3 && subParts[2] != "" {
		g, err := strconv.ParseInt(subParts[2], 10, 64)
		if err != nil {
			return rh.answer(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
		}
		expectedGen = &g
	}
	res, err := rh.Review(ctx, subParts[0], act, cb.SenderID, false, nil, expectedGen)
	if err != nil {
		var ge GuardError
		if errors.As(err, &ge) {
			return rh.answer(ctx, cb.QueryID, i18n.T(string(ge), nil), true)
		}
		return rh.answer(ctx, cb.QueryID, i18n.T("guard.operation_failed", nil), true)
	}
	if act == "delete" {
		return rh.answer(ctx, cb.QueryID, i18n.T("guard.report_delete_done", nil), true)
	}
	return rh.answer(ctx, cb.QueryID, fmt.Sprintf("处理完成：%s", rowStr(res, "status")), true)
}

// Review 执行人工审核（支持 Telegram 数字 ID 或 WebUI "admin" 字符串）。
func (rh *ReviewHandler) Review(
	ctx context.Context,
	caseID string,
	action string,
	reviewer any,
	web bool,
	expectedChatID *int64,
	expectedGen *int64,
) (guarddb.Row, error) {
	if !slices.Contains([]string{"allow", "blacklist", "unban", "ban", "delete", "kick"}, action) {
		return nil, GuardError("guard.invalid_callback")
	}
	if rh.svc == nil || rh.svc.repo == nil || rh.svc.repo.DB == nil {
		return nil, GuardError("guard.unavailable")
	}
	initial, err := rh.svc.repo.GetCase(ctx, caseID)
	if err != nil || initial == nil {
		return nil, GuardError("guard.invalid_callback")
	}
	chatID := rowInt64(initial, "chat_id")
	if expectedChatID != nil && chatID != *expectedChatID {
		return nil, GuardError("guard.invalid_callback")
	}
	if !rh.authorizedActor(ctx, reviewer, chatID, web) {
		return nil, GuardError("error.permission_denied")
	}
	if action == "blacklist" && !rh.isGlobalAdmin(reviewer) {
		return nil, GuardError("error.permission_denied")
	}

	userID := rowInt64(initial, "user_id")
	var out guarddb.Row
	err = rh.svc.repo.WithLock(ctx, userID, func(st *guarddb.Store) error {
		cur, cerr := st.Case(ctx, caseID)
		if cerr != nil || cur == nil {
			return GuardError("guard.invalid_callback")
		}
		caseAct := rowStr(cur, "action")
		if !slices.Contains([]string{"ban", "mute", "warn", "report"}, caseAct) {
			return GuardError("guard.invalid_callback")
		}
		payload := rowPayload(cur)
		if action != "delete" && expectedGen != nil && *expectedGen != rowInt64(payload, "review_generation") {
			return GuardError("guard.already_processing")
		}

		// 单独删除被举报消息子操作
		if action == "delete" {
			_, revActor := parseReviewerIdentity(reviewer)
			deleteErr := rh.requestMessageDeletion(ctx, st, cur, revActor, false)
			out, cerr = st.Case(ctx, caseID)
			rh.svc.notifier.Sync(ctx, st, out, rh.svc.Group(chatID))
			return errors.Join(deleteErr, cerr)
		}

		revIDPtr, revActor := parseReviewerIdentity(reviewer)
		if action == "blacklist" && rowStr(cur, "status") == "blacklisted" {
			updated := guarddb.BlacklistTask(payload, revIDPtr, revActor, true)
			if err := st.UpdateCase(ctx, caseID, "blacklisted", nil, updated); err != nil {
				return err
			}
			out, cerr = st.Case(ctx, caseID)
			if cerr == nil {
				rh.svc.notifier.Sync(ctx, st, out, rh.svc.Group(chatID))
			}
			return cerr
		}
		claimed, clErr := st.ClaimReview(ctx, caseID, action, revIDPtr, revActor)
		if clErr != nil {
			return clErr
		}
		if !claimed {
			return GuardError("guard.already_processing")
		}

		cur, cerr = st.Case(ctx, caseID)
		if cerr != nil || cur == nil {
			return cerr
		}
		if err := rh.resumeReview(ctx, st, cur); err != nil {
			return err
		}
		out, cerr = st.Case(ctx, caseID)
		return cerr
	})
	return out, err
}

// resumeReview retries a durable manual decision after an ambiguous Telegram or MySQL result.
func (rh *ReviewHandler) resumeReview(ctx context.Context, st *guarddb.Store, cur guarddb.Row) (resultErr error) {
	status := rowStr(cur, "status")
	action, ok := strings.CutPrefix(status, "reviewing_")
	if !ok || !slices.Contains([]string{"allow", "unban", "ban", "blacklist", "kick"}, action) {
		return GuardError("guard.invalid_callback")
	}
	caseID := rowStr(cur, "id")
	chatID, userID := rowInt64(cur, "chat_id"), rowInt64(cur, "user_id")
	revActor := rowStr(cur, "reviewer_actor")
	defer func() {
		if resultErr != nil && ctx.Err() == nil {
			detail := resultErr.Error()
			_, _ = st.TransitionCase(ctx, caseID, status, status, &detail, nil)
		}
	}()
	payload := rowPayload(cur)
	if rh.svc.ops == nil {
		return GuardError("guard.unavailable")
	}
	reviewerID := rowInt64(cur, "reviewer_id")
	if revActor != "webui:admin" && !rh.authorizedActor(ctx, reviewerID, chatID, false) {
		return GuardError("error.permission_denied")
	}
	isReportOnly := rowStr(cur, "action") == "report" && !rowBool(payload, "review_ban_confirmed")
	if (action == "allow" || action == "unban") && !isReportOnly {
		if black, err := st.Blacklist(ctx, userID); err != nil {
			return err
		} else if black != nil {
			return GuardError("guard.blacklist_conflict")
		}
	}

	// 执行对应的 Telegram 侧动作
	if rh.svc.ops != nil {
		switch action {
		case "allow", "unban":
			if err := rh.ReleaseRestriction(ctx, st, cur); err != nil {
				return err
			}
		case "ban", "blacklist":
			grp := rh.svc.Group(chatID)
			if rh.svc.checkProtection(ctx, chatID, userID, grp, true) != protectionUnprotected {
				return GuardError("guard.verification_target_protected")
			}
			if rowStr(cur, "action") == "report" && rowInt64(payload, "message_id") > 0 {
				// Telegram deletion confirmation is its own durable operation.
				if err := rh.requestMessageDeletion(ctx, st, cur, revActor, false); err != nil && payload["report_message_deletion"] == nil {
					return err
				}
			}
			if isBasicGroupID(chatID) {
				if err := rh.svc.ops.KickBasicGroup(ctx, chatID, userID); err != nil {
					return GuardError("guard.operation_failed")
				}
			} else {
				if err := rh.svc.ops.Ban(ctx, chatID, userID); err != nil {
					return GuardError("guard.operation_failed")
				}
			}
			if rh.svc.banHistory != nil && !isBasicGroupID(chatID) {
				rh.svc.banHistory.ScheduleOnPayload(ctx, chatID, userID, payload)
				_ = st.UpdateCase(ctx, caseID, rowStr(cur, "status"), nil, payload)
				cur, _ = st.Case(ctx, caseID)
			}
		case "kick":
			if err := rh.svc.executeKickTwoPhase(ctx, st, cur); err != nil {
				return GuardError("guard.operation_failed")
			}
			cur, _ = st.Case(ctx, caseID)
		}
	}

	if ferr := st.FinishReview(ctx, cur, action, nil); ferr != nil {
		if errors.Is(ferr, guarddb.ErrGlobalBlacklistConflict) {
			return GuardError("guard.blacklist_conflict")
		}
		return ferr
	}
	out, err := st.Case(ctx, caseID)
	if err != nil || out == nil {
		return err
	}
	if action == "ban" && out != nil {
		p := rowPayload(out)
		if p["guest_ban"] == nil && p["guest_actor"] != nil {
			prepareGuestBan(p)
			if err := st.UpdateCase(ctx, caseID, rowStr(out, "status"), nil, p); err != nil {
				return err
			}
			out, _ = st.Case(ctx, caseID)
		}
	}

	// AI 学习正负反馈闭环
	if rh.svc.learning != nil {
		switch action {
		case "allow", "unban":
			rh.svc.learning.RecordWhitelist(ctx, st, chatID, userID, revActor)
		case "ban", "blacklist":
			src := "review_outcome"
			if action == "blacklist" {
				src = "admin_blacklist"
			}
			if rh.svc.learning.Enabled(chatID) && rh.svc.learning.SourceAllowed(src) {
				sid := newUUIDHex()
				cidCopy := caseID
				if _, created, _ := st.CreateAISample(ctx, guarddb.AISampleInsert{
					ID:             sid,
					IdempotencyKey: sha256Hex("ai-review-outcome:" + caseID + ":" + action),
					ChatID:         chatID,
					UserID:         userID,
					Source:         src,
					CaseID:         &cidCopy,
					Label:          "positive",
					Status:         "queued",
					Payload:        rowPayload(out),
				}); created {
					_ = st.EnqueueAILearningJob(ctx, sid, chatID, userID)
					rh.svc.learning.Wake()
				}
			}
		}
	}

	if rh.svc.banHistory != nil && (action == "ban" || action == "blacklist") {
		rh.svc.banHistory.Resume(ctx, st, out, false)
		out, _ = st.Case(ctx, caseID)
	}
	if rh.svc.notifier != nil && out != nil {
		rh.svc.notifier.Sync(ctx, st, out, rh.svc.Group(chatID))
	}
	return nil
}

// ReleaseRestriction 安全解除案件对成员施加的封禁或禁言（检查无更新禁言/活跃封禁冲突）。
func (rh *ReviewHandler) ReleaseRestriction(ctx context.Context, st *guarddb.Store, caseRow guarddb.Row) error {
	if rh.svc.ops == nil {
		return nil
	}
	chatID := rowInt64(caseRow, "chat_id")
	userID := rowInt64(caseRow, "user_id")
	caseID := rowStr(caseRow, "id")
	caseAct := rowStr(caseRow, "action")
	payload := rowPayload(caseRow)
	isBan := caseAct == "ban" || rowBool(payload, "review_ban_confirmed")

	if otherBan, _ := st.ActiveBan(ctx, chatID, userID, caseID); otherBan != nil {
		return GuardError("guard.other_ban_conflict")
	}

	if isBan {
		if isBasicGroupID(chatID) {
			return nil
		}
		curRights, err := rh.svc.ops.Rights(ctx, chatID, userID)
		if err != nil {
			return nil
		}
		if curRights.ViewMessages {
			if err := rh.svc.ops.Unban(ctx, chatID, userID); err != nil {
				return GuardError("guard.operation_failed")
			}
		}
		return nil
	}

	if caseAct == "mute" {
		if later, _ := st.LaterMute(ctx, caseRow); later != nil {
			return nil
		}
		origMap, _ := payload["original_rights"].(map[string]any)
		origRights := mtproto.BannedRightsFromMap(origMap)
		if err := rh.svc.ops.SetRights(ctx, chatID, userID, origRights); err != nil {
			return GuardError("guard.operation_failed")
		}
	}
	return nil
}

func (rh *ReviewHandler) authorizedActor(ctx context.Context, actor any, chatID int64, web bool) bool {
	if s, ok := actor.(string); ok && s == "admin" && web {
		return true
	}
	uid := actorToInt64(actor)
	if uid <= 0 {
		return false
	}
	return rh.svc.Authorized(ctx, uid, chatID)
}

func (rh *ReviewHandler) isGlobalAdmin(actor any) bool {
	if s, ok := actor.(string); ok && s == "admin" {
		return true
	}
	uid := actorToInt64(actor)
	return uid > 0 && slices.Contains(rh.svc.DefaultAdmins(), uid)
}

func parseReviewerIdentity(actor any) (*int64, string) {
	if s, ok := actor.(string); ok && s == "admin" {
		return nil, "webui:admin"
	}
	uid := actorToInt64(actor)
	if uid > 0 {
		return &uid, fmt.Sprintf("telegram:%d", uid)
	}
	return nil, "webui:admin"
}

func actorToInt64(actor any) int64 {
	switch v := actor.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	default:
		return 0
	}
}

func (rh *ReviewHandler) answer(ctx context.Context, queryID int64, msg string, alert bool) error {
	if rh.svc.ops == nil || queryID == 0 {
		return nil
	}
	return rh.svc.ops.AnswerCallback(ctx, queryID, msg, alert)
}

// ────────────────────────────── BanHistoryCleanup ──────────────────────────────

// BanHistoryCleanup 通过 Bot API banChatMember(revoke_messages=true) 清理被封禁成员群内全部历史消息（对齐 ban_history.py）。
type BanHistoryCleanup struct {
	svc *Service
}

// NewBanHistoryCleanup 创建封禁历史清理器。
func NewBanHistoryCleanup(svc *Service) *BanHistoryCleanup {
	return &BanHistoryCleanup{svc: svc}
}

// ScheduleOnPayload 在刚完成超级群封禁后记录当时的封禁代际（kicked_by + date）并标记待清理。
func (bh *BanHistoryCleanup) ScheduleOnPayload(ctx context.Context, chatID, userID int64, payload map[string]any) {
	if bh == nil || bh.svc == nil || bh.svc.ops == nil || payload == nil || isBasicGroupID(chatID) {
		return
	}
	var kickedBy int64
	var banDateUnix int64
	if perm, err := bh.svc.ops.Permissions(ctx, chatID, userID); err == nil && perm != nil {
		kickedBy = perm.KickedBy
		banDateUnix = perm.Date
	}
	payload["ban_message_cleanup"] = map[string]any{
		"status":       "pending",
		"attempts":     0,
		"kicked_by":    kickedBy,
		"ban_date":     banDateUnix,
		"scheduled_at": time.Now().UTC().Format(time.RFC3339),
	}
}

// Resume 在持有用户锁下核验封禁代际未变后调用 Bot API 撤回该成员群内历史消息。
func (bh *BanHistoryCleanup) Resume(ctx context.Context, st *guarddb.Store, caseRow guarddb.Row, force bool) {
	if bh == nil || bh.svc == nil || bh.svc.ops == nil || bh.svc.appCfg == nil || caseRow == nil {
		return
	}
	payload := rowPayload(caseRow)
	cleanup, ok := payload["ban_message_cleanup"].(map[string]any)
	if !ok || rowStr(cleanup, "status") != "pending" {
		return
	}
	botToken := strings.TrimSpace(bh.svc.appCfg.TelegramBotToken())
	if botToken == "" {
		return
	}
	if idStr, _, ok := strings.Cut(botToken, ":"); ok {
		if tokenBotID, _ := strconv.ParseInt(idStr, 10, 64); tokenBotID != 0 && bh.svc.BotID() != 0 && tokenBotID != bh.svc.BotID() {
			cleanup["status"] = "cancelled"
			cleanup["error"] = "bot_token_mismatch"
			payload["ban_message_cleanup"] = cleanup
			_ = st.UpdateCase(ctx, rowStr(caseRow, "id"), rowStr(caseRow, "status"), nil, payload)
			return
		}
	}

	chatID := rowInt64(caseRow, "chat_id")
	userID := rowInt64(caseRow, "user_id")
	if perm, err := bh.svc.ops.Permissions(ctx, chatID, userID); err == nil && perm != nil {
		if perm.IsAdmin || perm.IsCreator || !perm.BannedRights.ViewMessages {
			cleanup["status"] = "cancelled"
			cleanup["error"] = "ban_generation_changed"
			payload["ban_message_cleanup"] = cleanup
			_ = st.UpdateCase(ctx, rowStr(caseRow, "id"), rowStr(caseRow, "status"), nil, payload)
			return
		}
		expectedBy := rowInt64(cleanup, "kicked_by")
		if expectedBy != 0 && perm.KickedBy != 0 && perm.KickedBy != expectedBy {
			cleanup["status"] = "cancelled"
			cleanup["error"] = "ban_actor_changed"
			payload["ban_message_cleanup"] = cleanup
			_ = st.UpdateCase(ctx, rowStr(caseRow, "id"), rowStr(caseRow, "status"), nil, payload)
			return
		}
	}

	if err := bh.svc.ops.RevokeMessagesViaBotAPI(ctx, botToken, chatID, userID); err != nil {
		attempts := int(rowInt64(cleanup, "attempts")) + 1
		cleanup["attempts"] = attempts
		cleanup["error"] = err.Error()
		if attempts >= 4 && !force {
			cleanup["status"] = "cancelled"
		} else {
			cleanup["retry_at"] = time.Now().UTC().Add(time.Duration(30*attempts) * time.Second).Format("2006-01-02T15:04:05.000000")
		}
		payload["ban_message_cleanup"] = cleanup
		_ = st.UpdateCase(ctx, rowStr(caseRow, "id"), rowStr(caseRow, "status"), nil, payload)
		return
	}

	cleanup["status"] = "deleted"
	delete(cleanup, "error")
	cleanup["completed_at"] = time.Now().UTC().Format(time.RFC3339)
	payload["ban_message_cleanup"] = cleanup
	if delMap, ok := payload["report_message_deletion"].(map[string]any); ok && rowStr(delMap, "status") == "pending" {
		delMap["status"] = "deleted"
		delMap["completed_via"] = "ban_message_cleanup"
		payload["report_message_deletion"] = delMap
	}
	_ = st.UpdateCase(ctx, rowStr(caseRow, "id"), rowStr(caseRow, "status"), nil, payload)
}

// ────────────────────────────── AppealWorkflow ──────────────────────────────

// AppealWorkflow 主进程封禁申诉状态机与授权执行后台协程（对齐 appeals.py）。
type AppealWorkflow struct {
	svc            *Service
	routingChanged bool
	logger         *slog.Logger

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewAppealWorkflow 创建主进程封禁申诉工作流。
func NewAppealWorkflow(svc *Service, logger *slog.Logger) *AppealWorkflow {
	if logger == nil {
		logger = slog.Default()
	}
	return &AppealWorkflow{svc: svc, logger: logger}
}

// MarkRoutingChanged 通知后台循环在配置变更后重发进行中申诉卡片。
func (aw *AppealWorkflow) MarkRoutingChanged() {
	aw.mu.Lock()
	aw.routingChanged = true
	aw.mu.Unlock()
}

// Start 启动 5 秒申诉恢复协程。
func (aw *AppealWorkflow) Start(ctx context.Context) {
	aw.mu.Lock()
	defer aw.mu.Unlock()
	if aw.running {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	aw.cancel = cancel
	aw.running = true
	aw.wg.Add(1)
	go func() {
		defer aw.wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				aw.RecoverOnce(runCtx)
			}
		}
	}()
}

// Stop 停止申诉恢复协程。
func (aw *AppealWorkflow) Stop() {
	aw.mu.Lock()
	if !aw.running {
		aw.mu.Unlock()
		return
	}
	aw.running = false
	cancel := aw.cancel
	aw.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	aw.wg.Wait()
}

func appealSourceSignature(caseRow guarddb.Row) string {
	p := rowPayload(caseRow)
	arr := []any{
		rowStr(caseRow, "id"),
		rowStr(caseRow, "action"),
		rowStr(caseRow, "status"),
		rowInt64(p, "review_generation"),
	}
	b, _ := json.Marshal(arr)
	return sha256Hex(string(b))
}

func appealCaseEligible(caseRow guarddb.Row) bool {
	if caseRow == nil {
		return false
	}
	st := rowStr(caseRow, "status")
	if st != "pending" && st != "blacklisted" && st != "done" {
		return false
	}
	p := rowPayload(caseRow)
	return rowStr(caseRow, "action") == "ban" || st == "blacklisted" || rowBool(p, "review_ban_confirmed")
}

// RecoverOnce 单轮扫描并推进申诉审核请求、草稿过期、通知发布与主 Bot 解封执行。
func (aw *AppealWorkflow) RecoverOnce(ctx context.Context) {
	if aw == nil || aw.svc == nil || aw.svc.repo == nil || aw.svc.repo.DB == nil {
		return
	}
	aw.mu.Lock()
	dirty := aw.routingChanged
	aw.routingChanged = false
	aw.mu.Unlock()
	if dirty {
		_ = aw.svc.repo.InvalidateRouting(ctx)
	}

	appeals, requests, err := aw.svc.repo.Work(ctx)
	if err != nil {
		return
	}
	for _, req := range requests {
		uid := rowInt64(req, "user_id")
		reqID := rowStr(req, "id")
		_ = aw.svc.repo.WithLock(ctx, uid, func(st *guarddb.Store) error {
			cur, _ := st.One(ctx, "SELECT * FROM moderation_appeal_requests WHERE id=?", reqID)
			if cur != nil && rowStr(cur, "status") == "pending" {
				aw.processRequest(ctx, st, cur)
			}
			return nil
		})
	}
	for _, cand := range appeals {
		uid := rowInt64(cand, "user_id")
		aid := rowStr(cand, "id")
		_ = aw.svc.repo.WithLock(ctx, uid, func(st *guarddb.Store) error {
			ap, _ := st.Get(ctx, aid)
			if ap == nil {
				return nil
			}
			stName := rowStr(ap, "status")
			if stName == "draft" {
				upd := parseRowTimeOrNow(ap, "updated_at", time.Now().UTC())
				if time.Since(upd) > 24*time.Hour {
					_ = st.Atomic(ctx, func(txSt *guarddb.Store) error {
						_, err := txSt.TransitionTx(ctx, ap, "withdrawn", "system", "expired", guarddb.TransitionOptions{})
						if err != nil {
							return err
						}
						if inp, _ := txSt.Input(ctx, uid); inp != nil && rowStr(inp, "appeal_id") == aid {
							_ = txSt.ClearInput(ctx, uid, rowStr(inp, "token"))
						}
						return nil
					})
				}
				return nil
			}
			if stName == "submitted" || stName == "pending" || stName == "need_info" {
				caseRow, _ := st.Case(ctx, rowStr(ap, "case_id"))
				p := rowPayload(ap)
				valid := appealCaseEligible(caseRow) && appealSourceSignature(caseRow) == rowStr(p, "source_signature")
				if stName == "submitted" || !valid {
					nextSt, nextAct := "superseded", "superseded"
					if valid {
						nextSt, nextAct = "pending", "accepted"
					}
					ap, _ = st.Transition(ctx, ap, nextSt, "system", nextAct, guarddb.TransitionOptions{})
				}
			}
			if ap != nil && rowStr(ap, "status") == "executing" {
				retryAt := parseRowTimeOrNow(ap, "retry_at", time.Time{})
				if !time.Now().UTC().Before(retryAt) {
					aw.executeApproval(ctx, st, ap)
					ap, _ = st.Get(ctx, aid)
				}
			}
			if ap != nil && rowInt64(ap, "published_revision") < rowInt64(ap, "revision") {
				aw.publishAppeal(ctx, st, ap)
			}
			return nil
		})
	}
}

func (aw *AppealWorkflow) publishAppeal(ctx context.Context, st *guarddb.Store, ap guarddb.Row) {
	chatID := rowInt64(ap, "chat_id")
	grp := aw.svc.Group(chatID)
	revs := grp.ReviewerIDs
	if len(revs) == 0 {
		revs = aw.svc.DefaultAdmins()
	}
	var targets []int64
	targets = append(targets, revs...)
	targets = append(targets, grp.NotificationIDs...)
	p := rowPayload(ap)
	if rowBool(p, "blacklisted") {
		targets = append(targets, aw.svc.DefaultAdmins()...)
	}
	if grp.ReviewChatID != nil {
		targets = append(targets, *grp.ReviewChatID)
	}
	if ev, ok := p["evidence"].([]any); !ok || len(ev) == 0 {
		targets = nil
	}
	_ = st.Publish(ctx, ap, targets)
}

func (aw *AppealWorkflow) processRequest(ctx context.Context, st *guarddb.Store, req guarddb.Row) {
	ap, _ := st.Get(ctx, rowStr(req, "appeal_id"))
	if ap == nil {
		errCode := "guard.invalid_callback"
		_ = st.FinishRequest(ctx, req, &errCode, nil)
		return
	}
	actorID := rowInt64(req, "actor_id")
	action := rowStr(req, "action")
	chatID := rowInt64(ap, "chat_id")
	if actorID == rowInt64(ap, "user_id") || !aw.svc.Authorized(ctx, actorID, chatID) {
		errCode := "error.permission_denied"
		_ = st.FinishRequest(ctx, req, &errCode, nil)
		return
	}
	if action == "global" && !slices.Contains(aw.svc.DefaultAdmins(), actorID) {
		errCode := "appeal.global_admin_required"
		_ = st.FinishRequest(ctx, req, &errCode, nil)
		return
	}
	if rowInt64(req, "revision") != rowInt64(ap, "revision") {
		errCode := "appeal.stale"
		_ = st.FinishRequest(ctx, req, &errCode, nil)
		return
	}

	apPayload := rowPayload(ap)
	switch action {
	case "evidence":
		_ = st.FinishRequest(ctx, req, nil, map[string]any{
			"evidence":          apPayload["evidence"],
			"source":            apPayload["source"],
			"source_created_at": apPayload["source_created_at"],
		})
		return
	case "audit":
		evs, _ := st.Events(ctx, rowStr(ap, "id"))
		_ = st.FinishRequest(ctx, req, nil, map[string]any{"audit": evs})
		return
	}

	reqPayload := rowPayload(req)
	reason := strings.TrimSpace(rowStr(reqPayload, "reason"))
	if reason == "" {
		errCode := "appeal.reason_required"
		_ = st.FinishRequest(ctx, req, &errCode, nil)
		return
	}
	decision := map[string]any{
		"actor_id":      actorID,
		"action":        action,
		"reason":        reason,
		"authorized_at": time.Now().UTC().Format(time.RFC3339),
		"request_id":    rowStr(req, "id"),
	}
	newPayload := deepCloneAnyMap(apPayload)
	newPayload["last_request_id"] = rowStr(req, "id")
	newPayload["last_review"] = decision

	if action == "reject" || action == "more" {
		targetSt := "need_info"
		evAct := "requested_info"
		if action == "reject" {
			targetSt = "rejected"
			evAct = "rejected"
		}
		updated, _ := st.Transition(ctx, ap, targetSt, fmt.Sprintf("telegram:%d", actorID), evAct, guarddb.TransitionOptions{
			Payload: newPayload,
			Detail:  decision,
		})
		if updated != nil {
			aw.publishAppeal(ctx, st, updated)
			_ = st.FinishRequest(ctx, req, nil, map[string]any{"status": rowStr(updated, "status")})
		}
		return
	}

	black, _ := st.Blacklist(ctx, rowInt64(ap, "user_id"))
	if black != nil && action != "global" {
		errCode := "guard.blacklist_conflict"
		_ = st.FinishRequest(ctx, req, &errCode, nil)
		return
	}
	if black != nil {
		decision["blacklist_case_id"] = rowStr(black, "case_id")
	}
	newPayload["decision"] = decision
	newPayload["blacklisted"] = black != nil
	updated, _ := st.Transition(ctx, ap, "executing", fmt.Sprintf("telegram:%d", actorID), "approved_intent", guarddb.TransitionOptions{
		Payload: newPayload,
		Detail:  decision,
	})
	if updated != nil {
		aw.executeApproval(ctx, st, updated)
		latest, _ := st.Get(ctx, rowStr(ap, "id"))
		if latest != nil {
			aw.publishAppeal(ctx, st, latest)
			_ = st.FinishRequest(ctx, req, nil, map[string]any{"status": rowStr(latest, "status")})
		}
	}
}

func (aw *AppealWorkflow) executeApproval(ctx context.Context, st *guarddb.Store, ap guarddb.Row) {
	caseRow, _ := st.Case(ctx, rowStr(ap, "case_id"))
	p := rowPayload(ap)
	if !appealCaseEligible(caseRow) || appealSourceSignature(caseRow) != rowStr(p, "source_signature") {
		errCode := "appeal.source_changed"
		_, _ = st.Transition(ctx, ap, "superseded", "system", "execution_blocked", guarddb.TransitionOptions{Error: &errCode})
		return
	}
	if aw.svc.reviews != nil {
		synthetic := guarddb.Row{
			"id":      rowStr(caseRow, "id"),
			"chat_id": rowInt64(caseRow, "chat_id"),
			"user_id": rowInt64(caseRow, "user_id"),
			"action":  "ban",
			"payload": caseRow["payload"],
		}
		if err := aw.svc.reviews.ReleaseRestriction(ctx, st, synthetic); err != nil {
			var ge GuardError
			if errors.As(err, &ge) {
				errCode := string(ge)
				_, _ = st.Transition(ctx, ap, "pending", "system", "execution_blocked", guarddb.TransitionOptions{Error: &errCode})
				return
			}
			_ = st.ExecutionError(ctx, ap, err.Error())
			return
		}
	}
	if _, err := st.FinishApproval(ctx, ap, caseRow); err != nil {
		_ = st.ExecutionError(ctx, ap, err.Error())
		return
	}
	decision, _ := p["decision"].(map[string]any)
	actorID := rowInt64(decision, "actor_id")
	if aw.svc.learning != nil {
		aw.svc.learning.RecordWhitelist(ctx, st, rowInt64(ap, "chat_id"), rowInt64(ap, "user_id"), fmt.Sprintf("telegram:%d", actorID))
	}
}
