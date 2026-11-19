// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package botcmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type fakeCmdMessenger struct {
	mtproto.Messenger
	sent    []string
	options []mtproto.SendOptions
	edited  []string
}

func (m *fakeCmdMessenger) SendMessage(_ context.Context, _ any, text string, opts mtproto.SendOptions) (int, error) {
	m.sent = append(m.sent, text)
	m.options = append(m.options, opts)
	return len(m.sent), nil
}

func (m *fakeCmdMessenger) SendLongMessage(_ context.Context, _ any, text string, _ mtproto.SendOptions) ([]int, error) {
	m.sent = append(m.sent, text)
	return []int{len(m.sent)}, nil
}

func (m *fakeCmdMessenger) EditMessage(_ context.Context, _ any, _ int, text string, _ mtproto.SendOptions) error {
	m.edited = append(m.edited, text)
	return nil
}

func (m *fakeCmdMessenger) AnswerCallback(_ context.Context, _ int64, _ string, _ bool) error {
	return nil
}

func TestAllBotCommandsCountAndMenuBounds(t *testing.T) {
	all := AllBotCommands()
	if len(all) != 79 || all[0].Command != "start" || all[1].Command != "help" {
		t.Fatalf("expected 79 registered BotCommands, got %d", len(all))
	}
	commands := make(map[string]bool)
	for _, menu := range append([]string{MenuMain, MenuBasic, MenuQA, MenuConfig}, menuOrder...) {
		rows := BuildMenuKeyboard(menu)
		for _, row := range rows {
			for _, btn := range row {
				if len(btn.Data) > MaxCallbackBytes {
					t.Fatalf("menu %s button %q callback exceeded 64B: %q", menu, btn.Text, btn.Data)
				}
				if command, ok := strings.CutPrefix(btn.Data, "mainbot:cmd:"); ok {
					commands[command] = true
				}
			}
		}
	}
	for _, command := range all {
		if !commands[command.Command] {
			t.Errorf("%s has no menu button", command.Command)
		}
	}
}

func TestAdminFailClosedFSMAndWebUIDangerGate(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	jsonPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(envPath, []byte("REPORT_ADMIN_IDS=9001\n"), 0o644)
	_ = os.WriteFile(jsonPath, []byte(`{"channels":["https://t.me/init_ch"],"base_url":"https://keep.example/v1","model":"gpt-4o"}`), 0o644)

	cfg := config.New(envPath, jsonPath)
	if err := cfg.Init(); err != nil {
		t.Fatalf("cfg.Init: %v", err)
	}

	bot := &fakeCmdMessenger{}
	router := NewRouter(Deps{
		Config:    cfg,
		BotClient: func() mtproto.Messenger { return bot },
	})

	// 非管理员调用 /addchannel 应被拒绝。
	router.HandleMessage(context.Background(), &mtproto.IncomingMessage{
		ID:       1,
		ChatID:   888,
		SenderID: 2002,
		Text:     "/添加频道 @hacker_ch",
	})
	if len(cfg.Channels()) != 1 {
		t.Fatalf("non-admin must not mutate channels, got %v", cfg.Channels())
	}

	click := func(actor int64, action string) {
		t.Helper()
		for _, row := range bot.options[len(bot.options)-1].Buttons {
			for _, button := range row {
				if strings.HasPrefix(button.Data, "mainbot:"+action+":") {
					if err := router.HandleMenuCallback(t.Context(), mtproto.CallbackEvent{
						ChatID: 9001, SenderID: actor, MessageID: len(bot.sent), Data: []byte(button.Data),
					}); err != nil {
						t.Fatal(err)
					}
					return
				}
			}
		}
		t.Fatalf("missing %s button", action)
	}
	router.HandleMessage(t.Context(), &mtproto.IncomingMessage{ID: 1, ChatID: 9001, SenderID: 9001, Text: "/start"})
	if !strings.Contains(bot.sent[len(bot.sent)-1], "Choose your language") {
		t.Fatal("first start did not ask for language")
	}
	click(9001, "language")
	router.HandleMessage(t.Context(), &mtproto.IncomingMessage{ID: 2, ChatID: 9001, SenderID: 9001, Text: "/start"})
	if strings.Contains(bot.sent[len(bot.sent)-1], "Choose your language") {
		t.Fatal("start asked again after language was saved")
	}

	// 修改 API Key，点击保留 Base URL，然后输入模型名。
	router.HandleMessage(context.Background(), &mtproto.IncomingMessage{ID: 2, ChatID: 9001, SenderID: 9001, Text: "/setaicfg"})
	click(2002, "run")
	if router.States().Mode(9001) != FSMNone {
		t.Fatal("another user opened the settings wizard")
	}
	click(9001, "run")
	router.HandleMessage(context.Background(), &mtproto.IncomingMessage{ID: 3, ChatID: 9001, SenderID: 9001, Text: "sk-new-key-123456"})
	click(9001, "skip")
	router.HandleMessage(context.Background(), &mtproto.IncomingMessage{ID: 5, ChatID: 9001, SenderID: 9001, Text: "gemini-2.5-pro"})

	eff := cfg.EffectiveAIConfig("summary")
	if eff.APIKey != "sk-new-key-123456" || !strings.Contains(eff.BaseURL, "keep.example") || eff.Model != "gemini-2.5-pro" {
		t.Fatalf("unexpected AI config after /setaicfg with /skip: %+v", eff)
	}

	// WebUI 命令目录核查：共 79 条命令，28 条可执行操作，危险操作未传 CONFIRM 被拦截。
	cat := ListWebUICommands()
	if cat["total"] != 79 || cat["executable_count"] != 28 {
		t.Fatalf("unexpected WebUI command catalog counts: total=%v exec=%v", cat["total"], cat["executable_count"])
	}
	res, err := router.ExecuteWebUICommand(context.Background(), "deletechannel", map[string]any{"channel": "@init_ch"}, false, "", "admin")
	if err != nil || res["success"] != false {
		t.Fatalf("expected danger command blocked without CONFIRM, got %+v (err=%v)", res, err)
	}
	res2, err := router.ExecuteWebUICommand(context.Background(), "deletechannel", map[string]any{"channel": "@init_ch"}, true, "CONFIRM", "admin")
	if err != nil || res2["success"] != true || len(cfg.Channels()) != 0 {
		t.Fatalf("expected deletechannel with CONFIRM to succeed, got %+v (err=%v)", res2, err)
	}
}
