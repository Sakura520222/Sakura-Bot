// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mainbot

import (
	"context"
	"log/slog"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/poll"
	"github.com/Sakura520222/Sakura-Bot/internal/process"
	"github.com/Sakura520222/Sakura-Bot/internal/scheduler"
	"github.com/Sakura520222/Sakura-Bot/internal/submission"
	"github.com/Sakura520222/Sakura-Bot/internal/summary"
)

func registerSummaryJobs(tasks *scheduler.Scheduler, cfg *config.Config, summaries *summary.Service, logger *slog.Logger) {
	const group = "summaries"
	tasks.RemoveGroup(group)
	for _, channel := range cfg.Channels() {
		schedule := cfg.ChannelSchedule(channel)
		var spec *scheduler.CronSpec
		var err error
		if schedule.Frequency == "daily" {
			spec, err = scheduler.DailyCron(schedule.Hour, schedule.Minute)
		} else {
			spec, err = scheduler.WeeklyCron(schedule.Days, schedule.Hour, schedule.Minute)
		}
		if err != nil {
			logger.Warn("跳过无效的总结计划", "channel", channel, "error", err)
			continue
		}
		tasks.AddCron("summary_job_"+channel, group, spec, true, func(ctx context.Context) {
			result := summaries.GenerateChannelSummary(ctx, channel, false, schedule.Frequency)
			if !result.Success && ctx.Err() == nil {
				logger.Warn("定时总结未生成", "channel", channel, "reason", result.Error)
			}
		})
	}
}

func registerJobs(
	tasks *scheduler.Scheduler, cfg *config.Config, manager *db.Manager,
	summaries *summary.Service, polls *poll.Service, requests *submission.RequestService,
	reviews *submission.ReviewService, assistant *process.QAManager, logger *slog.Logger,
) {
	registerSummaryJobs(tasks, cfg, summaries, logger)
	tasks.AddInterval("check_requests", "communication", 30*time.Second, 30*time.Second, false, requests.CheckPendingRequests)
	tasks.AddInterval("check_submissions", "communication", 30*time.Second, 30*time.Second, false, reviews.CheckPendingSubmissions)
	tasks.AddInterval("qa_bot_health_check", "assistant", time.Minute, time.Minute, false, func(ctx context.Context) {
		healthy, restart, message := assistant.CheckHealth()
		if !healthy {
			logger.Warn("助手健康检查失败", "message", message)
		}
		if restart && ctx.Err() == nil {
			if result := assistant.Start(); !result.Success {
				logger.Warn("恢复助手失败", "message", result.Message)
			}
		}
	})
	daily, _ := scheduler.DailyCron(3, 0)
	tasks.AddCron("cleanup_poll_regenerations", "maintenance", daily, false, func(ctx context.Context) {
		polls.RegenStore().CleanupOld(30)
		if _, err := manager.CleanupPollRegenerations(ctx, 30); err != nil {
			logger.Warn("清理投票重生成记录失败", "error", err)
		}
	})
}
