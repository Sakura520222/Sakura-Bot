// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package guard

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

const verificationCardVersion = 2

func verificationCard(id string, userID int64, status string, payload map[string]any, expiresAt, now time.Time, grp GuardGroup) (string, [][]mtproto.InlineButton) {
	prefix := "sg:v:" + id + ":"
	key := "guard.challenge"
	question := rowStr(payload, "question")
	attempts := grp.VerificationMaxAttempts
	var rows [][]mtproto.InlineButton
	assistant, _ := payload["assistant"].(map[string]any)
	switch {
	case status == "held":
		key = "guard.challenge_held"
	case assistant != nil:
		key = "guard.challenge_assistant_unavailable"
		username := strings.TrimPrefix(rowStr(assistant, "username"), "@")
		secret := rowStr(assistant, "secret")
		if value := rowInt(assistant, "max_attempts"); value > 0 {
			attempts = value
		}
		if rowInt64(assistant, "bot_id") > 0 && username != "" && assistantReceipt.MatchString(secret) {
			key = "guard.challenge_assistant"
			rows = append(rows, []mtproto.InlineButton{{
				Text: i18n.T("guard.verify_assistant_button", nil),
				URL:  fmt.Sprintf("https://t.me/%s?start=verify_%s%s", username, id, secret),
			}})
		}
	default:
		challenge, _ := payload["challenge"].(map[string]any)
		if prompt := rowStr(challenge, "prompt"); prompt != "" {
			key, question = "guard.challenge_local", legacyChallengeText(prompt)
			options := slices.Clone(parseAnyStringSlice(challenge["options"]))
			for i, option := range options {
				options[i] = legacyChallengeText(option)
			}
			rows = legacyChallengeButtons(id, options)
		} else {
			tokens, _ := payload["answer_tokens"].(map[string]any)
			options := parseAnyInt64Slice(payload["options"])
			var answers []mtproto.InlineButton
			for _, value := range options {
				for token := range tokens {
					if assistantReceipt.MatchString(token) && rowInt64(tokens, token) == value {
						answers = append(answers, mtproto.InlineButton{Text: strconv.FormatInt(value, 10), Data: prefix + "a" + token})
						break
					}
				}
			}
			if len(answers) > 0 {
				rows = append(rows, answers)
			}
		}
	}
	var management []mtproto.InlineButton
	for _, code := range []string{"b", "x", "j"} {
		action := verificationAdminCodes[code]
		if (action == "kick" && grp.KickButtonEnabled) || (action != "kick" && slices.Contains(grp.VerificationAdminActions, action)) {
			management = append(management, mtproto.InlineButton{Text: i18n.T("guard.verify_"+action+"_button", nil), Data: prefix + code})
		}
	}
	if len(management) > 0 {
		rows = append(rows, management)
	}
	skip := slices.Contains(grp.VerificationAdminActions, "skip")
	if skip {
		rows = append(rows, []mtproto.InlineButton{{Text: i18n.T("guard.verify_skip_button", nil), Data: prefix + "s"}})
	}
	rows = append(rows, []mtproto.InlineButton{{
		Text: i18n.T("guard.open_profile_button", nil),
		URL:  fmt.Sprintf("tg://user?id=%d", userID),
	}})
	hint := ""
	if len(management) > 0 || skip {
		hint = "\n" + i18n.T("guard.admin_verification_hint", nil)
	}
	name := strings.Join(strings.Fields(rowStr(payload, "name")), " ")
	if name == "" {
		name = i18n.T("guard.unnamed_user", nil)
	}
	username := i18n.T("guard.no_username", nil)
	if value := strings.TrimPrefix(strings.TrimSpace(rowStr(payload, "username")), "@"); value != "" {
		username = "@" + value
	}
	text := i18n.T(key, map[string]string{
		"name":           name,
		"username":       username,
		"user_id":        strconv.FormatInt(userID, 10),
		"question":       question,
		"seconds":        strconv.FormatInt(max(int64(0), int64(expiresAt.Sub(now)/time.Second)), 10),
		"attempts":       strconv.Itoa(attempts),
		"wrong_action":   i18n.T("guard.verify_action_"+grp.VerificationWrongAction, nil),
		"timeout_action": i18n.T("guard.verify_action_"+grp.VerificationTimeoutAction, nil),
		"admin_hint":     hint,
	})
	return text, rows
}

func legacyChallengeText(text string) string {
	text = strings.ReplaceAll(text, "个图标", "个词语")
	for _, word := range captchaWords {
		text = strings.ReplaceAll(text, word.Emoji+" "+word.Label, word.Label)
		text = strings.ReplaceAll(text, word.Emoji, word.Label)
	}
	return text
}

func (vm *VerificationManager) refreshVerificationCard(ctx context.Context, st *guarddb.Store, session guarddb.Row, grp GuardGroup) error {
	status := rowStr(session, "status")
	messageID := mtproto.MessageID(rowInt64(session, "message_id"))
	if !grp.Enabled || vm.svc.ops == nil || messageID <= 0 || (status != "waiting" && status != "held") {
		return nil
	}
	p := rowPayload(session)
	now := time.Now().UTC()
	expiresAt := parseRowTimeOrNow(session, "expires_at", now)
	assistant, _ := p["assistant"].(map[string]any)
	if status == "waiting" && assistant != nil && rowStr(assistant, "username") == "" && assistantReceipt.MatchString(rowStr(assistant, "secret")) {
		botID := vm.ConfiguredAssistantID()
		savedID := rowInt64(assistant, "bot_id")
		if botID > 0 && (savedID == 0 || savedID == botID) {
			active, err := st.ActiveAssistant(ctx, botID)
			if err != nil {
				return err
			}
			if name := strings.TrimPrefix(rowStr(active, "username"), "@"); name != "" {
				assistant["bot_id"], assistant["username"] = botID, name
				p["go_challenge_ui_version"] = 0
				if err := saveVerificationRow(ctx, st, session); err != nil {
					return err
				}
			}
		}
	}
	if rowInt64(p, "go_challenge_ui_version") == verificationCardVersion {
		return nil
	}
	text, buttons := verificationCard(rowStr(session, "id"), rowInt64(session, "user_id"), status, p, expiresAt, now, grp)
	if err := vm.svc.ops.EditMessage(ctx, rowInt64(session, "chat_id"), messageID, text, mtproto.SendOptions{
		ParseMode: "plain", NoWebpage: true, Buttons: buttons,
	}); err != nil && !missingTelegramMessage(err) {
		return err
	}
	p["go_challenge_ui_version"] = verificationCardVersion
	return saveVerificationRow(ctx, st, session)
}
