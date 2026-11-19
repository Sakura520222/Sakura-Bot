// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

func linkVerificationMessage(payload map[string]any, session guarddb.Row) {
	payload["verification_session_id"] = rowStr(session, "id")
	if mid := rowInt64(session, "message_id"); mid > 0 {
		chat := rowInt64(session, "chat_id")
		payload["message_id"] = mid
		payload["verification_message"] = true
		if isBasicGroupID(chat) {
			return
		}
		if kind, id := mtproto.ParseMarkedPeerID(chat); kind == mtproto.PeerKindChannel {
			payload["evidence_link"] = fmt.Sprintf("https://t.me/c/%d/%d", id, mid)
		}
	}
}

func (vm *VerificationManager) finishVerificationLifecycle(ctx context.Context, st *guarddb.Store, session guarddb.Row) error {
	p := rowPayload(session)
	chat, user := rowInt64(session, "chat_id"), rowInt64(session, "user_id")
	status := rowStr(session, "status")
	grp := vm.svc.Group(chat)
	var cleanupErr error
	if !guarddb.IsActiveVerificationStatus(status) && rowInt64(session, "message_id") > 0 {
		wantsCleanup := p["cleanup_pending"] == nil || rowBool(p, "cleanup_pending")
		if grp.VerificationMessageMode == "result" && rowStr(p, "message_result_status") != status {
			wantsCleanup = true
		}
		if wantsCleanup {
			name := rowStr(p, "name")
			if name == "" {
				name = strconv.FormatInt(user, 10)
			}
			username := rowStr(p, "username")
			if username != "" {
				username = "@" + username
			} else {
				username = i18n.T("guard.no_username", nil)
			}
			result := i18n.T("guard.verification_finished", map[string]string{
				"name": name, "username": username, "user_id": strconv.FormatInt(user, 10),
				"status": i18n.T("guard.session_"+status, nil),
			})
			cleanupErr = vm.cleanupChallengeMessage(ctx, chat, session, grp, result)
			if missingTelegramMessage(cleanupErr) {
				cleanupErr = nil
			}
			p["cleanup_pending"] = cleanupErr != nil
			if cleanupErr != nil {
				session["error"] = "cleanup:" + cleanupErr.Error()
			} else {
				session["error"] = nil
				if grp.VerificationMessageMode == "result" {
					p["message_result_status"] = status
				}
			}
			if err := saveVerificationRow(ctx, st, session); err != nil {
				return errors.Join(cleanupErr, err)
			}
		}
	}
	if rowBool(p, "lifecycle") && !rowBool(p, "lifecycle_recorded") && slices.Contains([]string{"passed", "cancelled", "left"}, status) {
		action := "verify_cancel"
		if status == "passed" {
			action = "verify_pass"
		}
		id := rowStr(session, "id")
		payload := map[string]any{
			"name": rowStr(p, "name"), "username": rowStr(p, "username"),
			"verification_state": status, "verification_admin_actions": grp.VerificationAdminActions,
			"kick_button_enabled": grp.KickButtonEnabled,
		}
		linkVerificationMessage(payload, session)
		if err := vm.svc.RecordEvent(ctx, st, chat, user, action, verificationLifecycleKey(chat, user, action, id), "verification_end", 0, payload); err != nil {
			return errors.Join(cleanupErr, err)
		}
		p["lifecycle_recorded"] = true
		if err := saveVerificationRow(ctx, st, session); err != nil {
			return errors.Join(cleanupErr, err)
		}
	}
	return errors.Join(cleanupErr, vm.syncVerificationControls(ctx, st, session))
}

func (vm *VerificationManager) syncVerificationControls(ctx context.Context, st *guarddb.Store, session guarddb.Row) error {
	p := rowPayload(session)
	chat, user := rowInt64(session, "chat_id"), rowInt64(session, "user_id")
	status, id := rowStr(session, "status"), rowStr(session, "id")
	grp := vm.svc.Group(chat)
	policy := []any{status, grp.VerificationAdminActions, grp.KickButtonEnabled, session["message_id"]}
	if equalNoticeValue(p["notification_controls_policy"], policy) {
		return nil
	}
	seen := make(map[string]bool)
	syncCase := func(row guarddb.Row) error {
		caseID := rowStr(row, "id")
		if caseID == "" || seen[caseID] || rowInt64(row, "chat_id") != chat || rowInt64(row, "user_id") != user {
			return nil
		}
		seen[caseID] = true
		casePayload := rowPayload(row)
		casePayload["verification_state"] = status
		casePayload["verification_admin_actions"] = grp.VerificationAdminActions
		casePayload["kick_button_enabled"] = grp.KickButtonEnabled
		linkVerificationMessage(casePayload, session)
		casePayload["report_notice_dirty"] = true
		if err := updateCaseNoticePayload(ctx, st, row, casePayload); err != nil {
			return err
		}
		vm.svc.notifier.Sync(ctx, st, row, grp)
		return nil
	}
	keys := []string{
		verificationLifecycleKey(chat, user, "verify_start", id),
		verificationLifecycleKey(chat, user, "verify_pass", id),
		verificationLifecycleKey(chat, user, "verify_cancel", id),
		sha256Hex("verify:" + rowStr(p, "join_token") + ":" + id),
		sha256Hex("verify_end:" + id + ":passed"),
		sha256Hex("verify_end:" + id + ":admin_skipped"),
	}
	for _, key := range keys {
		row, err := st.CaseForEvent(ctx, key)
		if err != nil {
			return err
		}
		if err := syncCase(row); err != nil {
			return err
		}
	}
	failure, _ := p["failure_intent"].(map[string]any)
	admin, _ := p["admin_action"].(map[string]any)
	for _, caseID := range []string{rowStr(failure, "case_id"), rowStr(admin, "case_id"), rowStr(admin, "notification_case_id")} {
		if caseID == "" || seen[caseID] {
			continue
		}
		row, err := st.Case(ctx, caseID)
		if err != nil {
			return err
		}
		if err := syncCase(row); err != nil {
			return err
		}
	}
	p["notification_controls_policy"] = policy
	return saveVerificationRow(ctx, st, session)
}

func equalNoticeValue(a, b any) bool {
	left, lerr := json.Marshal(a)
	right, rerr := json.Marshal(b)
	return lerr == nil && rerr == nil && string(left) == string(right)
}

func (vm *VerificationManager) finishLegacyKickSession(ctx context.Context, st *guarddb.Store, row guarddb.Row) error {
	id := rowStr(rowPayload(row), "kick_session_id")
	if !guardHexID.MatchString(id) {
		return nil
	}
	session, err := st.VerificationSessionByID(ctx, id)
	if err != nil {
		return err
	}
	if rowStr(session, "status") != "admin_kick_pending" ||
		rowInt64(session, "chat_id") != rowInt64(row, "chat_id") || rowInt64(session, "user_id") != rowInt64(row, "user_id") {
		return nil
	}
	session["status"], session["error"] = "admin_kicked", nil
	rowPayload(session)["cleanup_pending"] = true
	if err := saveVerificationRow(ctx, st, session); err != nil {
		return err
	}
	return vm.finishVerificationLifecycle(ctx, st, session)
}
