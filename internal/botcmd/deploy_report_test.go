// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package botcmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/process"
)

func TestFormatImageDeployReport(t *testing.T) {
	ok := FormatImageDeployReport(&process.ImageDeployResult{
		Status:   "ok",
		Revision: "2a618008a94a33f9da73823402450b41551a0c63",
	}, "", "2.6.2", true)
	for _, want := range []string{"部署完成", "v2.6.2", "2a61800"} {
		if !strings.Contains(ok, want) {
			t.Fatalf("rebuilt ok report %q missing %q", ok, want)
		}
	}
	latest := FormatImageDeployReport(&process.ImageDeployResult{Status: "ok"}, "", "2.6.2", false)
	if !strings.Contains(latest, "已是最新版本") {
		t.Fatalf("no-op report %q missing latest hint", latest)
	}
	failed := FormatImageDeployReport(&process.ImageDeployResult{Status: "failed", ExitCode: 1},
		"tail with **bold** markers", "2.6.2", true)
	for _, want := range []string{"退出码 1", "tail with"} {
		if !strings.Contains(failed, want) {
			t.Fatalf("failed report %q missing %q", failed, want)
		}
	}
	unknown := FormatImageDeployReport(&process.ImageDeployResult{}, "", "", true)
	if !strings.Contains(unknown, "异常") {
		t.Fatalf("unknown status report %q", unknown)
	}
}

func TestAwaitImageDeployResultIgnoresStaleAndAcceptsFresh(t *testing.T) {
	dir := t.TempDir()
	stale := "status=ok\nexit_code=0\nrevision=abc1234\nfinished_at=2026-11-20T01:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, ".image_deploy_result"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	requested := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	watchCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if result, err := awaitImageDeployResult(watchCtx, dir, requested); result != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stale result accepted: %+v, %v", result, err)
	}
	fresh := "status=failed\nexit_code=1\nrevision=abc1234\nfinished_at=2026-11-20T03:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, ".image_deploy_result"), []byte(fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := awaitImageDeployResult(context.Background(), dir, requested)
	if err != nil || result == nil || result.Status != "failed" || result.ExitCode != 1 {
		t.Fatalf("fresh result rejected: %+v, %v", result, err)
	}
}

func TestReportPendingImageDeployDeliversAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	requested := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(dir, ".image_update_pending"),
		[]byte("chat_id=777\nrequested_at="+requested+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	finished := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(dir, ".image_deploy_result"),
		[]byte("status=ok\nexit_code=0\nrevision=deadbeef\nfinished_at="+finished+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bot := &fakeCmdMessenger{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ReportPendingImageDeploy(ctx, bot, dir, nil)
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0], "当前运行") {
		t.Fatalf("unexpected reports: %q", bot.sent)
	}
	if _, err := os.Stat(filepath.Join(dir, ".image_update_pending")); !os.IsNotExist(err) {
		t.Fatalf("pending record not cleaned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".image_deploy_result")); !os.IsNotExist(err) {
		t.Fatalf("deploy result not cleaned: %v", err)
	}
}

func TestReportPendingImageDeployNoopWithoutPending(t *testing.T) {
	dir := t.TempDir()
	bot := &fakeCmdMessenger{}
	ReportPendingImageDeploy(context.Background(), bot, dir, nil)
	if len(bot.sent) != 0 {
		t.Fatalf("unexpected reports: %q", bot.sent)
	}
}

// writeDeployLog 在临时目录布置 logs/image-deploy.log，模拟宿主机日志布局
// （日志文件与 data 目录同级，位于父目录的 logs/ 下）。
func writeDeployLog(t *testing.T, content string) string {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "data")
	logsDir := filepath.Join(filepath.Dir(dataDir), "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "image-deploy.log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func TestDeployLogTailKeepsEndOfAppendOnlyLog(t *testing.T) {
	stale := strings.Repeat("上周拉取 feature 分支镜像的旧日志。", 100)
	fresh := "error: Your local changes to the following files would be overwritten by merge"
	content := stale + strings.Repeat("=", 4000) + fresh
	dataDir := writeDeployLog(t, content)
	tail := deployLogTail(dataDir)
	if !strings.HasPrefix(tail, "...") {
		t.Fatalf("oversized log tail missing cut marker: %.32s", tail)
	}
	if !strings.HasSuffix(tail, fresh) {
		t.Fatalf("tail does not end with the latest failure: %.64s", tail[len(tail)-64:])
	}
	if strings.Contains(tail, "旧日志") {
		t.Fatal("tail still contains stale head content")
	}
}

func TestDeployLogTailShortAndMissingLog(t *testing.T) {
	content := "==> 部署完成\n"
	dataDir := writeDeployLog(t, content)
	// 短日志按整段 TrimSpace 后原样透传，不追加截断标记。
	if tail := deployLogTail(dataDir); tail != strings.TrimSpace(content) {
		t.Fatalf("short log should pass through verbatim: %q", tail)
	}
	if tail := deployLogTail(filepath.Join(t.TempDir(), "data")); tail != "" {
		t.Fatalf("missing log should yield empty tail: %q", tail)
	}
}
