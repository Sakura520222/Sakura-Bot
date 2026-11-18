// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package botcmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/process"
)

type applicationUpdate struct {
	success bool
	message string
	git     string

	executable string
	staticDir  string
}

func (r *Router) updateApplication(ctx context.Context, check bool, reason string, reply func(applicationUpdate)) {
	if !r.updateMu.TryLock() {
		reply(applicationUpdate{message: "已有更新操作正在运行"})
		return
	}
	defer r.updateMu.Unlock()
	timeout := 10 * time.Minute
	if check {
		timeout = 30 * time.Second
	}
	updateCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	outcome := r.prepareApplicationUpdate(updateCtx, check)
	reply(outcome)
	if outcome.executable != "" {
		r.deps.TriggerReleaseRestart(reason, outcome.executable, outcome.staticDir)
	}
}

func (r *Router) prepareApplicationUpdate(ctx context.Context, check bool) applicationUpdate {
	workDir := r.deps.WorkDir
	if workDir == "" {
		workDir = "."
	}
	if process.IsDocker() {
		return r.requestDockerUpdate(workDir, check)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		return applicationUpdate{message: "当前安装没有 Git 工作区，请通过发布附件或部署脚本更新"}
	}
	if check {
		return r.checkGitUpdate(ctx, workDir)
	}
	if r.deps.TriggerReleaseRestart == nil {
		return applicationUpdate{message: "当前安装无法切换二进制，请使用启动脚本重新构建"}
	}
	git, err := runUpdateGit(ctx, workDir, "pull", "--ff-only")
	if err != nil {
		r.deps.Logger.Warn("无法快进更新 Git 工作区", "error", err)
		return applicationUpdate{message: "代码快进更新失败，请检查上游连接、分支分叉和本地修改"}
	}
	executable, staticDir, err := stageLocalRelease(ctx, workDir)
	if err != nil {
		r.deps.Logger.Error("本地更新构建失败", "error", err)
		return applicationUpdate{message: "源码已同步，构建失败，当前运行版本继续服务。请查看日志后重试"}
	}
	return applicationUpdate{
		success: true, message: "新版本构建成功，正在重启 Bot...",
		git: git, executable: executable, staticDir: staticDir,
	}
}

func (r *Router) requestDockerUpdate(workDir string, check bool) applicationUpdate {
	if check {
		return applicationUpdate{message: "容器内无法查询宿主机更新状态，请在宿主机检查 Git 上游和镜像"}
	}
	dataDir := "data"
	if r.deps.Config != nil {
		dataDir = r.deps.Config.DataDirectory()
	}
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(workDir, dataDir)
	}
	if !process.DeployerInstalled(dataDir) {
		return applicationUpdate{message: "未检测到宿主机部署器，请在宿主机运行 bash scripts/deploy.sh"}
	}
	if err := process.RequestImageUpdate(dataDir); err != nil {
		r.deps.Logger.Error("提交宿主机更新请求失败", "error", err)
		return applicationUpdate{message: "提交宿主机更新请求失败，请检查 data 目录权限"}
	}
	return applicationUpdate{success: true, message: "已提交宿主机部署请求，请在 logs/image-deploy.log 查看结果"}
}

func (r *Router) checkGitUpdate(ctx context.Context, workDir string) applicationUpdate {
	if _, err := runUpdateGit(ctx, workDir, "fetch", "--quiet"); err != nil {
		r.deps.Logger.Warn("获取 Git 更新失败", "error", err)
		return applicationUpdate{message: "无法查询 Git 上游，请检查网络和远程仓库配置"}
	}
	history, err := runUpdateGit(ctx, workDir, "rev-list", "--left-right", "--count", "HEAD...@{u}")
	if err != nil {
		return applicationUpdate{message: "当前分支没有可查询的 Git 上游"}
	}
	counts := strings.Fields(history)
	if len(counts) != 2 {
		return applicationUpdate{message: "无法解析当前分支与上游的差异"}
	}
	local, localErr := strconv.Atoi(counts[0])
	upstream, upstreamErr := strconv.Atoi(counts[1])
	if localErr != nil || upstreamErr != nil {
		return applicationUpdate{message: "无法解析当前分支与上游的差异"}
	}
	switch {
	case local > 0 && upstream > 0:
		return applicationUpdate{message: "当前分支与上游已分叉，需要在宿主机合并后更新"}
	case local > 0:
		return applicationUpdate{success: true, message: "当前分支领先上游，没有可应用的上游提交"}
	case upstream == 0:
		return applicationUpdate{success: true, message: "当前分支与上游一致"}
	}
	log, err := runUpdateGit(ctx, workDir, "log", "--oneline", "--max-count=15", "HEAD..@{u}")
	if err != nil {
		return applicationUpdate{message: "上游有新提交，无法读取更新列表"}
	}
	return applicationUpdate{success: true, message: fmt.Sprintf("发现 %d 个上游提交", upstream), git: truncateRunesStr(log, 2000)}
}

func runUpdateGit(ctx context.Context, workDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func stageLocalRelease(ctx context.Context, workDir string) (executable, staticDir string, err error) {
	absolute, err := filepath.Abs(workDir)
	if err != nil {
		return "", "", err
	}
	stagingRoot := filepath.Join(absolute, "bin", ".updates")
	if err := os.MkdirAll(stagingRoot, 0755); err != nil {
		return "", "", err
	}
	staged, err := os.MkdirTemp(stagingRoot, "release-")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(staged)
		}
	}()

	versionText, err := os.ReadFile(filepath.Join(absolute, "VERSION"))
	if err != nil {
		return "", "", fmt.Errorf("读取 VERSION: %w", err)
	}
	version := strings.TrimSpace(string(versionText))
	if version == "" || strings.ContainsAny(version, " \t\r\n") {
		return "", "", fmt.Errorf("无效的 VERSION")
	}

	builder := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-trimpath",
		"-ldflags", "-X github.com/Sakura520222/Sakura-Bot/internal/version.Version="+version,
		"-o", staged+string(filepath.Separator), "./cmd/sakura-main", "./cmd/sakura-qa")
	builder.Dir = absolute
	builder.Env = append(builder.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if err := runReleaseBuild(builder, "构建 Go 二进制"); err != nil {
		return "", "", err
	}

	webDir := filepath.Join(absolute, "web")
	if err := runReleaseBuild(npmUpdateCommand(ctx, webDir, "ci"), "安装 WebUI 构建依赖"); err != nil {
		return "", "", err
	}
	assetsRelative := "../bin/.updates/" + filepath.Base(staged) + "/web"
	if err := runReleaseBuild(npmUpdateCommand(ctx, webDir, "run", "build", "--", "--outDir", assetsRelative), "构建 WebUI"); err != nil {
		return "", "", err
	}

	staticDir = filepath.Join(staged, "web")
	if info, statErr := os.Stat(filepath.Join(staticDir, "index.html")); statErr != nil || !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("WebUI 构建产物缺少 index.html")
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	return filepath.Join(staged, "sakura-main"+suffix), staticDir, nil
}

func npmUpdateCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		// npm.cmd is a Windows command script. Arguments are fixed switches and
		// a staging basename generated by os.MkdirTemp, never user input.
		cmd := exec.CommandContext(ctx, "cmd.exe", append([]string{"/D", "/C", "npm"}, args...)...)
		cmd.Dir = dir
		return cmd
	}
	cmd := exec.CommandContext(ctx, "npm", args...)
	cmd.Dir = dir
	return cmd
}

func runReleaseBuild(cmd *exec.Cmd, step string) error {
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", step, err, truncateRunesStr(strings.TrimSpace(string(out)), 4000))
	}
	return nil
}
