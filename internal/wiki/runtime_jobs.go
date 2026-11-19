// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package wiki

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

func (r *Runtime) runJob(parent context.Context, job *JobRecord) {
	ctx, cancel := context.WithCancelCause(parent)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				heartbeat, stop := context.WithTimeout(ctx, 10*time.Second)
				err := r.Store.HeartbeatJob(heartbeat, job)
				stop()
				if errors.Is(err, ErrLeaseLost) {
					cancel(err)
					return
				}
				if err != nil && ctx.Err() == nil {
					r.logger.Warn("Wiki 任务续租延后", "job", job.ID, "error", err)
				}
			}
		}
	}()
	defer func() {
		cancel(nil)
		<-heartbeatDone
	}()

	err := r.advanceJob(ctx, job)
	switch {
	case err == nil, errors.Is(err, ErrLeaseLost), errors.Is(context.Cause(ctx), ErrLeaseLost):
		return
	case parent.Err() != nil:
		// Preserve the last checkpoint and relinquish the lease for a clean restart.
		finalCtx, stop := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer stop()
		job.Progress["active_requests"] = map[string]any{}
		job.Progress["reserved_tokens"] = 0
		err = r.Store.FinishJob(finalCtx, job, "pending", "", 0)
	default:
		err = r.handleJobError(ctx, job, err)
	}
	if err != nil && !errors.Is(err, ErrLeaseLost) {
		r.logger.Warn("Wiki 任务进度暂未保存，等待租约恢复", "job", job.ID, "error", err)
	}
}

func (r *Runtime) advanceJob(ctx context.Context, job *JobRecord) error {
	channel, err := r.Store.GetChannel(ctx, job.ChannelID)
	if err != nil {
		return err
	}
	if channel == nil {
		return ErrLeaseLost
	}
	if job.Phase == "collect" {
		if err := r.collectJob(ctx, job, channel); err != nil {
			return err
		}
		channel, err = r.Store.GetChannel(ctx, job.ChannelID)
		if err != nil {
			return err
		}
		if channel == nil {
			return ErrLeaseLost
		}
		job.Progress["source_generation"] = channel.SourceGeneration
		if err := r.Store.CheckpointJob(ctx, job, "extract", false); err != nil {
			return err
		}
	}
	if job.Phase == "extract" {
		if err := r.Engine.Generate(ctx, job, channel); err != nil {
			return err
		}
	}
	policy := mapString(job.Cursor, "review_policy")
	if job.Phase == "ready" && (policy == "" || (policy == SkippedAIReview) == channel.Config.AIReviewEnabled) {
		if err := r.Store.CheckpointJob(ctx, job, "review", false); err != nil {
			return err
		}
	}
	if job.Phase == "assemble" {
		if err := r.Store.ReportJob(ctx, job, "正在整理文章与知识地图", false, map[string]any{"stage": "assemble"}); err != nil {
			return err
		}
		if err := r.Engine.Assemble(ctx, job, channel); err != nil {
			return err
		}
	}
	if job.Phase == "review" {
		if err := r.Engine.Review(ctx, job, channel); err != nil {
			return err
		}
	}
	if job.Phase != "ready" {
		return fmt.Errorf("unknown Wiki job phase %q", job.Phase)
	}
	return r.completeJob(ctx, job, channel)
}

func (r *Runtime) collectJob(ctx context.Context, job *JobRecord, channel *ChannelRecord) error {
	switch job.Kind {
	case "backfill", "reconcile":
		if err := r.Collector.ImportLocal(ctx, job, channel); err != nil {
			return err
		}
		if err := r.Collector.FetchHistory(ctx, job, channel); err != nil {
			return err
		}
		if err := r.Store.WithTx(ctx, func(tx *sql.Tx) error {
			if err := r.Store.FenceJob(ctx, tx, job); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET reconciled_at=UTC_TIMESTAMP(6) WHERE id=?", channel.ID)
			return err
		}); err != nil {
			return err
		}
		return r.Collector.Reconcile(ctx, job, channel)
	case "update":
		if _, hasCursor := job.Cursor["history_id"]; !hasCursor {
			var maxID int64
			if err := r.Store.DB.QueryRowContext(ctx, `
				SELECT COALESCE(MAX(message_id),0) FROM wiki_sources
				WHERE channel_id=? AND source_type='message'
			`, channel.ID).Scan(&maxID); err != nil {
				return err
			}
			job.Cursor["history_id"] = maxID
		}
		return r.Collector.FetchHistory(ctx, job, channel)
	}
	return nil
}

func (r *Runtime) completeJob(ctx context.Context, job *JobRecord, channel *ChannelRecord) error {
	pageEdit := job.Kind == "page_edit"
	if job.Kind == "backfill" || job.Kind == "reconcile" {
		if err := r.Collector.UpdateCoverage(ctx, job, "accessible", "all_currently_accessible_messages_enumerated"); err != nil {
			return err
		}
	}
	fresh, err := r.Store.GetChannel(ctx, channel.ID)
	if err != nil {
		return err
	}
	if fresh == nil {
		return ErrLeaseLost
	}
	generation := mapInt64(job.Progress, "source_generation")
	if !pageEdit {
		job.Progress["newer_sources_pending"] = fresh.SourceGeneration != generation
	}
	published := mapBool(job.Progress, "published")
	if !published && fresh.Config.PublicationMode == "auto" &&
		mapBool(job.Progress, "review_complete") && mapInt(job.Progress, "pages") > 0 {
		activity := "正在发布频道内容"
		if pageEdit {
			activity = "正在发布词条修订"
		}
		if err := r.Store.ReportJob(ctx, job, activity, false, map[string]any{"stage": "publish"}); err != nil {
			return err
		}
		publication, err := r.Engine.Knowledge.Publish(ctx, channel.ID, job.ID, "wiki", WithRunningJob(job))
		if err != nil {
			return err
		}
		published = true
		job.Progress["publication_id"] = publication
		job.Progress["published"] = true
	}
	activity := "构建完成"
	switch {
	case pageEdit && published:
		activity = "词条修订已发布"
	case published:
		activity = "构建完成，内容已发布"
	case mapInt(job.Progress, "review_rejections") > 0:
		activity = "部分修订未通过来源审校，请在草稿中处理"
	case pageEdit && mapBool(job.Progress, "review_skipped"):
		activity = "词条修订等待发布"
	case pageEdit:
		activity = "词条修订已通过审校，等待发布"
	}
	job.Progress["activity"] = activity
	job.Progress["stage"] = "done"
	job.Progress["active_requests"] = map[string]any{}
	if !pageEdit {
		if err := r.Store.WithTx(ctx, func(tx *sql.Tx) error {
			if err := r.Store.FenceJob(ctx, tx, job); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET dirty_at=NULL WHERE id=? AND source_generation=?", channel.ID, generation)
			return err
		}); err != nil {
			return err
		}
	}
	if err := r.Store.CompleteJob(ctx, job, published); err != nil {
		return err
	}
	if r.Avatar != nil && published && !pageEdit {
		avatarCtx, cancel := context.WithTimeout(ctx, AvatarFetchTimeout)
		defer cancel()
		if _, _, _, err := r.Avatar.Refresh(avatarCtx, channel.ID); err != nil && ctx.Err() == nil {
			r.logger.Info("Wiki 头像刷新延后", "channel", channel.ID)
		}
	}
	return nil
}

func (r *Runtime) handleJobError(ctx context.Context, job *JobRecord, cause error) error {
	detail, code, delay := jobFailure(cause, job.Attempts)
	job.Progress["error_detail"] = detail
	job.Progress["activity"] = detail
	job.Progress["active_requests"] = map[string]any{}
	job.Progress["reserved_tokens"] = 0
	switch {
	case errors.Is(cause, ErrBudgetExceeded):
		job.Progress["activity"] = "任务预算不足，已保留当前进度"
		return r.Store.FinishJob(ctx, job, "paused", "token_budget_reached", 0)
	case errors.Is(cause, ErrPublicationAccessUnverified):
		return r.Store.FinishJob(ctx, job, "failed", "publication_access_unverified", 0)
	case errors.Is(cause, ErrPageEditConflict) ||
		(job.Kind == "page_edit" && errors.Is(cause, errPublicationEvidence)):
		return r.Store.FinishJob(ctx, job, "failed", "page_edit_conflict", 0)
	case errors.Is(cause, errPublicationEvidence):
		delete(job.Cursor, "page_after")
		job.Cursor["revisions"] = map[string]any{}
		delete(job.Cursor, "dropped_pages")
		job.Progress["assembled_pages"] = 0
		job.Progress["pages"] = 0
		job.Progress["activity"] = "来源有更新，正在刷新文章"
		err := r.Store.WithTx(ctx, func(tx *sql.Tx) error {
			if err := r.Store.FenceJob(ctx, tx, job); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `
				INSERT IGNORE INTO wiki_dirty_pages SELECT ?, id
				FROM wiki_pages WHERE channel_id=? AND hidden=FALSE
			`, job.ChannelID, job.ChannelID)
			return err
		})
		if err != nil {
			return err
		}
		if err := r.Store.CheckpointJob(ctx, job, "assemble", false); err != nil {
			return err
		}
		delay = 1
	case errors.Is(cause, errPublicationReview), errors.Is(cause, errPublicationReviewReenabled):
		job.Progress["review_complete"] = false
		if err := r.Store.CheckpointJob(ctx, job, "review", false); err != nil {
			return err
		}
	}
	if job.Phase == "collect" {
		job.Progress["collection_failures"] = mapInt(job.Progress, "collection_failures") + 1
		if err := r.Collector.UpdateCoverage(ctx, job, "partial", code); err != nil {
			return err
		}
	}
	retries := DefaultMaxJobAttempts
	if ch, err := r.Store.GetChannel(ctx, job.ChannelID); err != nil {
		return err
	} else if ch != nil {
		retries = ch.Config.Retries
	}
	status := "partial"
	if job.Attempts >= retries {
		status = "failed"
	}
	r.logger.Warn("Wiki 任务失败", "job", job.ID, "phase", job.Phase, "status", status, "code", code, "detail", detail)
	return r.Store.FinishJob(ctx, job, status, code, delay)
}

func jobFailure(cause error, attempts int) (detail, code string, delay int) {
	var (
		response *WikiResponseError
		flood    *mtproto.FloodWaitError
		api      *llm.APIError
		dbErr    *mysql.MySQLError
	)
	delay = min(10, 2<<min(3, max(0, attempts)))
	switch {
	case errors.As(cause, &response):
		return response.Error(), "WikiResponseError", 1
	case errors.As(cause, &flood):
		return fmt.Sprintf("Telegram 限流，等待 %d 秒后继续", flood.Seconds), "FloodWaitError", flood.Seconds
	case errors.As(cause, &api):
		if api.StatusCode == 429 {
			delay = 5
		}
		return fmt.Sprintf("模型服务返回 HTTP %d", api.StatusCode), "APIError", delay
	case errors.As(cause, &dbErr):
		return fmt.Sprintf("Wiki 数据库处理失败（MySQL %d），已保留当前进度", dbErr.Number), "DatabaseError", delay
	case errors.Is(cause, context.DeadlineExceeded):
		return "请求长时间未返回数据，正在保留进度并安排重试", "TimeoutError", delay
	case errors.Is(cause, ErrBudgetExceeded), errors.Is(cause, ErrPublicationAccessUnverified),
		errors.Is(cause, ErrPageEditConflict):
		return cause.Error(), "WikiError", 1
	case errors.Is(cause, errPublicationEvidence):
		return "修订引用的来源已失效", "ValueError", 1
	case errors.Is(cause, errPublicationReview), errors.Is(cause, errPublicationReviewReenabled):
		return "当前发布批次需要重新审校", "ValueError", 1
	default:
		return "处理失败，已保留当前进度", "WikiError", delay
	}
}
