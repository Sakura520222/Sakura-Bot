// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTypedEnvAccessors(t *testing.T) {
	// 1. 默认值校验
	defCfg := newTestConfig(t, nil, "", t.TempDir())
	if _, err := defCfg.TelegramAPIID(); err == nil {
		t.Fatal("未设置 TELEGRAM_API_ID 应返回错误")
	}
	if defCfg.TelegramAPIHash() != "" || defCfg.TelegramBotToken() != "" {
		t.Fatal("未设置 Telegram 凭据应为空")
	}
	if defCfg.UserBotEnabled() || defCfg.UserBotFallbackToBot() || defCfg.UserBotPhoneNumber() != "" {
		t.Fatal("UserBot 默认开关应为 false/空")
	}
	if defCfg.UserBotSessionPath() != "data/sessions/user_session" {
		t.Fatalf("UserBotSessionPath 默认值不符: %q", defCfg.UserBotSessionPath())
	}
	if defCfg.SummaryContextTokens() != 1000000 || defCfg.SummaryOutputTokens() != 64000 {
		t.Fatalf("Summary token 默认值不符: %d / %d", defCfg.SummaryContextTokens(), defCfg.SummaryOutputTokens())
	}
	if !defCfg.EnablePollEnv() || !defCfg.PollPublicVotersEnv() {
		t.Fatal("投票环境变量默认应为 true")
	}
	if defCfg.SchedulerTimezone() != "Asia/Shanghai" {
		t.Fatalf("默认调度时区应为 Asia/Shanghai，实际 %q", defCfg.SchedulerTimezone())
	}
	if defCfg.WebUIEnabled() || defCfg.WebUIDevMode() || defCfg.WebUIHost() != "0.0.0.0" || defCfg.WebUIPort() != 8080 {
		t.Fatalf("WebUI 默认值不符: enabled=%v host=%s port=%d dev=%v",
			defCfg.WebUIEnabled(), defCfg.WebUIHost(), defCfg.WebUIPort(), defCfg.WebUIDevMode())
	}
	if defCfg.WikiLoginEnabled() || defCfg.WikiLoginOrigin() != "" || defCfg.WikiTelegramClientSecret() != "" ||
		defCfg.WikiAssistantEnabled() || !defCfg.WikiAssistantCheckinEnabled() || defCfg.WikiAssistantPort() != 8091 {
		t.Fatal("Wiki Login / Assistant 默认值不符")
	}
	if defCfg.VectorDBPath() != "data/vectors" || defCfg.EmbeddingDimension() != 1024 ||
		defCfg.MessageSourceDir() != "data/message_sources" || defCfg.SummaryAuditDir() != "data/summary_runs" {
		t.Fatal("向量与归档目录默认值不符")
	}
	if len(defCfg.AdminIDs()) != 0 {
		t.Fatalf("未设置管理员应为空: %v", defCfg.AdminIDs())
	}

	// 2. 显式覆盖与容错回退校验
	customCfg := newTestConfig(t, map[string]string{
		"TELEGRAM_API_ID":                "123456",
		"TELEGRAM_API_HASH":              " hash_abc ",
		"TELEGRAM_BOT_TOKEN":             " 111:bot_tok ",
		"USERBOT_ENABLED":                "true",
		"USERBOT_PHONE_NUMBER":           "+8613800000000",
		"USERBOT_SESSION_PATH":           "custom/sess",
		"USERBOT_FALLBACK_TO_BOT":        "1",
		"QA_BOT_USERNAME":                "@@sakura_qa_bot",
		"SUMMARY_CONTEXT_TOKENS":         "500000",
		"SUMMARY_OUTPUT_TOKENS":          "32000",
		"ENABLE_POLL":                    "false",
		"POLL_PUBLIC_VOTERS":             "no",
		"TZ":                             "UTC",
		"WEBUI_ENABLED":                  "yes",
		"WEBUI_HOST":                     "127.0.0.1",
		"WEBUI_PORT":                     "9090",
		"WEBUI_DEV_MODE":                 "on",
		"WIKI_LOGIN_ENABLED":             "true",
		"WIKI_LOGIN_ORIGIN":              "https://wiki.example.com",
		"WIKI_TELEGRAM_CLIENT_SECRET":    "sec-xyz",
		"WIKI_ASSISTANT_ENABLED":         "true",
		"WIKI_ASSISTANT_CHECKIN_ENABLED": "false",
		"WIKI_ASSISTANT_PORT":            "18091",
		"VECTOR_DB_PATH":                 "var/vec",
		"EMBEDDING_DIMENSION":            "1536",
		"MESSAGE_SOURCE_DIR":             "var/src",
		"SUMMARY_AUDIT_DIR":              "var/audit",
		"REPORT_ADMIN_IDS":               "1001, bad, 2002",
	}, `{"wiki_login":{"assistant_enabled":false}}`, t.TempDir())

	apiID, err := customCfg.TelegramAPIID()
	if err != nil || apiID != 123456 {
		t.Fatalf("TelegramAPIID = (%d, %v)", apiID, err)
	}
	if customCfg.TelegramAPIHash() != "hash_abc" || customCfg.TelegramBotToken() != "111:bot_tok" {
		t.Fatal("Telegram hash/token 裁剪不符")
	}
	if !customCfg.UserBotEnabled() || !customCfg.UserBotFallbackToBot() ||
		customCfg.UserBotPhoneNumber() != "+8613800000000" || customCfg.UserBotSessionPath() != "custom/sess" {
		t.Fatal("UserBot 显式配置不符")
	}
	if customCfg.QABotUsername() != "sakura_qa_bot" {
		t.Fatalf("QABotUsername = %q", customCfg.QABotUsername())
	}
	if customCfg.SummaryContextTokens() != 500000 || customCfg.SummaryOutputTokens() != 32000 {
		t.Fatal("Summary token 覆盖未生效")
	}
	if customCfg.EnablePollEnv() || customCfg.PollPublicVotersEnv() {
		t.Fatal("投票环境变量 false/no 覆盖未生效")
	}
	if customCfg.SchedulerTimezone() != "UTC" {
		t.Fatalf("TZ=UTC 覆盖未生效: %q", customCfg.SchedulerTimezone())
	}
	if !customCfg.WebUIEnabled() || !customCfg.WebUIDevMode() || customCfg.WebUIHost() != "127.0.0.1" || customCfg.WebUIPort() != 9090 {
		t.Fatal("WebUI 覆盖未生效")
	}
	// wiki_login.assistant_enabled 在 config.json 中覆写为 false，Env 保持 true。
	if !customCfg.Env.WikiAssistantEnabled() || customCfg.WikiAssistantEnabled() {
		t.Fatal("wiki_login config.json 覆写优先级不符")
	}
	if !customCfg.WikiLoginEnabled() || customCfg.WikiLoginOrigin() != "https://wiki.example.com" ||
		customCfg.WikiTelegramClientSecret() != "sec-xyz" || customCfg.WikiAssistantCheckinEnabled() ||
		customCfg.WikiAssistantPort() != 18091 {
		t.Fatal("Wiki 配置覆盖不符")
	}
	if customCfg.VectorDBPath() != "var/vec" || customCfg.EmbeddingDimension() != 1536 ||
		customCfg.MessageSourceDir() != "var/src" || customCfg.SummaryAuditDir() != "var/audit" {
		t.Fatal("目录与维度覆盖不符")
	}
	if !reflect.DeepEqual(customCfg.AdminIDs(), []int64{1001, 2002}) {
		t.Fatalf("AdminIDs 不符: %v", customCfg.AdminIDs())
	}

	// 非法负值 API_ID 与非法端口回退
	badCfg := newTestConfig(t, map[string]string{
		"TELEGRAM_API_ID":    "-9",
		"WEBUI_PORT":         "70000",
		"SCHEDULER_TIMEZONE": "Not/A_Timezone",
	}, "", t.TempDir())
	if _, err := badCfg.TelegramAPIID(); err == nil {
		t.Fatal("负数 TELEGRAM_API_ID 应报错")
	}
	if badCfg.WebUIPort() != 8080 {
		t.Fatalf("非法端口应回退 8080: %d", badCfg.WebUIPort())
	}
	if badCfg.SchedulerTimezone() != "Asia/Shanghai" {
		t.Fatalf("非法时区应回退 Asia/Shanghai: %s", badCfg.SchedulerTimezone())
	}
}

func TestDomainConfigsSchedulesPollsCommentWelcomeForwardingRuntime(t *testing.T) {
	rawJSON := `{
		"send_report_to_source": false,
		"enable_poll": false,
		"poll_regen_threshold": 8,
		"enable_vote_regen_request": false,
		"public_voters": false,
		"enable_auto_poll": true,
		"qa_bot_runtime": "python",
		"sakura_guard": {"global_keywords": ["spam"]},
		"summary_schedules": {
			"https://t.me/DailyChan": {"frequency": "daily", "hour": 21, "minute": 30},
			"https://t.me/LegacyChan": {"day": "fri", "hour": 10, "minute": 15}
		},
		"channel_poll_settings": {
			"https://t.me/DailyChan": {"enabled": true, "send_to_channel": true}
		},
		"channel_auto_poll_settings": {
			"https://t.me/Nahida_Leak": {"enabled": false},
			"https://t.me/InheritChan": {"enabled": null}
		},
		"comment_welcome": {
			"default": {"enabled": true, "welcome_message": "custom.welcome"}
		},
		"channel_comment_welcome": {
			"https://t.me/DailyChan": {"enabled": false}
		},
		"forwarding": {
			"enabled": true,
			"show_default_footer": false,
			"rules": [
				{
					"source_channel": "https://t.me/src",
					"target_channel": "https://t.me/dst",
					"keywords": ["breaking"],
					"copy_mode": true,
					"forward_original_only": true,
					"custom_footer": "-- sakura"
				}
			]
		}
	}`

	cfg := newTestConfig(t, nil, rawJSON, t.TempDir())
	if err := cfg.Init(); err != nil {
		t.Fatal(err)
	}

	// Schedules
	scheds := cfg.SummarySchedules()
	if len(scheds) != 2 {
		t.Fatalf("SummarySchedules 数量不符: %+v", scheds)
	}
	daily := cfg.ChannelSchedule("@dailychan")
	if daily.Frequency != "daily" || daily.Hour != 21 || daily.Minute != 30 {
		t.Fatalf("daily 调度不符: %+v", daily)
	}
	if err := daily.Validate(); err != nil {
		t.Fatalf("合法 daily 校验不应失败: %v", err)
	}
	legacy := cfg.ChannelSchedule("LegacyChan")
	if legacy.Frequency != "weekly" || !reflect.DeepEqual(legacy.Days, []string{"fri"}) || legacy.Hour != 10 {
		t.Fatalf("旧版单 day 迁移不符: %+v", legacy)
	}
	fallback := cfg.ChannelSchedule("https://t.me/unconfigured")
	if fallback.Frequency != "weekly" || fallback.Hour != 9 || fallback.Days[0] != "mon" {
		t.Fatalf("默认调度回退不符: %+v", fallback)
	}
	if err := (SummarySchedule{Frequency: "weekly", Hour: 9, Minute: 0}).Validate(); err == nil {
		t.Fatal("空 days 的 weekly 应校验失败")
	}

	// Poll & Report
	if cfg.SendReportToSource() || cfg.EnablePoll() || cfg.PollRegenThreshold() != 8 ||
		cfg.EnableVoteRegenRequest() || cfg.PollPublicVoters() {
		t.Fatal("全局投票/报告配置读取不符")
	}
	ps := cfg.ChannelPollConfig("@dailychan")
	if ps.Enabled == nil || !*ps.Enabled || ps.SendToChannel == nil || !*ps.SendToChannel || ps.PublicVoters != nil {
		t.Fatalf("频道投票配置不符: %+v", ps)
	}

	// Auto Poll（含大小写与 URL 等价键匹配）
	if !cfg.EnableAutoPoll() {
		t.Fatal("EnableAutoPoll 应为 true")
	}
	if cfg.IsAutoPollEnabledForChannel("@nahida_leak") {
		t.Fatal("@nahida_leak 应匹配 Nahida_Leak 的 enabled=false")
	}
	if !cfg.IsAutoPollEnabledForChannel("https://t.me/InheritChan/") || !cfg.IsAutoPollEnabledForChannel("@brand_new") {
		t.Fatal("enabled=null 或未配置频道在全局开启时应继承 true")
	}

	// Comment Welcome
	cw := cfg.CommentWelcomeSettings()
	defForOther := cw.GetForChannel("@other_chan")
	if !defForOther.Enabled || defForOther.WelcomeMessage != "custom.welcome" || defForOther.ButtonText != "comment_welcome.button" {
		t.Fatalf("默认评论欢迎合并错误: %+v", defForOther)
	}
	overridden := cw.GetForChannel("@dailychan")
	if overridden.Enabled || overridden.WelcomeMessage != "custom.welcome" {
		t.Fatalf("频道局部覆盖评论欢迎合并错误: %+v", overridden)
	}

	// Forwarding
	fwd := cfg.ForwardingConfig()
	if !fwd.Enabled || fwd.ShowDefaultFooter || len(fwd.Rules) != 1 {
		t.Fatalf("ForwardingConfig 不符: %+v", fwd)
	}
	r0 := fwd.Rules[0]
	if r0.SourceChannel != "https://t.me/src" || !r0.CopyMode || !r0.ForwardOriginalOnly ||
		!reflect.DeepEqual(r0.Keywords, []string{"breaking"}) || r0.Blacklist == nil {
		t.Fatalf("ForwardingRule 不符: %+v", r0)
	}

	// QA Bot Runtime
	if cfg.QABotRuntime() != "python" {
		t.Fatalf("QABotRuntime = %q, want python", cfg.QABotRuntime())
	}

	// SubSection & RawSnapshot 深拷贝隔离
	guardRaw, ok := cfg.SubSection("sakura_guard")
	if !ok || !strings.Contains(string(guardRaw), `"spam"`) {
		t.Fatalf("SubSection(sakura_guard) 不符: %s %v", guardRaw, ok)
	}
	snap := cfg.RawSnapshot()
	snap["sakura_guard"].(map[string]any)["global_keywords"] = []any{"mutated"}
	guardAgain, _ := cfg.SubSection("sakura_guard")
	if strings.Contains(string(guardAgain), "mutated") {
		t.Fatal("修改 RawSnapshot 嵌套对象污染了缓存")
	}
}

func TestAtomicPersistenceBackupRotationAndRollback(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := New("", cfgPath)

	var events []ConfigChangedEvent
	var valErrors []ConfigValidationErrorEvent
	var mu sync.Mutex

	cfg.Subscribe(PriorityNormal, func(_ context.Context, ev ConfigChangedEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	cfg.EventBus().SubscribeValidationError(PriorityNormal, func(_ context.Context, ev ConfigValidationErrorEvent) {
		mu.Lock()
		valErrors = append(valErrors, ev)
		mu.Unlock()
	})

	// 连续保存 35 次不同合法配置：测试原子写入、锁文件创建、缓存即时同步（相同字节长度 zh-CN <-> en-US）及 30 份备份轮转。
	for i := 1; i <= 35; i++ {
		lang := "zh-CN"
		if i%2 == 0 {
			lang = "en-US"
		}
		err := cfg.SaveConfigMap(map[string]any{
			"language":             lang,
			"poll_regen_threshold": i,
		})
		if err != nil {
			t.Fatalf("第 %d 次 SaveConfigMap 失败: %v", i, err)
		}
		// 即使在毫秒级内连续写入相同尺寸，RuntimeJSON 缓存也必须立即反映最新值。
		if got := cfg.Language(); got != lang {
			t.Fatalf("第 %d 次写入后 Language 缓存未即时更新: got=%s want=%s", i, got, lang)
		}
		if got := cfg.PollRegenThreshold(); got != i {
			t.Fatalf("第 %d 次写入后阈值未即时更新: got=%d want=%d", i, got, i)
		}
	}

	if _, err := os.Stat(cfgPath + ".lock"); err != nil {
		t.Fatalf("应创建锁文件 %s.lock: %v", cfgPath, err)
	}
	if _, err := os.Stat(cfgPath + ".last_valid"); err != nil {
		t.Fatalf("应创建 %s.last_valid: %v", cfgPath, err)
	}
	backups := listBackupFilesDesc(cfgPath + ".backups")
	if len(backups) != MaxConfigBackups {
		t.Fatalf("历史备份数应严格轮转保留 %d 份，实际 %d 份", MaxConfigBackups, len(backups))
	}

	// 1. UpdateConfig 校验失败回滚：不应修改磁盘或内存。
	err := cfg.UpdateConfig(func(doc map[string]any) error {
		doc["log_level"] = "NOT_A_VALID_LEVEL"
		return nil
	})
	if !errors.Is(err, ErrConfigValidation) {
		t.Fatalf("非法 log_level 应返回 ErrConfigValidation，实际 %v", err)
	}
	if cfg.PollRegenThreshold() != 35 {
		t.Fatal("校验失败的 UpdateConfig 不应污染现有状态")
	}

	// 2. 外部直接写入损坏的 JSON 到磁盘：Reload 必须自动从备份回滚磁盘文件并发出 ConfigValidationErrorEvent{RolledBack:true}。
	if err := os.WriteFile(cfgPath, []byte("{corrupted json!!!"), 0o600); err != nil {
		t.Fatal(err)
	}
	rolledBack, reloadErr := cfg.Reload(context.Background())
	if reloadErr == nil || !rolledBack {
		t.Fatalf("外部损坏配置 Reload 应报错且自动回滚: rolledBack=%v err=%v", rolledBack, reloadErr)
	}
	repairedDisk, _, verifyErr := readAndValidateConfigFile(cfgPath)
	if verifyErr != nil {
		t.Fatalf("回滚后的磁盘文件应恢复合法: %v", verifyErr)
	}
	if n, ok := parseLooseInt(repairedDisk["poll_regen_threshold"]); !ok || n != 35 {
		t.Fatalf("回滚后的阈值不符: %+v", repairedDisk)
	}

	mu.Lock()
	if len(events) != 35 {
		t.Fatalf("应收到 35 次 ConfigChangedEvent，实际 %d", len(events))
	}
	if len(valErrors) != 1 || !valErrors[0].RolledBack {
		t.Fatalf("应收到 1 次 RolledBack=true 的 ConfigValidationErrorEvent: %+v", valErrors)
	}
	mu.Unlock()
}

func TestEventBusPriorityOrderUnsubscribeAndPanicIsolation(t *testing.T) {
	bus := NewEventBus()
	var order []string

	unsubLow := bus.Subscribe(PriorityLow, func(_ context.Context, _ ConfigChangedEvent) {
		order = append(order, "low")
	})
	bus.Subscribe(PriorityNormal, func(_ context.Context, _ ConfigChangedEvent) {
		order = append(order, "normal-1")
	})
	bus.Subscribe(PriorityHigh, func(_ context.Context, _ ConfigChangedEvent) {
		order = append(order, "high-panic")
		panic("boom in subscriber")
	})
	bus.Subscribe(PriorityCritical, func(_ context.Context, _ ConfigChangedEvent) {
		order = append(order, "critical")
	})
	bus.Subscribe(PriorityNormal, func(_ context.Context, _ ConfigChangedEvent) {
		order = append(order, "normal-2")
	})

	ev := ConfigChangedEvent{
		Config:        map[string]any{"api_key": "sk-super-secret"},
		Version:       1,
		ChangedFields: []string{"api_key"},
		Timestamp:     time.Now(),
	}
	if strings.Contains(ev.String(), "sk-super-secret") {
		t.Fatalf("ConfigChangedEvent.String() 不得暴露密钥: %s", ev.String())
	}

	bus.Publish(context.Background(), ev)
	want := []string{"critical", "high-panic", "normal-1", "normal-2", "low"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("EventBus 优先级执行顺序不符: got=%v want=%v", order, want)
	}

	// 取消 low 订阅后再次发布。
	order = nil
	unsubLow()
	unsubLow() // 幂等调用安全
	bus.Publish(context.Background(), ev)
	wantAfter := []string{"critical", "high-panic", "normal-1", "normal-2"}
	if !reflect.DeepEqual(order, wantAfter) {
		t.Fatalf("取消订阅后执行顺序不符: got=%v want=%v", order, wantAfter)
	}
}

func TestPromptsAndFileWatcherGoroutine(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"language":"zh-CN"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := New("", cfgPath)
	if err := cfg.Init(); err != nil {
		t.Fatal(err)
	}

	// 1. 三类提示词默认读取（自动建文件）、自定义保存与重置。
	for _, kind := range []PromptType{PromptSummary, PromptPoll, PromptQA} {
		isDef, err := cfg.IsDefaultPrompt(kind)
		if err != nil || !isDef {
			t.Fatalf("%s 初始应为默认提示词: isDef=%v err=%v", kind, isDef, err)
		}
		defText, _ := DefaultPrompt(kind)
		got, err := cfg.GetPrompt(kind)
		if err != nil || got != defText {
			t.Fatalf("%s GetPrompt 默认值不符: got=%q want=%q err=%v", kind, got, defText, err)
		}

		if err := cfg.SavePrompt(kind, "自定义内容-"+string(kind)); err != nil {
			t.Fatalf("%s SavePrompt 失败: %v", kind, err)
		}
		isDef, _ = cfg.IsDefaultPrompt(kind)
		if isDef {
			t.Fatalf("%s 修改后不应判定为默认", kind)
		}
		got, _ = cfg.GetPrompt(kind)
		if got != "自定义内容-"+string(kind) {
			t.Fatalf("%s 修改后读取不符: %q", kind, got)
		}

		if err := cfg.ResetPrompt(kind); err != nil {
			t.Fatalf("%s ResetPrompt 失败: %v", kind, err)
		}
		isDef, _ = cfg.IsDefaultPrompt(kind)
		if !isDef {
			t.Fatalf("%s 重置后应恢复默认", kind)
		}
	}
	if _, err := cfg.GetPrompt("invalid_kind"); !errors.Is(err, ErrInvalidPromptType) {
		t.Fatalf("未知 PromptType 应返回 ErrInvalidPromptType: %v", err)
	}

	// 2. 启动后台 FileWatcher 监听外部 config.json 与提示词文件修改。
	cfgCh := make(chan ConfigChangedEvent, 4)
	promptCh := make(chan PromptChangedEvent, 4)

	cfg.Subscribe(PriorityHigh, func(_ context.Context, ev ConfigChangedEvent) {
		cfgCh <- ev
	})
	cfg.EventBus().SubscribePrompt(PriorityHigh, func(_ context.Context, ev PromptChangedEvent) {
		promptCh <- ev
	})

	watchCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher := cfg.NewFileWatcher()
	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.Run(watchCtx, 20*time.Millisecond)
	}()

	// 稍等基线建立后，外部原子替换 config.json 与写入 poll_prompt.txt。
	time.Sleep(40 * time.Millisecond)
	if err := atomicWriteFile(cfgPath, []byte("{\n  \"language\": \"en-US\",\n  \"enable_auto_poll\": true\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-cfgCh:
		if ev.Config["language"] != "en-US" || !ev.HasChanged("language") {
			t.Fatalf("FileWatcher 捕获的 ConfigChangedEvent 不符: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待 FileWatcher 发布 ConfigChangedEvent 超时")
	}

	pollPath, _ := cfg.PromptFilePath(PromptPoll)
	if err := atomicWriteFile(pollPath, []byte("外部热更新的投票提示词 {summary_text}"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case pev := <-promptCh:
		if pev.PromptType != PromptPoll || pev.Content != "外部热更新的投票提示词 {summary_text}" {
			t.Fatalf("FileWatcher 捕获的 PromptChangedEvent 不符: %+v", pev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待 FileWatcher 发布 PromptChangedEvent 超时")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("FileWatcher 未在 ctx 取消后及时退出")
	}
}

func TestAIProfilesAndProvidersWebUIHelpersAndPythonRevisionParity(t *testing.T) {
	// 1. 验证 Python `json.dumps(content, sort_keys=True)` SHA-256 逐字节精确对齐！
	// 空配置下 Python: json.dumps({"ai_profiles": {}, "ai_routing": {}, "ai_vision": {}}, sort_keys=True)
	const pyEmptyProfilesJSON = `{"ai_profiles": {}, "ai_routing": {}, "ai_vision": {}}`
	wantEmptyProfSum := sha256.Sum256([]byte(pyEmptyProfilesJSON))
	if got := ProfilesRevision(map[string]any{}); got != hex.EncodeToString(wantEmptyProfSum[:]) {
		t.Fatalf("空配置 ProfilesRevision 与 Python 不一致:\n got=%s\nwant=%s", got, hex.EncodeToString(wantEmptyProfSum[:]))
	}

	// 含中文 Unicode 的 v2 providers_revision 对齐测试：中文 "默认" → \u9ed8\u8ba4
	unicodeDoc := map[string]any{
		"ai_config_version": 2,
		"ai_providers": map[string]any{
			"p1": map[string]any{"name": "默认"},
		},
		"model": "deepseek-chat",
	}
	const pyUnicodeProvidersJSON = `{"ai_config_version": 2, "ai_profiles": null, "ai_providers": {"p1": {"name": "\u9ed8\u8ba4"}}, "ai_routing": null, "ai_vision": null, "api_key": null, "base_url": null, "model": "deepseek-chat"}`
	wantUnicodeSum := sha256.Sum256([]byte(pyUnicodeProvidersJSON))
	if got := ProvidersRevision(unicodeDoc); got != hex.EncodeToString(wantUnicodeSum[:]) {
		t.Fatalf("含中文 ProvidersRevision 与 Python 不一致:\n got=%s\nwant=%s", got, hex.EncodeToString(wantUnicodeSum[:]))
	}

	// 2. v1 PublicAIProfiles 读取脱敏、乐观锁冲突、省略 api_key 自动保留与保存。
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	initData := profileTestConfig()
	initData["ai_routing"].(map[string]any)["poll"] = map[string]any{"profile_id": "", "model": "legacy-poll-model"}
	rawInit, _ := json.Marshal(initData)
	if err := os.WriteFile(cfgPath, rawInit, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := New("", cfgPath)
	if err := cfg.Init(); err != nil {
		t.Fatal(err)
	}

	v1View, err := cfg.PublicAIProfiles()
	if err != nil {
		t.Fatalf("PublicAIProfiles 失败: %v", err)
	}
	if !v1View.Profiles["primary"].APIKeySet || v1View.Profiles["primary"].APIKeyPreview != "********" {
		t.Fatalf("v1 APIKey 脱敏预览不符: %+v", v1View.Profiles["primary"])
	}
	if v1View.Profiles["local"].APIKeySet || v1View.Profiles["local"].APIKeyPreview != "" {
		t.Fatalf("空密钥档案脱敏不符: %+v", v1View.Profiles["local"])
	}

	// 错误 revision 触发 ErrConfigConflict (409)
	_, err = cfg.SaveAIProfiles(map[string]any{
		"profiles": map[string]any{},
		"routing":  map[string]any{},
		"vision":   map[string]any{},
		"revision": "stale-revision",
	})
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("过期 revision 应返回 ErrConfigConflict，实际 %v", err)
	}

	// 合法 v1 保存：不传 primary.api_key，必须自动保留磁盘中的 "primary-key"！
	savedV1, err := cfg.SaveAIProfiles(map[string]any{
		"profiles": map[string]any{
			"primary": map[string]any{
				"name":            "Primary Updated",
				"provider":        "openai",
				"base_url":        "https://primary.example/v1",
				"model":           "primary-v2",
				"supports_vision": true,
			},
			"local": map[string]any{
				"name":     "Local",
				"base_url": "http://localhost:8000/v1",
				"model":    "local-model",
			},
		},
		"routing": map[string]any{
			"assistant": map[string]any{"profile_id": "primary"},
			"poll":      map[string]any{"profile_id": "", "model": "legacy-poll-model"},
		},
		"vision":   map[string]any{"enabled": true, "ocr": true, "description": false},
		"revision": v1View.Revision,
	})
	if err != nil {
		t.Fatalf("SaveAIProfiles 失败: %v", err)
	}
	if savedV1.Revision == v1View.Revision {
		t.Fatal("保存后 revision 应更新")
	}
	if got := cfg.EffectiveAIConfig("assistant"); got.APIKey != "primary-key" || got.Model != "primary-v2" {
		t.Fatalf("v1 保存后密钥保留或模型更新失败: %+v", got)
	}

	// ResolveProfileConnection 草稿覆盖 base_url 但沿用已保存 api_key
	bURL, bKey, bModel, err := cfg.ResolveProfileConnection("primary", map[string]any{"base_url": "https://draft.example/v1"})
	if err != nil || bURL != "https://draft.example/v1" || bKey != "primary-key" || bModel != "primary-v2" {
		t.Fatalf("ResolveProfileConnection 不符: url=%s key=%s model=%s err=%v", bURL, bKey, bModel, err)
	}
	if _, _, _, err := cfg.ResolveProfileConnection("missing", nil); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("不存在档案应返回 ErrProfileNotFound: %v", err)
	}

	// 3. v1 → v2 只读迁移（包含 legacy_default 合成厂商）与 SaveAIProviders 正式提交升级。
	v2Migrated, err := cfg.PublicAIProviders()
	if err != nil {
		t.Fatalf("PublicAIProviders 迁移视图失败: %v", err)
	}
	if v2Migrated.Version != 2 {
		t.Fatalf("迁移视图版本应为 2，实际 %d", v2Migrated.Version)
	}
	legDef, hasLegDef := v2Migrated.Providers["legacy_default"]
	if !hasLegDef || !legDef.APIKeySet || legDef.APIKeyPreview != "********" {
		t.Fatalf("未绑定档案但覆写模型的 poll 路由应迁移出 legacy_default 厂商: %+v", v2Migrated.Providers)
	}
	if v2Migrated.Effective["poll"].ProviderID != "legacy_default" || v2Migrated.Effective["poll"].ModelID != "legacy-poll-model" {
		t.Fatalf("v2 迁移后 poll effective 不符: %+v", v2Migrated.Effective["poll"])
	}

	// 提交 v2 保存（同样省略 api_key，验证从 v1→v2 迁移基线恢复密钥）
	savedV2, err := cfg.SaveAIProviders(map[string]any{
		"version": 2,
		"providers": map[string]any{
			"primary": map[string]any{
				"name":     "Primary V2 Provider",
				"provider": "openai",
				"base_url": "https://primary.example/v1",
				"models": map[string]any{
					"primary-v2": map[string]any{"name": "Main", "supports_vision": true},
					"fast-chat":  map[string]any{"name": "Fast", "supports_vision": false},
				},
			},
		},
		"routing": map[string]any{
			"assistant": map[string]any{"provider_id": "primary", "model_id": "primary-v2"},
			"summary":   map[string]any{"provider_id": "primary", "model_id": "fast-chat"},
		},
		"vision":   map[string]any{"enabled": true, "ocr": true, "description": true},
		"revision": v2Migrated.Revision,
	})
	if err != nil {
		t.Fatalf("SaveAIProviders 失败: %v", err)
	}
	if savedV2.Effective["summary"].ModelID != "fast-chat" {
		t.Fatalf("v2 summary 路由未生效: %+v", savedV2.Effective["summary"])
	}
	if got := cfg.EffectiveAIConfig("summary"); got.APIKey != "primary-key" || got.Model != "fast-chat" || got.ProfileID != "primary" {
		t.Fatalf("v2 保存后运行时解析不符: %+v", got)
	}

	// 升级到 v2 后再调用 v1 SaveAIProfiles 必须返回 ErrConfigConflict
	if _, err := cfg.SaveAIProfiles(map[string]any{
		"profiles": map[string]any{},
		"routing":  map[string]any{},
		"vision":   map[string]any{},
		"revision": ProfilesRevision(cfg.RawSnapshot()),
	}); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("v2 下调用 SaveAIProfiles 应返回 ErrConfigConflict，实际 %v", err)
	}

	// ResolveProviderConnection 支持 clear_api_key: true
	_, clearedKey, err := cfg.ResolveProviderConnection("primary", map[string]any{"clear_api_key": true})
	if err != nil || clearedKey != "" {
		t.Fatalf("clear_api_key=true 应清空测试密钥: key=%q err=%v", clearedKey, err)
	}
	_, keptKey, err := cfg.ResolveProviderConnection("primary", nil)
	if err != nil || keptKey != "primary-key" {
		t.Fatalf("默认应保留厂商密钥: key=%q err=%v", keptKey, err)
	}

	// LegacyAIConfig 脱敏预览
	if got := MaskLegacyAPIKeyPreview("sk-1234567890abcd"); got != "sk-1...abcd" {
		t.Fatalf("MaskLegacyAPIKeyPreview 长密钥不符: %q", got)
	}
	if got := MaskLegacyAPIKeyPreview("short"); got != "***" {
		t.Fatalf("MaskLegacyAPIKeyPreview 短密钥不符: %q", got)
	}
	legView, err := cfg.LegacyAIConfig()
	if err != nil || !legView.APIKeySet || legView.APIKeyPreview != "lega...-key" {
		t.Fatalf("LegacyAIConfig 不符: %+v err=%v", fmt.Sprintf("%+v", legView), err)
	}
}
