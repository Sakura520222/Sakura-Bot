// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type verificationAdmission struct {
	Status  string                   `json:"status"`
	Text    string                   `json:"text"`
	Buttons [][]mtproto.InlineButton `json:"buttons"`
}

func staleMembershipEvent(ev mtproto.ParticipantEvent, joinToken string) bool {
	fields := strings.SplitN(joinToken, ":", 4)
	if len(fields) != 4 || fields[0] != "membership" || ev.Date.IsZero() {
		return false
	}
	joinedAt, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return false
	}
	if ev.Date.Unix() < joinedAt {
		return true
	}
	if ev.Date.Unix() != joinedAt || fields[2] != "raw" {
		return false
	}
	qts, err := strconv.Atoi(fields[3])
	return err == nil && qts > 0 && ev.QTS <= qts
}

func (vm *VerificationManager) queueVerificationCheck(ctx context.Context, st *guarddb.Store, chatID int64, user mtproto.ProfileSummary, grp GuardGroup, token string) error {
	session, err := st.VerificationSession(ctx, chatID, user.ID)
	if err != nil {
		return err
	}
	if session != nil && (guarddb.IsActiveVerificationStatus(rowStr(session, "status")) || rowStr(rowPayload(session), "join_token") == token) {
		return nil
	}
	return st.SaveVerificationSession(ctx, guarddb.VerificationSessionSave{
		ID: newUUIDHex(), ChatID: chatID, UserID: user.ID, Status: "checking",
		ExpiresAt: time.Now().UTC().Add(time.Duration(grp.VerificationTimeout) * time.Second),
		Payload:   map[string]any{"join_token": token, "recheck_join_rules": true, "lifecycle": true},
	})
}

func (vm *VerificationManager) processJoinedMember(ctx context.Context, st *guarddb.Store, chatID int64, user mtproto.ProfileSummary, grp GuardGroup, token string) error {
	if hit := vm.svc.matchSnapshot(ctx, st, chatID, user.ID, user, nil, grp); hit != nil {
		name := strings.TrimSpace(user.FirstName + " " + user.LastName)
		vm.svc.punishLocked(ctx, st, chatID, user.ID, name, 0, hit, grp, token)
		if hit.Action != "warn" && hit.Action != "mute" {
			return nil
		}
	}
	return vm.StartVerification(ctx, st, chatID, user, grp, token)
}

func (vm *VerificationManager) resumeVerificationCheck(ctx context.Context, st *guarddb.Store, row guarddb.Row, grp GuardGroup) error {
	now := time.Now().UTC()
	payload := rowPayload(row)
	chat, user := rowInt64(row, "chat_id"), rowInt64(row, "user_id")
	expiry := parseRowTimeOrNow(row, "expires_at", now)
	saveStatus := func(status string) error {
		return st.SaveVerificationSession(ctx, guarddb.VerificationSessionSave{
			ID: rowStr(row, "id"), ChatID: chat, UserID: user, Status: status, Payload: payload, ExpiresAt: expiry,
		})
	}
	if !grp.Enabled || !now.Before(expiry) {
		return saveStatus("cancelled")
	}
	switch vm.svc.checkProtection(ctx, chat, user, grp, false) {
	case protectionUnknown:
		return nil
	case protectionProtected:
		return saveStatus("cancelled")
	}
	if vm.svc.ops == nil {
		return GuardError("guard.unavailable")
	}
	profile, err := vm.svc.ops.GetFullUserBio(ctx, user)
	if err != nil {
		return err
	}
	if profile == nil || profile.ID != user {
		return GuardError("guard.permissions_unknown")
	}
	token := rowStr(payload, "join_token")
	if rowBool(payload, "recheck_join_rules") {
		if err := vm.processJoinedMember(ctx, st, chat, *profile, grp, token); err != nil {
			return err
		}
	} else if err := vm.StartVerification(ctx, st, chat, *profile, grp, token); err != nil {
		return err
	}
	latest, err := st.VerificationSession(ctx, chat, user)
	if err != nil {
		return err
	}
	if rowStr(latest, "status") == "checking" {
		return saveStatus("cancelled")
	}
	return nil
}

func (vm *VerificationManager) resumeVerificationAdmission(ctx context.Context, st *guarddb.Store, row guarddb.Row) error {
	p := rowPayload(row)
	persistLegacyStage := p["go_admission"] == nil
	var admission verificationAdmission
	if persistLegacyStage {
		legacy, err := vm.legacyVerificationAdmission(ctx, st, row)
		if err != nil {
			return err
		}
		admission = legacy
		if admission.Status == "" {
			return nil
		}
		p["go_admission"] = admission
	} else {
		raw, err := json.Marshal(p["go_admission"])
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &admission); err != nil {
			return err
		}
	}
	save := guarddb.VerificationSessionSave{
		ID: rowStr(row, "id"), ChatID: rowInt64(row, "chat_id"), UserID: rowInt64(row, "user_id"),
		Status: "preparing", Payload: p, Attempts: int(rowInt64(row, "attempts")),
		ExpiresAt: parseRowTimeOrNow(row, "expires_at", time.Time{}),
	}
	if persistLegacyStage {
		if err := st.SaveVerificationSession(ctx, save); err != nil {
			return err
		}
	}
	return vm.applyVerificationAdmission(ctx, st, save, admission)
}

func (vm *VerificationManager) legacyVerificationAdmission(ctx context.Context, st *guarddb.Store, row guarddb.Row) (verificationAdmission, error) {
	p := rowPayload(row)
	original, _ := p["original_rights"].(map[string]any)
	if original == nil {
		return verificationAdmission{}, GuardError("guard.permissions_changed")
	}
	p["requested_rights"] = mtproto.BannedRightsToMap(mtproto.VerificationRights(mtproto.BannedRightsFromMap(original)))
	id := rowStr(row, "id")
	chat := rowInt64(row, "chat_id")
	grp := vm.svc.Group(chat)
	name := rowStr(p, "name")
	if name == "" {
		name = strconv.FormatInt(rowInt64(row, "user_id"), 10)
	}
	admission := verificationAdmission{Status: "waiting"}
	now := time.Now().UTC()
	if !grp.Enabled || !grp.VerifyJoin || !now.Before(parseRowTimeOrNow(row, "expires_at", now)) {
		// Expired sessions only need a persisted rights snapshot for cleanup.
		return admission, nil
	}
	if assistant, _ := p["assistant"].(map[string]any); assistant != nil {
		if rowStr(assistant, "state") == "held" {
			admission.Status = "held"
			admission.Text = fmt.Sprintf("⚠️ %s 的入群验证需要管理员审核。", name)
		} else {
			botID := vm.ConfiguredAssistantID()
			savedBot := rowInt64(assistant, "bot_id")
			if savedBot != 0 && savedBot != botID {
				return verificationAdmission{}, GuardError("guard.verification_release_required")
			}
			row, err := st.ActiveAssistant(ctx, botID)
			if err != nil {
				return verificationAdmission{}, err
			}
			user := strings.TrimPrefix(rowStr(row, "username"), "@")
			secret := rowStr(assistant, "secret")
			if user == "" || !assistantReceipt.MatchString(secret) {
				return verificationAdmission{}, GuardError("guard.verification_release_required")
			}
			assistant["bot_id"], assistant["username"] = botID, user
			admission.Text = fmt.Sprintf("👋 欢迎 %s！请在截止前前往助手完成入群验证。", name)
			admission.Buttons = [][]mtproto.InlineButton{{
				{Text: "🔐 前往助手完成验证", URL: fmt.Sprintf("https://t.me/%s?start=verify_%s%s", user, id, secret)},
			}}
		}
	} else {
		question := rowStr(p, "question")
		options := parseAnyInt64Slice(p["options"])
		if !pythonVerificationQuestion.MatchString(question) || len(options) != 4 {
			return verificationAdmission{}, GuardError("guard.invalid_callback")
		}
		context, err := json.Marshal([]any{id, chat, rowInt64(row, "user_id"), rowInt64(p, "generation"), question, options})
		if err != nil {
			return verificationAdmission{}, err
		}
		p["answer_context"] = sha256Hex(string(context))
		tokens := make(map[string]any, len(options))
		for start := 0; start < len(options); start += 2 {
			buttons := make([]mtproto.InlineButton, 0, 2)
			for _, value := range options[start:min(start+2, len(options))] {
				token := newUUIDHex()[:24]
				tokens[token] = value
				buttons = append(buttons, mtproto.InlineButton{Text: strconv.FormatInt(value, 10), Data: "sg:v:" + id + ":a" + token})
			}
			admission.Buttons = append(admission.Buttons, buttons)
		}
		p["answer_tokens"] = tokens
		admission.Text = fmt.Sprintf("👋 欢迎 %s！请选择验证题答案：%s =", name, question)
	}
	admission.Buttons = append(admission.Buttons, vm.buildAdminButtons(id, grp)...)
	return admission, nil
}

func (vm *VerificationManager) applyVerificationAdmission(ctx context.Context, st *guarddb.Store, session guarddb.VerificationSessionSave, admission verificationAdmission) error {
	if vm.svc.ops == nil || (admission.Status != "waiting" && admission.Status != "held") {
		return GuardError("guard.invalid_callback")
	}
	p := session.Payload
	originalMap, _ := p["original_rights"].(map[string]any)
	requestedMap, _ := p["requested_rights"].(map[string]any)
	if originalMap == nil || requestedMap == nil {
		return GuardError("guard.permissions_changed")
	}
	original, requested := mtproto.BannedRightsFromMap(originalMap), mtproto.BannedRightsFromMap(requestedMap)
	member, err := vm.svc.ops.Permissions(ctx, session.ChatID, session.UserID)
	if err != nil {
		var rpc *mtproto.TelegramActionError
		if errors.As(err, &rpc) && (rpc.Code == "USER_NOT_PARTICIPANT" || rpc.Code == "UserNotParticipantError") {
			session.Status = "left"
			return st.SaveVerificationSession(ctx, session)
		}
		return err
	}
	if member == nil || (member.UserID != 0 && member.UserID != session.UserID) {
		return GuardError("guard.permissions_unknown")
	}
	if member.IsAdmin || member.IsCreator || member.IsBanned || member.BannedRights.ViewMessages {
		session.Status = "blocked"
		return st.SaveVerificationSession(ctx, session)
	}
	have := mtproto.ComparableRights(member.BannedRights)
	originalComparable := mtproto.ComparableRights(original)
	requestedComparable := mtproto.ComparableRights(requested)
	grp := vm.svc.Group(session.ChatID)
	if !grp.Enabled || !grp.VerifyJoin || !time.Now().UTC().Before(session.ExpiresAt) {
		// No challenge was delivered. Expiration clears only the restriction owned by this session.
		if have == requestedComparable && have != originalComparable && member.KickedBy == vm.svc.BotID() {
			if err := vm.svc.ops.SetRights(ctx, session.ChatID, session.UserID, original); err != nil {
				return err
			}
		}
		session.Status = "cancelled"
		p["cleanup_pending"] = false
		delete(p, "go_admission")
		return st.SaveVerificationSession(ctx, session)
	}
	if have != originalComparable && (have != requestedComparable || member.KickedBy != vm.svc.BotID()) {
		return GuardError("guard.permissions_changed")
	}
	if have != requestedComparable {
		if err := vm.svc.ops.SetRights(ctx, session.ChatID, session.UserID, requested); err != nil {
			return err
		}
		member, err = vm.svc.ops.Permissions(ctx, session.ChatID, session.UserID)
		if err != nil {
			return err
		}
		// Telegram may briefly report a stale participant snapshot after a successful write.
		if member == nil || member.IsAdmin || member.IsCreator ||
			mtproto.ComparableRights(member.BannedRights) != requestedComparable {
			return GuardError("guard.permissions_unknown")
		}
	}
	p["restriction_confirmed"] = true
	if err := st.SaveVerificationSession(ctx, session); err != nil {
		return err
	}
	message, err := vm.svc.ops.SendOnce(ctx, session.ChatID, admission.Text, mtproto.SendOptions{
		Buttons: admission.Buttons, NoWebpage: true, ParseMode: "plain",
	}, mtproto.StableRandomID("verification:"+session.ID))
	if err != nil {
		return err
	}
	if message <= 0 {
		return errors.New("Telegram did not return a verification message ID")
	}
	mid := int64(message)
	session.MessageID = &mid
	session.Status = admission.Status
	delete(p, "go_admission")
	if err := st.SaveVerificationSession(ctx, session); err != nil {
		return err
	}
	event := "verification_start"
	if admission.Status == "held" {
		event = "verification_fail"
	}
	notice := map[string]any{
		"name": rowStr(p, "name"), "username": rowStr(p, "username"),
		"session_id": session.ID, "verification_state": admission.Status,
		"verification_admin_actions": grp.VerificationAdminActions, "kick_button_enabled": grp.KickButtonEnabled,
	}
	linkVerificationMessage(notice, guarddb.Row{"id": session.ID, "chat_id": session.ChatID, "message_id": mid})
	return vm.svc.RecordEvent(ctx, st, session.ChatID, session.UserID, "verify_start",
		verificationLifecycleKey(session.ChatID, session.UserID, "verify_start", session.ID), event, 0, notice)
}
