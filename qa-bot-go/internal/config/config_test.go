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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestConfig 构建注入式 Config：envMap 为环境快照，configJSON 为
// data/config.json 内容（空串表示文件不存在），dataDir 为 persona 等数据目录。
func newTestConfig(t *testing.T, envMap map[string]string, configJSON, dataDir string) *Config {
	t.Helper()
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	if configJSON != "" {
		if err := os.WriteFile(jsonPath, []byte(configJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Config{
		Env:     newEnvFromMap(envMap),
		runtime: NewRuntimeJSON(jsonPath),
		dataDir: dataDir,
	}
}

func TestParseDotEnv(t *testing.T) {
	content := "" +
		"# 整行注释\n" +
		"\n" +
		"QA_BOT_TOKEN=123:abc\n" +
		"  SPACED = value  \n" +
		"QUOTED=\"double quoted\"\n" +
		"SINGLE='single quoted'\n" +
		"export TOKEN=x\n" +
		"EQ=a=b=c\n" +
		"EMPTY=\n" +
		"BAD LINE WITHOUT EQUALS\n" +
		"=NOKEY\n"
	got := ParseDotEnv(content)
	want := map[string]string{
		"QA_BOT_TOKEN": "123:abc",
		"SPACED":       "value",
		"QUOTED":       "double quoted",
		"SINGLE":       "single quoted",
		"TOKEN":        "x",
		"EQ":           "a=b=c",
		"EMPTY":        "",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("键 %s = %q，期望 %q", k, got[k], v)
		}
	}
	// 坏行不产生键。
	for _, bad := range []string{"BAD LINE WITHOUT EQUALS", "EXPORT"} {
		if _, ok := got[bad]; ok {
			t.Fatalf("坏行不应产生键: %s", bad)
		}
	}
	if _, ok := got["NOKEY"]; ok {
		t.Fatal("空键不应收录")
	}
}

func TestNewEnvDotEnvOverridesProcess(t *testing.T) {
	t.Setenv("SAKURA_TEST_FROM_PROC", "proc")
	t.Setenv("SAKURA_TEST_BOTH", "proc")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "SAKURA_TEST_BOTH=file\nSAKURA_TEST_FROM_FILE=fileonly\n"
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	env := NewEnv(envPath)

	if v, _ := env.Lookup("SAKURA_TEST_FROM_PROC"); v != "proc" {
		t.Fatalf("进程环境键丢失: %q", v)
	}
	if v, _ := env.Lookup("SAKURA_TEST_BOTH"); v != "file" {
		t.Fatalf(".env 应覆盖进程环境，实际 %q", v)
	}
	if v, _ := env.Lookup("SAKURA_TEST_FROM_FILE"); v != "fileonly" {
		t.Fatalf(".env 新键未收录: %q", v)
	}

	// .env 缺失 → 仅进程环境，不报错。
	env2 := NewEnv(filepath.Join(dir, "not-exists.env"))
	if v, _ := env2.Lookup("SAKURA_TEST_FROM_PROC"); v != "proc" {
		t.Fatal(".env 缺失时应退回进程环境")
	}
}

func TestLookupPresenceSemantics(t *testing.T) {
	env := newEnvFromMap(map[string]string{"EMPTY_KEY": ""})
	if v, ok := env.Lookup("EMPTY_KEY"); !ok || v != "" {
		t.Fatalf("存在但为空应返回 (\"\", true)，实际 (%q, %v)", v, ok)
	}
	if _, ok := env.Lookup("MISSING"); ok {
		t.Fatal("缺失键 ok 应为 false")
	}
	// os.getenv("K", "def") 语义：存在但为空 → 空串而非默认值。
	if v := env.GetDefault("EMPTY_KEY", "def"); v != "" {
		t.Fatalf("GetDefault 对存在但为空的键应返回空串，实际 %q", v)
	}
}

func TestGetIntDef(t *testing.T) {
	env := newEnvFromMap(map[string]string{
		"N1": "42",
		"N2": " 7 ",
		"N3": "abc",
		"N4": "3.0",
	})
	cases := []struct {
		key  string
		want int64
	}{
		{"N1", 42},
		{"N2", 7},
		{"N3", 9}, // 非法 → 默认
		{"N4", 9}, // 小数 → 默认
		{"N5", 9}, // 缺失 → 默认
	}
	for _, c := range cases {
		if got := env.GetIntDef(c.key, 9); got != c.want {
			t.Fatalf("GetIntDef(%s) = %d，期望 %d", c.key, got, c.want)
		}
	}
}

func TestQABotEnabled(t *testing.T) {
	cases := []struct {
		raw  string
		have bool
		want bool
	}{
		{raw: "", have: false, want: true}, // 缺省 True
		{raw: "True", have: true, want: true}, {raw: "true", have: true, want: true},
		{raw: "False", have: true, want: false}, {raw: "false", have: true, want: false},
		{raw: "", have: true, want: false}, // 已设置但为空 → 禁用（qa_bot.py:94）
		{raw: "1", have: true, want: false},
	}
	for _, c := range cases {
		values := map[string]string{}
		if c.have {
			values["QA_BOT_ENABLED"] = c.raw
		}
		cfg := newTestConfig(t, values, "", t.TempDir())
		if got := cfg.QABotEnabled(); got != c.want {
			t.Fatalf("QA_BOT_ENABLED=%q(存在=%v) → %v，期望 %v", c.raw, c.have, got, c.want)
		}
	}
}

func TestEffectiveQuotaDefaults(t *testing.T) {
	cfg := newTestConfig(t, nil, "", t.TempDir())
	limits, err := cfg.EffectiveQuota()
	if err != nil {
		t.Fatalf("默认配额不应报错: %v", err)
	}
	if limits.UserDaily != 3 || limits.TotalDaily != 200 {
		t.Fatalf("默认配额应为 3/200，实际 %+v", limits)
	}
}

func TestEffectiveQuotaEnvClamp(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		wantUser  int64
		wantTotal int64
	}{
		{"用户下限", map[string]string{"QA_BOT_USER_LIMIT": "-5"}, 1, 200},
		{"用户上限", map[string]string{"QA_BOT_USER_LIMIT": "99999999999"}, 2147483647, 200},
		{"总数下限", map[string]string{"QA_BOT_DAILY_LIMIT": "0"}, 3, 10},
		{"总数上限", map[string]string{"QA_BOT_DAILY_LIMIT": "3000000000"}, 3, 2147483647},
		{"合法覆盖", map[string]string{"QA_BOT_USER_LIMIT": "7", "QA_BOT_DAILY_LIMIT": "500"}, 7, 500},
		{"非法回退", map[string]string{"QA_BOT_USER_LIMIT": "abc", "QA_BOT_DAILY_LIMIT": "2.5"}, 3, 200},
		{"边界恰等", map[string]string{"QA_BOT_USER_LIMIT": "1", "QA_BOT_DAILY_LIMIT": "10"}, 1, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := newTestConfig(t, c.env, "", t.TempDir())
			limits, err := cfg.EffectiveQuota()
			if err != nil {
				t.Fatalf("env clamp 不应报错: %v", err)
			}
			if limits.UserDaily != c.wantUser || limits.TotalDaily != c.wantTotal {
				t.Fatalf("配额 = %+v，期望 %d/%d", limits, c.wantUser, c.wantTotal)
			}
		})
	}
}

func TestEffectiveQuotaConfigOverrideAndValidate(t *testing.T) {
	cases := []struct {
		name       string
		configJSON string
		wantErr    bool
		wantUser   int64
		wantTotal  int64
	}{
		{name: "合法覆盖", configJSON: `{"qa_bot_quota": {"user_daily_limit": 9, "total_daily_limit": 90}}`,
			wantErr: false, wantUser: 9, wantTotal: 90},
		{name: "部分覆盖", configJSON: `{"qa_bot_quota": {"user_daily_limit": 9}}`,
			wantErr: false, wantUser: 9, wantTotal: 200},
		{name: "用户越界 0", configJSON: `{"qa_bot_quota": {"user_daily_limit": 0}}`, wantErr: true},
		{name: "用户越界超 int32", configJSON: `{"qa_bot_quota": {"user_daily_limit": 2147483648}}`, wantErr: true},
		{name: "总数越界 9", configJSON: `{"qa_bot_quota": {"total_daily_limit": 9}}`, wantErr: true},
		{name: "小数拒绝", configJSON: `{"qa_bot_quota": {"user_daily_limit": 3.0}}`, wantErr: true},
		{name: "布尔拒绝", configJSON: `{"qa_bot_quota": {"user_daily_limit": true}}`, wantErr: true},
		{name: "字符串拒绝", configJSON: `{"qa_bot_quota": {"user_daily_limit": "5"}}`, wantErr: true},
		{name: "未知字段拒绝", configJSON: `{"qa_bot_quota": {"user_daily_limit": 3, "extra": 1}}`, wantErr: true},
		{name: "非对象拒绝", configJSON: `{"qa_bot_quota": "x"}`, wantErr: true},
		{name: "其他键不影响", configJSON: `{"language": "en-US"}`, wantErr: false, wantUser: 3, wantTotal: 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := newTestConfig(t, nil, c.configJSON, t.TempDir())
			limits, err := cfg.EffectiveQuota()
			if c.wantErr {
				if err == nil {
					t.Fatalf("config.json 越界应报错（配额检查整体失败），实际 %+v", limits)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if limits.UserDaily != c.wantUser || limits.TotalDaily != c.wantTotal {
				t.Fatalf("配额 = %+v，期望 %d/%d", limits, c.wantUser, c.wantTotal)
			}
		})
	}
}

func TestEffectiveAIConfig(t *testing.T) {
	t.Run("env 默认", func(t *testing.T) {
		cfg := newTestConfig(t, nil, "", t.TempDir())
		ai := cfg.EffectiveAIConfig()
		if ai.APIKey != "" || ai.BaseURL != "https://api.deepseek.com" || ai.Model != "deepseek-chat" {
			t.Fatalf("默认 AI 配置不符: %+v", ai)
		}
	})
	t.Run("env 覆盖与 DEEPSEEK 兜底", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{
			"LLM_API_KEY":      "", // 为空 → 回退 DEEPSEEK_API_KEY
			"DEEPSEEK_API_KEY": "sk-deepseek",
			"LLM_BASE_URL":     "https://llm.example.com",
			"LLM_MODEL":        "my-model",
		}, "", t.TempDir())
		ai := cfg.EffectiveAIConfig()
		if ai.APIKey != "sk-deepseek" || ai.BaseURL != "https://llm.example.com" || ai.Model != "my-model" {
			t.Fatalf("env AI 配置不符: %+v", ai)
		}
	})
	t.Run("config.json 优先", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{
			"LLM_API_KEY":  "sk-env",
			"LLM_BASE_URL": "https://env.example.com",
			"LLM_MODEL":    "env-model",
		}, `{"api_key": "sk-json", "base_url": "https://json.example.com", "model": "json-model"}`, t.TempDir())
		ai := cfg.EffectiveAIConfig()
		if ai.APIKey != "sk-json" || ai.BaseURL != "https://json.example.com" || ai.Model != "json-model" {
			t.Fatalf("config.json AI 配置不符: %+v", ai)
		}
	})
	t.Run("config.json 空串回退 env", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{
			"LLM_API_KEY": "sk-env",
			"LLM_MODEL":   "env-model",
		}, `{"api_key": "", "model": ""}`, t.TempDir())
		ai := cfg.EffectiveAIConfig()
		if ai.APIKey != "sk-env" || ai.Model != "env-model" {
			t.Fatalf("空串应回退 env: %+v", ai)
		}
	})
	t.Run("非字符串字段忽略", func(t *testing.T) {
		cfg := newTestConfig(t, nil, `{"api_key": 42}`, t.TempDir())
		if ai := cfg.EffectiveAIConfig(); ai.APIKey != "" {
			t.Fatalf("非字符串 api_key 应忽略: %+v", ai)
		}
	})
}

func TestNormalizeChannelID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"jffnekjdnfn", "https://t.me/jffnekjdnfn"},
		{"@jffnekjdnfn", "https://t.me/jffnekjdnfn"},
		{"https://t.me/jffnekjdnfn", "https://t.me/jffnekjdnfn"},
		{"https://t.me/jffnekjdnfn/", "https://t.me/jffnekjdnfn"},
		{"https://t.me/jffnekjdnfn//", "https://t.me/jffnekjdnfn"},
		{"t.me/jffnekjdnfn", "https://t.me/jffnekjdnfn"},
		{"https://www.t.me/jffnekjdnfn", "https://t.me/jffnekjdnfn"},
		{"http://www.t.me/jffnekjdnfn", "https://t.me/jffnekjdnfn"},
		{"https://telegram.me/jffnekjdnfn", "https://t.me/jffnekjdnfn"},
		{"www.telegram.me/jffnekjdnfn", "https://t.me/jffnekjdnfn"},
		{"  spaced  ", "https://t.me/spaced"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeChannelID(c.in); got != c.want {
			t.Fatalf("NormalizeChannelID(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestChannels(t *testing.T) {
	t.Run("env 拆分", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{"TARGET_CHANNEL": "chan_a, @ChanB, https://t.me/chan_c"}, "", t.TempDir())
		got := cfg.Channels()
		want := []string{"https://t.me/chan_a", "https://t.me/ChanB", "https://t.me/chan_c"}
		if len(got) != len(want) {
			t.Fatalf("频道数不符: %v", got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("频道[%d] = %s，期望 %s", i, got[i], want[i])
			}
		}
	})
	t.Run("config.json 覆盖 env", func(t *testing.T) {
		cfg := newTestConfig(t,
			map[string]string{"TARGET_CHANNEL": "env_chan"},
			`{"channels": ["json_chan", "@json2"]}`,
			t.TempDir())
		got := cfg.Channels()
		if len(got) != 2 || got[0] != "https://t.me/json_chan" || got[1] != "https://t.me/json2" {
			t.Fatalf("config.json 频道覆盖不符: %v", got)
		}
	})
	t.Run("config.json 空数组仍生效", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{"TARGET_CHANNEL": "env_chan"}, `{"channels": []}`, t.TempDir())
		if got := cfg.Channels(); len(got) != 0 {
			t.Fatalf("空数组应覆盖为空: %v", got)
		}
	})
	t.Run("config.json 类型不符回退 env", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{"TARGET_CHANNEL": "env_chan"}, `{"channels": "not-a-list"}`, t.TempDir())
		if got := cfg.Channels(); len(got) != 1 || got[0] != "https://t.me/env_chan" {
			t.Fatalf("应回退 env: %v", got)
		}
	})
	t.Run("未配置", func(t *testing.T) {
		cfg := newTestConfig(t, nil, "", t.TempDir())
		if got := cfg.Channels(); got != nil {
			t.Fatalf("未配置应为 nil: %v", got)
		}
	})
}

func TestPersonaFourLevels(t *testing.T) {
	t.Run("config.json 优先", func(t *testing.T) {
		dir := t.TempDir()
		cfg := newTestConfig(t, map[string]string{"QA_BOT_PERSONA": "env人格"}, `{"qa_bot_persona": "json人格"}`, dir)
		if got := cfg.Persona(); got != "json人格" {
			t.Fatalf("config.json 人格应最高优先: %q", got)
		}
	})
	t.Run("env 纯文本", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{"QA_BOT_PERSONA": "env人格"}, "", t.TempDir())
		if got := cfg.Persona(); got != "env人格" {
			t.Fatalf("env 人格不符: %q", got)
		}
	})
	t.Run("env 路径存在则读文件并 strip", func(t *testing.T) {
		dir := t.TempDir()
		personaFile := filepath.Join(dir, "persona.txt")
		if err := os.WriteFile(personaFile, []byte("  文件人格\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := newTestConfig(t, map[string]string{"QA_BOT_PERSONA": personaFile}, "", t.TempDir())
		if got := cfg.Persona(); got != "文件人格" {
			t.Fatalf("env 路径人格不符: %q", got)
		}
	})
	t.Run("env 路径不存在返回 env 原文", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{"QA_BOT_PERSONA": filepath.Join(t.TempDir(), "no.txt")}, "", t.TempDir())
		envValue := filepath.Join(cfg.Env.Get("QA_BOT_PERSONA"))
		if got := cfg.Persona(); got != envValue {
			t.Fatalf("读文件失败应返回 env 原文: %q", got)
		}
	})
	t.Run("文件存在取文件内容", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "qa_persona.txt"), []byte("\n文件人格2\n\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := newTestConfig(t, nil, "", dir)
		if got := cfg.Persona(); got != "文件人格2" {
			t.Fatalf("文件人格不符: %q", got)
		}
	})
	t.Run("文件不存在自动创建默认人格", func(t *testing.T) {
		dir := t.TempDir()
		cfg := newTestConfig(t, nil, "", dir)
		got := cfg.Persona()
		if got != DefaultQAPersona() {
			t.Fatalf("应返回默认人格: %q", got)
		}
		b, err := os.ReadFile(filepath.Join(dir, "qa_persona.txt"))
		if err != nil {
			t.Fatalf("应自动创建 qa_persona.txt: %v", err)
		}
		if string(b) != DefaultQAPersona() {
			t.Fatalf("自动创建的文件内容应为默认人格原样: %q", string(b))
		}
		if strings.HasSuffix(string(b), "\n") {
			t.Fatal("默认人格结尾不应有换行（对齐 Python 三引号常量）")
		}
	})
	t.Run("默认人格包含关键句", func(t *testing.T) {
		p := DefaultQAPersona()
		for _, frag := range []string{"你是一个友好、专业的通用智能助手。", "【角色特点】", "不要编造"} {
			if !strings.Contains(p, frag) {
				t.Fatalf("默认人格缺少片段: %q", frag)
			}
		}
	})
}

func TestIsAdmin(t *testing.T) {
	cfg := newTestConfig(t, map[string]string{"REPORT_ADMIN_IDS": " 111, abc , 222,,"}, "", t.TempDir())
	if !cfg.IsAdmin(111) || !cfg.IsAdmin(222) {
		t.Fatal("已配置 ID 应判定为管理员")
	}
	if cfg.IsAdmin(333) {
		t.Fatal("未配置 ID 不应是管理员")
	}
	empty := newTestConfig(t, nil, "", t.TempDir())
	if empty.IsAdmin(111) {
		t.Fatal("未配置 REPORT_ADMIN_IDS 必须 fail-closed")
	}
	invalid := newTestConfig(t, map[string]string{"REPORT_ADMIN_IDS": "me, *"}, "", t.TempDir())
	if invalid.IsAdmin(0) {
		t.Fatal("全非法配置应 fail-closed")
	}
}

func TestLanguage(t *testing.T) {
	t.Run("config.json 优先", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{"LANGUAGE": "en-US"}, `{"language": "en-US"}`, t.TempDir())
		if got := cfg.Language(); got != "en-US" {
			t.Fatalf("语言不符: %s", got)
		}
	})
	t.Run("env 次之", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{"LANGUAGE": "en-US"}, "", t.TempDir())
		if got := cfg.Language(); got != "en-US" {
			t.Fatalf("语言不符: %s", got)
		}
	})
	t.Run("默认 zh-CN", func(t *testing.T) {
		cfg := newTestConfig(t, map[string]string{"LANGUAGE": ""}, "", t.TempDir())
		if got := cfg.Language(); got != "zh-CN" {
			t.Fatalf("默认语言不符: %s", got)
		}
	})
}

func TestRuntimeJSONHotRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	r := NewRuntimeJSON(path)

	// 未加载且文件缺失 → 空 map 不报错（启动失败由 Init 报告）。
	if got := r.Read(); len(got) != 0 {
		t.Fatalf("缺失文件应返回空 map: %v", got)
	}
	if err := r.Init(); err == nil {
		t.Fatal("Init 对缺失文件应报错")
	}

	if err := os.WriteFile(path, []byte(`{"language": "en-US"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r2 := NewRuntimeJSON(path)
	if err := r2.Init(); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	if got := r2.Read(); got["language"] != "en-US" {
		t.Fatalf("Init 后读取不符: %v", got)
	}

	// 修改文件（尺寸变化 → stat 版本变化）→ 新值。
	if err := os.WriteFile(path, []byte(`{"language": "zh-CN", "extra": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := r2.Read()
	if got["language"] != "zh-CN" {
		t.Fatalf("热读未生效: %v", got)
	}

	// 文件临时删除 → 保留旧值。
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got = r2.Read()
	if got["language"] != "zh-CN" {
		t.Fatalf("临时缺失应保留旧值: %v", got)
	}

	// 重写为非法 JSON → 保留旧值（版本号照常推进）。
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = r2.Read()
	if got["language"] != "zh-CN" {
		t.Fatalf("解析失败应保留旧值: %v", got)
	}

	// 修复为合法 JSON → 新值。
	if err := os.WriteFile(path, []byte(`{"language": "en-US"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got = r2.Read(); got["language"] != "en-US" {
		t.Fatalf("修复后应读新值: %v", got)
	}

	// Read 返回的是拷贝：调用方修改不得影响缓存。
	got["language"] = "hacked"
	if again := r2.Read(); again["language"] != "en-US" {
		t.Fatalf("缓存被外部修改污染: %v", again)
	}
}

func TestDefaultSingleton(t *testing.T) {
	a := Default()
	b := Default()
	if a != b {
		t.Fatal("Default 应返回同一实例")
	}
	if a.Env == nil || a.runtime == nil {
		t.Fatal("Default 实例字段未初始化")
	}
	if a.dataDir != DataDir {
		t.Fatalf("默认数据目录应为 data，实际 %s", a.dataDir)
	}
}
