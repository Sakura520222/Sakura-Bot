// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/summary"
)

const (
	maxRunningSummaries  = 4
	maxFinishedSummaries = 64
)

type summaryJob struct {
	ID     string          `json:"job_id"`
	Status string          `json:"status"`
	Result *summaryOutcome `json:"result,omitempty"`
}

type summaryOutcome struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type summaryJobs struct {
	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger
	run    func(context.Context, string, bool, string) summary.GenerateResult

	mu       sync.Mutex
	wg       sync.WaitGroup
	active   map[string]string
	jobs     map[string]summaryJob
	finished []string
}

func newSummaryJobs(ctx context.Context, logger *slog.Logger, run func(context.Context, string, bool, string) summary.GenerateResult) *summaryJobs {
	ctx, cancel := context.WithCancel(ctx)
	return &summaryJobs{
		ctx: ctx, cancel: cancel, logger: logger, run: run,
		active: make(map[string]string), jobs: make(map[string]summaryJob),
	}
}

func (q *summaryJobs) start(channel string) (summaryJob, error) {
	channel = config.NormalizeChannelID(channel)
	key := strings.ToLower(channel)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ctx.Err() != nil {
		return summaryJob{}, errors.New("后台总结服务正在关闭")
	}
	if id := q.active[key]; id != "" {
		return q.jobs[id], nil
	}
	if len(q.active) >= maxRunningSummaries {
		return summaryJob{}, errors.New("后台总结任务已满，请稍后重试")
	}
	job := summaryJob{ID: rand.Text(), Status: "running"}
	q.active[key] = job.ID
	q.jobs[job.ID] = job
	q.wg.Add(1)
	go q.execute(job, channel, key)
	return job, nil
}

func (q *summaryJobs) execute(job summaryJob, channel, key string) {
	defer q.wg.Done()
	started := time.Now()
	q.logger.Info("WebUI 总结任务开始", "job_id", job.ID, "channel", channel)
	result := func() (result summary.GenerateResult) {
		defer func() {
			if value := recover(); value != nil {
				q.logger.Error("WebUI 总结任务异常", "job_id", job.ID, "panic", value, "stack", string(debug.Stack()))
				result = summary.GenerateResult{Error: "总结任务异常，请通过任务编号查看服务日志"}
			}
		}()
		return q.run(q.ctx, channel, false, "manual")
	}()
	outcome := summaryResponse(result)
	job.Result = &outcome
	if result.Success {
		job.Status = "succeeded"
		q.logger.Info("WebUI 总结任务完成", "job_id", job.ID, "channel", channel,
			"messages", result.MessageCount, "elapsed", time.Since(started))
	} else {
		job.Status = "failed"
		q.logger.Warn("WebUI 总结任务失败", "job_id", job.ID, "channel", channel,
			"error", outcome.Message, "elapsed", time.Since(started))
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.active, key)
	q.jobs[job.ID] = job
	q.finished = append(q.finished, job.ID)
	if len(q.finished) > maxFinishedSummaries {
		delete(q.jobs, q.finished[0])
		q.finished = slices.Delete(q.finished, 0, 1)
	}
}

func (q *summaryJobs) get(id string) (summaryJob, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.jobs[id]
	return job, ok
}

func (q *summaryJobs) stop() {
	q.mu.Lock()
	q.cancel()
	q.mu.Unlock()
	q.wg.Wait()
}

func (s *Server) handleSummaryJob(w http.ResponseWriter, r *http.Request) {
	if s.summaryJobs != nil {
		if job, ok := s.summaryJobs.get(r.PathValue("id")); ok {
			w.Header().Set("Cache-Control", "no-store")
			writeEnvelopeOK(w, job, "")
			return
		}
	}
	writeHTTPError(w, http.StatusNotFound, "找不到总结任务。服务可能已重启，或任务记录已过期")
}

func summaryResponse(res summary.GenerateResult) summaryOutcome {
	if !res.Success {
		message := res.Error
		if message == "" {
			message = "当前时间窗口内无新消息或生成失败"
		}
		return summaryOutcome{Message: message}
	}
	preview := res.SummaryText
	length := utf8.RuneCountInString(preview)
	if length > 300 {
		preview = string([]rune(preview)[:300]) + "..."
	}
	return summaryOutcome{
		Success: true,
		Message: "总结生成并推送成功",
		Data: map[string]any{
			"channel_name":    res.ChannelName,
			"message_count":   res.MessageCount,
			"summary_preview": preview,
			"summary_length":  length,
		},
	}
}
