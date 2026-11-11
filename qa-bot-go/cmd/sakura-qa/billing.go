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
	result, err := q.db.ReserveMessageOnDay(ctx, id, user, limits, q.cfg.IsAdmin(user), settings.BillingDay(time.Now()), turn)
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
