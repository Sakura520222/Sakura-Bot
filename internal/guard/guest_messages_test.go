// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"strings"
	"testing"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

func TestGuestSourceBindsCallerBotChatAndMessage(t *testing.T) {
	msg := &mtproto.IncomingMessage{
		ID: 21, ChatID: -1000000002000, SenderID: 87, GuestCallerID: 9007199254740993,
	}
	source := guestMessageSource(msg)
	if rowInt64(source, "user_id") != msg.GuestCallerID {
		t.Fatalf("guest binding lost a large ID: %v", source)
	}
	payload := map[string]any{"message_id": msg.ID, "guest_actor": source}
	parent := guarddb.Row{
		"id": "parent", "user_id": msg.SenderID, "chat_id": msg.ChatID, "action": "ban",
		"status": "pending", "payload": payload,
	}
	if guestBinding(parent, 88) == nil {
		t.Fatal("valid caller rejected")
	}
	prepareGuestBan(payload)
	task := payload["guest_ban"].(map[string]any)
	if task["action"] == "blacklist" || !rowBool(payload, "guest_ban_pending") {
		t.Fatalf("automatic ban must create only a group ban intent: %v", task)
	}
	if !strings.Contains(RenderCaseCardHTML(parent), "9007199254740993") {
		t.Fatal("guest caller missing from parent's notification")
	}
	if guestBinding(parent, msg.GuestCallerID) != nil {
		t.Fatal("the main bot can never become a guest caller target")
	}
	for _, key := range []string{"bot_id", "chat_id", "message_id", "source"} {
		saved := source[key]
		source[key] = 0
		if guestBinding(parent, 88) != nil {
			t.Fatalf("forged %s accepted", key)
		}
		source[key] = saved
	}
	original := rowStr(task, "operation_id")
	prepareGuestBan(payload)
	if op := rowStr(payload["guest_ban"].(map[string]any), "operation_id"); op != original {
		t.Fatalf("duplicate preparation replaced the operation ID: %q -> %q", original, op)
	}
}

func TestGlobalNicknameBlacklistOverridesJoinRequestPolicy(t *testing.T) {
	profile := mtproto.ProfileSummary{ID: 123, FirstName: "normal", ActiveUsernames: []string{"blockedalias"}}
	cfg, grp := DefaultGuardConfig(), DefaultGuardGroup()
	cfg.NicknameKeywords = []string{"blockedalias"}
	grp.NicknameFilter = false
	grp.JoinRequests = "manual"
	grp.JoinRequestMatchAction = "approve"
	matcher := NewMatcher()
	if hit := matcher.MatchNickname(profile, cfg, grp); hit != nil {
		t.Fatalf("normal nickname filter is disabled, got %+v", hit)
	}
	hit := matcher.matchGlobalNickname(profile, cfg.NicknameKeywords, grp.NicknameAction)
	if hit == nil || hit.RuleID != "global-nickname-keyword" {
		t.Fatalf("active alias failed the global join blacklist: %+v", hit)
	}
	if action := joinRequestDecision(grp, hit, true); action != "reject" {
		t.Fatalf("globally blocked nickname produced %q", action)
	}
	if action := joinRequestDecision(grp, hit, false); action != "approve" {
		t.Fatalf("group rule ignored the configured %q policy", grp.JoinRequestMatchAction)
	}
	if action := joinRequestDecision(grp, nil, false); action != "manual" {
		t.Fatalf("unmatched applicant ignored the configured %q policy", grp.JoinRequests)
	}
}
