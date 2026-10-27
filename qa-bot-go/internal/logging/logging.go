// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package logging 提供与 Python core/infrastructure/logging/config.py::
// setup_component_logging（:200）逐格式对齐的 slog 双写日志：
// stdout（docker logs 可见）+ 组件文件 logs/components/qa-bot.log，
// 并手写 rename 链实现 10MB×5 轮转（对齐 RotatingFileHandler 语义）。
//
// 行格式（逐字对齐 config.py:30-32 的 FORMAT 常量）：
//
//	控制台：2006-01-02 15:04:05 - {name} - {LEVEL} - [{component}] {message}
//	文件　：2006-01-02 15:04:05 - {name} - {LEVEL} - {file}:{line} - {message}
//
// 其中 [component] 前缀仅控制台（对齐 ComponentFormatter），
// 文件行多出 源文件:行号（对齐 FILE_FORMAT）。
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// 组件日志的环境键（对齐 qa_bot.py:65-66 与 settings.py LogSettings）。
const (
	KeySakuraLogComponent     = "SAKURA_LOG_COMPONENT"      // 默认 qa
	KeySakuraComponentLogFile = "SAKURA_COMPONENT_LOG_FILE" // 默认 qa-bot.log
	KeyLogLevel               = "LOG_LEVEL"                 // 默认 DEBUG
	KeyLogToFile              = "LOG_TO_FILE"               // 默认 true
	KeyLogFilePath            = "LOG_FILE_PATH"             // 默认 logs/sakura-bot.log（仅用于推导组件目录）
	KeyLogFileMaxSize         = "LOG_FILE_MAX_SIZE"         // 默认 10485760
	KeyLogFileBackupCount     = "LOG_FILE_BACKUP_COUNT"     // 默认 5
	KeyLogToConsole           = "LOG_TO_CONSOLE"            // 默认 true
	KeyLogColorize            = "LOG_COLORIZE"              // 默认 true
)

// Python logging 格式与默认值常量（config.py:24-32 与 settings.py:120-130）。
const (
	// timeLayout 即 DATE_FORMAT "%Y-%m-%d %H:%M:%S"。
	timeLayout = "2006-01-02 15:04:05"
	// defaultLogDir 即 DEFAULT_LOG_DIR "logs"。
	defaultLogDir = "logs"
	// defaultMaxBytes 即 DEFAULT_MAX_BYTES 10MB。
	defaultMaxBytes = 10 * 1024 * 1024
	// defaultBackupCount 即 DEFAULT_BACKUP_COUNT。
	defaultBackupCount = 5
	// defaultBaseLogFilePath 即 settings.py LogSettings.log_file_path 默认值。
	defaultBaseLogFilePath = "logs/sakura-bot.log"
	// defaultComponent 即 qa_bot.py:65 的 SAKURA_LOG_COMPONENT 默认值。
	defaultComponent = "qa"
	// defaultComponentFile 即 qa_bot.py:66 的 SAKURA_COMPONENT_LOG_FILE 默认值。
	defaultComponentFile = "qa-bot.log"
	// defaultLevelName 即 constants.py DEFAULT_LOG_LEVEL "DEBUG"。
	defaultLevelName = "DEBUG"
)

// LevelCritical 对齐 Python CRITICAL(50)，在 slog 刻度上取 ERROR+4。
const LevelCritical = slog.LevelError + 4

// colorReset 即 ColorFormatter.RESET。
const colorReset = "\033[0m"

// Env 抽象环境读取，避免本包反向依赖 config 包（装配由 main 完成）。
type Env interface {
	Lookup(key string) (string, bool)
}

// Options 组件日志选项；字段语义对齐 setup_component_logging 的参数。
type Options struct {
	// Component 组件名，控制台消息前缀 [Component]。
	Component string
	// ComponentLogFile 组件日志文件名（如 qa-bot.log）。
	ComponentLogFile string
	// BaseLogFilePath 主日志文件路径，仅用于推导组件日志目录。
	BaseLogFilePath string
	// LevelName 取 DEBUG/INFO/WARNING/ERROR/CRITICAL；非法值按 DEBUG。
	LevelName string
	// LogToFile 是否写组件日志文件。
	LogToFile bool
	// MaxSizeBytes 单文件最大字节数；<=0 不做轮转判定（对齐 maxBytes=0）。
	MaxSizeBytes int
	// BackupCount 轮转保留份数。
	BackupCount int
	// LogToConsole 是否写 stdout。
	LogToConsole bool
	// Colorize 是否对控制台级别名着色（且仅 stdout 为终端时生效）。
	Colorize bool
}

// DefaultOptions 返回与 Python 默认一致的选项。
func DefaultOptions() Options {
	return Options{
		Component:        defaultComponent,
		ComponentLogFile: defaultComponentFile,
		BaseLogFilePath:  defaultBaseLogFilePath,
		LevelName:        defaultLevelName,
		LogToFile:        true,
		MaxSizeBytes:     defaultMaxBytes,
		BackupCount:      defaultBackupCount,
		LogToConsole:     true,
		Colorize:         true,
	}
}

// LoadEnv 用环境值覆盖默认选项（对齐 qa_bot.py:_setup_qa_logging 的取值链）。
// 布尔值按 pydantic 宽松语义解析；数值非法时回退默认值
// （Python 侧非法数值会在 settings 校验时终止进程，Go 侧选择回退默认值，
// 属已注释的偏差：日志配置失败不应阻止 QA 启动）。
func (o *Options) LoadEnv(env Env) {
	if v, ok := env.Lookup(KeySakuraLogComponent); ok && v != "" {
		o.Component = v
	}
	if v, ok := env.Lookup(KeySakuraComponentLogFile); ok && v != "" {
		o.ComponentLogFile = v
	}
	if v, ok := env.Lookup(KeyLogLevel); ok {
		name := strings.ToUpper(strings.TrimSpace(v))
		switch name {
		case "DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL":
			o.LevelName = name
		default:
			// 对齐 settings.py:validate_log_level：非法级别回落 DEBUG。
			o.LevelName = defaultLevelName
		}
	}
	if v, ok := env.Lookup(KeyLogToFile); ok {
		if b, err := parsePyBool(v); err == nil {
			o.LogToFile = b
		}
	}
	if v, ok := env.Lookup(KeyLogFilePath); ok && v != "" {
		o.BaseLogFilePath = v
	}
	if v, ok := env.Lookup(KeyLogFileMaxSize); ok {
		// 对齐 settings.py:validate_log_file_max_size：最小 1KB。
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 1024 {
			o.MaxSizeBytes = n
		}
	}
	if v, ok := env.Lookup(KeyLogFileBackupCount); ok {
		// 对齐 settings.py:validate_log_file_backup_count：0-20。
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 && n <= 20 {
			o.BackupCount = n
		}
	}
	if v, ok := env.Lookup(KeyLogToConsole); ok {
		if b, err := parsePyBool(v); err == nil {
			o.LogToConsole = b
		}
	}
	if v, ok := env.Lookup(KeyLogColorize); ok {
		if b, err := parsePyBool(v); err == nil {
			o.Colorize = b
		}
	}
}

// SetupOptionsFromEnv 组合 DefaultOptions + LoadEnv 的便捷函数。
func SetupOptionsFromEnv(env Env) Options {
	opts := DefaultOptions()
	opts.LoadEnv(env)
	return opts
}

// parsePyBool 按 pydantic v2 的宽松布尔语义解析。
func parsePyBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("非法布尔值: %q", v)
	}
}

// ComponentLogFilePath 推导组件日志文件路径
// （对齐 config.py:get_component_log_file_path，:69-85）。
func ComponentLogFilePath(componentLogFile, baseLogFilePath string) string {
	if baseLogFilePath != "" {
		return filepath.Join(filepath.Dir(baseLogFilePath), componentLogFile)
	}
	return filepath.Join(defaultLogDir, componentLogFile)
}

// Setup 按选项初始化双写 slog.Logger，返回 logger 与组件日志文件路径
// （对齐 setup_component_logging 的返回值）。调用方随后可用
// slog.SetDefault 接管全局。
func Setup(opts Options) (*slog.Logger, string, error) {
	path := ComponentLogFilePath(opts.ComponentLogFile, opts.BaseLogFilePath)

	var file *rotatingWriter
	if opts.LogToFile {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, "", fmt.Errorf("创建日志目录失败: %w", err)
		}
		w, err := newRotatingWriter(path, opts.MaxSizeBytes, opts.BackupCount)
		if err != nil {
			return nil, "", err
		}
		file = w
	}

	st := &handlerState{
		opts:    opts,
		level:   levelFromName(opts.LevelName),
		console: consoleSink(opts),
		file:    file,
		colored: opts.Colorize && stdoutIsTTY(),
	}
	return slog.New(&Handler{st: st, name: "qa_bot"}), path, nil
}

// consoleSink 返回控制台目的地；LogToConsole=false 时为 nil。
func consoleSink(opts Options) io.Writer {
	if !opts.LogToConsole {
		return nil
	}
	return os.Stdout
}

// handlerState 是共享的可变状态；WithName 通过指针共享，避免拷贝锁。
type handlerState struct {
	mu      sync.Mutex
	opts    Options
	level   slog.Level
	console io.Writer
	file    *rotatingWriter
	colored bool // 预计算 Colorize && stdout isatty（对齐 config.py:99 的判断）
}

// Handler 实现 slog.Handler；零值不可用，须经 Setup 创建。
type Handler struct {
	st    *handlerState
	name  string
	attrs []slog.Attr
}

// WithName 返回带指定 logger 名的新 *slog.Logger（等价 Python logging.getLogger(name)）。
func (h *Handler) WithName(name string) *slog.Logger {
	return slog.New(&Handler{st: h.st, name: name, attrs: h.attrs})
}

// Enabled 报告该级别是否启用。
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.st.level
}

// WithAttrs 返回携带预置属性的 handler（渲染为追加在消息后的 k=v 串）。
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &Handler{st: h.st, name: h.name, attrs: merged}
}

// WithGroup 本实现不分组渲染，返回自身（QA 日志不需要组语义）。
func (h *Handler) WithGroup(string) slog.Handler { return h }

// Close 关闭底层日志文件句柄（进程优雅退出时调用；幂等）。
// 对齐 Python handler.close() 的位置——shutdown 阶段释放文件。
func (h *Handler) Close() error {
	h.st.mu.Lock()
	defer h.st.mu.Unlock()
	if h.st.file == nil {
		return nil
	}
	err := h.st.file.Close()
	h.st.file = nil
	return err
}

// Handle 渲染并双写一行日志。
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	if r.Level < h.st.level {
		return nil
	}
	msg := r.Message
	if extra := renderAttrs(h.attrs, r); extra != "" {
		msg += extra
	}
	now := r.Time.Format(timeLayout)
	level := LevelName(r.Level)

	h.st.mu.Lock()
	defer h.st.mu.Unlock()

	if h.st.console != nil {
		line := ConsoleLine(now, h.name, h.st.opts.Component, level, msg, h.st.colored)
		_, _ = io.WriteString(h.st.console, line)
	}
	if h.st.file != nil {
		file, ln := sourceInfo(r.PC)
		line := FileLine(now, h.name, level, file, ln, msg)
		// 对齐 SafeRotatingFileHandler.emit：写失败吞掉，不拖垮业务。
		_, _ = h.st.file.Write([]byte(line))
	}
	return nil
}

// renderAttrs 把 WithAttrs 与记录级属性渲染为 " k=v k2=v2" 形式。
func renderAttrs(pre []slog.Attr, r slog.Record) string {
	var b strings.Builder
	appendAttr := func(a slog.Attr) {
		if a.Equal(slog.Attr{}) {
			return
		}
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteByte('=')
		b.WriteString(a.Value.String())
	}
	for _, a := range pre {
		appendAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(a)
		return true
	})
	return b.String()
}

// ConsoleLine 渲染控制台行（config.py:30 CONSOLE_FORMAT + ComponentFormatter 前缀）。
// colored 为真时按 ColorFormatter.COLORS 给级别名着色。
func ConsoleLine(now, name, component, level, msg string, colored bool) string {
	levelField := level
	if colored {
		levelField = colorizeLevel(level)
	}
	// ComponentFormatter：消息未带 [component] 前缀时补上（config.py:57-66）。
	if !strings.HasPrefix(msg, "["+component+"]") {
		msg = "[" + component + "] " + msg
	}
	return fmt.Sprintf("%s - %s - %s - %s\n", now, name, levelField, msg)
}

// FileLine 渲染文件行（config.py:31 FILE_FORMAT，含 filename:lineno）。
func FileLine(now, name, level, file string, line int, msg string) string {
	return fmt.Sprintf("%s - %s - %s - %s:%d - %s\n", now, name, level, file, line, msg)
}

// stdoutIsTTY 以字符设备判断 stdout 是否终端（等价 sys.stdout.isatty 的工程近似）。
func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// sourceInfo 由 slog 记录的 PC 解析 源文件名:行号（对齐 %(filename)s:%(lineno)d）。
func sourceInfo(pc uintptr) (string, int) {
	if pc == 0 {
		return "unknown", 0
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	if frame.File == "" {
		return "unknown", 0
	}
	return filepath.Base(frame.File), frame.Line
}

// LevelName 返回 Python 风格级别名（WARNING/CRITICAL 而非 slog 的 WARN）。
func LevelName(l slog.Level) string {
	switch {
	case l == LevelCritical:
		return "CRITICAL"
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

// colorizeLevel 给级别名包裹 ANSI 颜色（formatters.py:26-33 逐色一致）。
func colorizeLevel(level string) string {
	var code string
	switch level {
	case "DEBUG":
		code = "\033[36m" // 青色
	case "INFO":
		code = "\033[32m" // 绿色
	case "WARNING":
		code = "\033[33m" // 黄色
	case "ERROR":
		code = "\033[31m" // 红色
	case "CRITICAL":
		code = "\033[35m" // 紫色
	default:
		return level
	}
	return code + level + colorReset
}

// levelFromName 把 Python 风格级别名映射为 slog 级别；非法值按 DEBUG
// （对齐 settings.py:validate_log_level 与 logging_level 映射）。
func levelFromName(name string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	case "CRITICAL":
		return LevelCritical
	default:
		return slog.LevelDebug
	}
}
