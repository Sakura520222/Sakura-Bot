// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

const guestCallerSource = "telegram:guestchat_via_from"

var errGuestDecisionChanged = errors.New("GuestSourceDecisionChanged")

func guestMessageSource(msg *mtproto.IncomingMessage) map[string]any {
	if msg == nil || msg.GuestCallerID <= 0 || msg.SenderID <= 0 ||
		msg.GuestCallerID == msg.SenderID || msg.ID <= 0 || msg.ChatID >= 0 {
		return nil
	}
	return map[string]any{
		"user_id":    msg.GuestCallerID,
		"bot_id":     msg.SenderID,
		"message_id": msg.ID,
		"chat_id":    msg.ChatID,
		"source":     guestCallerSource,
	}
}

func prepareGuestBan(payload map[string]any) {
	if source, ok := payload["guest_actor"].(map[string]any); !ok || source == nil || payload["guest_ban"] != nil {
		return
	}
	payload["guest_ban"] = map[string]any{
		"status": "pending", "attempts": 0, "operation_id": newUUIDHex(),
	}
	payload["guest_ban_pending"] = true
	payload["report_notice_dirty"] = true
}

func guestBinding(parent guarddb.Row, self int64) map[string]any {
	p := rowPayload(parent)
	actor, _ := p["guest_actor"].(map[string]any)
	caller := rowInt64(actor, "user_id")
	bot, chat := rowInt64(parent, "user_id"), rowInt64(parent, "chat_id")
	mid := rowInt64(p, "message_id")
	if caller <= 0 || caller == self || caller == bot || bot <= 0 || chat >= 0 || mid <= 0 ||
		rowStr(actor, "source") != guestCallerSource || rowInt64(actor, "bot_id") != bot ||
		rowInt64(actor, "message_id") != mid || rowInt64(actor, "chat_id") != chat {
		return nil
	}
	return actor
}

func (s *Service) guestIntentActive(parent guarddb.Row) bool {
	p := rowPayload(parent)
	status := rowStr(parent, "status")
	return parent != nil && rowBool(p, "guest_ban_pending") &&
		slices.Contains([]string{"pending", "blacklisted", "execution_failed"}, status) &&
		(rowStr(parent, "action") == "ban" || rowBool(p, "review_ban_confirmed") || status == "blacklisted") &&
		s.Group(rowInt64(parent, "chat_id")).Enabled
}

func (s *Service) guestBlacklistAllowed(ctx context.Context, st *guarddb.Store, parent guarddb.Row, task map[string]any) (bool, error) {
	if !guarddb.CurrentBlacklistTask(parent, rowStr(task, "operation_id")) {
		return false, nil
	}
	actor := rowStr(task, "actor")
	reviewerID := rowInt64(task, "reviewer_id")
	if actor != "webui:admin" && (reviewerID <= 0 || actor != fmt.Sprintf("telegram:%d", reviewerID) ||
		!slices.Contains(s.DefaultAdmins(), reviewerID)) {
		return false, nil
	}
	black, err := st.Blacklist(ctx, rowInt64(parent, "user_id"))
	return black != nil, err
}

// checkGuestSource protects retry execution if the original case changed after the linked case was created.
func (s *Service) checkGuestSource(ctx context.Context, st *guarddb.Store, child guarddb.Row) error {
	p := rowPayload(child)
	parentID := rowStr(p, "guest_source_case")
	if parentID == "" {
		return nil
	}
	parent, err := st.Case(ctx, parentID)
	if err != nil {
		return err
	}
	source := guestBinding(parent, s.BotID())
	if !s.guestIntentActive(parent) || source == nil ||
		rowInt64(source, "user_id") != rowInt64(child, "user_id") ||
		rowInt64(source, "chat_id") != rowInt64(child, "chat_id") {
		return errGuestDecisionChanged
	}
	snapshot, _ := p["guest_source"].(map[string]any)
	for _, field := range []string{"user_id", "bot_id", "chat_id", "message_id"} {
		if rowInt64(snapshot, field) != rowInt64(source, field) {
			return errGuestDecisionChanged
		}
	}
	if rowStr(snapshot, "source") != guestCallerSource {
		return errGuestDecisionChanged
	}
	task, _ := rowPayload(parent)["guest_ban"].(map[string]any)
	operationID := rowStr(task, "operation_id")
	if old := rowStr(p, "guest_operation_id"); (old != "" && old != operationID) ||
		(old == "" && rowStr(child, "event_key") != guestChildKey(parentID, source, task)) {
		return errGuestDecisionChanged
	}
	if rowStr(task, "action") == "blacklist" {
		allowed, err := s.guestBlacklistAllowed(ctx, st, parent, task)
		if err != nil {
			return err
		}
		if !allowed {
			return errGuestDecisionChanged
		}
	}
	exempt, err := s.guestWhitelisted(ctx, st, source)
	if err != nil {
		return err
	}
	if exempt {
		return errGuestDecisionChanged
	}
	return nil
}

func (s *Service) guestWhitelisted(ctx context.Context, st *guarddb.Store, source map[string]any) (bool, error) {
	chat, caller := rowInt64(source, "chat_id"), rowInt64(source, "user_id")
	whitelisted, err := st.Whitelisted(ctx, chat, caller)
	if err != nil || !whitelisted || !s.Group(chat).ShareGlobalBlacklist {
		return whitelisted, err
	}
	black, err := st.Blacklist(ctx, caller)
	return black == nil, err
}

type guestBanResult struct {
	status string
	caseID string
	reason string
}

func (s *Service) resumeGuestMessage(ctx context.Context, id string) {
	parent, err := s.repo.GetCase(ctx, id)
	if err != nil || parent == nil || s.ops == nil {
		return
	}
	payload := rowPayload(parent)
	task, _ := payload["guest_ban"].(map[string]any)
	now := time.Now().UTC()
	if !rowBool(payload, "guest_ban_pending") || parseRowTimeOrNow(task, "retry_at", now).After(now) {
		return
	}
	opID := rowStr(task, "operation_id")
	source := guestBinding(parent, s.BotID())
	result := guestBanResult{status: "skipped", reason: "InvalidGuestBinding"}
	if source != nil && opID != "" {
		caller := rowInt64(source, "user_id")
		// Never acquire the bot and caller locks at the same time.
		err := s.repo.WithLock(ctx, caller, func(st *guarddb.Store) error {
			current, err := st.Case(ctx, id)
			if err != nil {
				return err
			}
			result, err = s.processGuestBan(ctx, st, current, opID)
			return err
		})
		if err != nil && ctx.Err() != nil {
			return
		}
		if err != nil {
			s.logger.Warn("Guard 访客联动处理延后", "case", id, "error", err)
			result = guestBanResult{status: "pending", reason: "GuestBanNotApplied"}
		}
	}
	if err := s.repo.WithLock(ctx, rowInt64(parent, "user_id"), func(st *guarddb.Store) error {
		current, err := st.Case(ctx, id)
		if err != nil || current == nil {
			return err
		}
		p := rowPayload(current)
		currentTask, _ := p["guest_ban"].(map[string]any)
		if !rowBool(p, "guest_ban_pending") || rowStr(currentTask, "operation_id") != opID || currentTask == nil {
			return nil
		}
		attempts := rowInt64(currentTask, "attempts") + 1
		currentTask["attempts"] = attempts
		currentTask["status"] = result.status
		currentTask["updated_at"] = db.PythonISOFormat(db.NowUTC())
		delete(currentTask, "error")
		delete(currentTask, "retry_at")
		if result.caseID != "" {
			currentTask["case_id"] = result.caseID
		}
		if result.reason != "" {
			currentTask["error"] = result.reason
		}
		if result.status == "pending" {
			delay := time.Duration(min(30, attempts)) * 30 * time.Second
			currentTask["retry_at"] = db.PythonISOFormat(db.NowUTC().Add(delay))
		}
		p["guest_ban_pending"] = result.status == "pending"
		p["report_notice_dirty"] = true
		var currentErr *string
		if saved := rowStr(current, "error"); saved != "" {
			currentErr = &saved
		}
		if err := st.UpdateCase(ctx, id, rowStr(current, "status"), currentErr, p); err != nil {
			return err
		}
		latest, err := st.Case(ctx, id)
		if err != nil {
			return err
		}
		s.notifier.Sync(ctx, st, latest, s.Group(rowInt64(current, "chat_id")))
		return nil
	}); err != nil && ctx.Err() == nil {
		s.logger.Warn("Guard 访客联动进度暂未保存", "case", id, "error", err)
	}
}

func (s *Service) processGuestBan(ctx context.Context, st *guarddb.Store, parent guarddb.Row, operationID string) (guestBanResult, error) {
	source := guestBinding(parent, s.BotID())
	task, _ := rowPayload(parent)["guest_ban"].(map[string]any)
	if !s.guestIntentActive(parent) || source == nil || rowStr(task, "operation_id") != operationID {
		return guestBanResult{status: "skipped", reason: "SourceDecisionChanged"}, nil
	}
	caseID := rowStr(parent, "id")
	chat, caller := rowInt64(source, "chat_id"), rowInt64(source, "user_id")
	blacklist := rowStr(task, "action") == "blacklist"
	auditKey := sha256Hex(fmt.Sprintf("guest-blacklist:%s:%s", caseID, operationID))
	if blacklist {
		allowed, err := s.guestBlacklistAllowed(ctx, st, parent, task)
		if err != nil {
			return guestBanResult{}, err
		}
		if !allowed {
			return guestBanResult{status: "skipped", reason: "GuestBlacklistAuthorizationChanged"}, nil
		}
		audit, err := st.CaseForEvent(ctx, auditKey)
		if err != nil {
			return guestBanResult{}, err
		}
		if audit != nil {
			listed, err := st.Blacklist(ctx, caller)
			if err != nil {
				return guestBanResult{}, err
			}
			if listed == nil {
				return guestBanResult{status: "skipped", caseID: rowStr(audit, "id"), reason: "GuestBlacklistRemoved"}, nil
			}
			return guestBanResult{status: "blacklisted", caseID: rowStr(audit, "id")}, nil
		}
	}
	profile, err := s.ops.GetFullUserBio(ctx, caller)
	if err != nil {
		return guestBanResult{}, err
	}
	if profile == nil || profile.ID != caller {
		return guestBanResult{}, errors.New("guest caller identity unavailable")
	}
	exempt, err := s.guestWhitelisted(ctx, st, source)
	if err != nil {
		return guestBanResult{}, err
	}
	if exempt || s.checkProtection(ctx, chat, caller, s.Group(chat), true) != protectionUnprotected {
		return guestBanResult{status: "skipped", reason: "ProtectedGuestCaller"}, nil
	}
	child, err := s.applyGuestBan(ctx, st, caseID, source, task, profile)
	if err != nil {
		return guestBanResult{}, err
	}
	childID := rowStr(child, "id")
	switch rowStr(child, "status") {
	case "pending", "blacklisted":
		if !blacklist {
			return guestBanResult{status: "banned", caseID: childID}, nil
		}
	case "allowed", "unbanned", "cancelled", "kicked", "unmuted", "ignored":
		return guestBanResult{status: "skipped", caseID: childID, reason: "SourceDecisionChanged"}, nil
	default:
		return guestBanResult{status: "pending", caseID: childID, reason: "GuestBanNotApplied"}, nil
	}
	if err := s.checkGuestSource(ctx, st, child); err != nil {
		if errors.Is(err, errGuestDecisionChanged) {
			return guestBanResult{status: "skipped", caseID: childID, reason: "GuestBlacklistDecisionChanged"}, nil
		}
		return guestBanResult{}, err
	}
	if s.checkProtection(ctx, chat, caller, s.Group(chat), true) != protectionUnprotected {
		return guestBanResult{status: "skipped", caseID: childID, reason: "ProtectedGuestCaller"}, nil
	}
	audit := guarddb.Row{
		"id": auditKey[:32], "event_key": auditKey, "chat_id": chat, "user_id": caller,
		"action": "guest_blacklist", "status": "done", "reviewer_id": task["reviewer_id"],
		"reviewer_actor": "system:guest_message", "reviewed_at": db.NowUTC(),
		"payload": map[string]any{
			"name":                          strings.TrimSpace(profile.FirstName + " " + profile.LastName),
			"guest_source_case":             caseID,
			"guest_source":                  source,
			"guest_ban_case":                childID,
			"guest_blacklist_authorization": task,
			"notification_event":            "guest_link_audit",
			"evidence": map[string]any{
				"target": "guest_invoker", "rule_id": "guest-invoker-blacklist",
				"reason":  i18n.T("guard.guest_blacklist_reason", nil),
				"snippet": strconv.FormatInt(rowInt64(source, "message_id"), 10),
			},
		},
	}
	err = st.FinishReview(ctx, audit, "blacklist", &guarddb.GuestParentRef{ParentCaseID: caseID, OperationID: operationID})
	if errors.Is(err, guarddb.ErrGuestSourceDecisionChanged) || errors.Is(err, guarddb.ErrGuestSourceBindingChanged) {
		return guestBanResult{status: "skipped", caseID: childID, reason: "GuestBlacklistDecisionChanged"}, nil
	}
	if err != nil {
		return guestBanResult{}, err
	}
	return guestBanResult{status: "blacklisted", caseID: auditKey[:32]}, nil
}

func (s *Service) applyGuestBan(ctx context.Context, st *guarddb.Store, parentID string, source, task map[string]any, profile *mtproto.ProfileSummary) (guarddb.Row, error) {
	chat, caller := rowInt64(source, "chat_id"), profile.ID
	eventKey := guestChildKey(parentID, source, task)
	child, err := st.CaseForEvent(ctx, eventKey)
	if err != nil {
		return nil, err
	}
	if child == nil {
		id, actor := newUUIDHex(), "system:guest_message"
		now := db.NowUTC()
		err := st.CreateCase(ctx, guarddb.CaseInsert{
			ID: id, EventKey: eventKey, ChatID: chat, UserID: caller, Status: "executing", Action: "ban",
			ReviewerActor: &actor, ReviewedAt: &now,
			Payload: map[string]any{
				"name":              strings.TrimSpace(profile.FirstName + " " + profile.LastName),
				"username":          profile.Username,
				"guest_source_case": parentID, "guest_source": source,
				"guest_operation_id": rowStr(task, "operation_id"),
				"notification_event": "guest_link_audit",
				"evidence": map[string]any{
					"target": "guest_invoker", "rule_id": "guest-invoker-ban",
					"reason":  i18n.T("guard.guest_ban_reason", nil),
					"snippet": strconv.FormatInt(rowInt64(source, "message_id"), 10),
				},
			},
		})
		if err != nil {
			return nil, err
		}
		child, err = st.Case(ctx, id)
		if err != nil || child == nil {
			return child, err
		}
	}
	childID := rowStr(child, "id")
	if rowStr(child, "status") == "execution_failed" {
		if err := st.UpdateCase(ctx, childID, "executing", nil, nil); err != nil {
			return nil, err
		}
		child["status"] = "executing"
	}
	if rowStr(child, "status") == "executing" {
		_ = s.ExecuteCase(ctx, st, child)
		return st.Case(ctx, childID)
	}
	return child, nil
}

func guestChildKey(parentID string, source, task map[string]any) string {
	chat, caller := rowInt64(source, "chat_id"), rowInt64(source, "user_id")
	token := fmt.Sprintf("guest:%s:%d", parentID, caller)
	if rowStr(task, "action") == "blacklist" {
		token += ":blacklist:" + rowStr(task, "operation_id")
	}
	return sha256Hex(fmt.Sprintf("%d:%d:%s:guest-invoker-ban", chat, caller, token))
}
