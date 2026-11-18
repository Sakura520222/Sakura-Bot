// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package summary

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/tg"
)

// SubscriberRepo 抽象总结订阅推送所需的数据库能力（由 *db.Manager 实现）。
type SubscriberRepo interface {
	ListChannelSummarySubscribers(ctx context.Context, channelID string) ([]int64, error)
	RemoveBlockedSubscriber(ctx context.Context, userID int64, channelID string) error
}

// BotAPISender 抽象 HTTP Bot API 消息发送（由 *tg.DirectBotAPI 实现）。
type BotAPISender interface {
	SendMessage(ctx context.Context, token string, req tg.DirectSendMessageParams) (int64, error)
}

// PushHandler 通过 QA Bot Token 向订阅了频道周报的用户推送新总结通知（对齐 mainbot_push_handler.py:MainBotPushHandler）。
type PushHandler struct {
	repo        SubscriberRepo
	botAPI      BotAPISender
	tokenGetter func() string
	logger      *slog.Logger
}

// NewPushHandler 创建主 Bot 总结订阅推送器。
func NewPushHandler(repo SubscriberRepo, botAPI BotAPISender, tokenGetter func() string, logger *slog.Logger) *PushHandler {
	if botAPI == nil {
		botAPI = tg.NewDirectBotAPI()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PushHandler{
		repo:        repo,
		botAPI:      botAPI,
		tokenGetter: tokenGetter,
		logger:      logger,
	}
}

// NotifySummarySubscribers 向该频道的所有活跃总结订阅者发送 HTML 预览，并在用户拉黑/销号时自动退订。
func (p *PushHandler) NotifySummarySubscribers(ctx context.Context, channelID, channelName, summaryText string) int {
	if p == nil || p.repo == nil {
		return 0
	}
	token := ""
	if p.tokenGetter != nil {
		token = strings.TrimSpace(p.tokenGetter())
	}
	if token == "" {
		p.logger.Warn("未配置 QA_BOT_TOKEN，跳过总结订阅推送")
		return 0
	}

	subscribers, err := p.repo.ListChannelSummarySubscribers(ctx, channelID)
	if err != nil {
		p.logger.Error("读取频道订阅者失败", "channel", channelID, "error", err)
		return 0
	}
	if len(subscribers) == 0 {
		return 0
	}

	preview := truncateRunes(summaryText, 300)
	msgText := fmt.Sprintf(
		"📬 <b>新总结通知</b>\n\n频道 %s 有新的总结发布了！\n\n<b>总结预览</b>:\n%s\n\n💡 使用 <code>/mysubscriptions</code> 查看您的订阅\n💡 使用 <code>/unsubscribe &lt;频道链接&gt;</code> 取消订阅",
		html.EscapeString(channelName),
		html.EscapeString(preview),
	)

	successCount := 0
	for _, uid := range subscribers {
		_, serr := p.botAPI.SendMessage(ctx, token, tg.DirectSendMessageParams{
			ChatID:    uid,
			Text:      msgText,
			ParseMode: "HTML",
		})
		if serr != nil {
			errLower := strings.ToLower(serr.Error())
			if strings.Contains(errLower, "bot was blocked by the user") || strings.Contains(errLower, "user is deactivated") {
				p.logger.Warn("订阅用户已阻止或停用账号，自动移除订阅", "user_id", uid, "channel", channelID)
				_ = p.repo.RemoveBlockedSubscriber(ctx, uid, channelID)
			} else {
				p.logger.Error("向订阅者发送总结推送失败", "user_id", uid, "error", serr)
			}
			continue
		}
		successCount++
	}
	p.logger.Info("总结订阅推送完成", "channel", channelName, "success", successCount, "total", len(subscribers))
	return successCount
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "..."
}
