// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package process

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFrontendFingerprintAndPrepareFromImageCache(t *testing.T) {
	t.Setenv("SAKURA_DOCKER", "1")
	root := t.TempDir()
	runtimeDir := filepath.Join(t.TempDir(), "runtime")

	webSrc := filepath.Join(root, "web", "src")
	if err := os.MkdirAll(webSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "package.json"), []byte(`{"name":"web"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webSrc, "main.ts"), []byte(`console.log(1)`), 0o644); err != nil {
		t.Fatal(err)
	}
	// node_modules 和 .tsbuildinfo 不应影响指纹
	if err := os.MkdirAll(filepath.Join(root, "web", "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "tsconfig.tsbuildinfo"), []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}

	fp1, err := FrontendFingerprint(root)
	if err != nil {
		t.Fatalf("FrontendFingerprint failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "node_modules", "pkg", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp2, err := FrontendFingerprint(root)
	if err != nil || fp1 != fp2 {
		t.Fatalf("fingerprint changed due to node_modules: %q vs %q (err=%v)", fp1, fp2, err)
	}

	// 模拟镜像构建期写入缓存与指纹
	cacheDist := filepath.Join(runtimeDir, "web-dist")
	if err := os.MkdirAll(cacheDist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDist, "index.html"), []byte("<html>cached</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecordImageBuild(root, runtimeDir); err != nil {
		t.Fatalf("RecordImageBuild failed: %v", err)
	}
	// 删除挂载目录的 dist，验证 PrepareRuntime 从镜像缓存恢复而不调用 npm
	_ = os.RemoveAll(filepath.Join(root, "web", "dist"))
	if err := PrepareRuntime(root, runtimeDir); err != nil {
		t.Fatalf("PrepareRuntime failed: %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(root, "web", "dist", "index.html"))
	if err != nil || string(restored) != "<html>cached</html>" {
		t.Fatalf("unexpected restored dist: %q, err=%v", string(restored), err)
	}
}

func TestImageUpdateAndPIDLock(t *testing.T) {
	if !NeedsImageUpdate([]string{"README.md", "internal/webapi/server.go"}) {
		t.Fatal("expected internal/ change to require image update")
	}
	if NeedsImageUpdate([]string{"README.md", "docs/sakura-wiki.md"}) {
		t.Fatal("docs-only change should not require image update")
	}

	dir := t.TempDir()
	if DeployerInstalled(dir) {
		t.Fatal("expected DeployerInstalled == false")
	}
	if err := RequestImageUpdate(dir); err != nil {
		t.Fatalf("RequestImageUpdate: %v", err)
	}

	pidFile := filepath.Join(dir, "bot.pid")
	release, err := AcquirePIDFile(pidFile)
	if err != nil {
		t.Fatalf("AcquirePIDFile: %v", err)
	}
	defer release()
	release()
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatal("expected pidFile removed on release")
	}
}

func TestImageUpdatePendingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if pending, err := ReadImageUpdatePending(dir); err != nil || pending != nil {
		t.Fatalf("missing pending file must read as (nil, nil): %+v, %v", pending, err)
	}
	requestedAt, err := SaveImageUpdatePending(dir, 4242)
	if err != nil {
		t.Fatalf("SaveImageUpdatePending: %v", err)
	}
	if requestedAt.IsZero() {
		t.Fatal("SaveImageUpdatePending returned zero time")
	}
	pending, err := ReadImageUpdatePending(dir)
	if err != nil || pending == nil || pending.ChatID != 4242 {
		t.Fatalf("unexpected pending: %+v, %v", pending, err)
	}
	// RFC3339 秒级精度，允许 1 秒内的舍入误差。
	if d := pending.RequestedAt.Sub(requestedAt); d < -time.Second || d > time.Second {
		t.Fatalf("requested_at drift: saved=%v read=%v", requestedAt, pending.RequestedAt)
	}
	ClearImageUpdatePending(dir)
	if pending, err := ReadImageUpdatePending(dir); err != nil || pending != nil {
		t.Fatalf("cleared pending must read as (nil, nil): %+v, %v", pending, err)
	}
}

func TestImageDeployResultParsing(t *testing.T) {
	dir := t.TempDir()
	if result, err := ReadImageDeployResult(dir); err != nil || result != nil {
		t.Fatalf("missing result file must read as (nil, nil): %+v, %v", result, err)
	}
	content := "status=failed\nexit_code=3\nrevision=0123456789abcdef\nfinished_at=2026-11-20T06:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, imageDeployResultFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := ReadImageDeployResult(dir)
	if err != nil || result == nil {
		t.Fatalf("ReadImageDeployResult: %+v, %v", result, err)
	}
	if result.Status != "failed" || result.ExitCode != 3 || result.Revision != "0123456789abcdef" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !result.FinishedAt.Equal(time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected finished_at: %v", result.FinishedAt)
	}
	ClearImageDeployResult(dir)
	if result, err := ReadImageDeployResult(dir); err != nil || result != nil {
		t.Fatalf("cleared result must read as (nil, nil): %+v, %v", result, err)
	}
}
