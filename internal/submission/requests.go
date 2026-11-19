// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package submission

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/summary"
)

// RequestRepo 抽象跨 Bot 总结请求队列与通知队列仓储（由 *db.Manager 实现）。
type RequestRepo interface {
	FetchPendingRequests(ctx context.Context, limit int) ([]db.BotRequestRow, error)
	GetRequestByID(ctx context.Context, id int64) (*db.BotRequestRow, error)
	UpdateRequestStatus(ctx context.Context, id int64, status string, result map[string]any, errMsg string) error
	GetUserInfo(ctx context.Context, userID int64) (*db.UserRecord, error)
	EnqueueNotification(ctx context.Context, in db.NotificationInsert) (int64, error)
}

// SummaryGenerator 抽象总结生成执行器（由 *summary.Service 实现）。
type SummaryGenerator interface {
	GenerateChannelSummary(ctx context.Context, channelRef string, skipAdmins bool, summaryType string) summary.GenerateResult
}

// RequestDeps 配置跨 Bot 总结请求审批处理器。
type RequestDeps struct {
	Config    *config.Config
	Repo      RequestRepo
	BotClient func() mtproto.Messenger
	Generator SummaryGenerator
	Logger    *slog.Logger
}

// RequestService 负责轮询 request_queue 待办总结申请并处理管理员 confirm_summary_ / reject_summary_ 回调（对齐 mainbot_request_handler.py）。
type RequestService struct {
	deps RequestDeps
}

// NewRequestService 创建跨 Bot 请求审批服务。
func NewRequestService(deps RequestDeps) *RequestService {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &RequestService{deps: deps}
}

// CheckPendingRequests 轮询 pending 请求并向 ADMIN_LIST 推送审批卡片。
func (s *RequestService) CheckPendingRequests(ctx context.Context) {
	if s.deps.Repo == nil || s.deps.Config == nil {
		return
	}
	botCli := s.resolveBotClient()
	if botCli == nil {
		return
	}
	adminIDs := s.deps.Config.AdminIDs()
	if len(adminIDs) == 0 {
		return
	}

	pending, err := s.deps.Repo.FetchPendingRequests(ctx, 20)
	if err != nil || len(pending) == 0 {
		return
	}

	for _, req := range pending {
		if err := s.deps.Repo.UpdateRequestStatus(ctx, req.ID, "processing", nil, ""); err != nil {
			continue
		}
		msgText := s.buildAdminMessage(ctx, req)
		buttons := [][]mtproto.InlineButton{{
			{Text: "✅ 确认生成", Data: fmt.Sprintf("confirm_summary_%d", req.ID)},
			{Text: "❌ 拒绝", Data: fmt.Sprintf("reject_summary_%d", req.ID)},
		}}
		sentAny := false
		for _, adminID := range adminIDs {
			if _, serr := botCli.SendMessage(ctx, adminID, msgText, mtproto.SendOptions{
				ParseMode: "markdown",
				Buttons:   buttons,
			}); serr == nil {
				sentAny = true
			}
		}
		if !sentAny {
			_ = s.deps.Repo.UpdateRequestStatus(ctx, req.ID, "pending", nil, "")
		}
	}
}

// HandleCallback 处理管理员点击 confirm_summary_{id} 或 reject_summary_{id}。
func (s *RequestService) HandleCallback(ctx context.Context, ev mtproto.CallbackEvent) error {
	botCli := s.resolveBotClient()
	if botCli == nil || s.deps.Repo == nil {
		return nil
	}
	if s.deps.Config != nil && !s.deps.Config.IsAdmin(ev.SenderID) {
		return botCli.AnswerCallback(ctx, ev.QueryID, i18n.T("error.permission_denied", nil), true)
	}
	_ = botCli.AnswerCallback(ctx, ev.QueryID, "", false)

	data := ev.DataString()
	var action string
	var reqID int64
	switch {
	case strings.HasPrefix(data, "confirm_summary_"):
		action = "confirm"
		id, err := strconv.ParseInt(strings.TrimPrefix(data, "confirm_summary_"), 10, 64)
		if err != nil || id <= 0 {
			return nil
		}
		reqID = id
	case strings.HasPrefix(data, "reject_summary_"):
		action = "reject"
		id, err := strconv.ParseInt(strings.TrimPrefix(data, "reject_summary_"), 10, 64)
		if err != nil || id <= 0 {
			return nil
		}
		reqID = id
	default:
		return nil
	}

	reqRow, err := s.deps.Repo.GetRequestByID(ctx, reqID)
	if err != nil || reqRow == nil {
		return botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, "❌ 请求不存在或已过期", mtproto.SendOptions{})
	}

	if action == "reject" {
		_ = s.deps.Repo.UpdateRequestStatus(ctx, reqID, "rejected", map[string]any{"message": "管理员拒绝了该请求"}, "")
		_ = botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, fmt.Sprintf("❌ 已拒绝总结请求\n\n频道: %s\n请求ID: %d", reqRow.TargetChannel, reqID), mtproto.SendOptions{})
		s.notifyRequester(ctx, *reqRow, "❌ 您的总结请求已被管理员拒绝")
		return nil
	}

	if s.deps.Generator == nil {
		return botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, "❌ 总结服务未就绪", mtproto.SendOptions{})
	}
	_ = botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, fmt.Sprintf("⏳ 正在为频道生成总结...\n\n频道: %s\n请求ID: %d", reqRow.TargetChannel, reqID), mtproto.SendOptions{})

	res := s.deps.Generator.GenerateChannelSummary(ctx, reqRow.TargetChannel, true, "manual")
	if res.Success {
		_ = s.deps.Repo.UpdateRequestStatus(ctx, reqID, "completed", map[string]any{
			"message":       "总结生成成功",
			"summary_text":  res.SummaryText,
			"message_count": res.MessageCount,
			"channel_name":  res.ChannelName,
		}, "")
		doneMsg := fmt.Sprintf(
			"✅ 总结生成完成！\n\n📢 频道: %s\n📊 处理消息数: %d\n📝 请求ID: %d\n\n📋 总结预览:\n%s",
			res.ChannelName, res.MessageCount, reqID, truncateRunes(res.SummaryText, 200),
		)
		_ = botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, doneMsg, mtproto.SendOptions{})
		s.notifyRequester(ctx, *reqRow, fmt.Sprintf("✅ 总结已成功生成！\n\n频道: %s\n处理消息数: %d", res.ChannelName, res.MessageCount))
		return nil
	}

	errMsg := res.Error
	if errMsg == "" {
		errMsg = "未知错误"
	}
	_ = s.deps.Repo.UpdateRequestStatus(ctx, reqID, "failed", nil, errMsg)
	_ = botCli.EditMessage(ctx, ev.ChatID, ev.MessageID, "❌ 生成总结失败: "+errMsg, mtproto.SendOptions{})
	s.notifyRequester(ctx, *reqRow, "❌ 总结生成失败: "+errMsg)
	return nil
}

func (s *RequestService) buildAdminMessage(ctx context.Context, req db.BotRequestRow) string {
	userName := fmt.Sprintf("用户_%d", req.RequestedBy)
	if u, err := s.deps.Repo.GetUserInfo(ctx, req.RequestedBy); err == nil && u != nil {
		if u.Username != "" {
			userName = u.Username
		} else if u.FirstName != "" {
			userName = u.FirstName
		}
	}
	created := ""
	if !req.CreatedAt.IsZero() {
		created = req.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	return fmt.Sprintf(
		"📝 **新的总结请求**\n\n**请求ID**: `%d`\n**频道**: `%s`\n**请求者**: %s (ID: `%d`)\n**时间**: %s\n\n请确认是否为该频道生成总结？",
		req.ID, req.TargetChannel, userName, req.RequestedBy, created,
	)
}

func (s *RequestService) notifyRequester(ctx context.Context, req db.BotRequestRow, message string) {
	if req.RequestedBy <= 0 {
		return
	}
	_, _ = s.deps.Repo.EnqueueNotification(ctx, db.NotificationInsert{
		UserID:           req.RequestedBy,
		NotificationType: "request_result",
		Content: map[string]any{
			"request_id": req.ID,
			"channel_id": req.TargetChannel,
			"message":    message,
		},
	})
}

func (s *RequestService) resolveBotClient() mtproto.Messenger {
	if s.deps.BotClient != nil {
		return s.deps.BotClient()
	}
	return nil
}
