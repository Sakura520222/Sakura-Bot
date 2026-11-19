// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

// HandleJoinRequest records the requested action before responding to Telegram.
func (s *Service) HandleJoinRequest(ctx context.Context, ev mtproto.JoinRequestEvent) {
	if ev.ChatID >= 0 || ev.UserID <= 0 || s.ops == nil {
		return
	}
	query := ev.HasQueryID && ev.QueryID != 0
	expires := ev.Date.Unix() + 10
	manual := func() error {
		if query {
			return s.ops.AnswerJoinRequestQuery(ctx, ev.QueryID, "manual", float64(expires))
		}
		return nil
	}
	grp := s.Group(ev.ChatID)
	if !grp.Enabled || s.repo == nil || s.repo.DB == nil ||
		s.checkProtection(ctx, ev.ChatID, ev.UserID, grp, true) != protectionUnprotected {
		_ = manual()
		return
	}

	if err := s.repo.WithLock(ctx, ev.UserID, func(st *guarddb.Store) error {
		freshGrp := s.Group(ev.ChatID)
		if !freshGrp.Enabled {
			return manual()
		}
		whitelisted, err := st.Whitelisted(ctx, ev.ChatID, ev.UserID)
		if err != nil {
			return err
		}
		profile := ev.User
		profile.ID = ev.UserID
		if profile.Bio == "" {
			profile.Bio = ev.Bio
		}
		var blocked *Match
		if !whitelisted {
			blocked = NewMatcher().matchGlobalNickname(profile, s.Config().NicknameKeywords, freshGrp.NicknameAction)
		}
		hit := blocked
		if hit == nil {
			hit = s.matchSnapshot(ctx, st, ev.ChatID, ev.UserID, profile, nil, freshGrp)
		}
		decision := joinRequestDecision(freshGrp, hit, blocked != nil)
		token := fmt.Sprintf("request:%s:%d", ev.Date.UTC().Format("2006-01-02 15:04:05-07:00"), ev.QTS)
		if query {
			token = fmt.Sprintf("request-query:%d", ev.QueryID)
		}
		rule, reason, snippet := "request-"+decision, "manual_join_request", ""
		if decision == "approve" {
			reason = "approve_then_group_verification"
		}
		if hit != nil {
			rule, reason, snippet = hit.RuleID, hit.Reason, hit.Snippet
		}
		eventKey := sha256Hex(fmt.Sprintf("%d:%d:%s:%s", ev.ChatID, ev.UserID, token, rule))
		prior, err := st.CaseForEvent(ctx, eventKey)
		if err != nil || prior != nil {
			return err
		}
		name := strings.TrimSpace(profile.FirstName + " " + profile.LastName)
		if name == "" {
			name = strconv.FormatInt(ev.UserID, 10)
		}
		payload := map[string]any{
			"name":               name,
			"username":           profile.Username,
			"notification_event": "join_request",
			"evidence": map[string]any{
				"target": "join_request", "rule_id": rule, "reason": reason, "snippet": snippet,
			},
		}
		if query {
			payload["join_request_query"] = map[string]any{"id": strconv.FormatInt(ev.QueryID, 10), "expires_at": expires}
		}
		id := newUUIDHex()
		if err := st.CreateCase(ctx, guarddb.CaseInsert{
			ID: id, EventKey: eventKey, ChatID: ev.ChatID, UserID: ev.UserID, Action: decision, Status: "executing", Payload: payload,
		}); err != nil {
			return err
		}
		row, err := st.Case(ctx, id)
		if err != nil {
			return err
		}
		return s.ExecuteCase(ctx, st, row)
	}); err != nil {
		s.logger.Warn("Guard 入群申请处理失败", "chat", ev.ChatID, "user", ev.UserID, "error", err)
	}
}

func joinRequestDecision(grp GuardGroup, hit *Match, globallyBlocked bool) string {
	switch {
	case globallyBlocked:
		return "reject"
	case hit != nil:
		return grp.JoinRequestMatchAction
	default:
		return grp.JoinRequests
	}
}

func (s *Service) executeJoinRequest(ctx context.Context, st *guarddb.Store, row guarddb.Row, grp GuardGroup) error {
	action := rowStr(row, "action")
	chat, user := rowInt64(row, "chat_id"), rowInt64(row, "user_id")
	payload := rowPayload(row)
	if query, ok := payload["join_request_query"].(map[string]any); ok {
		if !rowBool(query, "answered") {
			if err := s.ops.AnswerJoinRequestQuery(ctx, rowInt64(query, "id"), action, float64(rowInt64(query, "expires_at"))); err != nil {
				return err
			}
			query["answered"] = true
			if err := st.UpdateCase(ctx, rowStr(row, "id"), "executing", nil, payload); err != nil {
				return err
			}
		}
	} else if action != "manual" {
		if err := s.ops.JoinRequest(ctx, chat, user, action == "approve"); err != nil {
			var rpc *mtproto.TelegramActionError
			if !errors.As(err, &rpc) || (rpc.Code != "HideRequesterMissingError" && rpc.Code != "UserAlreadyParticipantError" &&
				rpc.Code != "HIDE_REQUESTER_MISSING" && rpc.Code != "USER_ALREADY_PARTICIPANT") {
				return err
			}
			if action == "approve" {
				if _, err := s.ops.Permissions(ctx, chat, user); err != nil {
					return err
				}
			}
		}
	}
	if action == "approve" && grp.VerifyJoin {
		// Telegram can acknowledge approval before the participant is visible.
		return s.verification.queueVerificationCheck(ctx, st, chat, user, grp, "request:"+rowStr(row, "id"), false)
	}
	return nil
}
