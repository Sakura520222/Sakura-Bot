// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package botcmd

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type menuActor struct{ chat, user int64 }

const menuRunTTL = 10 * time.Minute

type menuRun struct {
	token, command, args string
	messageID            int
	expires              time.Time
	awaitText, fsm       bool
}

type menuInput struct {
	hint     string
	optional bool
	choices  []string
}

var menuInputs = map[string]menuInput{
	"summary":                       {hint: "@channel [@channel …]", optional: true},
	"addchannel":                    {hint: "@channel [@channel …]"},
	"deletechannel":                 {hint: "@channel [@channel …]"},
	"showchannelschedule":           {hint: "@channel", optional: true},
	"setchannelschedule":            {hint: "@channel daily HH:MM\n@channel weekly mon,wed HH:MM"},
	"deletechannelschedule":         {hint: "@channel"},
	"clearsummarytime":              {hint: "@channel", optional: true},
	"setsendtosource":               {choices: []string{"on", "off"}},
	"history":                       {hint: "@channel [days]", optional: true},
	"export":                        {hint: "@channel [json|csv|md]", optional: true, choices: []string{"json", "csv", "md"}},
	"stats":                         {hint: "@channel", optional: true},
	"channelpoll":                   {hint: "@channel", optional: true},
	"setchannelpoll":                {hint: "@channel on|off [channel|discussion]"},
	"deletechannelpoll":             {hint: "@channel"},
	"showcommentwelcome":            {hint: "@channel", optional: true},
	"setcommentwelcome":             {hint: "@channel on|off [welcome_message]"},
	"deletecommentwelcome":          {hint: "@channel"},
	"showautopoll":                  {hint: "@channel", optional: true},
	"setautopoll":                   {hint: "@channel on|off", choices: []string{"on", "off"}},
	"deleteautopoll":                {hint: "@channel"},
	"forwarding_add_rule":           {hint: "@source @target"},
	"forwarding_remove_rule":        {hint: "rule_number | @source @target"},
	"forwarding_keywords":           {hint: "rule_number [keyword1,keyword2 | clear]"},
	"forwarding_blacklist":          {hint: "rule_number [keyword1,keyword2 | clear]"},
	"forwarding_patterns":           {hint: "rule_number [pattern1;;pattern2 | clear]"},
	"forwarding_blacklist_patterns": {hint: "rule_number [pattern1;;pattern2 | clear]"},
	"forwarding_copy_mode":          {hint: "rule_number on|off"},
	"forwarding_original_only":      {hint: "rule_number on|off"},
	"forwarding_rule_info":          {hint: "rule_number"},
	"forwarding_footer":             {hint: "rule_number template|clear"},
	"forwarding_default_footer":     {choices: []string{"on", "off"}},
	"guard":                         {hint: "group_id", optional: true},
	"guard_on":                      {hint: "group_id", optional: true},
	"guard_off":                     {hint: "group_id", optional: true},
	"guard_verify":                  {hint: "[group_id] on|off", choices: []string{"on", "off"}},
	"guard_bots":                    {hint: "[group_id] on|off", choices: []string{"on", "off"}},
	"guard_addword":                 {hint: "keyword"},
	"guard_delword":                 {hint: "keyword"},
	"guard_whitelist":               {hint: "[group_id] add|remove user_id"},
	"guard_logs":                    {hint: "group_id", optional: true},
	"guard_unban":                   {hint: "[group_id] case_id"},
	"wiki_status":                   {hint: "@channel"},
	"wiki_build":                    {hint: "@channel"},
	"wiki_pause":                    {hint: "@channel"},
	"userbot_join":                  {hint: "@channel | https://t.me/+invite"},
	"userbot_leave":                 {hint: "@channel"},
	"setloglevel":                   {choices: []string{"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"}},
	"clearcache":                    {hint: "@channel", optional: true},
	"changelog":                     {hint: "version", optional: true},
	"update":                        {hint: "version", optional: true},
	"qa_runtime":                    {optional: true, choices: []string{"go"}},
}

func (r *Router) menuContext(ctx context.Context, user int64) context.Context {
	if r.deps.Config != nil && user > 0 {
		if lang, err := r.deps.Config.MainBotLanguage(user); err == nil && lang != "" {
			return i18n.WithLanguage(ctx, lang)
		}
	}
	return ctx
}

func (r *Router) showMenu(ctx context.Context, bot mtproto.Messenger, msg *mtproto.IncomingMessage, menu string) error {
	_, err := bot.SendMessage(ctx, msg.ChatID, localizedMenuText(ctx, menu), mtproto.SendOptions{
		Buttons: localizedMenuKeyboard(ctx, menu, r.isAdmin(msg.SenderID)),
	})
	return err
}

func (r *Router) openMenuCommand(ctx context.Context, bot mtproto.Messenger, msg *mtproto.IncomingMessage, command, args string) error {
	if !hasOwnCommandAuthorization(command) && !r.isAdmin(msg.SenderID) {
		r.reply(ctx, bot, msg.ChatID, 0, i18n.Text(ctx, "error.permission_denied", nil))
		return nil
	}
	if command == "help" {
		return r.showMenu(ctx, bot, msg, MenuMain)
	}
	if command == "start" || command == "language" {
		if command == "start" && r.deps.Config != nil {
			lang, err := r.deps.Config.MainBotLanguage(msg.SenderID)
			if err != nil {
				return err
			}
			if lang != "" {
				return r.showMenu(ctx, bot, msg, MenuMain)
			}
		}
		_, err := bot.SendMessage(ctx, msg.ChatID, "请选择语言\nChoose your language", mtproto.SendOptions{
			Buttons: [][]mtproto.InlineButton{{
				{Text: "简体中文", Data: BuildMenuCallback("language", i18n.LangZhCN)},
				{Text: "English", Data: BuildMenuCallback("language", i18n.LangEnUS)},
			}},
		})
		return err
	}
	if (command == "setaicfg" || command == "setprompt" || command == "setpollprompt") && msg.ChatID != msg.SenderID {
		r.reply(ctx, bot, msg.ChatID, 0, menuLabel(ctx, "请在与 Bot 的私聊中打开此设置。", "Open this setting in a private chat with the bot."))
		return nil
	}

	now := time.Now()
	run := menuRun{token: rand.Text(), command: command, args: args, expires: now.Add(menuRunTTL)}
	input, hasInput := menuInputs[command]
	// Values are not included in callback data or echoed back to Telegram.
	run.awaitText = hasInput && input.hint != "" && args == ""
	text := menuCommandLabel(ctx, command)
	if command == "db_clear" {
		text += "\n" + menuLabel(ctx, "将清空历史总结、订阅、对话、转发记录和向量索引。", "This clears summaries, subscriptions, conversations, forwarding data and vector indexes.")
	}
	if run.awaitText {
		text += "\n\n" + menuLabel(ctx, "请回复这条消息，输入所需内容。", "Reply to this message with the parameters.") + "\n" + input.hint
	}
	button := func(label, action string) []mtproto.InlineButton {
		return []mtproto.InlineButton{{Text: label, Data: BuildMenuCallback(action, run.token)}}
	}
	var rows [][]mtproto.InlineButton
	if args == "" && len(input.choices) > 0 {
		for i, choice := range input.choices {
			rows = append(rows, button(choice, fmt.Sprintf("pick%d", i)))
		}
	}
	if !hasInput || input.optional || args != "" {
		rows = append(rows, button(menuLabel(ctx, "确认执行", "Run"), "run"))
	}
	rows = append(rows, button(menuLabel(ctx, "取消", "Cancel"), "cancel"))
	id, err := bot.SendMessage(ctx, msg.ChatID, text, mtproto.SendOptions{Buttons: rows, NoWebpage: true})
	if err == nil {
		run.messageID = id
		r.menuMu.Lock()
		for key, old := range r.menuRuns {
			if !old.expires.After(now) {
				if old.fsm {
					r.states.Cancel(key.user)
				}
				delete(r.menuRuns, key)
			}
		}
		key := menuActor{msg.ChatID, msg.SenderID}
		if r.menuRuns[key].fsm {
			r.states.Cancel(key.user)
		}
		r.menuRuns[key] = run
		r.menuMu.Unlock()
	}
	return err
}

var oldMenuCommands = map[string]string{
	CmdChannels: "showchannels", CmdForwardingStatus: "forwarding",
	CmdForwardingStats: "forwarding_stats", CmdForwardingHelp: "forwarding_help",
	CmdShowPrompt: "showprompt", CmdShowAIConfig: "showaicfg", CmdShowSchedule: "showchannelschedule",
	CmdShowPoll: "channelpoll", CmdConfigHelp: "help",
}

func (r *Router) HandleMenuCallback(ctx context.Context, ev mtproto.CallbackEvent) error {
	bot := r.resolveBot()
	parts := strings.SplitN(ev.DataString(), ":", 3)
	if bot == nil || ev.SenderID <= 0 || len(parts) != 3 || parts[0] != MenuCallbackPrefix {
		return nil
	}
	ctx = r.menuContext(ctx, ev.SenderID)
	action, value := parts[1], parts[2]
	msg := &mtproto.IncomingMessage{ChatID: ev.ChatID, SenderID: ev.SenderID, Sender: ev.Sender, IsPrivate: ev.ChatID == ev.SenderID}
	msg.Sender.ID = ev.SenderID
	switch action {
	case "menu":
		_ = bot.AnswerCallback(ctx, ev.QueryID, "", false)
		if msg.IsPrivate {
			return bot.EditMessage(ctx, ev.ChatID, ev.MessageID, localizedMenuText(ctx, value), mtproto.SendOptions{
				Buttons: localizedMenuKeyboard(ctx, value, r.isAdmin(ev.SenderID)),
			})
		}
		return r.showMenu(ctx, bot, msg, value)
	case "cmd":
		_ = bot.AnswerCallback(ctx, ev.QueryID, "", false)
		if canonical := oldMenuCommands[value]; canonical != "" {
			value = canonical
		}
		if canonical := canonicalCommand(value); canonical != "" {
			return r.openMenuCommand(ctx, bot, msg, canonical, "")
		}
	case "language":
		if r.deps.Config == nil || (value != i18n.LangZhCN && value != i18n.LangEnUS) {
			break
		}
		if err := r.deps.Config.SaveMainBotLanguage(ctx, ev.SenderID, value); err != nil {
			_ = bot.AnswerCallback(ctx, ev.QueryID, "Language could not be saved", true)
			return err
		}
		_ = bot.AnswerCallback(ctx, ev.QueryID, "", false)
		return r.showMenu(i18n.WithLanguage(ctx, value), bot, msg, MenuMain)
	default:
		key := menuActor{ev.ChatID, ev.SenderID}
		r.menuMu.Lock()
		run, ok := r.menuRuns[key]
		valid := ok && run.token == value && run.messageID == ev.MessageID && time.Now().Before(run.expires)
		if valid {
			delete(r.menuRuns, key)
		}
		r.menuMu.Unlock()
		if !valid {
			return bot.AnswerCallback(ctx, ev.QueryID, menuLabel(ctx, "此操作已过期或属于其他用户。", "This action expired or belongs to another user."), true)
		}
		_ = bot.AnswerCallback(ctx, ev.QueryID, "", false)
		msg.ID = ev.MessageID
		switch action {
		case "cancel":
			if run.fsm {
				r.states.Cancel(ev.SenderID)
			}
			return r.showMenu(ctx, bot, msg, MenuMain)
		case "skip":
			if run.fsm && r.isAdmin(ev.SenderID) && r.states.Mode(ev.SenderID) == FSMAIConfig {
				r.handleFSMInput(ctx, bot, msg, "/skip")
				return r.continueMenuFSM(ctx, bot, key, run)
			}
		case "run":
			if input, found := menuInputs[run.command]; !found || input.optional || run.args != "" {
				return r.executeMenuRun(ctx, bot, msg, run)
			}
		default:
			for i, choice := range menuInputs[run.command].choices {
				if action == fmt.Sprintf("pick%d", i) {
					run.args = choice
					return r.executeMenuRun(ctx, bot, msg, run)
				}
			}
		}
	}
	return nil
}

func (r *Router) acceptMenuInput(ctx context.Context, bot mtproto.Messenger, msg *mtproto.IncomingMessage, text string) bool {
	if strings.HasPrefix(text, "/") {
		return false
	}
	key := menuActor{msg.ChatID, msg.SenderID}
	r.menuMu.Lock()
	run, ok := r.menuRuns[key]
	accept := ok && run.awaitText && time.Now().Before(run.expires) &&
		(msg.ChatID == msg.SenderID || msg.ReplyToMsgID == run.messageID)
	if accept {
		delete(r.menuRuns, key)
	}
	r.menuMu.Unlock()
	if !accept {
		return false
	}
	ctx = r.menuContext(ctx, msg.SenderID)
	var err error
	if run.fsm && r.isAdmin(msg.SenderID) {
		r.handleFSMInput(ctx, bot, msg, text)
		err = r.continueMenuFSM(ctx, bot, key, run)
	} else {
		err = r.openMenuCommand(ctx, bot, msg, run.command, text)
	}
	if err != nil {
		r.deps.Logger.Warn("主 Bot 操作输入处理失败", "command", run.command, "chat", msg.ChatID, "error", err)
	}
	return true
}

func (r *Router) executeMenuRun(ctx context.Context, bot mtproto.Messenger, msg *mtproto.IncomingMessage, run menuRun) error {
	if run.command == "db_clear" {
		run.args = "CONFIRM"
	}
	r.dispatchCanonical(ctx, bot, msg, run.command, run.args, 0)
	return r.continueMenuFSM(ctx, bot, menuActor{msg.ChatID, msg.SenderID}, run)
}

func (r *Router) continueMenuFSM(ctx context.Context, bot mtproto.Messenger, key menuActor, run menuRun) error {
	switch run.command {
	case "setaicfg", "setprompt", "setpollprompt":
	default:
		return nil
	}
	mode := r.states.Mode(key.user)
	if mode == FSMNone {
		return nil
	}
	run.token, run.awaitText, run.fsm = rand.Text(), true, true
	buttons := []mtproto.InlineButton{
		{Text: menuLabel(ctx, "取消", "Cancel"), Data: BuildMenuCallback("cancel", run.token)},
	}
	if mode == FSMAIConfig {
		buttons = append(buttons, mtproto.InlineButton{
			Text: menuLabel(ctx, "保留当前值", "Keep current value"), Data: BuildMenuCallback("skip", run.token),
		})
	}
	id, err := bot.SendMessage(ctx, key.chat, menuLabel(ctx, "请回复所需内容，或点击下方按钮。", "Reply with the requested value or use the buttons."), mtproto.SendOptions{
		Buttons: [][]mtproto.InlineButton{buttons},
	})
	if err == nil {
		run.messageID, run.expires = id, time.Now().Add(menuRunTTL)
		r.menuMu.Lock()
		r.menuRuns[key] = run
		r.menuMu.Unlock()
	} else {
		r.states.Cancel(key.user)
	}
	return err
}
