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
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestHandlerSetGetLevelAndDefaultLevelConcurrent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sakura-bot.log")

	logger, h, gotPath, err := SetupMain(Options{
		BaseLogFilePath: logPath,
		LevelName:       "INFO",
		LogToFile:       true,
		LogToConsole:    false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	if gotPath != logPath {
		t.Fatalf("SetupMain 路径 = %q, 期望 %q", gotPath, logPath)
	}
	if h.GetLevel() != "INFO" || GetDefaultLevel() != "INFO" {
		t.Fatalf("初始级别应为 INFO: handler=%s default=%s", h.GetLevel(), GetDefaultLevel())
	}

	// 非法级别返回 false 且不改动原级别。
	for _, bad := range []string{"", "WARN", "TRACE", "bogus"} {
		if h.SetLevel(bad) {
			t.Fatalf("非法级别 %q 应返回 false", bad)
		}
		if SetDefaultLevel(bad) {
			t.Fatalf("SetDefaultLevel(%q) 应返回 false", bad)
		}
	}
	if h.GetLevel() != "INFO" {
		t.Fatalf("非法级别不应改变原级别: %s", h.GetLevel())
	}

	// 大小写不敏感修改并同步派生命名 logger。
	child := h.WithName("core.scheduler")
	if !SetDefaultLevel("  warning ") {
		t.Fatal("SetDefaultLevel(warning) 应成功")
	}
	if h.GetLevel() != "WARNING" || GetDefaultLevel() != "WARNING" {
		t.Fatalf("级别未同步更新为 WARNING: %s / %s", h.GetLevel(), GetDefaultLevel())
	}
	logger.Info("被过滤的 info")
	child.Warn("保留的 warning")

	// 并发读写日志级别与输出，验证无数据竞争。
	var wg sync.WaitGroup
	levels := []string{"debug", "INFO", "warning", "ERROR", "Critical"}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				lvl := levels[(id+j)%len(levels)]
				_ = h.SetLevel(lvl)
				_ = h.GetLevel()
				logger.Info("concurrent log", "worker", id, "iter", j)
			}
		}(i)
	}
	wg.Wait()

	if !h.SetLevel("CRITICAL") || h.GetLevel() != "CRITICAL" {
		t.Fatalf("最终切换 CRITICAL 失败: %s", h.GetLevel())
	}

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, "被过滤的 info") {
		t.Fatalf("WARNING 级别下不应写出 INFO: %s", text)
	}
	if !strings.Contains(text, " - core.scheduler - WARNING - ") || !strings.Contains(text, "保留的 warning") {
		t.Fatalf("派生 logger 日志缺失: %s", text)
	}
}

func TestSetupMainDefaultsAndConsolePrefix(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "main", "sakura-bot.log")

	logger, h, gotPath, err := SetupMain(Options{
		BaseLogFilePath: logPath,
		LevelName:       "DEBUG",
		LogToFile:       true,
		LogToConsole:    false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()

	if gotPath != logPath {
		t.Fatalf("gotPath = %q, want %q", gotPath, logPath)
	}
	// 默认组件名应为 main，默认轮转大小 10MiB×5。
	if h.st.opts.Component != "main" {
		t.Fatalf("默认 Component = %q, 期望 main", h.st.opts.Component)
	}
	if h.st.opts.MaxSizeBytes != defaultMaxBytes || h.st.opts.BackupCount != defaultBackupCount {
		t.Fatalf("默认轮转参数不符: max=%d backup=%d", h.st.opts.MaxSizeBytes, h.st.opts.BackupCount)
	}

	var consoleBuf bytes.Buffer
	h.st.mu.Lock()
	h.st.console = &consoleBuf
	h.st.colored = false
	h.st.mu.Unlock()

	logger.Info("主进程启动")
	if !strings.Contains(consoleBuf.String(), " - sakura_bot - INFO - [main] 主进程启动\n") {
		t.Fatalf("主进程控制台格式不符: %q", consoleBuf.String())
	}

	fileBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fileBytes), " - sakura_bot - INFO - logging_test.go:") {
		t.Fatalf("主进程文件格式不符: %q", string(fileBytes))
	}
}

func TestReadRecentLogsFilteringAndBoundedTail(t *testing.T) {
	dir := t.TempDir()

	// 1. 不存在文件或目录：返回空切片且无错误。
	lines, err := ReadRecentLogs(filepath.Join(dir, "missing.log"), 50, "", "")
	if err != nil || len(lines) != 0 {
		t.Fatalf("缺失文件应返回空切片无错: lines=%v err=%v", lines, err)
	}
	lines, err = ReadRecentLogs(dir, 50, "", "")
	if err != nil || len(lines) != 0 {
		t.Fatalf("目录路径应返回空切片无错: lines=%v err=%v", lines, err)
	}

	// 2. 空文件：返回空切片。
	emptyPath := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if lines, err = ReadRecentLogs(emptyPath, 10, "", ""); err != nil || len(lines) != 0 {
		t.Fatalf("空文件结果不符: %v %v", lines, err)
	}

	// 3. CRLF、级别过滤、大小写无关关键字过滤与顺序验证。
	samplePath := filepath.Join(dir, "sample.log")
	sample := strings.Join([]string{
		"2026-01-01 00:00:01 - sakura_bot - DEBUG - a.go:1 - boot step 1",
		"2026-01-01 00:00:02 - sakura_bot - INFO - a.go:2 - Connected to Telegram",
		"2026-01-01 00:00:03 - sakura_bot - WARNING - a.go:3 - FloodWait 5s on channel",
		"2026-01-01 00:00:04 - sakura_bot - ERROR - a.go:4 - Summary failed for @test_chan",
		"2026-01-01 00:00:05 - sakura_bot - ERROR - a.go:5 - Telegram timeout on @other",
	}, "\r\n") + "\r\n"
	if err := os.WriteFile(samplePath, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadRecentLogs(samplePath, 2, "", "")
	if err != nil || len(got) != 2 {
		t.Fatalf("取尾部 2 行失败: %v %v", got, err)
	}
	if !strings.HasSuffix(got[0], "@test_chan") || !strings.HasSuffix(got[1], "@other") {
		t.Fatalf("尾部顺序或 CRLF 清理错误: %q", got)
	}

	got, err = ReadRecentLogs(samplePath, 10, "error", "TELEGRAM")
	if err != nil || len(got) != 1 || !strings.Contains(got[0], "Telegram timeout") {
		t.Fatalf("level + keyword 组合过滤不符: %v %v", got, err)
	}

	// 负数截断为 1。
	got, err = ReadRecentLogs(samplePath, -5, "", "")
	if err != nil || len(got) != 1 || !strings.HasSuffix(got[0], "@other") {
		t.Fatalf("负数 maxLines 应钳制为 1: %v %v", got, err)
	}

	// 4. 大文件（> 5MiB > maxTailScanBytes 4MiB）：测试有界尾部扫描、跨 64KiB 分块拼接、上限 1000 钳制，以及不返回半截断行。
	bigPath := filepath.Join(dir, "huge.log")
	f, err := os.Create(bigPath)
	if err != nil {
		t.Fatal(err)
	}
	// 先写 ~4.5MiB 的头部噪音（包含关键字 EARLY_SENTINEL，位于距尾部 >4MiB 处，不应被扫描到）。
	earlyLine := "2026-01-01 00:00:00 - sakura_bot - ERROR - early.go:1 - EARLY_SENTINEL " + strings.Repeat("x", 120) + "\n"
	for written := 0; written < 4600*1024; written += len(earlyLine) {
		if _, err := f.WriteString(earlyLine); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
	}
	// 再写 1200 行尾部日志（跨多个 64KiB 块，含中文 UTF-8）。
	for i := 1; i <= 1200; i++ {
		line := fmt.Sprintf("2026-01-02 12:00:00 - sakura_bot - INFO - main.go:%d - 尾部消息记录 #%04d %s\n", i, i, strings.Repeat("桜", 20))
		if _, err := f.WriteString(line); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// maxLines=5000 应钳制到 1000，且起止行严格为 #0201..#1200。
	tail1000, err := ReadRecentLogs(bigPath, 5000, "INFO", "尾部消息记录")
	if err != nil {
		t.Fatal(err)
	}
	if len(tail1000) != 1000 {
		t.Fatalf("maxLines>1000 应返回 1000 行，实际 %d", len(tail1000))
	}
	if !strings.Contains(tail1000[0], "#0201 ") || !strings.Contains(tail1000[999], "#1200 ") {
		t.Fatalf("跨分块回溯起止行不符: first=%q last=%q", tail1000[0], tail1000[999])
	}

	// 查询位于 >4MiB 之前的 EARLY_SENTINEL：因有界扫描只看最后 4MiB，若最后 4MiB 之外的不应全盘读取；
	// 在 4.6MiB 头部 + ~150KB 尾部下，最后 4MiB 内仍会有部分 earlyLine，但绝不会出现截断半行（每行均以 2026- 开头）。
	earlyGot, err := ReadRecentLogs(bigPath, 1000, "ERROR", "EARLY_SENTINEL")
	if err != nil {
		t.Fatal(err)
	}
	for idx, l := range earlyGot {
		if !strings.HasPrefix(l, "2026-01-01 00:00:00 - sakura_bot - ERROR") {
			t.Fatalf("4MiB 边界处出现了截断半行 [%d]: %q", idx, l)
		}
	}

	// 再追加 >4.2MiB 的纯 INFO 行，此时 EARLY_SENTINEL 完全落在距文件末尾 >4MiB 之外，应返回 0 条。
	f2, err := os.OpenFile(bigPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	padLine := "2026-01-03 00:00:00 - sakura_bot - INFO - pad.go:1 - " + strings.Repeat("p", 200) + "\n"
	for written := 0; written < 4300*1024; written += len(padLine) {
		if _, err := f2.WriteString(padLine); err != nil {
			_ = f2.Close()
			t.Fatal(err)
		}
	}
	_ = f2.Close()

	beyondCap, err := ReadRecentLogs(bigPath, 50, "ERROR", "EARLY_SENTINEL")
	if err != nil {
		t.Fatal(err)
	}
	if len(beyondCap) != 0 {
		t.Fatalf("超过 4MiB 尾部窗口的旧日志不应被全盘扫描命中，实际命中 %d 条", len(beyondCap))
	}
}
