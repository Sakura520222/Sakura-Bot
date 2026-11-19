// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package forwarding

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

// MinFreeDiskSpace 是落盘缓存下载要求的最少可用磁盘空间（500 MiB，对齐 download_manager.py:MIN_FREE_SPACE）。
const MinFreeDiskSpace uint64 = 500 << 20

// DownloadManager 负责大文件与大媒体组的并发受控落盘缓存与清理（对齐 download_manager.py:DownloadManager）。
type DownloadManager struct {
	cacheDir string
	sem      chan struct{}
	freeFn   func(dir string) (uint64, error)
	logger   *slog.Logger
}

// NewDownloadManager 创建下载管理器（默认 data/cache，最多 2 路并发）。
func NewDownloadManager(cacheDir string, maxConcurrent int, logger *slog.Logger) *DownloadManager {
	dir := strings.TrimSpace(cacheDir)
	if dir == "" {
		dir = "data/cache"
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}
	if logger == nil {
		logger = slog.Default()
	}
	_ = os.MkdirAll(dir, 0o755)
	return &DownloadManager{
		cacheDir: dir,
		sem:      make(chan struct{}, maxConcurrent),
		freeFn:   availableDiskBytes,
		logger:   logger,
	}
}

// CheckDiskSpace 检查缓存目录所在磁盘剩余空间是否 >= 500 MiB（查询异常时放行以避免阻塞业务）。
func (m *DownloadManager) CheckDiskSpace() bool {
	_ = os.MkdirAll(m.cacheDir, 0o755)
	freeBytes, err := m.freeFn(m.cacheDir)
	if err != nil {
		m.logger.Warn("检查磁盘剩余空间失败，默认放行", "error", err)
		return true
	}
	if freeBytes < MinFreeDiskSpace {
		m.logger.Warn("磁盘剩余空间不足 500MB，拒绝下载大媒体", "free_mb", freeBytes/(1024*1024))
		return false
	}
	return true
}

// DownloadMedia 将单条消息的媒体下载至 data/cache/{forwardID}/{filename}。
func (m *DownloadManager) DownloadMedia(
	ctx context.Context,
	client mtproto.Messenger,
	msg *mtproto.IncomingMessage,
	index int,
	forwardID string,
) (string, error) {
	if msg == nil || msg.Media == nil {
		return "", fmt.Errorf("message has no media")
	}
	if !m.CheckDiskSpace() {
		return "", fmt.Errorf("insufficient disk space (<500MB)")
	}

	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	subDir := filepath.Join(m.cacheDir, sanitizePathSegment(forwardID))
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		return "", err
	}
	fileName := buildMediaFilename(msg, index)
	destPath := filepath.Join(subDir, fileName)
	if info, err := os.Stat(destPath); err == nil && !info.IsDir() && info.Size() > 0 {
		return destPath, nil
	}
	if _, err := client.DownloadMediaToFile(ctx, msg.Media, destPath, 0); err != nil {
		_ = os.Remove(destPath)
		return "", err
	}
	return destPath, nil
}

// DownloadGroup 顺序下载媒体组内全部含附件的消息文件。
func (m *DownloadManager) DownloadGroup(
	ctx context.Context,
	client mtproto.Messenger,
	msgs []*mtproto.IncomingMessage,
	forwardID string,
) ([]string, error) {
	paths := make([]string, 0, len(msgs))
	for i, msg := range msgs {
		if msg == nil || msg.Media == nil {
			continue
		}
		p, err := m.DownloadMedia(ctx, client, msg, i, forwardID)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// CleanupCache 删除指定 forwardID 的临时缓存子目录。
func (m *DownloadManager) CleanupCache(forwardID string) {
	sub := sanitizePathSegment(forwardID)
	if sub == "" || sub == "." || sub == ".." {
		return
	}
	_ = os.RemoveAll(filepath.Join(m.cacheDir, sub))
}

func buildMediaFilename(msg *mtproto.IncomingMessage, index int) string {
	if msg.Media != nil && strings.TrimSpace(msg.Media.FileName) != "" {
		return fmt.Sprintf("%03d_%s", index, sanitizePathSegment(msg.Media.FileName))
	}
	ext := inferExtension(msg.Media)
	return fmt.Sprintf("%03d_media%s", index, ext)
}

func inferExtension(att *mtproto.MediaAttachment) string {
	if att == nil {
		return ".bin"
	}
	if att.Kind == "photo" {
		return ".jpg"
	}
	if att.MimeType != "" {
		if exts, err := mime.ExtensionsByType(att.MimeType); err == nil && len(exts) > 0 {
			return exts[0]
		}
		switch {
		case strings.HasPrefix(att.MimeType, "video/"):
			return ".mp4"
		case strings.HasPrefix(att.MimeType, "audio/"):
			return ".mp3"
		case strings.HasPrefix(att.MimeType, "image/png"):
			return ".png"
		case strings.HasPrefix(att.MimeType, "image/"):
			return ".jpg"
		}
	}
	return ".bin"
}

func sanitizePathSegment(s string) string {
	s = strings.TrimSpace(s)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_")
	return replacer.Replace(s)
}
