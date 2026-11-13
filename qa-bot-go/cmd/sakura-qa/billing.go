package main

import (
	"context"
	"fmt"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"time"
)

func (q *quotaManager) ReserveMessage(ctx context.Context, id string, user int64) contracts.QuotaCheckResult {
	return q.ReserveTurnMessage(ctx, id, user, "")
}
func (q *quotaManager) ReserveTurnMessage(ctx context.Context, id string, user int64, turn string) contracts.QuotaCheckResult {
	limits, err := q.cfg.EffectiveQuota()
	if err != nil {
		return quotaSystemError()
	}
	settings, err := q.cfg.AssistantSettings()
	if err != nil {
		return quotaSystemError()
	}
	if user < 0 {
		chatID, dailyLimit, authorized := contracts.AnonymousQuota(ctx)
		if !authorized || chatID != user {
			return contracts.QuotaCheckResult{Message: "匿名群组身份尚未获得助手准入授权，请联系管理员开启本群匿名身份策略。"}
		}
		channel := contracts.AnonymousChannel(ctx)
		currentLimit := 0
		for _, rule := range settings.Comments {
			if rule.Channel == channel && rule.Enabled && rule.AnonymousEnabled {
				currentLimit = rule.AnonymousDailyLimit
				break
			}
		}
		if currentLimit <= 0 {
			return contracts.QuotaCheckResult{Message: "本群匿名助手授权已关闭，请使用真实账号提问。"}
		}
		limits.UserDaily = int64(min(dailyLimit, currentLimit))
	}
	result, err := q.db.ReserveMessageOnDay(ctx, id, user, limits, user > 0 && q.cfg.IsAdmin(user), settings.BillingDay(time.Now()), turn)
	if err != nil {
		q.log.Error("消息额度预留失败", "error", err)
		return quotaSystemError()
	}
	return result
}
func (q *quotaManager) FinishReservation(ctx context.Context, id string, success bool) {
	if err := q.db.FinishReservation(ctx, id, success); err != nil {
		q.log.Error("消息额度结算失败，保留记录供恢复", "reservation", id, "error", err)
	}
}

func (q *quotaManager) Checkin(ctx context.Context, user int64) string {
	if user <= 0 {
		return "本群匿名管理员使用群组共享额度。请使用真实账号签到获取个人积分。"
	}
	s, e := q.cfg.AssistantSettings()
	if e != nil || !s.CheckinEnabled {
		return "签到暂不可用。"
	}
	n, added, e := q.db.Checkin(ctx, user, s.BillingDay(time.Now()))
	if e != nil {
		return "签到失败，请稍后重试。"
	}
	if !added {
		return "今天已经签到过了。"
	}
	return fmt.Sprintf("签到成功，获得 %d.%d 积分。", n/10, n%10)
}
func (q *quotaManager) BillingStatus(ctx context.Context, user int64) string {
	s, e := q.cfg.AssistantSettings()
	if e != nil {
		return "计费配置无效"
	}
	day := s.BillingDay(time.Now())
	units, e := q.db.Points(ctx, user)
	if e != nil {
		return "积分余额暂不可用"
	}
	used, total, e := q.db.BillingUsage(ctx, user, day)
	if e != nil {
		return "用量暂不可用"
	}
	return fmt.Sprintf("\n当日消息：%d；积分：%d.%d；全站消息：%d\n每日按 %s 00:00 重置。\n", used, units/10, units%10, total, s.Timezone)
}
