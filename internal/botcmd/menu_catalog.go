// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package botcmd

import (
	"context"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

var menuOrder = []string{
	"basic_core", "channel", "schedule_push", "stats_history", "channel_interaction",
	"ai", "forwarding", "guard", "wiki", "userbot", "system_ops", "preferences",
}

// LocalizedBotCommands keeps the same order in Telegram's localized command menus.
func LocalizedBotCommands(lang string) []mtproto.BotCommandDef {
	ctx := i18n.WithLanguage(context.Background(), lang)
	commands := AllBotCommands()
	for i := range commands {
		commands[i].Description = commandLabel(ctx, commands[i])
	}
	return commands
}

func orderedCommandGroups() []CategoryGroup {
	groups := make([]CategoryGroup, 0, len(BotCommandGroups))
	for _, key := range menuOrder {
		for _, group := range BotCommandGroups {
			if group.I18nKey == key {
				groups = append(groups, group)
				break
			}
		}
	}
	return groups
}

func menuLabel(ctx context.Context, zh, en string) string {
	if i18n.LanguageFor(ctx) == i18n.LangEnUS {
		return en
	}
	return zh
}

func menuGroupTitle(ctx context.Context, group CategoryGroup) string {
	title := i18n.Text(ctx, "help.section_"+group.I18nKey, nil)
	if strings.HasPrefix(title, "help.section_") {
		return group.Category
	}
	return strings.TrimSpace(mtproto.StripEmoji(title))
}

func commandLabel(ctx context.Context, command mtproto.BotCommandDef) string {
	label := i18n.Text(ctx, "cmd."+command.Command, nil)
	if _, description, ok := strings.Cut(label, " - "); ok && strings.HasPrefix(label, "/") {
		label = description
	}
	if label == "cmd."+command.Command {
		label = command.Description
		switch command.Command {
		case "wiki_status":
			label = menuLabel(ctx, label, "View channel Wiki status")
		case "wiki_build":
			label = menuLabel(ctx, label, "Build channel Wiki history")
		case "wiki_pause":
			label = menuLabel(ctx, label, "Pause channel Wiki updates")
		}
	}
	return strings.TrimSpace(mtproto.StripEmoji(label))
}

func menuCommandLabel(ctx context.Context, name string) string {
	for _, group := range BotCommandGroups {
		for _, command := range group.Commands {
			if command.Command == name {
				return commandLabel(ctx, command)
			}
		}
	}
	return name
}

func menuKey(menu string) string {
	switch menu {
	case MenuBasic:
		return "basic_core"
	case MenuConfig:
		return "ai"
	default:
		return menu
	}
}

func localizedMenuText(ctx context.Context, menu string) string {
	for _, group := range BotCommandGroups {
		if group.I18nKey == menuKey(menu) {
			return menuGroupTitle(ctx, group)
		}
	}
	if menu == MenuQA {
		return "Sakura QA"
	}
	return menuLabel(ctx, "Sakura Bot 主菜单", "Sakura Bot main menu")
}

func localizedMenuKeyboard(ctx context.Context, menu string, admin bool) [][]mtproto.InlineButton {
	menu = menuKey(menu)
	var rows [][]mtproto.InlineButton
	for _, group := range orderedCommandGroups() {
		if menu == MenuMain {
			if admin || group.I18nKey == "guard" || group.I18nKey == "wiki" || group.I18nKey == "preferences" {
				rows = append(rows, []mtproto.InlineButton{{Text: menuGroupTitle(ctx, group), Data: BuildMenuCallback("menu", group.I18nKey)}})
			}
			continue
		}
		if menu != group.I18nKey && !(menu == MenuQA && group.I18nKey == "system_ops") {
			continue
		}
		for _, command := range group.Commands {
			if menu == MenuQA && !strings.HasPrefix(command.Command, "qa_") || !admin && !hasOwnCommandAuthorization(command.Command) {
				continue
			}
			rows = append(rows, []mtproto.InlineButton{{Text: commandLabel(ctx, command), Data: BuildMenuCallback("cmd", command.Command)}})
		}
	}
	if menu != MenuMain {
		rows = append(rows, []mtproto.InlineButton{{Text: menuLabel(ctx, "返回主菜单", "Back to main menu"), Data: BuildMenuCallback("menu", MenuMain)}})
	}
	return rows
}

func hasOwnCommandAuthorization(cmd string) bool {
	return cmd == "start" || cmd == "help" || cmd == "language" || cmd == "wiki_status" ||
		cmd == "guard" || strings.HasPrefix(cmd, "guard_")
}
