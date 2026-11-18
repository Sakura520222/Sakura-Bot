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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ImageLayerPrefixes 定义触及编译后二进制或前端/容器镜像层的路径前缀。
var ImageLayerPrefixes = []string{
	"cmd/",
	"internal/",
	"go.mod",
	"go.sum",
	"web/",
	".github/workflows/docker-build.yml",
	"Dockerfile",
	"docker-compose.yml",
	"docker-entrypoint.sh",
}

// NeedsImageUpdate 判断变更路径列表是否需要触发新镜像拉取/重编译。
func NeedsImageUpdate(changedPaths []string) bool {
	for _, p := range changedPaths {
		norm := filepath.ToSlash(strings.TrimSpace(p))
		for _, prefix := range ImageLayerPrefixes {
			if norm == prefix || strings.HasPrefix(norm, prefix) {
				return true
			}
		}
	}
	return false
}

// DeployerInstalled 判断宿主机 systemd 自动部署器标记是否存在。
func DeployerInstalled(dataDir string) bool {
	if dataDir == "" {
		dataDir = "data"
	}
	fi, err := os.Stat(filepath.Join(dataDir, ".image_deployer_installed"))
	return err == nil && !fi.IsDir()
}

// RequestImageUpdate 写入 data/.image_update_request 触发宿主机自动部署。
func RequestImageUpdate(dataDir string) error {
	if dataDir == "" {
		dataDir = "data"
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, ".image_update_request"), []byte("requested\n"), 0o644)
}

// AcquirePIDFile 创建单实例 PID 文件锁并返回释放闭包。
func AcquirePIDFile(pidFile string) (func(), error) {
	if pidFile == "" {
		pidFile = os.Getenv("SAKURA_PID_FILE")
		if pidFile == "" {
			pidFile = filepath.Join(".sakura", "bot.pid")
		}
	}
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		return nil, err
	}
	if raw, err := os.ReadFile(pidFile); err == nil {
		if oldPID, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil && oldPID > 0 && oldPID != os.Getpid() {
			if processAlive(oldPID) {
				return nil, fmt.Errorf("Sakura-Bot 已在运行 (PID: %d, pidFile: %s)", oldPID, pidFile)
			}
		}
	}
	myPID := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(pidFile, []byte(myPID+"\n"), 0o644); err != nil {
		return nil, err
	}
	release := func() {
		if raw, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(raw)) == myPID {
			_ = os.Remove(pidFile)
		}
	}
	return release, nil
}
