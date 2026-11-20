// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package botcmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/process"
	"github.com/Sakura520222/Sakura-Bot/internal/version"
)

// imageDeployWatchTimeout 是提交请求的进程等待宿主机部署结果的上限
// （覆盖镜像拉取重试与慢网络；容器被成功重建时本协程随旧进程终止）。
const imageDeployWatchTimeout = 15 * time.Minute

// watchImageDeployResult 在提交更新请求后轮询宿主机部署结果：
// 失败、超时与"已是最新无需重建"由本进程回报；容器被重建时本协程随旧进程
// 终止，由新进程启动时的 ReportPendingImageDeploy 兜底回报（两端互斥：
// 部署结果在旧容器完全停止后才落盘，旧进程不可能读到重建场景的结果）。
func (r *Router) watchImageDeployResult(dataDir string, chatID int64, requestedAt time.Time) {
	if chatID == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), imageDeployWatchTimeout)
		defer cancel()
		result, err := awaitImageDeployResult(ctx, dataDir, requestedAt)
		if errors.Is(err, context.DeadlineExceeded) {
			process.ClearImageUpdatePending(dataDir)
			r.sendImageDeployText(chatID,
				"等待宿主机部署结果超时，请检查宿主机 sakura-image-deploy 服务与 logs/image-deploy.log")
			return
		}
		if result == nil {
			return
		}
		// 走到这里说明容器未被重建（版本无变化），由本进程收尾并回报。
		process.ClearImageUpdatePending(dataDir)
		process.ClearImageDeployResult(dataDir)
		r.sendImageDeployText(chatID, FormatImageDeployReport(result, deployLogTail(dataDir), version.Version, false))
	}()
}

// ReportPendingImageDeploy 在容器被部署重建后的新进程启动时，回报上一次
// /update 的部署结果。由 internal/mainbot 在启动通知后调用；无待回报记录时
// 立即返回。logs/image-deploy.log 经 bind mount 对容器可见，失败时附日志尾部。
func ReportPendingImageDeploy(ctx context.Context, bot mtproto.Messenger, dataDir string, logger *slog.Logger) {
	pending, err := process.ReadImageUpdatePending(dataDir)
	if err != nil {
		if logger != nil {
			logger.Warn("读取宿主机部署回报记录失败", "error", err)
		}
		return
	}
	if pending == nil || pending.ChatID == 0 {
		return
	}
	result, err := awaitImageDeployResult(ctx, dataDir, pending.RequestedAt)
	if err != nil && logger != nil {
		logger.Warn("等待宿主机部署结果失败", "chat_id", pending.ChatID, "error", err)
	}
	text := ""
	switch {
	case result != nil:
		text = FormatImageDeployReport(result, deployLogTail(dataDir), version.Version, true)
	case ctx.Err() != nil:
		// 典型场景：宿主机部署器仍是旧版单元（不写结果文件）。容器既已被
		// 重建，提示刷新部署器即可获得后续回报。
		text = "Bot 已在 /update 部署请求后重启，但宿主机部署器未回报详细结果；" +
			"请在宿主机运行 bash deploy/install-deployer.sh 更新部署器"
	}
	if text != "" {
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if _, serr := bot.SendMessage(sendCtx, pending.ChatID, text,
			mtproto.SendOptions{ParseMode: "plain", NoWebpage: true}); serr != nil && logger != nil {
			logger.Warn("发送宿主机部署回报失败", "chat_id", pending.ChatID, "error", serr)
		}
		cancel()
	}
	process.ClearImageUpdatePending(dataDir)
	process.ClearImageDeployResult(dataDir)
}

// awaitImageDeployResult 轮询部署结果，忽略早于 requestedAt 的陈旧结果。
func awaitImageDeployResult(ctx context.Context, dataDir string, requestedAt time.Time) (*process.ImageDeployResult, error) {
	for {
		result, err := process.ReadImageDeployResult(dataDir)
		if err != nil {
			return nil, err
		}
		if result != nil && (requestedAt.IsZero() || result.FinishedAt.After(requestedAt)) {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// FormatImageDeployReport 生成宿主机部署结果回报文案（纯文本，避免日志内容
// 破坏 Markdown 解析）。rebuilt 区分"容器已被重建（新版本生效）"与"版本无
// 变化"两种成功语义。
func FormatImageDeployReport(result *process.ImageDeployResult, logTail, currentVersion string, rebuilt bool) string {
	revision := result.Revision
	if len(revision) > 7 {
		revision = revision[:7]
	}
	suffix := ""
	if currentVersion != "" {
		suffix += " v" + currentVersion
	}
	if revision != "" && revision != "unknown" {
		suffix += "（" + revision + "）"
	}
	switch result.Status {
	case "ok":
		if rebuilt {
			return "宿主机镜像部署完成，当前运行" + suffix
		}
		return "宿主机部署完成，当前已是最新版本" + suffix
	case "failed":
		text := fmt.Sprintf("宿主机部署失败（退出码 %d）", result.ExitCode)
		if logTail != "" {
			text += "\n\n最近日志：\n" + logTail
		} else {
			text += "\n\n完整日志见宿主机 logs/image-deploy.log"
		}
		return text
	default:
		return "宿主机部署结果异常（status=" + result.Status + "），请查看宿主机 logs/image-deploy.log"
	}
}

// sendImageDeployText 向发起 /update 的对话发送部署回报文本。
func (r *Router) sendImageDeployText(chatID int64, text string) {
	bot := r.resolveBot()
	if bot == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := bot.SendMessage(ctx, chatID, text, mtproto.SendOptions{ParseMode: "plain", NoWebpage: true}); err != nil {
		if r.deps.Logger != nil {
			r.deps.Logger.Warn("发送宿主机部署回报失败", "chat_id", chatID, "error", err)
		}
	}
}

// deployLogTailRunes 是部署回报附带日志的长度上限（按 rune 计）。
const deployLogTailRunes = 1500

// deployLogTail 读取宿主机部署日志末尾（logs 目录与 data 目录同级，容器内
// 经 bind mount 可见）；读取失败返回空串。日志跨多次部署追加写入，必须
// 保留末尾才能反映本次部署的失败原因。
func deployLogTail(dataDir string) string {
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(dataDir), "logs", "image-deploy.log"))
	if err != nil {
		return ""
	}
	content := []rune(strings.TrimSpace(string(raw)))
	if len(content) <= deployLogTailRunes {
		return string(content)
	}
	return "..." + string(content[len(content)-deployLogTailRunes:])
}
