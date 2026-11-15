package handlers

import (
	"context"
	"errors"
	"fmt"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"log/slog"
	"sync"
	"time"
)

// AIPool bounds both executing and queued requests. Update dispatch stays serial.
type AIPool struct {
	cleanup                             context.Context
	cleanupCancel                       context.CancelFunc
	mu                                  sync.Mutex
	ctx                                 context.Context
	cancel                              context.CancelFunc
	jobs                                chan *AIJob
	slots                               chan struct{}
	active                              map[string]*AIJob
	userSlots                           map[int64]int
	timeout                             time.Duration
	wg                                  sync.WaitGroup
	closed                              bool
	MaxMessages, InboxCapacity, PerUser int
}
type AIJob struct {
	predecessor  *AIJob
	pool         *AIPool
	user         int64
	ctx          context.Context
	cancel       context.CancelFunc
	run          func(context.Context)
	key          string
	ID           string
	sealed       bool
	inbox        []engine.TurnInput
	inputs       []engine.TurnInput
	reservations []string
	done         chan struct{}
	previous     <-chan struct{}
	releaseOnce  sync.Once
}

func NewAIPool(ctx context.Context, workers, capacity int, timeout time.Duration) *AIPool {
	// Enforce config.AIPoolConfig's documented resource limits at the allocation
	// boundary too. This bounds the sum to 1056, even for direct callers.
	if workers < 1 || workers > 32 || capacity < 1 || capacity > 1024 || timeout <= 0 {
		panic("invalid AI pool configuration")
	}
	// Use an explicit wide type for allocation arithmetic on all Go targets.
	totalCapacity := int64(workers) + int64(capacity)
	ctx, cancel := context.WithCancel(ctx)
	p := &AIPool{ctx: ctx, cancel: cancel, jobs: make(chan *AIJob, totalCapacity), slots: make(chan struct{}, totalCapacity), active: make(map[string]*AIJob), userSlots: make(map[int64]int), timeout: timeout, MaxMessages: 8, InboxCapacity: 8, PerUser: 4}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}

// Acquire reserves capacity before billing. Release or Submit must follow.
func (p *AIPool) Acquire(user int64) (*AIJob, error) {
	j, existing, err := p.AcquireScoped(user, "private")
	if existing {
		return nil, errors.New("当前问题仍在处理中")
	}
	return j, err
}
func (p *AIPool) AcquireScoped(user int64, scope string) (*AIJob, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := fmt.Sprintf("%d:%s", user, scope)
	if p.closed || p.ctx.Err() != nil {
		return nil, false, errors.New("助手正在关闭，请稍后重试。")
	}
	old := p.active[key]
	if old != nil && !old.sealed && old.ctx.Err() == nil {
		return old, true, nil
	}
	if p.userSlots[user] >= p.PerUser {
		return nil, false, errors.New("你的并发回合已达上限，请稍候。")
	}
	select {
	case p.slots <- struct{}{}:
	default:
		return nil, false, errors.New("助手繁忙，本条消息未扣费，请稍后重试。")
	}
	ctx, cancel := context.WithTimeout(p.ctx, p.timeout)
	j := &AIJob{pool: p, user: user, key: key, ID: fmt.Sprintf("%d-%d", user, time.Now().UnixNano()), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if old != nil {
		j.previous = old.done
		j.predecessor = old
	}
	p.active[key] = j
	p.userSlots[user]++
	return j, false, nil
}
func (j *AIJob) Admit(input engine.TurnInput, id string, reserve func() contracts.QuotaCheckResult) (contracts.QuotaCheckResult, bool) {
	p := j.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if j.sealed || j.ctx.Err() != nil {
		return contracts.QuotaCheckResult{Message: "回合已结束，请重新发送。"}, false
	}
	if len(j.inputs) >= p.MaxMessages || len(j.inbox) >= p.InboxCapacity {
		return contracts.QuotaCheckResult{Message: "本回合追问已达上限，本条消息未扣费。"}, false
	}
	q := reserve()
	if !q.Allowed {
		return q, false
	}
	if len(j.inputs) > 0 {
		j.inbox = append(j.inbox, input)
	}
	j.inputs = append(j.inputs, input)
	j.reservations = append(j.reservations, id)
	return q, true
}
func (j *AIJob) Take(final bool) []engine.TurnInput {
	p := j.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	out := j.inbox
	j.inbox = nil
	if len(out) == 0 && final {
		j.sealed = true
	}
	return out
}
func (j *AIJob) Inputs() []engine.TurnInput {
	j.pool.mu.Lock()
	defer j.pool.mu.Unlock()
	return append([]engine.TurnInput(nil), j.inputs...)
}
func (j *AIJob) ReservationIDs() []string {
	j.pool.mu.Lock()
	defer j.pool.mu.Unlock()
	j.sealed = true
	return append([]string(nil), j.reservations...)
}
func (j *AIJob) Release() {
	j.releaseOnce.Do(func() {
		j.cancel()
		p := j.pool
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.active[j.key] == j {
			delete(p.active, j.key)
			for predecessor := j.predecessor; predecessor != nil; predecessor = predecessor.predecessor {
				select {
				case <-predecessor.done:
					continue
				default:
					p.active[j.key] = predecessor
				}
				break
			}
		}
		p.userSlots[j.user]--
		if p.userSlots[j.user] == 0 {
			delete(p.userSlots, j.user)
		}
		<-p.slots
		close(j.done)
	})
}
func (j *AIJob) Submit(run func(context.Context)) {
	j.run = run
	p := j.pool
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		defer j.Release()
		run(j.ctx)
		return
	}
	p.jobs <- j // Capacity was reserved by Acquire.
	p.mu.Unlock()
}
func (p *AIPool) worker() {
	defer p.wg.Done()
	for j := range p.jobs {
		func() {
			defer j.Release()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("AI worker panic", "panic", r)
				}
			}()
			if j.previous != nil {
				select {
				case <-j.previous:
				case <-j.ctx.Done():
				}
			}
			j.run(j.ctx)
		}()
	}
}
func (p *AIPool) Cancel(user int64) { p.CancelScope(user, "private") }
func (p *AIPool) CancelScope(user int64, scope string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if j := p.active[fmt.Sprintf("%d:%s", user, scope)]; j != nil {
		for current := j; current != nil; current = current.predecessor {
			current.sealed = true
			current.cancel()
		}
	}
}
func (p *AIPool) Active(user int64) bool { return p.ActiveScope(user, "private") }
func (p *AIPool) ActiveScope(user int64, scope string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active[fmt.Sprintf("%d:%s", user, scope)] != nil
}
func (p *AIPool) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		p.cleanup, p.cleanupCancel = context.WithTimeout(context.Background(), 5*time.Second)
		p.cancel()
		close(p.jobs)
	}
	p.mu.Unlock()
	p.wg.Wait()
	if p.cleanupCancel != nil {
		p.cleanupCancel()
	}
}

func (j *AIJob) Sealed() bool { j.pool.mu.Lock(); defer j.pool.mu.Unlock(); return j.sealed }

func (j *AIJob) CleanupContext() context.Context {
	j.pool.mu.Lock()
	defer j.pool.mu.Unlock()
	if j.pool.cleanup != nil {
		return j.pool.cleanup
	}
	return context.Background()
}
