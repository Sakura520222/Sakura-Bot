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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type wordChallenge struct {
	Label string
	Emoji string
}

var captchaWords = []wordChallenge{
	{Label: "苹果", Emoji: "🍎"},
	{Label: "樱花", Emoji: "🌸"},
	{Label: "月亮", Emoji: "🌙"},
	{Label: "火箭", Emoji: "🚀"},
	{Label: "小猫", Emoji: "🐱"},
	{Label: "鲸鱼", Emoji: "🐋"},
	{Label: "闪电", Emoji: "⚡"},
	{Label: "星星", Emoji: "⭐"},
}

// VerificationManager 管理入群验证（legacy 群内按钮与 assistant 助手私聊双模式）、风险评分与超时推进（对齐 verification.py）。
type VerificationManager struct {
	svc *Service
}

// NewVerificationManager 创建入群验证管理器。
func NewVerificationManager(svc *Service) *VerificationManager {
	return &VerificationManager{svc: svc}
}

// ConfiguredAssistantID 从 QA_BOT_TOKEN 提取助手机器人数字 ID（供 /api/guard/groups/{chat_id}/verification 诊断查询）。
func (vm *VerificationManager) ConfiguredAssistantID() int64 {
	if vm == nil || vm.svc == nil || vm.svc.appCfg == nil {
		return 0
	}
	if !vm.svc.appCfg.QABotEnabled() || vm.svc.appCfg.QABotToken() == "" {
		return 0
	}
	idStr, _, ok := strings.Cut(strings.TrimSpace(vm.svc.appCfg.QABotToken()), ":")
	if !ok {
		return 0
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 || id == vm.svc.BotID() {
		return 0
	}
	return id
}

// ComputeRiskScore 计算新入群成员的风险分及命中特征（对齐 verification.py:risk_profile）。
func ComputeRiskScore(
	user mtproto.ProfileSummary,
	similarNamesCount int,
	hasPriorEnforcement bool,
	weights map[string]int,
) (int, []string, string) {
	fullName := strings.TrimSpace(user.FirstName + " " + user.LastName)
	normName := NormalizeText(fullName)
	cluster := ""
	if normName != "" {
		runes := []rune(normName)
		if len(runes) > 6 {
			runes = runes[:6]
		}
		cluster = string(runes)
	}

	var flags []string
	score := 0
	w := func(key string, def int) int {
		if weights != nil {
			if v, ok := weights[key]; ok {
				return v
			}
		}
		return def
	}

	if user.Username == "" && len(user.ActiveUsernames) == 0 {
		score += w("no_username", 1)
		flags = append(flags, "no_username")
	}
	if !user.HasPhoto {
		score += w("no_photo", 1)
		flags = append(flags, "no_photo")
	}
	if looksRandomName(fullName) {
		score += w("random_name", 2)
		flags = append(flags, "random_name")
	}
	if similarNamesCount >= 2 {
		score += w("similar_names", 2)
		flags = append(flags, "similar_names")
	}
	if hasPriorEnforcement {
		score += w("prior_enforcement", 4)
		flags = append(flags, "prior_enforcement")
	}
	return score, flags, cluster
}

func looksRandomName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return true
	}
	digits := 0
	letters := 0
	for _, r := range trimmed {
		if unicode.IsDigit(r) {
			digits++
		} else if unicode.IsLetter(r) {
			letters++
		}
	}
	return digits >= 5 && digits > letters
}

// BuildLegacyChallenge 生成一道防机器人的内联按钮挑战题，返回 (提示文案, 选项数组, 正确答案下标)。
func BuildLegacyChallenge(questionType string) (string, []string, int) {
	if questionType == "position" {
		indices := cryptoPerm(len(captchaWords), 4)
		posIdx := cryptoIntn(4)
		options := make([]string, 4)
		labels := make([]string, 4)
		for i, idx := range indices {
			options[i] = captchaWords[idx].Emoji
			labels[i] = captchaWords[idx].Emoji
		}
		prompt := fmt.Sprintf("请点击下方第 %d 个图标（排列：%s）", posIdx+1, strings.Join(labels, " "))
		return prompt, options, posIdx
	}

	indices := cryptoPerm(len(captchaWords), 4)
	ansPos := cryptoIntn(4)
	target := captchaWords[indices[ansPos]]
	options := make([]string, 4)
	for i, idx := range indices {
		options[i] = captchaWords[idx].Emoji + " " + captchaWords[idx].Label
	}
	prompt := fmt.Sprintf("请在规定时间内点击包含「%s」的按钮完成入群验证：", target.Label)
	return prompt, options, ansPos
}

func cryptoIntn(n int) int {
	if n <= 1 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return int(time.Now().UnixNano() % int64(n))
	}
	return int(v.Int64())
}

func cryptoPerm(total, take int) []int {
	arr := make([]int, total)
	for i := range arr {
		arr[i] = i
	}
	for i := total - 1; i > 0; i-- {
		j := cryptoIntn(i + 1)
		arr[i], arr[j] = arr[j], arr[i]
	}
	if take < total {
		return arr[:take]
	}
	return arr
}

// StartVerification 在持有用户锁的前提下为新入群成员启动入群验证（禁言快照 -> 风险评估 -> 发送挑战卡片）。
func (vm *VerificationManager) StartVerification(
	ctx context.Context,
	st *guarddb.Store,
	chatID int64,
	user mtproto.ProfileSummary,
	grp GuardGroup,
	eventToken string,
) error {
	if !grp.Enabled || !grp.VerifyJoin || vm.svc.ops == nil {
		return nil
	}
	whitelisted, err := st.Whitelisted(ctx, chatID, user.ID)
	if err != nil {
		return err
	}
	if whitelisted {
		return nil
	}

	existing, err := st.VerificationSession(ctx, chatID, user.ID)
	if err != nil {
		return err
	}
	if existing != nil && rowStr(existing, "status") != "checking" &&
		(guarddb.IsActiveVerificationStatus(rowStr(existing, "status")) || rowStr(rowPayload(existing), "join_token") == eventToken) {
		return nil
	}

	// Persist the intended restriction before applying it to Telegram.
	origRights, err := vm.svc.ops.Rights(ctx, chatID, user.ID)
	if err != nil {
		return err
	}

	prior, _ := st.PreviousVerificationEnforcement(ctx, user.ID)
	_, _, prelimCluster := ComputeRiskScore(user, 0, prior, grp.VerificationRiskWeights)
	simCount := 0
	if prelimCluster != "" {
		simCount, _ = st.SimilarVerificationNames(ctx, chatID, user.ID, prelimCluster)
	}
	score, flags, cluster := ComputeRiskScore(user, simCount, prior, grp.VerificationRiskWeights)

	steps := grp.VerificationSteps
	holdForAdmin := false
	if score >= grp.VerificationRiskThreshold {
		if grp.VerificationRiskAction == "hold" {
			holdForAdmin = true
		} else {
			steps = min(5, steps+grp.VerificationRiskExtraSteps)
		}
	}

	sessionID := newUUIDHex()
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(grp.VerificationTimeout) * time.Second)
	if rowStr(existing, "status") == "checking" {
		sessionID = rowStr(existing, "id")
		expiresAt = parseRowTimeOrNow(existing, "expires_at", expiresAt)
	}
	fullName := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if fullName == "" {
		fullName = strconv.FormatInt(user.ID, 10)
	}

	payload := map[string]any{
		"name":                  fullName,
		"username":              user.Username,
		"original_rights":       mtproto.BannedRightsToMap(origRights),
		"requested_rights":      mtproto.BannedRightsToMap(mtproto.VerificationRights(origRights)),
		"restriction_confirmed": false,
		"join_token":            eventToken,
		"cleanup_pending":       true,
		"lifecycle":             true,
		"lifecycle_recorded":    false,
		"profile": map[string]any{
			"risk_score":   score,
			"risk_flags":   flags,
			"name_cluster": cluster,
		},
	}

	status := "waiting"
	var text string
	var rows [][]mtproto.InlineButton

	assistantUsername := ""
	if grp.VerificationMode == "assistant" {
		botID := vm.ConfiguredAssistantID()
		if botID != 0 {
			if row, _ := st.ActiveAssistant(ctx, botID); row != nil {
				assistantUsername = strings.TrimPrefix(rowStr(row, "username"), "@")
			}
		}
	}

	if holdForAdmin {
		status = "held"
		text = fmt.Sprintf("⚠️ 新成员 %s 触发高风险入群拦截（风险分 %d），已冻结发言等待管理员人工审核。", fullName, score)
		rows = vm.buildAdminButtons(sessionID, grp)
	} else if grp.VerificationMode == "assistant" && assistantUsername != "" {
		var secretBytes [12]byte
		if _, err := rand.Read(secretBytes[:]); err != nil {
			return err
		}
		secret := hex.EncodeToString(secretBytes[:])
		payload["assistant"] = map[string]any{
			"version":        1,
			"state":          "pending",
			"secret":         secret,
			"username":       assistantUsername,
			"bot_id":         vm.ConfiguredAssistantID(),
			"steps":          steps,
			"question_types": grp.VerificationQuestionTypes,
			"max_attempts":   grp.VerificationMaxAttempts,
			"session_id":     sessionID,
			"chat_id":        chatID,
			"actor_id":       user.ID,
			"receipts":       []string{},
		}
		text = fmt.Sprintf("👋 欢迎 %s 加入本群！\n请在 %d 秒内点击下方按钮前往验证助手完成人机验证：", fullName, grp.VerificationTimeout)
		rows = append(rows, []mtproto.InlineButton{
			{Text: "🔐 前往助手完成验证", URL: fmt.Sprintf("https://t.me/%s?start=verify_%s%s", assistantUsername, sessionID, secret)},
		})
		rows = append(rows, vm.buildAdminButtons(sessionID, grp)...)
	} else {
		qType := "word"
		if len(grp.VerificationQuestionTypes) > 0 {
			qType = grp.VerificationQuestionTypes[0]
		}
		prompt, options, answer := BuildLegacyChallenge(qType)
		payload["challenge"] = map[string]any{
			"prompt":  prompt,
			"options": options,
			"answer":  answer,
		}
		text = fmt.Sprintf("👋 欢迎 %s！%s（剩余 %d 秒）", fullName, prompt, grp.VerificationTimeout)
		rows = append(rows, legacyChallengeButtons(sessionID, options)...)
		rows = append(rows, vm.buildAdminButtons(sessionID, grp)...)
	}

	admission := verificationAdmission{Status: status, Text: text, Buttons: rows}
	payload["go_admission"] = admission
	session := guarddb.VerificationSessionSave{
		ID:        sessionID,
		ChatID:    chatID,
		UserID:    user.ID,
		Status:    "preparing",
		Payload:   payload,
		ExpiresAt: expiresAt,
	}
	if err := st.SaveVerificationSession(ctx, session); err != nil {
		return err
	}
	return vm.applyVerificationAdmission(ctx, st, session, admission)
}

func legacyChallengeButtons(sessionID string, options []string) [][]mtproto.InlineButton {
	var rows [][]mtproto.InlineButton
	for start := 0; start < len(options); start += 2 {
		var row []mtproto.InlineButton
		for index := start; index < min(start+2, len(options)); index++ {
			row = append(row, mtproto.InlineButton{Text: options[index], Data: fmt.Sprintf("sg:v:%s:%d", sessionID, index)})
		}
		rows = append(rows, row)
	}
	return rows
}

func (vm *VerificationManager) buildAdminButtons(sessionID string, grp GuardGroup) [][]mtproto.InlineButton {
	var row []mtproto.InlineButton
	for _, act := range grp.VerificationAdminActions {
		switch act {
		case "ban":
			row = append(row, mtproto.InlineButton{Text: "🚫 封禁", Data: fmt.Sprintf("sg:v:a:%s:ban", sessionID)})
		case "blacklist":
			row = append(row, mtproto.InlineButton{Text: "⛔ 全局拉黑", Data: fmt.Sprintf("sg:v:a:%s:blacklist", sessionID)})
		case "skip":
			row = append(row, mtproto.InlineButton{Text: "✅ 直接放行", Data: fmt.Sprintf("sg:v:a:%s:skip", sessionID)})
		}
	}
	if grp.KickButtonEnabled {
		row = append(row, mtproto.InlineButton{Text: i18n.T("guard.verify_kick_button", nil), Data: fmt.Sprintf("sg:v:a:%s:kick", sessionID)})
	}
	if len(row) == 0 {
		return nil
	}
	return [][]mtproto.InlineButton{row}
}

// HandleCallback 处理 sg:v:{sessionID}:{choice} 与管理员按钮 sg:v:a:{sessionID}:{ban|blacklist|skip}。
func (vm *VerificationManager) HandleCallback(ctx context.Context, cb *mtproto.CallbackEvent, subParts []string) error {
	if len(subParts) < 2 {
		return vm.answer(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
	}
	if subParts[0] == "a" {
		if len(subParts) != 3 {
			return vm.answer(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
		}
		return vm.handleAdminAction(ctx, cb, subParts[1], subParts[2])
	}
	sessionID := subParts[0]
	if len(subParts) != 2 || cb.QueryID <= 0 {
		return vm.answer(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
	}
	token := subParts[1]
	switch token {
	case "b":
		return vm.handleAdminAction(ctx, cb, sessionID, "ban")
	case "x":
		return vm.handleAdminAction(ctx, cb, sessionID, "blacklist")
	case "j":
		return vm.handleAdminAction(ctx, cb, sessionID, "kick")
	case "s":
		return vm.handleAdminAction(ctx, cb, sessionID, "skip")
	}
	if len(token) == 25 && strings.HasPrefix(token, "a") {
		token = token[1:]
	}
	legacy := assistantReceipt.MatchString(token)
	choice, err := strconv.Atoi(token)
	if err != nil && !legacy {
		return vm.answer(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
	}
	return vm.handleMemberAnswer(ctx, cb, sessionID, choice, token, legacy)
}

func (vm *VerificationManager) handleMemberAnswer(ctx context.Context, cb *mtproto.CallbackEvent, sessionID string, choice int, token string, legacy bool) error {
	sess, err := vm.svc.repo.GetVerificationSession(ctx, sessionID)
	if err != nil || sess == nil {
		return vm.answer(ctx, cb.QueryID, "验证会话已结束", true)
	}
	targetUserID := rowInt64(sess, "user_id")
	if cb.SenderID != targetUserID {
		return vm.answer(ctx, cb.QueryID, "仅限入群本人点击验证按钮", true)
	}

	var toast string
	var alert bool
	_ = vm.svc.repo.WithLock(ctx, targetUserID, func(st *guarddb.Store) error {
		cur, cerr := st.VerificationSessionByID(ctx, sessionID)
		if cerr != nil || cur == nil || rowStr(cur, "status") != "waiting" {
			toast = "验证会话已结束"
			alert = true
			return nil
		}
		chatID := rowInt64(cur, "chat_id")
		grp := vm.svc.Group(chatID)
		payload := rowPayload(cur)
		expires := parseRowTimeOrNow(cur, "expires_at", time.Time{})
		if !grp.Enabled || cb.ChatID != chatID || cb.MessageID <= 0 || int64(cb.MessageID) != rowInt64(cur, "message_id") ||
			!time.Now().UTC().Before(expires) || !rowBool(payload, "restriction_confirmed") || payload["assistant"] != nil {
			toast, alert = i18n.T("guard.invalid_callback", nil), true
			return nil
		}
		var correct, valid bool
		if legacy {
			correct, valid = pythonChallengeAnswer(cur, token)
		} else {
			challenge, _ := payload["challenge"].(map[string]any)
			options := parseAnyStringSlice(challenge["options"])
			answer := int(rowInt64(challenge, "answer"))
			valid = choice >= 0 && choice < len(options) && answer >= 0 && answer < len(options)
			correct = choice == answer
		}
		if !valid {
			toast, alert = i18n.T("guard.invalid_callback", nil), true
			return nil
		}
		query := strconv.FormatInt(cb.QueryID, 10)
		handled := parseAnyStringSlice(payload["handled_queries"])
		if slices.Contains(handled, query) {
			toast, alert = i18n.T("guard.invalid_callback", nil), true
			return nil
		}
		payload["handled_queries"] = append(handled, query)
		attempts := int(rowInt64(cur, "attempts"))

		if correct {
			if rerr := vm.passSession(ctx, st, cur, grp, "passed", 0); rerr != nil {
				toast = "放行失败，请稍后重试"
				alert = true
				return nil
			}
			toast = "✅ 验证通过，欢迎入群！"
			return nil
		}

		attempts++
		if attempts >= grp.VerificationMaxAttempts {
			cur["attempts"] = attempts
			_ = vm.failSession(ctx, st, cur, grp, grp.VerificationWrongAction, "wrong_attempts")
			toast = "❌ 验证错误次数已达上限"
			alert = true
			return nil
		}

		// 刷新新题目防止盲猜
		qType := "word"
		if len(grp.VerificationQuestionTypes) > 0 {
			qType = grp.VerificationQuestionTypes[attempts%len(grp.VerificationQuestionTypes)]
		}
		prompt, options, newAns := BuildLegacyChallenge(qType)
		payload["challenge"] = map[string]any{
			"prompt":  prompt,
			"options": options,
			"answer":  newAns,
		}
		var msgIDPtr *int64
		if mid := rowInt64(cur, "message_id"); mid > 0 {
			msgIDPtr = &mid
			rows := append(legacyChallengeButtons(sessionID, options), vm.buildAdminButtons(sessionID, grp)...)
			_ = vm.svc.ops.EditMessage(ctx, chatID, int(mid),
				fmt.Sprintf("👋 欢迎 %s！%s（错误 %d/%d）", rowStr(payload, "name"), prompt, attempts, grp.VerificationMaxAttempts),
				mtproto.SendOptions{Buttons: rows, ParseMode: "plain"})
		}
		delete(payload, "answer_tokens")
		delete(payload, "answer_context")
		expAt := parseRowTimeOrNow(cur, "expires_at", time.Now().UTC().Add(60*time.Second))
		_ = st.SaveVerificationSession(ctx, guarddb.VerificationSessionSave{
			ID:        sessionID,
			ChatID:    chatID,
			UserID:    targetUserID,
			Status:    "waiting",
			Payload:   payload,
			MessageID: msgIDPtr,
			Attempts:  attempts,
			ExpiresAt: expAt,
		})
		toast = fmt.Sprintf("❌ 选择错误，剩余 %d 次机会", grp.VerificationMaxAttempts-attempts)
		alert = true
		return nil
	})
	return vm.answer(ctx, cb.QueryID, toast, alert)
}

func (vm *VerificationManager) passSession(
	ctx context.Context,
	st *guarddb.Store,
	sess guarddb.Row,
	grp GuardGroup,
	finalStatus string,
	adminID int64,
	receiptRequired ...bool,
) error {
	payload := rowPayload(sess)
	origMap, _ := payload["original_rights"].(map[string]any)
	origRights := mtproto.BannedRightsFromMap(origMap)

	if vm.svc.ops != nil {
		requireAssistantProof := len(receiptRequired) > 0 && receiptRequired[0]
		if err := vm.restoreVerificationRights(ctx, st, sess, origRights, requireAssistantProof); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	if grp.ObservationMinutes > 0 && finalStatus == "passed" {
		payload["observation"] = map[string]any{
			"started_at":  now.Format(time.RFC3339),
			"expires_at":  now.Add(time.Duration(grp.ObservationMinutes) * time.Minute).Format(time.RFC3339),
			"message_ids": []int{},
			"signatures":  []string{},
			"timestamps":  []int64{},
			"hits":        0,
		}
	}
	payload["cleanup_pending"] = true
	payload["lifecycle"] = true
	sess["status"], sess["error"] = finalStatus, nil
	if err := saveVerificationRow(ctx, st, sess); err != nil {
		return err
	}
	return vm.finishVerificationLifecycle(ctx, st, sess)
}

func (vm *VerificationManager) failSession(
	ctx context.Context,
	st *guarddb.Store,
	sess guarddb.Row,
	grp GuardGroup,
	failAction string,
	reason string,
) error {
	chatID := rowInt64(sess, "chat_id")
	userID := rowInt64(sess, "user_id")
	sessionID := rowStr(sess, "id")
	payload := rowPayload(sess)
	intent, _ := payload["failure_intent"].(map[string]any)
	if intent != nil {
		failAction, reason = rowStr(intent, "action"), rowStr(intent, "reason")
	} else {
		intent = map[string]any{
			"case_id": newUUIDHex(), "action": failAction, "reason": reason, "session_id": sessionID,
		}
		payload["failure_intent"] = intent
	}
	if !validMemberActions[failAction] || rowStr(intent, "case_id") == "" {
		return GuardError("guard.invalid_callback")
	}
	save := guarddb.VerificationSessionSave{
		ID: sessionID, ChatID: chatID, UserID: userID, Status: "kicking", Payload: payload,
		Attempts: int(rowInt64(sess, "attempts")), ExpiresAt: parseRowTimeOrNow(sess, "expires_at", time.Now().UTC()),
	}
	if mid := rowInt64(sess, "message_id"); mid > 0 {
		save.MessageID = &mid
	}
	if err := st.SaveVerificationSession(ctx, save); err != nil {
		return err
	}
	caseID := rowStr(intent, "case_id")
	caseRow, err := st.Case(ctx, caseID)
	if err != nil {
		return err
	}
	final := "held"
	action := failAction
	switch failAction {
	case "ban":
		final = "failed_banned"
	case "kick":
		final = "failed_kicked"
	case "allow":
		action = "verify_hold"
	}
	if caseRow == nil {
		casePayload := map[string]any{
			"name": rowStr(payload, "name"), "verification_failure": intent, "verification_result": final,
			"notification_event": "verification_fail",
			"evidence":           map[string]any{"target": "verification", "rule_id": "verification-failed", "reason": reason},
		}
		linkVerificationMessage(casePayload, sess)
		if err := st.CreateCase(ctx, guarddb.CaseInsert{
			ID: caseID, EventKey: sha256Hex("verify-failed:" + sessionID),
			ChatID: chatID, UserID: userID, Action: action, Status: "executing",
			Payload: casePayload,
		}); err != nil {
			return err
		}
		caseRow, err = st.Case(ctx, caseID)
		if err != nil || caseRow == nil {
			return err
		}
	}
	if rowInt64(caseRow, "chat_id") != chatID || rowInt64(caseRow, "user_id") != userID {
		return GuardError("guard.permissions_changed")
	}
	if rowStr(caseRow, "status") == "execution_failed" {
		if err := st.UpdateCase(ctx, caseID, "executing", nil, nil); err != nil {
			return err
		}
		caseRow["status"] = "executing"
	}
	if rowStr(caseRow, "status") == "executing" {
		if err := vm.svc.ExecuteCase(ctx, st, caseRow); err != nil {
			detail := err.Error()
			save.Error = &detail
			if writeErr := st.SaveVerificationSession(ctx, save); writeErr != nil {
				return writeErr
			}
			return err
		}
		caseRow, err = st.Case(ctx, caseID)
		if err != nil || caseRow == nil {
			return err
		}
	}
	if rowStr(caseRow, "status") == "cancelled" {
		final = "blocked"
	}
	sess["status"], sess["error"] = final, nil
	payload["cleanup_pending"] = final != "held"
	if err := saveVerificationRow(ctx, st, sess); err != nil {
		return err
	}
	return vm.finishVerificationLifecycle(ctx, st, sess)
}

func (vm *VerificationManager) cleanupChallengeMessage(ctx context.Context, chatID int64, sess guarddb.Row, grp GuardGroup, resultText string) error {
	if vm.svc.ops == nil {
		return nil
	}
	mid := int(rowInt64(sess, "message_id"))
	if mid <= 0 {
		return nil
	}
	if grp.VerificationMessageMode == "result" && resultText != "" {
		return vm.svc.ops.EditMessage(ctx, chatID, mid, resultText, mtproto.SendOptions{ParseMode: "plain", Buttons: [][]mtproto.InlineButton{}})
	}
	return vm.svc.ops.DeleteMessages(ctx, chatID, []int{mid})
}

// ProcessDueSession 推进单条由 GuardWork 查询出的待处理入群验证会话（助手完成同步、超时失败及残留消息清理）。
func (vm *VerificationManager) ProcessDueSession(ctx context.Context, rawSess guarddb.Row) {
	userID := rowInt64(rawSess, "user_id")
	chatID := rowInt64(rawSess, "chat_id")
	if userID == 0 || chatID == 0 {
		return
	}
	err := vm.svc.repo.WithLock(ctx, userID, func(st *guarddb.Store) error {
		cur, err := st.VerificationSession(ctx, chatID, userID)
		if err != nil || cur == nil {
			return err
		}
		status := rowStr(cur, "status")
		grp := vm.svc.Group(chatID)
		payload := rowPayload(cur)

		if status == "admin_pending" {
			return vm.resumeAdminVerification(ctx, st, cur)
		}
		if status == "checking" {
			return vm.resumeVerificationCheck(ctx, st, cur, grp)
		}
		if status == "preparing" {
			return vm.resumeVerificationAdmission(ctx, st, cur)
		}
		if status == "kicking" {
			return vm.failSession(ctx, st, cur, grp, grp.VerificationTimeoutAction, "timeout")
		}
		// 助手私聊验证结果同步
		if assistant := matchingAssistant(cur, vm.ConfiguredAssistantID()); assistant != nil && (status == "waiting" || status == "passing") {
			switch assistant.State {
			case "completed":
				if grp.Enabled && verifiedAssistant(cur, assistant, time.Now().UTC()) {
					return vm.passSession(ctx, st, cur, grp, "passed", 0, true)
				}
			case "failed":
				if grp.Enabled {
					return vm.failSession(ctx, st, cur, grp, grp.VerificationWrongAction, "assistant_failed")
				}
			}
		}
		if status == "cancelling" || (status == "passing" && payload["assistant"] == nil) {
			mid := rowInt64(cur, "message_id")
			expiry := parseRowTimeOrNow(cur, "expires_at", time.Time{})
			if status == "passing" && grp.Enabled && verifiedPythonReceipt(cur, time.Now().UTC()) {
				return vm.passSession(ctx, st, cur, grp, "passed", 0)
			}
			delete(payload, "answer_receipt")
			var messageID *int64
			if mid > 0 {
				messageID = &mid
			}
			return st.SaveVerificationSession(ctx, guarddb.VerificationSessionSave{
				ID: rowStr(cur, "id"), ChatID: chatID, UserID: userID, Status: "waiting",
				Payload: payload, MessageID: messageID, Attempts: int(rowInt64(cur, "attempts")), ExpiresAt: expiry,
			})
		}

		// 超时自动处置
		if status == "waiting" || status == "passing" {
			expAt := parseRowTimeOrNow(cur, "expires_at", time.Now().UTC().Add(time.Hour))
			if !time.Now().UTC().Before(expAt) {
				action := grp.VerificationTimeoutAction
				if payload["assistant"] != nil {
					configuredID := vm.ConfiguredAssistantID()
					active, err := st.ActiveAssistant(ctx, configuredID)
					if err != nil {
						return err
					}
					if matchingAssistant(cur, configuredID) == nil || active == nil {
						action = "allow"
					}
				}
				return vm.failSession(ctx, st, cur, grp, action, "timeout")
			}
		}

		if !guarddb.IsActiveVerificationStatus(status) {
			return vm.finishVerificationLifecycle(ctx, st, cur)
		}
		return vm.syncVerificationControls(ctx, st, cur)
	})
	if err != nil && ctx.Err() == nil {
		vm.svc.logger.Warn("Guard 验证操作待恢复", "session", rowStr(rawSess, "id"), "error", err)
	}
}

func matchingAssistant(session guarddb.Row, botID int64) *guarddb.Verification {
	if botID <= 0 {
		return nil
	}
	raw, err := json.Marshal(rowPayload(session)["assistant"])
	if err != nil {
		return nil
	}
	var state guarddb.Verification
	if err := json.Unmarshal(raw, &state); err != nil || state.Version != 1 || state.BotID != botID ||
		state.ActorID != rowInt64(session, "user_id") || state.ChatID != rowInt64(session, "chat_id") ||
		state.SessionID != rowStr(session, "id") {
		return nil
	}
	return &state
}

var assistantReceipt = regexp.MustCompile(`^[0-9a-f]{24}$`)
var pythonVerificationQuestion = regexp.MustCompile(`^([1-9]|1[0-9]|20) \+ ([1-9]|1[0-9]|20)$`)

func pythonChallengeAnswer(session guarddb.Row, token string) (correct, valid bool) {
	p := rowPayload(session)
	question := rowStr(p, "question")
	terms := pythonVerificationQuestion.FindStringSubmatch(question)
	if terms == nil {
		return false, false
	}
	options := parseAnyInt64Slice(p["options"])
	context, err := json.Marshal([]any{
		rowStr(session, "id"), rowInt64(session, "chat_id"), rowInt64(session, "user_id"),
		rowInt64(p, "generation"), question, options,
	})
	if err != nil || sha256Hex(string(context)) != rowStr(p, "answer_context") {
		return false, false
	}
	tokens, _ := p["answer_tokens"].(map[string]any)
	value := rowInt64(tokens, token)
	if !slices.Contains(options, value) {
		return false, false
	}
	left, _ := strconv.ParseInt(terms[1], 10, 64)
	right, _ := strconv.ParseInt(terms[2], 10, 64)
	return value == left+right, true
}

func verifiedPythonReceipt(session guarddb.Row, now time.Time) bool {
	p := rowPayload(session)
	proof, _ := p["answer_receipt"].(map[string]any)
	token := rowStr(proof, "token")
	if !assistantReceipt.MatchString(token) || rowInt64(proof, "actor_id") != rowInt64(session, "user_id") ||
		rowStr(proof, "session_id") != rowStr(session, "id") || rowInt64(proof, "chat_id") != rowInt64(session, "chat_id") ||
		rowInt64(proof, "message_id") <= 0 || rowInt64(proof, "message_id") != rowInt64(session, "message_id") ||
		rowStr(proof, "context") != rowStr(p, "answer_context") ||
		rowInt64(proof, "query_id") <= 0 || !slices.Contains(parseAnyStringSlice(p["handled_queries"]), rowStr(proof, "query_id")) {
		return false
	}
	accepted := parseRowTimeOrNow(proof, "accepted_at", time.Time{})
	expires := parseRowTimeOrNow(session, "expires_at", time.Time{})
	if accepted.IsZero() || accepted.After(now) || accepted.After(expires) || !now.Before(expires) {
		return false
	}
	correct, valid := pythonChallengeAnswer(session, token)
	tokens, _ := p["answer_tokens"].(map[string]any)
	return correct && valid && rowInt64(proof, "value") == rowInt64(tokens, token)
}

func verifiedAssistant(session guarddb.Row, assistant *guarddb.Verification, now time.Time) bool {
	expiry := parseRowTimeOrNow(session, "expires_at", time.Time{})
	if assistant == nil || assistant.State != "completed" || assistant.Steps < 2 || assistant.Steps > 5 ||
		len(assistant.Receipts) != assistant.Steps || assistant.StartedAt <= 0 ||
		assistant.CompletedAt < assistant.StartedAt || assistant.CompletedAt > now.Unix() ||
		!now.Before(expiry) || assistant.CompletedAt > expiry.Unix() {
		return false
	}
	seen := make(map[string]bool, assistant.Steps)
	for _, receipt := range assistant.Receipts {
		if seen[receipt] || !assistantReceipt.MatchString(receipt) {
			return false
		}
		seen[receipt] = true
	}
	return true
}

func (vm *VerificationManager) restoreVerificationRights(ctx context.Context, st *guarddb.Store, session guarddb.Row, original mtproto.BannedRights, assistant bool) error {
	user, chat := rowInt64(session, "user_id"), rowInt64(session, "chat_id")
	payload := rowPayload(session)
	grp := vm.svc.Group(chat)
	if active, err := st.ActiveBan(ctx, chat, user, rowStr(session, "id")); err != nil {
		return err
	} else if active != nil {
		return GuardError("guard.permissions_changed")
	}
	if assistant {
		if !grp.Enabled || !verifiedAssistant(session, matchingAssistant(session, vm.ConfiguredAssistantID()), time.Now().UTC()) {
			return GuardError("guard.verification_release_required")
		}
		if grp.ShareGlobalBlacklist {
			listed, err := st.Blacklist(ctx, user)
			if err != nil {
				return err
			}
			if listed != nil {
				return GuardError("guard.permissions_changed")
			}
		}
	}
	requested := mtproto.VerificationRights(original)
	if stored, ok := payload["requested_rights"].(map[string]any); ok {
		requested = mtproto.BannedRightsFromMap(stored)
	}
	current, err := vm.svc.ops.Permissions(ctx, chat, user)
	if err != nil {
		return err
	}
	if current == nil || current.IsAdmin || current.IsCreator || current.IsBanned || current.BannedRights.ViewMessages ||
		(current.UserID != 0 && current.UserID != user) {
		return GuardError("guard.permissions_changed")
	}
	have := mtproto.ComparableRights(current.BannedRights)
	if have == mtproto.ComparableRights(original) && (!assistant || rowBool(payload, "release_intent")) {
		return nil
	}
	if have != mtproto.ComparableRights(requested) ||
		(assistant && (!rowBool(payload, "restriction_confirmed") || current.KickedBy != vm.svc.BotID())) {
		return GuardError("guard.permissions_changed")
	}
	if assistant && !rowBool(payload, "release_intent") {
		payload["release_intent"] = true
		mid := rowInt64(session, "message_id")
		var messageID *int64
		if mid > 0 {
			messageID = &mid
		}
		if err := st.SaveVerificationSession(ctx, guarddb.VerificationSessionSave{
			ID: rowStr(session, "id"), ChatID: chat, UserID: user, Status: "passing", Payload: payload,
			MessageID: messageID, Attempts: int(rowInt64(session, "attempts")),
			ExpiresAt: parseRowTimeOrNow(session, "expires_at", time.Time{}),
		}); err != nil {
			return err
		}
	}
	return vm.svc.ops.SetRights(ctx, chat, user, original)
}

func (vm *VerificationManager) answer(ctx context.Context, queryID int64, msg string, alert bool) error {
	if vm.svc.ops == nil || queryID == 0 {
		return nil
	}
	return vm.svc.ops.AnswerCallback(ctx, queryID, msg, alert)
}

func parseRowTimeOrNow(row map[string]any, key string, fallback time.Time) time.Time {
	if row == nil {
		return fallback
	}
	if t, ok := row[key].(time.Time); ok {
		return t.UTC()
	}
	raw := strings.TrimSpace(rowStr(row, key))
	if raw == "" {
		return fallback
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return fallback
}
