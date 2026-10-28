// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package logging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mapEnv 测试用环境实现。
type mapEnv map[string]string

func (m mapEnv) Lookup(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

func TestDefaultOptions(t *testing.T) {
	o := DefaultOptions()
	if o.Component != "qa" || o.ComponentLogFile != "qa-bot.log" {
		t.Fatalf("组件默认值不符: %+v", o)
	}
	if o.LevelName != "DEBUG" || !o.LogToFile || o.MaxSizeBytes != 10485760 || o.BackupCount != 5 {
		t.Fatalf("日志默认值不符: %+v", o)
	}
	if !o.LogToConsole || !o.Colorize {
		t.Fatalf("控制台默认值不符: %+v", o)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	env := mapEnv{
		"SAKURA_LOG_COMPONENT":      "qa2",
		"SAKURA_COMPONENT_LOG_FILE": "custom.log",
		"LOG_LEVEL":                 "warning", // 应转大写生效
		"LOG_TO_FILE":               "False",
		"LOG_FILE_PATH":             "var/log/sakura-bot.log",
		"LOG_FILE_MAX_SIZE":         "2048",
		"LOG_FILE_BACKUP_COUNT":     "3",
		"LOG_TO_CONSOLE":            "0",
		"LOG_COLORIZE":              "off",
	}
	o := SetupOptionsFromEnv(env)
	if o.Component != "qa2" || o.ComponentLogFile != "custom.log" {
		t.Fatalf("组件 env 覆盖失败: %+v", o)
	}
	if o.LevelName != "WARNING" || o.LogToFile || o.BaseLogFilePath != "var/log/sakura-bot.log" {
		t.Fatalf("文件 env 覆盖失败: %+v", o)
	}
	if o.MaxSizeBytes != 2048 || o.BackupCount != 3 || o.LogToConsole || o.Colorize {
		t.Fatalf("数值/开关 env 覆盖失败: %+v", o)
	}
}

func TestLoadEnvInvalidFallsBack(t *testing.T) {
	o := SetupOptionsFromEnv(mapEnv{
		"LOG_LEVEL":             "not-a-level",
		"LOG_FILE_MAX_SIZE":     "abc",
		"LOG_FILE_BACKUP_COUNT": "99", // 超出 0-20 → 回落默认
	})
	if o.LevelName != "DEBUG" {
		t.Fatalf("非法级别应回落 DEBUG: %s", o.LevelName)
	}
	if o.MaxSizeBytes != defaultMaxBytes || o.BackupCount != defaultBackupCount {
		t.Fatalf("非法数值应回落默认: %+v", o)
	}
}

func TestParsePyBool(t *testing.T) {
	for _, s := range []string{"True", "true", "TRUE", "1", "yes", "on"} {
		if v, err := parsePyBool(s); err != nil || !v {
			t.Fatalf("%q 应解析为 true", s)
		}
	}
	for _, s := range []string{"False", "false", "0", "no", "off"} {
		if v, err := parsePyBool(s); err != nil || v {
			t.Fatalf("%q 应解析为 false", s)
		}
	}
	if _, err := parsePyBool("maybe"); err == nil {
		t.Fatal("非法布尔值应报错")
	}
}

func TestComponentLogFilePath(t *testing.T) {
	if got := ComponentLogFilePath("qa-bot.log", "logs/sakura-bot.log"); got != filepath.Join("logs", "qa-bot.log") {
		t.Fatalf("组件路径推导不符: %s", got)
	}
	if got := ComponentLogFilePath("qa-bot.log", ""); got != filepath.Join("logs", "qa-bot.log") {
		t.Fatalf("无主路径时应回 logs/: %s", got)
	}
	if got := ComponentLogFilePath("qa-bot.log", "var/log/main/sakura-bot.log"); got != filepath.Join("var", "log", "main", "qa-bot.log") {
		t.Fatalf("嵌套主路径推导不符: %s", got)
	}
}

func TestConsoleLineFormat(t *testing.T) {
	got := ConsoleLine("2026-01-02 03:04:05", "__main__", "qa", "INFO", "hello", false)
	want := "2026-01-02 03:04:05 - __main__ - INFO - [qa] hello\n"
	if got != want {
		t.Fatalf("控制台行不符:\n got %q\nwant %q", got, want)
	}
	// 已带组件前缀的消息不重复加前缀（ComponentFormatter 语义）。
	got = ConsoleLine("2026-01-02 03:04:05", "__main__", "qa", "INFO", "[qa] hello", false)
	want = "2026-01-02 03:04:05 - __main__ - INFO - [qa] hello\n"
	if got != want {
		t.Fatalf("前缀去重不符: %q", got)
	}
	// 着色仅包裹级别名（ColorFormatter.COLORS）。
	got = ConsoleLine("2026-01-02 03:04:05", "__main__", "qa", "INFO", "hello", true)
	want = "2026-01-02 03:04:05 - __main__ - \x1b[32mINFO\x1b[0m - [qa] hello\n"
	if got != want {
		t.Fatalf("着色行不符: %q", got)
	}
}

func TestColorizeLevelAllLevels(t *testing.T) {
	cases := map[string]string{
		"DEBUG":    "\x1b[36mDEBUG\x1b[0m",
		"INFO":     "\x1b[32mINFO\x1b[0m",
		"WARNING":  "\x1b[33mWARNING\x1b[0m",
		"ERROR":    "\x1b[31mERROR\x1b[0m",
		"CRITICAL": "\x1b[35mCRITICAL\x1b[0m",
		"OTHER":    "OTHER",
	}
	for in, want := range cases {
		if got := colorizeLevel(in); got != want {
			t.Fatalf("colorizeLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileLineFormat(t *testing.T) {
	got := FileLine("2026-01-02 03:04:05", "core.handlers.appeal_handler", "WARNING", "appeal_handler.py", 131, "fail-closed")
	want := "2026-01-02 03:04:05 - core.handlers.appeal_handler - WARNING - appeal_handler.py:131 - fail-closed\n"
	if got != want {
		t.Fatalf("文件行不符:\n got %q\nwant %q", got, want)
	}
}

func TestLevelNameAndFromName(t *testing.T) {
	pairs := []struct {
		name  string
		level slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"WARNING", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"CRITICAL", LevelCritical},
	}
	for _, p := range pairs {
		if got := levelFromName(p.name); got != p.level {
			t.Fatalf("levelFromName(%q) = %v, want %v", p.name, got, p.level)
		}
		if got := LevelName(p.level); got != p.name {
			t.Fatalf("LevelName(%v) = %q, want %q", p.level, got, p.name)
		}
	}
}

func TestRotatingWriterRenameChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qa.log")

	w, err := newRotatingWriter(path, 32, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("a", 29) + "\n" // 30 字节 < 32，不触发
	for i := 0; i < 4; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()

	// backup=2：应存在 qa.log、qa.log.1、qa.log.2，且没有 .3。
	for _, name := range []string{"qa.log", "qa.log.1", "qa.log.2"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("轮转后应存在 %s: %v", name, err)
		}
	}
	if fileExists(filepath.Join(dir, "qa.log.3")) {
		t.Fatal("不应存在 qa.log.3")
	}

	// 每个文件恰一行（30 字节 < 32 不会连写两行）。
	for _, name := range []string{"qa.log", "qa.log.1", "qa.log.2"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if len(b) != 30 {
			t.Fatalf("%s 大小应为 30，实际 %d", name, len(b))
		}
	}
}

func TestRotatingWriterNoRotationWhenMaxZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qa.log")
	w, err := newRotatingWriter(path, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := w.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()
	if fileExists(path + ".1") {
		t.Fatal("maxBytes=0 不应轮转")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 50 {
		t.Fatalf("maxBytes=0 应持续追加，实际 %d 字节", len(b))
	}
}

func TestSetupDualWriteAndLevelFilter(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "qa-bot.log")

	logger, path, err := Setup(Options{
		Component:        "qa",
		ComponentLogFile: filepath.Base(logPath),
		BaseLogFilePath:  logPath,
		LevelName:        "INFO",
		LogToFile:        true,
		MaxSizeBytes:     10 * 1024 * 1024,
		BackupCount:      5,
		LogToConsole:     false, // 测试不打印到 stdout
		Colorize:         false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != logPath {
		t.Fatalf("组件路径返回不符: %s", path)
	}
	t.Cleanup(func() {
		if h, ok := logger.Handler().(*Handler); ok {
			_ = h.Close()
		}
	})

	logger.Debug("debug 不应落盘")
	logger.Info("信息一条")

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if strings.Contains(content, "debug") {
		t.Fatalf("DEBUG 级别不应落盘: %q", content)
	}
	if !strings.Contains(content, " - qa_bot - INFO - logging_test.go:") {
		t.Fatalf("文件行格式不符: %q", content)
	}
	if !strings.HasSuffix(strings.TrimRight(content, "\n"), " - 信息一条") {
		t.Fatalf("消息体缺失: %q", content)
	}
}

func TestSetupCriticalLevelName(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "c.log")

	logger, _, err := Setup(Options{
		ComponentLogFile: filepath.Base(logPath),
		BaseLogFilePath:  logPath,
		LevelName:        "CRITICAL",
		LogToFile:        true,
		LogToConsole:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if h, ok := logger.Handler().(*Handler); ok {
			_ = h.Close()
		}
	})
	logger.Info("不应落盘")
	logger.Log(context.Background(), LevelCritical, "致命")
	logger.Error("ERROR 低于 CRITICAL 不应落盘")

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if strings.Contains(content, "不应落盘") || strings.Contains(content, "ERROR") {
		t.Fatalf("级别过滤失败: %q", content)
	}
	if !strings.Contains(content, " - CRITICAL - ") {
		t.Fatalf("CRITICAL 级别名不符: %q", content)
	}
}

func TestHandlerWithNameAndAttrs(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "n.log")

	base, _, err := Setup(Options{
		ComponentLogFile: filepath.Base(logPath),
		BaseLogFilePath:  logPath,
		LevelName:        "DEBUG",
		LogToFile:        true,
		LogToConsole:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 通过 Handler.WithName 取得命名 logger（等价 Python getLogger(name)）。
	h, ok := base.Handler().(*Handler)
	if !ok {
		t.Fatal("Setup 应返回本包 Handler")
	}
	t.Cleanup(func() { _ = h.Close() })
	named := h.WithName("core.handlers.appeal_handler")
	named.With("case", "abc123").Info("投递失败")

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if !strings.HasPrefix(content, " - core.handlers.appeal_handler - INFO - ") &&
		!strings.Contains(content, " - core.handlers.appeal_handler - INFO - ") {
		t.Fatalf("命名 logger 未生效: %q", content)
	}
	if !strings.Contains(content, " case=abc123") {
		t.Fatalf("属性未渲染: %q", content)
	}
}
