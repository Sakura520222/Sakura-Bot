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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// ImageUpdatePending 记录 /update 的发起对话，供部署结束后的回报路由使用。
type ImageUpdatePending struct {
	ChatID      int64
	RequestedAt time.Time
}

// ImageDeployResult 是宿主机部署器在部署结束后写入 data/.image_deploy_result 的结果。
type ImageDeployResult struct {
	Status     string // "ok" 或 "failed"
	ExitCode   int
	Revision   string
	FinishedAt time.Time
}

const (
	imageUpdatePendingFile = ".image_update_pending"
	imageDeployResultFile  = ".image_deploy_result"
)

func imageUpdateStatePath(dataDir, name string) string {
	if dataDir == "" {
		dataDir = "data"
	}
	return filepath.Join(dataDir, name)
}

// SaveImageUpdatePending 写入 data/.image_update_pending，记录等待部署回报的对话，
// 返回本次请求时间（用于过滤上一轮部署遗留的陈旧结果）。
func SaveImageUpdatePending(dataDir string, chatID int64) (time.Time, error) {
	requestedAt := time.Now().UTC()
	path := imageUpdateStatePath(dataDir, imageUpdatePendingFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return time.Time{}, err
	}
	content := "chat_id=" + strconv.FormatInt(chatID, 10) +
		"\nrequested_at=" + requestedAt.Format(time.RFC3339) + "\n"
	return requestedAt, os.WriteFile(path, []byte(content), 0o644)
}

// ReadImageUpdatePending 读取待回报记录；文件不存在时返回 (nil, nil)。
func ReadImageUpdatePending(dataDir string) (*ImageUpdatePending, error) {
	raw, err := os.ReadFile(imageUpdateStatePath(dataDir, imageUpdatePendingFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pending := &ImageUpdatePending{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "chat_id":
			pending.ChatID, _ = strconv.ParseInt(value, 10, 64)
		case "requested_at":
			pending.RequestedAt, _ = time.Parse(time.RFC3339, value)
		}
	}
	return pending, nil
}

// ClearImageUpdatePending 删除待回报记录（幂等）。
func ClearImageUpdatePending(dataDir string) {
	_ = os.Remove(imageUpdateStatePath(dataDir, imageUpdatePendingFile))
}

// ReadImageDeployResult 解析宿主机部署器写入的 key=value 部署结果；
// 文件不存在时返回 (nil, nil)。
func ReadImageDeployResult(dataDir string) (*ImageDeployResult, error) {
	raw, err := os.ReadFile(imageUpdateStatePath(dataDir, imageDeployResultFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := &ImageDeployResult{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "status":
			result.Status = value
		case "exit_code":
			result.ExitCode, _ = strconv.Atoi(value)
		case "revision":
			result.Revision = value
		case "finished_at":
			result.FinishedAt, _ = time.Parse(time.RFC3339, value)
		}
	}
	return result, nil
}

// ClearImageDeployResult 删除部署结果文件（幂等）。
func ClearImageDeployResult(dataDir string) {
	_ = os.Remove(imageUpdateStatePath(dataDir, imageDeployResultFile))
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
