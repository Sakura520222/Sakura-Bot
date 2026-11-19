// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package process 管理问答助手子进程（sakura-qa）生命周期、单实例 PID 锁、
// Docker 挂载目录前端构建指纹自愈及 /update 镜像更新请求标记。
package process

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
)

// QAConfigSource 抽象 QA 子进程所需的配置读取与运行时持久化接口。
type QAConfigSource interface {
	QABotEnabled() bool
	QABotToken() string
	LookupEnv(key string) (string, bool)
	ConfiguredQARuntime() string
	SaveQARuntime(rt string) error
}

// QAActionResult 对齐 process_manager.py 的 start/stop/restart 返回字典。
type QAActionResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	PID     *int   `json:"pid,omitempty"`
}

// QAStatus 对齐 get_qa_bot_status 返回字典。
type QAStatus struct {
	Running           bool     `json:"running"`
	PID               *int     `json:"pid"`
	StartTime         *float64 `json:"start_time"`
	UptimeSeconds     *float64 `json:"uptime_seconds"`
	Enabled           bool     `json:"enabled"`
	TokenConfigured   bool     `json:"token_configured"`
	Runtime           string   `json:"runtime"`
	ConfiguredRuntime string   `json:"configured_runtime"`
}

// QAManager 线程安全地管理独立 sakura-qa 子进程生命周期。
type QAManager struct {
	mu      sync.Mutex
	cfg     QAConfigSource
	workDir string
	logger  *slog.Logger

	cmd       *exec.Cmd
	done      chan error
	exitErr   error
	exited    bool
	startedAt time.Time
}

// NewQAManager 创建 QA 子进程管理器。
func NewQAManager(cfg QAConfigSource, workDir string, logger *slog.Logger) *QAManager {
	if workDir == "" {
		workDir = "."
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &QAManager{
		cfg:     cfg,
		workDir: workDir,
		logger:  logger,
	}
}

// ResolveBinary 按优先级查找 sakura-qa 可执行文件路径。
func (m *QAManager) ResolveBinary() string {
	exeSuffix := ""
	if runtime.GOOS == "windows" {
		exeSuffix = ".exe"
	}
	var candidates []string
	if envBin, ok := m.cfg.LookupEnv("QA_BOT_BINARY"); ok && strings.TrimSpace(envBin) != "" {
		candidates = append(candidates, strings.TrimSpace(envBin))
	}
	if dockerFlag, _ := m.cfg.LookupEnv("SAKURA_DOCKER"); dockerFlag == "1" {
		candidates = append(candidates, "/usr/local/bin/sakura-qa")
	}
	if selfExe, err := os.Executable(); err == nil && selfExe != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(selfExe), "sakura-qa"+exeSuffix))
	}
	candidates = append(candidates,
		"/usr/local/bin/sakura-qa",
		filepath.Join(m.workDir, "bin", "sakura-qa"+exeSuffix),
		filepath.Join(m.workDir, "bin", "sakura-qa-linux-amd64"),
		filepath.Join(m.workDir, "sakura-qa"+exeSuffix),
	)
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// EffectiveRuntime 返回当前有效运行时标识（纯 Go 架构统一为 "go"）。
func (m *QAManager) EffectiveRuntime() string {
	raw := strings.ToLower(strings.TrimSpace(m.cfg.ConfiguredQARuntime()))
	if raw == "python" {
		// Python 生产运行时已退役，自动映射至 Go 二进制以兼容历史配置。
		return "go"
	}
	return "go"
}

// Start 在后台启动 sakura-qa 子进程。
func (m *QAManager) Start() QAActionResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.reapLocked()
	if m.cmd != nil && m.cmd.Process != nil {
		pid := m.cmd.Process.Pid
		return QAActionResult{
			Success: false,
			Message: i18n.T("qabot.already_running", map[string]string{"pid": strconv.Itoa(pid)}),
			PID:     &pid,
		}
	}
	if !m.cfg.QABotEnabled() {
		return QAActionResult{
			Success: false,
			Message: i18n.T("qabot.not_enabled", nil),
		}
	}
	if strings.TrimSpace(m.cfg.QABotToken()) == "" {
		return QAActionResult{
			Success: false,
			Message: i18n.T("qabot.token_not_configured", nil),
		}
	}

	bin := m.ResolveBinary()
	if bin == "" {
		msg := "未找到 sakura-qa 可执行文件（请检查 QA_BOT_BINARY 或 bin/sakura-qa）"
		m.logger.Error(msg)
		return QAActionResult{
			Success: false,
			Message: msg,
		}
	}

	env := os.Environ()
	env = ensureEnvDefault(env, "SAKURA_LOG_COMPONENT", "qa")
	env = ensureEnvDefault(env, "SAKURA_COMPONENT_LOG_FILE", "qa-bot.log")
	env = ensureEnvDefault(env, "GOMEMLIMIT", "96MiB")

	cmd := exec.Command(bin)
	cmd.Dir = m.workDir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		m.logger.Error("启动 sakura-qa 失败", "binary", bin, "error", err)
		return QAActionResult{
			Success: false,
			Message: i18n.T("qabot.start_success", map[string]string{"message": "Startup failed: " + err.Error()}),
		}
	}

	done := make(chan error, 1)
	m.cmd = cmd
	m.done = done
	m.exited = false
	m.exitErr = nil
	m.startedAt = time.Now()
	pid := cmd.Process.Pid

	go func(c *exec.Cmd, ch chan error) {
		err := c.Wait()
		m.mu.Lock()
		if m.done == ch {
			m.exited = true
			m.exitErr = err
		}
		m.mu.Unlock()
		ch <- err
		close(ch)
	}(cmd, done)

	m.logger.Info("问答Bot(Go)已启动", "pid", pid, "binary", bin)
	return QAActionResult{
		Success: true,
		Message: i18n.T("qabot.started", map[string]string{"pid": strconv.Itoa(pid)}),
		PID:     &pid,
	}
}

// Stop 优雅停止 sakura-qa 子进程（发送 SIGTERM 并等待最多 timeout，超时则 Kill）。
func (m *QAManager) Stop(timeout time.Duration) QAActionResult {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	m.mu.Lock()
	m.reapLocked()
	if m.cmd == nil || m.cmd.Process == nil {
		m.mu.Unlock()
		return QAActionResult{
			Success: true,
			Message: i18n.T("qabot.not_running_short", nil),
		}
	}
	cmd := m.cmd
	done := m.done
	pid := cmd.Process.Pid
	m.mu.Unlock()

	m.logger.Info("正在停止问答Bot...", "pid", pid)
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
	} else {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}

	forced := false
	select {
	case <-done:
	case <-time.After(timeout):
		forced = true
		_ = cmd.Process.Kill()
		<-done
	}

	m.mu.Lock()
	if m.cmd == cmd {
		m.cmd = nil
		m.done = nil
		m.exited = false
		m.startedAt = time.Time{}
	}
	m.mu.Unlock()

	if forced {
		return QAActionResult{
			Success: true,
			Message: i18n.T("qabot.force_stopped", nil),
		}
	}
	return QAActionResult{
		Success: true,
		Message: i18n.T("qabot.stopped", map[string]string{"pid": strconv.Itoa(pid)}),
	}
}

// Restart 重启 sakura-qa 子进程。
func (m *QAManager) Restart() QAActionResult {
	stopRes := m.Stop(5 * time.Second)
	if !stopRes.Success {
		return QAActionResult{
			Success: false,
			Message: "Stop failed: " + stopRes.Message,
		}
	}
	time.Sleep(200 * time.Millisecond)
	startRes := m.Start()
	if startRes.Success && startRes.PID != nil {
		return QAActionResult{
			Success: true,
			Message: i18n.T("qabot.restarted", map[string]string{"pid": strconv.Itoa(*startRes.PID)}),
			PID:     startRes.PID,
		}
	}
	return QAActionResult{
		Success: false,
		Message: "Restart failed: " + startRes.Message,
	}
}

// SetRuntime 持久化运行时设置并按需重启（仅接受 "go"；传入 "python" 返回退役提示或保持兼容校验）。
func (m *QAManager) SetRuntime(rt string) map[string]any {
	rt = strings.ToLower(strings.TrimSpace(rt))
	if rt != "go" {
		return map[string]any{
			"success": false,
			"message": i18n.T("qabot.runtime_invalid", map[string]string{"runtime": rt}),
			"runtime": "go",
			"restart": nil,
		}
	}
	prev := m.EffectiveRuntime()
	if err := m.cfg.SaveQARuntime("go"); err != nil {
		return map[string]any{
			"success": false,
			"message": fmt.Sprintf("写入配置失败: %v", err),
			"runtime": prev,
			"restart": nil,
		}
	}
	var restartRes any
	if m.Status().Running {
		res := m.Restart()
		restartRes = res
	}
	return map[string]any{
		"success": true,
		"message": i18n.T("qabot.runtime_switched", map[string]string{"previous": prev, "runtime": "go"}),
		"runtime": "go",
		"restart": restartRes,
	}
}

// Status 返回当前子进程状态。
func (m *QAManager) Status() QAStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.reapLocked()
	st := QAStatus{
		Enabled:           m.cfg.QABotEnabled(),
		TokenConfigured:   strings.TrimSpace(m.cfg.QABotToken()) != "",
		Runtime:           "go",
		ConfiguredRuntime: m.EffectiveRuntime(),
	}
	if m.cmd != nil && m.cmd.Process != nil {
		pid := m.cmd.Process.Pid
		startSec := float64(m.startedAt.UnixNano()) / 1e9
		upSec := time.Since(m.startedAt).Seconds()
		st.Running = true
		st.PID = &pid
		st.StartTime = &startSec
		st.UptimeSeconds = &upSec
	}
	return st
}

// CheckHealth 返回 (healthy, shouldRestart, message)。
func (m *QAManager) CheckHealth() (bool, bool, string) {
	st := m.Status()
	if !st.Enabled {
		return true, false, i18n.T("qabot.not_enabled", nil)
	}
	if !st.TokenConfigured {
		return false, false, i18n.T("qabot.token_not_configured", nil)
	}
	if st.Running && st.PID != nil {
		up := "0"
		if st.UptimeSeconds != nil {
			up = fmt.Sprintf("%.0f", *st.UptimeSeconds)
		}
		return true, false, i18n.T("qabot.running_normal", map[string]string{
			"pid":    strconv.Itoa(*st.PID),
			"uptime": up,
		})
	}
	return false, true, i18n.T("qabot.process_not_running", nil)
}

// FormatUptime 将秒数格式化为中文人类可读时长。
func FormatUptime(seconds *float64) string {
	if seconds == nil {
		return "未知"
	}
	total := int(*seconds)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d小时%d分钟%d秒", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%d分钟%d秒", m, s)
	}
	return fmt.Sprintf("%d秒", s)
}

func (m *QAManager) reapLocked() {
	if m.cmd != nil && m.exited {
		if m.exitErr != nil {
			m.logger.Warn("问答Bot子进程已退出", "error", m.exitErr)
		} else {
			m.logger.Info("问答Bot子进程已正常退出")
		}
		m.cmd = nil
		m.done = nil
		m.exited = false
		m.exitErr = nil
		m.startedAt = time.Time{}
	}
}

func ensureEnvDefault(env []string, key, defVal string) []string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return env
		}
	}
	return append(env, prefix+defVal)
}
