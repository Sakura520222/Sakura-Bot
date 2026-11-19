// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/gotd/td/tgerr"
)

// Sync delivers and updates case notices under the same user lock used by review and verification.
func (n *Notifier) Sync(ctx context.Context, st *guarddb.Store, row guarddb.Row, grp GuardGroup) {
	if n == nil || n.svc == nil || st == nil || row == nil || n.svc.ops == nil {
		return
	}
	if err := n.syncCaseNotices(ctx, st, row, grp); err != nil && ctx.Err() == nil {
		n.svc.logger.Warn("Guard 通知同步延后", "case", rowStr(row, "id"), "error", err)
	}
}

func (n *Notifier) syncCaseNotices(ctx context.Context, st *guarddb.Store, row guarddb.Row, grp GuardGroup) error {
	id, status := rowStr(row, "id"), rowStr(row, "status")
	p := rowPayload(row)
	if rowStr(row, "action") == "verify_start" && rowStr(p, "notification_replaced_by") != "" {
		return nil
	}
	existing, err := st.CaseNotices(ctx, id)
	if err != nil {
		return err
	}
	if noticeControlsChanged(row, grp) {
		for _, notice := range existing {
			if retiredNotice(notice) {
				continue
			}
			if err := st.UpsertCaseNotice(ctx, id, rowInt64(notice, "target_id"), noticeMessageID(notice), nil, nil, int(rowInt64(notice, "attempts"))); err != nil {
				return err
			}
			notice["synced_status"], notice["error"] = nil, nil
		}
		p["controls_version"] = 2
		if rowStr(row, "action") == "report" {
			p["report_controls_version"] = 2
		}
		p["kick_button_enabled"], p["report_notice_dirty"], p["ban_message_cleanup_dirty"] = grp.KickButtonEnabled, false, false
		if err := updateCaseNoticePayload(ctx, st, row, p); err != nil {
			return err
		}
	}
	category := notificationCategory(row)
	current := n.Targets(grp, category)
	potential := make(map[int64]bool)
	targets := make([]int64, 0, len(current)+len(existing))
	addTarget := func(id int64) {
		if id != 0 && !potential[id] {
			targets = append(targets, id)
			potential[id] = true
		}
	}
	for _, t := range current {
		addTarget(t)
	}
	notices := make(map[int64]guarddb.Row, len(existing))
	for _, notice := range existing {
		t := rowInt64(notice, "target_id")
		notices[t] = notice
		if !retiredNotice(notice) {
			addTarget(t)
		}
	}
	fallback := int64(0)
	if grp.NotificationEnabled && slices.Contains(grp.NotificationEvents, category) && grp.ReviewChatID != nil {
		fallback = *grp.ReviewChatID
		if fallback != 0 {
			potential[fallback] = true
		}
	}
	old, err := n.prepareVerificationNoticeCleanup(ctx, st, row, grp, potential)
	if err != nil {
		return err
	}
	if len(targets) == 0 && fallback == 0 {
		if rowStr(notices[0], "synced_status") == status {
			return nil
		}
		return st.MarkCaseNoticeSynced(ctx, id, 0, nil, status)
	}
	card := maps.Clone(row)
	card["payload"] = maps.Clone(rowPayload(row))
	n.fillNoticeGroup(ctx, card)
	text := RenderCaseCardHTML(card)
	buttons := BuildReviewButtons(row, grp)
	opts := mtproto.SendOptions{ParseMode: "html", Buttons: buttons, NoWebpage: true}
	failed := len(current) == 0
	alreadyDelivered := make(map[int64]bool, len(targets))
	for _, t := range targets {
		ok, err := n.deliverCaseNotice(ctx, st, row, notices[t], t, text, opts)
		if err != nil {
			return err
		}
		alreadyDelivered[t] = true
		failed = failed || !ok
	}
	if failed && fallback != 0 && !alreadyDelivered[fallback] {
		if _, err := n.deliverCaseNotice(ctx, st, row, notices[fallback], fallback, text, opts); err != nil {
			return err
		}
		alreadyDelivered[fallback] = true
	}
	if fallback != 0 && !alreadyDelivered[fallback] {
		delete(potential, fallback)
	}
	return n.cleanupVerificationNotices(ctx, st, row, old, potential)
}

func (n *Notifier) fillNoticeGroup(ctx context.Context, row guarddb.Row) {
	p := rowPayload(row)
	if rowStr(p, "chat_title") != "" && rowStr(p, "chat_username") != "" {
		return
	}
	chatID := rowInt64(row, "chat_id")
	if chatID >= 0 {
		return
	}
	var title, username string
	for _, peer := range n.svc.ops.KnownGroupPeers() {
		if peer.MarkedID == chatID {
			title, username = peer.Title, peer.Username
			break
		}
	}
	if title == "" && (rowStr(p, "chat_title") == "" || rowStr(p, "chat_username") == "") {
		group, err := n.svc.ops.ResolveChannel(ctx, strconv.FormatInt(chatID, 10))
		if err == nil && group != nil {
			title, username = group.Title, group.Username
		}
	}
	if rowStr(p, "chat_title") == "" && title != "" {
		p["chat_title"] = title
	}
	if username = strings.TrimPrefix(username, "@"); username != "" {
		p["chat_username"] = username
		if rowStr(p, "chat_link") == "" {
			p["chat_link"] = "https://t.me/" + username
		}
	}
}

func notificationCategory(row guarddb.Row) string {
	p := rowPayload(row)
	if category := rowStr(p, "notification_event"); category != "" {
		return category
	}
	status, action := rowStr(row, "status"), rowStr(row, "action")
	evidence, _ := p["evidence"].(map[string]any)
	switch {
	case p["verification_admin"] != nil:
		return "admin_action"
	case p["verification_failure"] != nil || rowStr(evidence, "rule_id") == "verification-failed":
		return "verification_fail"
	case action == "verify_start":
		return "verification_start"
	case action == "verify_pass" || action == "verify_cancel":
		return "verification_end"
	case status == "blacklisted" || action == "blacklist_remove":
		return "blacklist"
	case status == "allowed" || strings.HasPrefix(action, "whitelist_"):
		return "whitelist"
	case slices.Contains([]string{"approve", "reject", "manual"}, action):
		return "join_request"
	default:
		return "moderation"
	}
}

func noticeControlsChanged(row guarddb.Row, grp GuardGroup) bool {
	p := rowPayload(row)
	return rowInt64(p, "controls_version") != 2 ||
		(rowStr(row, "action") == "report" && rowInt64(p, "report_controls_version") != 2) ||
		p["kick_button_enabled"] != grp.KickButtonEnabled ||
		rowBool(p, "report_notice_dirty") || rowBool(p, "ban_message_cleanup_dirty")
}

func (n *Notifier) deliverCaseNotice(ctx context.Context, st *guarddb.Store, row, previous guarddb.Row, target int64, text string, opts mtproto.SendOptions) (bool, error) {
	status, id := rowStr(row, "status"), rowStr(row, "id")
	if retiredNotice(previous) || (rowStr(previous, "synced_status") == status && previous["error"] == nil) {
		return true, nil
	}
	now := db.NowUTC()
	if previous["error"] != nil && parseRowTimeOrNow(previous, "retry_at", now).After(now) {
		return false, nil
	}
	mid := noticeMessageID(previous)
	unavailable, err := n.forwardReportEvidence(ctx, st, row, target)
	if unavailable {
		text += "\n" + i18n.T("guard.report_evidence_unavailable", nil)
	}
	if err == nil {
		if mid != nil {
			err = n.svc.ops.EditMessage(ctx, target, int(*mid), text, opts)
			if tgerr.Is(err, "MESSAGE_NOT_MODIFIED") {
				err = nil
			}
		} else {
			message, sendErr := n.svc.ops.SendOnce(ctx, target, text, opts, mtproto.StableRandomID(fmt.Sprintf("guard:%s:%d", id, target)))
			err = sendErr
			if err == nil {
				if message <= 0 {
					err = errors.New("Telegram did not return a case notice message ID")
				} else {
					value := int64(message)
					mid = &value
				}
			}
		}
	}
	if err != nil {
		var oldStatus *string
		if previousStatus := rowStr(previous, "synced_status"); previousStatus != "" {
			oldStatus = &previousStatus
		}
		n.svc.logger.Warn("Guard 通知投递失败", "case", id, "target", target, "error", err)
		return false, st.MarkCaseNoticeFailed(ctx, id, target, mid, oldStatus, err.Error(), int(rowInt64(previous, "attempts"))+1)
	}
	return true, st.MarkCaseNoticeSynced(ctx, id, target, mid, status)
}

func retiredNotice(row guarddb.Row) bool {
	switch rowStr(row, "synced_status") {
	case "deleting", "removed":
		return true
	}
	return false
}

func missingTelegramMessage(err error) bool {
	if err == nil {
		return false
	}
	var telegramErr *mtproto.TelegramActionError
	return tgerr.Is(err, "MESSAGE_ID_INVALID", "MSG_ID_INVALID", "MESSAGE_NOT_MODIFIED") ||
		(errors.As(err, &telegramErr) && (telegramErr.Code == "MessageIdInvalidError" ||
			telegramErr.Code == "MESSAGE_ID_INVALID" || telegramErr.Code == "MessageNotModifiedError"))
}

func updateCaseNoticePayload(ctx context.Context, st *guarddb.Store, row guarddb.Row, payload map[string]any) error {
	var previousError *string
	if value := rowStr(row, "error"); value != "" {
		previousError = &value
	}
	if err := st.UpdateCase(ctx, rowStr(row, "id"), rowStr(row, "status"), previousError, payload); err != nil {
		return err
	}
	row["payload"] = payload
	return nil
}

func noticeMessageID(row guarddb.Row) *int64 {
	if mid := rowInt64(row, "message_id"); mid > 0 {
		return &mid
	}
	return nil
}

func verificationNoticeSession(row guarddb.Row) string {
	p := rowPayload(row)
	if id := rowStr(p, "verification_session_id"); guardHexID.MatchString(id) {
		return id
	}
	for _, key := range []string{"verification_admin", "verification_failure"} {
		intent, _ := p[key].(map[string]any)
		if id := rowStr(intent, "session_id"); guardHexID.MatchString(id) {
			return id
		}
	}
	if id := rowStr(p, "session_id"); guardHexID.MatchString(id) {
		return id
	}
	return ""
}

func isVerificationNoticeResult(row guarddb.Row) bool {
	p := rowPayload(row)
	status := rowStr(row, "status")
	return status != "executing" && status != "execution_failed" &&
		(rowStr(row, "action") == "verify_pass" || p["verification_failure"] != nil || p["verification_admin"] != nil)
}

func replacementNoticeReady(replacement guarddb.Row, status string) bool {
	return rowInt64(replacement, "message_id") > 0 && replacement["error"] == nil &&
		rowStr(replacement, "synced_status") == status && !retiredNotice(replacement)
}

func (n *Notifier) prepareVerificationNoticeCleanup(ctx context.Context, st *guarddb.Store, row guarddb.Row, grp GuardGroup, potential map[int64]bool) (guarddb.Row, error) {
	p := rowPayload(row)
	clear := func() (guarddb.Row, error) {
		if !rowBool(p, "notification_cleanup_pending") {
			return nil, nil
		}
		p["notification_cleanup_pending"], p["notification_cleanup_retry_at"] = false, nil
		return nil, updateCaseNoticePayload(ctx, st, row, p)
	}
	id := verificationNoticeSession(row)
	if !isVerificationNoticeResult(row) || id == "" {
		return clear()
	}
	chat, user := rowInt64(row, "chat_id"), rowInt64(row, "user_id")
	key := verificationLifecycleKey(chat, user, "verify_start", id)
	parent, err := st.CaseForEvent(ctx, key)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		if parentID := rowStr(p, "notification_cleanup_source"); guardHexID.MatchString(parentID) {
			parent, err = st.Case(ctx, parentID)
		} else {
			session, readErr := st.VerificationSessionByID(ctx, id)
			if readErr != nil {
				return nil, readErr
			}
			if session != nil {
				parent, err = st.CaseForEvent(ctx, sha256Hex("verify:"+rowStr(rowPayload(session), "join_token")+":"+id))
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if parent == nil || rowStr(parent, "id") == rowStr(row, "id") || rowStr(parent, "action") != "verify_start" ||
		rowInt64(parent, "chat_id") != chat || rowInt64(parent, "user_id") != user ||
		(rowStr(parent, "event_key") != key && verificationNoticeSession(parent) != id) {
		return clear()
	}
	if _, hasPolicy := p["notification_cleanup_enabled"]; !hasPolicy {
		p["notification_cleanup_enabled"] = grp.VerificationNoticeCleanup
		if err := updateCaseNoticePayload(ctx, st, row, p); err != nil {
			return nil, err
		}
	}
	pp := rowPayload(parent)
	if !rowBool(p, "notification_cleanup_enabled") || len(potential) == 0 ||
		(rowStr(pp, "notification_replaced_by") != "" && rowStr(pp, "notification_replaced_by") != rowStr(row, "id")) {
		return clear()
	}
	notices, err := st.CaseNotices(ctx, rowStr(parent, "id"))
	if err != nil {
		return nil, err
	}
	pending := false
	for _, notice := range notices {
		target := rowInt64(notice, "target_id")
		if target != 0 && potential[target] && rowStr(notice, "synced_status") != "removed" {
			pending = true
			break
		}
	}
	if rowStr(p, "notification_cleanup_source") != rowStr(parent, "id") ||
		rowStr(pp, "notification_replaced_by") != rowStr(row, "id") ||
		rowBool(p, "notification_cleanup_pending") != pending || rowBool(pp, "notification_cleanup_pending") != pending {
		p["verification_session_id"] = id
		p["notification_cleanup_source"] = rowStr(parent, "id")
		p["notification_cleanup_pending"] = pending
		pp["notification_replaced_by"] = rowStr(row, "id")
		pp["notification_cleanup_pending"] = pending
		if err := st.Atomic(ctx, func(tx *guarddb.Store) error {
			if err := updateCaseNoticePayload(ctx, tx, row, p); err != nil {
				return err
			}
			return updateCaseNoticePayload(ctx, tx, parent, pp)
		}); err != nil {
			return nil, err
		}
	}
	return parent, nil
}

func (n *Notifier) cleanupVerificationNotices(ctx context.Context, st *guarddb.Store, replacement, old guarddb.Row, potential map[int64]bool) error {
	if old == nil || n.svc.ops == nil || !rowBool(rowPayload(replacement), "notification_cleanup_pending") {
		return nil
	}
	replacementID, oldID := rowStr(replacement, "id"), rowStr(old, "id")
	delivered, err := st.CaseNotices(ctx, replacementID)
	if err != nil {
		return err
	}
	byTarget := make(map[int64]guarddb.Row, len(delivered))
	for _, notice := range delivered {
		byTarget[rowInt64(notice, "target_id")] = notice
	}
	oldNotices, err := st.CaseNotices(ctx, oldID)
	if err != nil {
		return err
	}
	now := db.NowUTC()
	var retry time.Time
	markRetry := func(next time.Time) {
		if retry.IsZero() || next.Before(retry) {
			retry = next
		}
	}
	for _, oldNotice := range oldNotices {
		target := rowInt64(oldNotice, "target_id")
		if target == 0 || !potential[target] || rowStr(oldNotice, "synced_status") == "removed" {
			continue
		}
		replacementNotice := byTarget[target]
		if !replacementNoticeReady(replacementNotice, rowStr(replacement, "status")) {
			next := parseRowTimeOrNow(replacementNotice, "retry_at", now.Add(30*time.Second))
			if !next.After(now) {
				next = now.Add(30 * time.Second)
			}
			markRetry(next)
			continue
		}
		if rowStr(oldNotice, "synced_status") == "deleting" && oldNotice["error"] != nil {
			if next := parseRowTimeOrNow(oldNotice, "retry_at", now); next.After(now) {
				markRetry(next)
				continue
			}
		}
		attempts := int(rowInt64(oldNotice, "attempts"))
		messageID := noticeMessageID(oldNotice)
		deleting := "deleting"
		if err := st.UpsertCaseNotice(ctx, oldID, target, messageID, &deleting, nil, attempts); err != nil {
			return err
		}
		if messageID != nil && *messageID != rowInt64(replacementNotice, "message_id") {
			if err := n.svc.ops.DeleteMessages(ctx, target, []int{int(*messageID)}); err != nil && !missingTelegramMessage(err) {
				detail := "cleanup:" + err.Error()
				if err := st.MarkCaseNoticeFailed(ctx, oldID, target, messageID, &deleting, detail, attempts+1); err != nil {
					return err
				}
				markRetry(now.Add(time.Duration(min(3600, 30<<min(max(attempts+1, 0), 7))) * time.Second))
				continue
			}
		}
		if err := st.MarkCaseNoticeRemoved(ctx, oldID, target); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	p, pp := rowPayload(replacement), rowPayload(old)
	p["notification_cleanup_pending"], pp["notification_cleanup_pending"] = !retry.IsZero(), !retry.IsZero()
	p["notification_cleanup_retry_at"] = nil
	if !retry.IsZero() {
		p["notification_cleanup_retry_at"] = db.PythonISOFormat(retry)
	}
	return st.Atomic(ctx, func(tx *guarddb.Store) error {
		if err := updateCaseNoticePayload(ctx, tx, replacement, p); err != nil {
			return err
		}
		return updateCaseNoticePayload(ctx, tx, old, pp)
	})
}

func (n *Notifier) forwardReportEvidence(ctx context.Context, st *guarddb.Store, row guarddb.Row, target int64) (bool, error) {
	p := rowPayload(row)
	message := rowInt64(p, "message_id")
	if rowStr(row, "action") != "report" || message <= 0 {
		return false, nil
	}
	evidence, _ := p["report_evidence"].(map[string]any)
	if evidence == nil {
		evidence = make(map[string]any)
	}
	key := strconv.FormatInt(target, 10)
	if saved := rowStr(evidence, key); saved != "" {
		return saved != "forwarded", nil
	}
	result := "forwarded"
	randomID := mtproto.StableRandomID(fmt.Sprintf("guard-report-evidence:%s:%d", rowStr(row, "id"), target))
	if err := n.svc.ops.ForwardOnce(ctx, target, rowInt64(row, "chat_id"), int(message), randomID); err != nil {
		if !tgerr.Is(err, "CHAT_FORWARDS_RESTRICTED", "MESSAGE_ID_INVALID", "MESSAGE_IDS_EMPTY", "MESSAGE_EMPTY", "CHANNEL_PRIVATE") &&
			!missingTelegramMessage(err) {
			var rpc *mtproto.TelegramActionError
			if !errors.As(err, &rpc) {
				return false, err
			}
			switch rpc.Code {
			case "ChatForwardsRestrictedError", "MessageIdsEmptyError", "MessageEmptyError", "ChannelPrivateError":
			default:
				return false, err
			}
		}
		result = "unavailable:" + err.Error()
	}
	evidence[key] = result
	p["report_evidence"] = evidence
	return result != "forwarded", updateCaseNoticePayload(ctx, st, row, p)
}
