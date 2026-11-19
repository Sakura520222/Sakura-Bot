// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

var guardHexID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var verificationAdminResults = map[string]string{
	"ban": "admin_banned", "blacklist": "admin_blacklisted", "kick": "admin_kicked", "skip": "admin_skipped",
}
var verificationAdminCodes = map[string]string{"b": "ban", "x": "blacklist", "j": "kick", "s": "skip"}

func (vm *VerificationManager) handleAdminAction(ctx context.Context, cb *mtproto.CallbackEvent, sessionID, action string) error {
	return vm.handleAdminOrigin(ctx, cb, sessionID, action, "")
}

func (vm *VerificationManager) handleNoticeAction(ctx context.Context, cb *mtproto.CallbackEvent, parts []string) error {
	if len(parts) != 2 || !guardHexID.MatchString(parts[0]) || verificationAdminCodes[parts[1]] == "" || vm.svc.repo == nil {
		return vm.answer(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
	}
	notice, err := vm.svc.repo.GetCase(ctx, parts[0])
	if err != nil {
		return err
	}
	sessionID := rowStr(rowPayload(notice), "verification_session_id")
	return vm.handleAdminOrigin(ctx, cb, sessionID, verificationAdminCodes[parts[1]], parts[0])
}

func (vm *VerificationManager) handleAdminOrigin(ctx context.Context, cb *mtproto.CallbackEvent, sessionID, action, noticeID string) error {
	if !guardHexID.MatchString(sessionID) || verificationAdminResults[action] == "" || cb.QueryID <= 0 ||
		cb.SenderID <= 0 || cb.MessageID <= 0 || vm.svc.repo == nil || vm.svc.repo.DB == nil {
		return vm.answer(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
	}
	initial, err := vm.svc.repo.GetVerificationSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if initial == nil {
		return vm.answer(ctx, cb.QueryID, i18n.T("guard.invalid_callback", nil), true)
	}
	user := rowInt64(initial, "user_id")
	state := ""
	err = vm.svc.repo.WithLock(ctx, user, func(st *guarddb.Store) error {
		session, err := st.VerificationSessionByID(ctx, sessionID)
		if err != nil || session == nil {
			return errors.Join(GuardError("guard.invalid_callback"), err)
		}
		if !vm.validAdminOrigin(ctx, st, session, cb, noticeID) {
			return GuardError("guard.invalid_callback")
		}
		chat := rowInt64(session, "chat_id")
		if !vm.canManageVerification(ctx, chat, cb.SenderID) {
			return GuardError("error.permission_denied")
		}
		state = rowStr(session, "status")
		now := db.NowUTC()
		p := rowPayload(session)
		if state == "admin_pending" {
			previous, _ := p["admin_action"].(map[string]any)
			if rowStr(previous, "action") != action {
				return GuardError("guard.already_processing")
			}
			if action == "skip" && (!validAdminIntent(session, previous, now) || rowInt64(previous, "actor_id") != cb.SenderID) {
				if caseID := rowStr(previous, "case_id"); guardHexID.MatchString(caseID) {
					oldCase, err := st.Case(ctx, caseID)
					if err != nil {
						return err
					}
					if oldCase != nil && rowStr(oldCase, "status") == "executing" &&
						rowInt64(oldCase, "chat_id") == chat && rowInt64(oldCase, "user_id") == user {
						if err := vm.completeVerificationAdminCase(ctx, st, oldCase, "cancelled", "held"); err != nil {
							return err
						}
					}
				}
				state = "held"
			}
		}
		if state != "admin_pending" {
			postVerification := noticeID != "" && (state == "passed" || state == "admin_skipped") && action != "skip"
			if !slices.Contains([]string{"waiting", "passing", "cancelling", "held"}, state) && !postVerification {
				return GuardError("guard.invalid_callback")
			}
			grp := vm.svc.Group(chat)
			if !grp.Enabled || (action == "kick" && !grp.KickButtonEnabled) ||
				(action != "kick" && !slices.Contains(grp.VerificationAdminActions, action)) {
				return GuardError("guard.verification_action_disabled")
			}
			if state != "held" && !postVerification && !now.Before(parseRowTimeOrNow(session, "expires_at", now)) {
				return GuardError("guard.verification_expired")
			}
			blocked, err := st.ActiveBan(ctx, chat, user, "")
			if err != nil {
				return err
			}
			if blocked != nil {
				return GuardError("guard.verification_blocked")
			}
			p["admin_action"] = map[string]any{
				"action": action, "actor_id": cb.SenderID, "case_id": newUUIDHex(),
				"authorized_at": db.PythonISOFormat(now), "session_id": sessionID, "chat_id": chat,
				"message_id": session["message_id"], "notification_case_id": nilIfEmptyGuard(noticeID),
				"origin_chat_id": cb.ChatID, "origin_message_id": cb.MessageID,
			}
			session["status"], session["error"] = "admin_pending", nil
			if err := saveVerificationRow(ctx, st, session); err != nil {
				return err
			}
			if err := vm.syncVerificationControls(ctx, st, session); err != nil {
				return err
			}
		}
		err = vm.resumeAdminVerification(ctx, st, session)
		state = rowStr(session, "status")
		return err
	})
	if err != nil {
		detail := "guard.operation_failed"
		var guardErr GuardError
		if errors.As(err, &guardErr) {
			detail = string(guardErr)
		}
		if state == "admin_pending" {
			return vm.answer(ctx, cb.QueryID, i18n.T("guard.admin_operation_pending", map[string]string{"reason": i18n.T(detail, nil)}), true)
		}
		return vm.answer(ctx, cb.QueryID, i18n.T(detail, nil), true)
	}
	if state == verificationAdminResults[action] {
		return vm.answer(ctx, cb.QueryID, i18n.T("guard."+state, nil), true)
	}
	return vm.answer(ctx, cb.QueryID, i18n.T("guard.session_"+state, nil), true)
}

func nilIfEmptyGuard(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (vm *VerificationManager) canManageVerification(ctx context.Context, chat, actor int64) bool {
	if actor <= 0 || vm.svc.ops == nil {
		return false
	}
	if slices.Contains(vm.svc.DefaultAdmins(), actor) {
		return true
	}
	p, err := vm.svc.ops.Permissions(ctx, chat, actor)
	return err == nil && p != nil && (p.IsCreator || (p.IsAdmin && p.CanBanUsers))
}

func (vm *VerificationManager) validAdminOrigin(ctx context.Context, st *guarddb.Store, session guarddb.Row, cb *mtproto.CallbackEvent, caseID string) bool {
	if caseID == "" {
		return cb.ChatID == rowInt64(session, "chat_id") && int64(cb.MessageID) == rowInt64(session, "message_id")
	}
	row, err := st.Case(ctx, caseID)
	if err != nil {
		return false
	}
	notices, err := st.CaseNotices(ctx, caseID)
	return err == nil && validNoticeOrigin(session, row, notices, cb.ChatID, cb.MessageID)
}

func validNoticeOrigin(session, row guarddb.Row, notices []guarddb.Row, chat int64, message int) bool {
	p := rowPayload(row)
	if row == nil || p["notification_replaced_by"] != nil || rowInt64(row, "chat_id") != rowInt64(session, "chat_id") ||
		rowInt64(row, "user_id") != rowInt64(session, "user_id") ||
		rowStr(p, "verification_session_id") != rowStr(session, "id") ||
		(!slices.Contains([]string{"verify_start", "verify_pass", "verify_skip"}, rowStr(row, "action")) && p["verification_failure"] == nil) {
		return false
	}
	for _, notice := range notices {
		if rowInt64(notice, "target_id") == chat && rowInt64(notice, "message_id") == int64(message) &&
			message > 0 && !retiredNotice(notice) {
			return true
		}
	}
	return false
}

// An old destructive intent still requires an active reviewer and exact case binding.
// Releasing a member also requires the original, persisted authorization receipt.
func validAdminIntent(session guarddb.Row, intent map[string]any, now time.Time) bool {
	action := rowStr(intent, "action")
	if rowStr(session, "status") != "admin_pending" || verificationAdminResults[action] == "" ||
		!guardHexID.MatchString(rowStr(intent, "case_id")) || rowInt64(intent, "actor_id") <= 0 ||
		rowInt64(session, "chat_id") >= 0 || rowInt64(session, "user_id") <= 0 {
		return false
	}
	strict := action == "skip"
	for _, key := range []string{"chat_id", "message_id"} {
		if _, exists := intent[key]; (strict || exists) && rowInt64(intent, key) != rowInt64(session, key) {
			return false
		}
	}
	if id := rowStr(intent, "session_id"); (strict || id != "") && id != rowStr(session, "id") {
		return false
	}
	authorized := parseRowTimeOrNow(intent, "authorized_at", time.Time{})
	return !authorized.After(now) && (!strict || (!authorized.IsZero() && rowInt64(session, "message_id") > 0))
}

func saveVerificationRow(ctx context.Context, st *guarddb.Store, row guarddb.Row) error {
	var messageID *int64
	if mid := rowInt64(row, "message_id"); mid > 0 {
		messageID = &mid
	}
	var errMessage *string
	if value := rowStr(row, "error"); value != "" {
		errMessage = &value
	}
	return st.SaveVerificationSession(ctx, guarddb.VerificationSessionSave{
		ID: rowStr(row, "id"), ChatID: rowInt64(row, "chat_id"), UserID: rowInt64(row, "user_id"),
		Status: rowStr(row, "status"), Payload: rowPayload(row), MessageID: messageID, Error: errMessage,
		Attempts: rowInt(row, "attempts"), ExpiresAt: parseRowTimeOrNow(row, "expires_at", db.NowUTC()),
	})
}

func (vm *VerificationManager) resumeAdminVerification(ctx context.Context, st *guarddb.Store, session guarddb.Row) error {
	p := rowPayload(session)
	intent, _ := p["admin_action"].(map[string]any)
	if !validAdminIntent(session, intent, db.NowUTC()) {
		return GuardError("error.permission_denied")
	}
	caseID, action := rowStr(intent, "case_id"), rowStr(intent, "action")
	row, err := st.Case(ctx, caseID)
	if err != nil {
		return err
	}
	if row == nil {
		id := rowStr(session, "id")
		caseAction := "ban"
		event := "verification_fail"
		switch action {
		case "skip":
			caseAction, event = "verify_skip", "verification_end"
		case "kick":
			caseAction = "kick"
		}
		caseIntent := maps.Clone(intent)
		caseIntent["session_id"] = id
		casePayload := map[string]any{
			"name": rowStr(p, "name"), "username": rowStr(p, "username"),
			"notification_event": event, "verification_session_id": id,
			"verification_admin": caseIntent, "verification_result": verificationAdminResults[action],
			"evidence": map[string]any{
				"target": "verification", "rule_id": "admin-verify-" + action,
				"reason": i18n.T("guard.verify_reason_"+action, nil),
			},
		}
		linkVerificationMessage(casePayload, session)
		actor := rowInt64(intent, "actor_id")
		actorKey := "telegram:" + strconv.FormatInt(actor, 10)
		now := db.NowUTC()
		if err := st.CreateCase(ctx, guarddb.CaseInsert{
			ID: caseID, EventKey: sha256Hex("verify-admin:" + id + ":" + caseID),
			ChatID: rowInt64(session, "chat_id"), UserID: rowInt64(session, "user_id"),
			Action: caseAction, Status: "executing", Payload: casePayload,
			ReviewerID: &actor, ReviewerActor: &actorKey, ReviewedAt: &now,
		}); err != nil {
			return err
		}
		row, err = st.Case(ctx, caseID)
		if err != nil {
			return err
		}
	}
	if !matchingAdminCase(session, row, intent) {
		return GuardError("error.permission_denied")
	}
	if rowStr(row, "status") == "executing" {
		if err := vm.executeAdminVerificationCase(ctx, st, row, session); err != nil {
			session["error"] = err.Error()
			return errors.Join(err, saveVerificationRow(ctx, st, session))
		}
		row, err = st.Case(ctx, caseID)
		if err != nil {
			return err
		}
	}
	status := rowStr(row, "status")
	result := rowStr(rowPayload(row), "verification_result")
	switch status {
	case "done", "pending", "blacklisted":
		if result != verificationAdminResults[action] {
			return GuardError("guard.invalid_callback")
		}
	case "allowed":
		result = "cancelled"
	case "cancelled":
		if result != "blocked" && result != "held" && result != "cancelled" {
			result = "held"
		}
	default:
		return GuardError("guard.operation_failed")
	}
	session["status"], session["error"] = result, nil
	p["cleanup_pending"] = result != "held"
	if err := saveVerificationRow(ctx, st, session); err != nil {
		return err
	}
	err = vm.finishVerificationLifecycle(ctx, st, session)
	vm.svc.notifier.Sync(ctx, st, row, vm.svc.Group(rowInt64(session, "chat_id")))
	return err
}

func matchingAdminCase(session, row guarddb.Row, intent map[string]any) bool {
	if row == nil || rowStr(row, "id") != rowStr(intent, "case_id") ||
		rowInt64(row, "chat_id") != rowInt64(session, "chat_id") || rowInt64(row, "user_id") != rowInt64(session, "user_id") {
		return false
	}
	caseIntent, _ := rowPayload(row)["verification_admin"].(map[string]any)
	if rowStr(caseIntent, "session_id") != rowStr(session, "id") ||
		rowStr(caseIntent, "action") != rowStr(intent, "action") ||
		rowInt64(caseIntent, "actor_id") != rowInt64(intent, "actor_id") ||
		rowInt64(row, "reviewer_id") != rowInt64(intent, "actor_id") {
		return false
	}
	action := rowStr(intent, "action")
	if action == "skip" {
		return validAdminIntent(session, caseIntent, db.NowUTC()) && rowStr(row, "action") == "verify_skip" &&
			rowStr(caseIntent, "authorized_at") == rowStr(intent, "authorized_at") &&
			rowStr(caseIntent, "case_id") == rowStr(intent, "case_id")
	}
	if action == "kick" {
		return rowStr(row, "action") == "kick"
	}
	return rowStr(row, "action") == "ban"
}

func (vm *VerificationManager) executeAdminVerificationCase(ctx context.Context, st *guarddb.Store, row, session guarddb.Row) (resultErr error) {
	p := rowPayload(row)
	caseIntent, _ := p["verification_admin"].(map[string]any)
	if session == nil {
		var err error
		session, err = st.VerificationSessionByID(ctx, rowStr(caseIntent, "session_id"))
		if err != nil {
			return err
		}
	}
	intent, _ := rowPayload(session)["admin_action"].(map[string]any)
	if !validAdminIntent(session, intent, db.NowUTC()) || !matchingAdminCase(session, row, intent) {
		return vm.completeVerificationAdminCase(ctx, st, row, "cancelled", "held")
	}
	caseID, action := rowStr(row, "id"), rowStr(intent, "action")
	chat, user := rowInt64(row, "chat_id"), rowInt64(row, "user_id")
	grp := vm.svc.Group(chat)
	defer func() {
		if resultErr != nil && ctx.Err() == nil {
			detail := resultErr.Error()
			_, _ = st.TransitionCase(ctx, caseID, "executing", "executing", &detail, nil)
		}
	}()
	if vm.svc.ops == nil {
		return GuardError("guard.unavailable")
	}
	if action == "kick" && rowBool(p, "kick_banned") {
		if err := vm.svc.executeKickTwoPhase(ctx, st, row); err != nil {
			return err
		}
		return st.UpdateCase(ctx, caseID, "done", nil, p)
	}
	if !vm.canManageVerification(ctx, chat, rowInt64(intent, "actor_id")) {
		return GuardError("error.permission_denied")
	}
	active, err := st.ActiveBan(ctx, chat, user, caseID)
	if err != nil {
		return err
	}
	if active != nil {
		return vm.completeVerificationAdminCase(ctx, st, row, "cancelled", "blocked")
	}
	if action == "skip" {
		profile, err := vm.svc.ops.GetFullUserBio(ctx, user)
		if err != nil {
			return err
		}
		if profile == nil || profile.ID != user {
			return GuardError("guard.permissions_unknown")
		}
		hit := vm.svc.matchSnapshot(ctx, st, chat, user, *profile, nil, grp)
		if grp.Enabled && hit != nil && (hit.Action == "ban" || hit.Action == "kick") {
			vm.svc.punishLocked(ctx, st, chat, user, rowStr(p, "name"), 0, hit, grp, "verify:"+rowStr(session, "id"))
			return vm.completeVerificationAdminCase(ctx, st, row, "cancelled", "blocked")
		}
		original, _ := rowPayload(session)["original_rights"].(map[string]any)
		if original == nil {
			return GuardError("guard.permissions_changed")
		}
		if err := vm.restoreVerificationRights(ctx, st, session, mtproto.BannedRightsFromMap(original), false); err != nil {
			return err
		}
		if grp.Enabled && hit != nil {
			vm.svc.punishLocked(ctx, st, chat, user, rowStr(p, "name"), 0, hit, grp, "verify:"+rowStr(session, "id"))
		}
		return st.UpdateCase(ctx, caseID, "done", nil, p)
	}
	white, err := st.Whitelisted(ctx, chat, user)
	if err != nil {
		return err
	}
	switch vm.svc.checkProtection(ctx, chat, user, grp, true) {
	case protectionUnknown:
		return GuardError("guard.permissions_unknown")
	case protectionProtected:
		white = true
	}
	if white {
		return vm.completeVerificationAdminCase(ctx, st, row, "cancelled", "held")
	}
	if action == "kick" {
		if err := vm.svc.executeKickTwoPhase(ctx, st, row); err != nil {
			return err
		}
		return st.UpdateCase(ctx, caseID, "done", nil, p)
	}
	if err := vm.svc.ops.Ban(ctx, chat, user); err != nil {
		return err
	}
	vm.svc.banHistory.ScheduleOnPayload(ctx, chat, user, p)
	if action == "blacklist" {
		if err := st.FinishReview(ctx, row, "blacklist", nil); err != nil {
			return err
		}
	} else if err := st.UpdateCase(ctx, caseID, "pending", nil, p); err != nil {
		return err
	}
	latest, err := st.Case(ctx, caseID)
	if err == nil {
		vm.svc.banHistory.Resume(ctx, st, latest, false)
	}
	return err
}

func (vm *VerificationManager) completeVerificationAdminCase(ctx context.Context, st *guarddb.Store, row guarddb.Row, status, result string) error {
	p := rowPayload(row)
	p["verification_result"] = result
	return st.UpdateCase(ctx, rowStr(row, "id"), status, nil, p)
}

func verificationLifecycleKey(chat, user int64, action, sessionID string) string {
	return sha256Hex(fmt.Sprintf("event:%d:%d:%s:%s", chat, user, action, sessionID))
}
