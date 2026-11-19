// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package scheduler

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"
)

// JobFunc 是调度任务执行体；调度器关停时 ctx 会被取消。
type JobFunc func(ctx context.Context)

// JobInfo 描述任务的只读运行状态。
type JobInfo struct {
	ID             string
	Kind           string // "cron" | "interval"
	Group          string
	NextRun        time.Time
	Running        bool
	LastRun        time.Time
	PauseSensitive bool
}

type jobEntry struct {
	id             string
	group          string
	kind           string
	cron           *CronSpec
	interval       time.Duration
	fn             JobFunc
	nextRun        time.Time
	lastRun        time.Time
	pauseSensitive bool // true 表示受 /pause 全局暂停控制（如总结任务），系统维护任务可设为 false
}

const maxActiveJobs = 8

// Scheduler 是并发安全的轻量定时任务调度器。
type Scheduler struct {
	mu      sync.Mutex
	loc     *time.Location
	logger  *slog.Logger
	paused  bool
	started bool
	jobs    map[string]*jobEntry
	active  map[string]bool
	notify  chan struct{}

	runCtx    context.Context
	runCancel context.CancelFunc
	wg        sync.WaitGroup
}

// New 创建调度器。loc 为 nil 时默认 time.UTC；logger 为 nil 时使用 slog.Default()。
func New(loc *time.Location, logger *slog.Logger) *Scheduler {
	if loc == nil {
		loc = time.UTC
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		loc:    loc,
		logger: logger,
		jobs:   make(map[string]*jobEntry),
		active: make(map[string]bool),
		notify: make(chan struct{}, 1),
	}
}

// SetLocation 动态更新调度时区并重算所有 Cron 任务的下次触发时间。
func (s *Scheduler) SetLocation(loc *time.Location) {
	if loc == nil {
		return
	}
	s.mu.Lock()
	s.loc = loc
	now := time.Now().In(loc)
	for _, j := range s.jobs {
		if j.cron != nil {
			j.nextRun = j.cron.Next(now)
		}
	}
	s.mu.Unlock()
	s.signal()
}

// AddCron 注册或替换一个 Cron 任务。pauseSensitive=true 的任务在 Pause() 期间跳过触发。
func (s *Scheduler) AddCron(id, group string, spec *CronSpec, pauseSensitive bool, fn JobFunc) {
	if spec == nil || fn == nil {
		return
	}
	s.mu.Lock()
	now := time.Now().In(s.loc)
	prev := s.jobs[id]
	entry := &jobEntry{
		id:             id,
		group:          group,
		kind:           "cron",
		cron:           spec,
		fn:             fn,
		nextRun:        spec.Next(now),
		pauseSensitive: pauseSensitive,
	}
	if prev != nil {
		entry.lastRun = prev.lastRun
	}
	s.jobs[id] = entry
	s.mu.Unlock()
	s.signal()
}

// AddInterval 注册或替换一个固定间隔任务（对齐 APScheduler interval + max_instances=1 + coalesce=True）。
func (s *Scheduler) AddInterval(id, group string, interval, initialDelay time.Duration, pauseSensitive bool, fn JobFunc) {
	if interval <= 0 || fn == nil {
		return
	}
	if initialDelay < 0 {
		initialDelay = 0
	}
	s.mu.Lock()
	now := time.Now().In(s.loc)
	prev := s.jobs[id]
	entry := &jobEntry{
		id:             id,
		group:          group,
		kind:           "interval",
		interval:       interval,
		fn:             fn,
		nextRun:        now.Add(initialDelay),
		pauseSensitive: pauseSensitive,
	}
	if prev != nil {
		entry.lastRun = prev.lastRun
	}
	s.jobs[id] = entry
	s.mu.Unlock()
	s.signal()
}

// Remove 移除指定 ID 的任务。
func (s *Scheduler) Remove(id string) bool {
	s.mu.Lock()
	_, existed := s.jobs[id]
	delete(s.jobs, id)
	s.mu.Unlock()
	if existed {
		s.signal()
	}
	return existed
}

// RemoveGroup 批量移除指定分组下的全部任务（用于配置热重载重排 summary_job_*）。
func (s *Scheduler) RemoveGroup(group string) int {
	s.mu.Lock()
	removed := 0
	for id, j := range s.jobs {
		if j.group == group {
			delete(s.jobs, id)
			removed++
		}
	}
	s.mu.Unlock()
	if removed > 0 {
		s.signal()
	}
	return removed
}

// Wake 将指定任务的下次执行提前至 now+delay（仅当更早时生效，复刻 Wiki runtime.wake(delay=1)）。
func (s *Scheduler) Wake(id string, delay time.Duration) bool {
	if delay < 0 {
		delay = 0
	}
	s.mu.Lock()
	j, ok := s.jobs[id]
	if ok {
		candidate := time.Now().In(s.loc).Add(delay)
		if j.nextRun.IsZero() || candidate.Before(j.nextRun) {
			j.nextRun = candidate
		}
	}
	s.mu.Unlock()
	if ok {
		s.signal()
	}
	return ok
}

// Pause 暂停所有 pauseSensitive=true 的任务；若已暂停返回 false。
func (s *Scheduler) Pause() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused {
		return false
	}
	s.paused = true
	return true
}

// Resume 恢复调度并重算 Cron 下次执行时间；若未暂停返回 false。
func (s *Scheduler) Resume() bool {
	s.mu.Lock()
	if !s.paused {
		s.mu.Unlock()
		return false
	}
	s.paused = false
	now := time.Now().In(s.loc)
	for _, j := range s.jobs {
		if j.cron != nil && j.nextRun.Before(now) {
			j.nextRun = j.cron.Next(now)
		}
	}
	s.mu.Unlock()
	s.signal()
	return true
}

// IsPaused 返回当前是否处于暂停态。
func (s *Scheduler) IsPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused
}

// Jobs 返回按 ID 排序的任务快照列表。
func (s *Scheduler) Jobs() []JobInfo {
	s.mu.Lock()
	cp := maps.Clone(s.jobs)
	s.mu.Unlock()

	keys := slices.Sorted(maps.Keys(cp))
	out := make([]JobInfo, 0, len(keys))
	for _, k := range keys {
		j := cp[k]
		out = append(out, JobInfo{
			ID:             j.id,
			Kind:           j.kind,
			Group:          j.group,
			NextRun:        j.nextRun,
			Running:        s.active[j.id],
			LastRun:        j.lastRun,
			PauseSensitive: j.pauseSensitive,
		})
	}
	return out
}

// Start 启动后台调度循环（幂等）。
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.runCtx, s.runCancel = context.WithCancel(ctx)
	loopCtx := s.runCtx
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		s.loop(loopCtx)
	}()
}

// Stop 取消在途任务并等待所有任务 goroutine 优雅退出。
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	cancel := s.runCancel
	s.started = false
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Scheduler) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Scheduler) loop(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	defer timer.Stop()

	for {
		wait := s.dispatchDue(ctx)
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return
		case <-s.notify:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func (s *Scheduler) dispatchDue(ctx context.Context) time.Duration {
	if ctx.Err() != nil {
		return time.Hour
	}

	s.mu.Lock()
	now := time.Now().In(s.loc)
	nextWake := now.Add(time.Minute)

	for _, j := range s.jobs {
		if j.nextRun.IsZero() {
			continue
		}
		if !now.Before(j.nextRun) {
			if !(s.paused && j.pauseSensitive) && !s.active[j.id] && len(s.active) >= maxActiveJobs {
				// Keep the due time. A slot release wakes the dispatcher.
				continue
			}
			// 已到期：计算下一次触发时间（coalesce=True）
			var next time.Time
			if j.cron != nil {
				next = j.cron.Next(now)
			} else {
				next = now.Add(j.interval)
			}
			j.nextRun = next

			if s.paused && j.pauseSensitive {
				if !next.IsZero() && next.Before(nextWake) {
					nextWake = next
				}
				continue
			}
			if s.active[j.id] {
				// max_instances=1：当前仍在执行则跳过本轮合并触发
				if !next.IsZero() && next.Before(nextWake) {
					nextWake = next
				}
				continue
			}

			s.active[j.id] = true
			j.lastRun = now
			jobID := j.id
			fn := j.fn
			s.wg.Add(1)
			go s.runJob(ctx, jobID, fn)
		}
		if !j.nextRun.IsZero() && j.nextRun.Before(nextWake) {
			nextWake = j.nextRun
		}
	}
	s.mu.Unlock()

	wait := time.Until(nextWake)
	if wait < 5*time.Millisecond {
		wait = 5 * time.Millisecond
	}
	return wait
}

func (s *Scheduler) runJob(ctx context.Context, id string, fn JobFunc) {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("定时任务发生 panic", "job_id", id, "panic", r)
		}
		s.mu.Lock()
		delete(s.active, id)
		s.mu.Unlock()
		s.signal()
	}()
	fn(ctx)
}
