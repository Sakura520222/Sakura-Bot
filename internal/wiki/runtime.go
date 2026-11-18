// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package wiki

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

const (
	wikiTickInterval        = 15 * time.Second
	wikiRefreshInterval     = time.Minute
	wikiMaintenanceInterval = time.Hour
	wikiJobWorkers          = 4
)

// Runtime owns Wiki background jobs, the live event outbox and publication access checks.
type Runtime struct {
	Store     *Store
	Collector *Collector
	Engine    *Engine
	Community *CommunityService
	Accounts  *AccountService
	Reader    *PublicReader
	Research  *ResearchService
	Avatar    *AvatarService

	cfg     *config.Config
	clients AvatarClientsProvider
	outbox  *EventOutbox
	logger  *slog.Logger
	wake    chan struct{}
	running atomic.Bool

	stateMu      sync.RWMutex
	ready        bool
	startupError string
	known        map[string]bool // channel ID mapped to the enabled flag
	recovery     map[string]bool
	eventMu      [32]sync.Mutex

	// Only the Run loop reads and writes these timestamps.
	lastRefresh     time.Time
	lastMaintenance time.Time
}

func NewRuntime(store *Store, cfg *config.Config, clients AvatarClientsProvider, collectorOpts ...CollectorOption) *Runtime {
	research := NewResearchService(store, cfg)
	engine := NewEngine(store, NewConfigLLMProvider(cfg), research)
	r := &Runtime{
		Store:     store,
		Engine:    engine,
		Community: NewCommunityService(store, engine.Knowledge),
		Accounts:  NewAccountService(store),
		Reader:    NewPublicReader(store),
		Research:  research,
		Avatar:    NewAvatarService(store, clients, ""),
		cfg:       cfg,
		clients:   clients,
		outbox:    NewEventOutbox(""),
		logger:    slog.Default(),
		wake:      make(chan struct{}, 1),
		known:     make(map[string]bool),
		recovery:  make(map[string]bool),
	}
	r.Collector = NewCollector(store, r.userBot, collectorOpts...)
	return r
}

func (r *Runtime) Ready() bool {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.ready
}

func (r *Runtime) StartupError() string {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.startupError
}

func (r *Runtime) userBot() mtproto.Messenger {
	if r.clients == nil {
		return nil
	}
	user, _ := r.clients()
	return user
}

func (r *Runtime) bot() mtproto.Messenger {
	if r.clients == nil {
		return nil
	}
	_, bot := r.clients()
	return bot
}

// Wake requests a tick and coalesces overlapping wakeups.
func (r *Runtime) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run retries transient startup failures and waits for all leased jobs on cancellation.
func (r *Runtime) Run(ctx context.Context) {
	if !r.running.CompareAndSwap(false, true) {
		return
	}
	defer r.running.Store(false)
	defer func() {
		r.stateMu.Lock()
		r.ready = false
		r.stateMu.Unlock()
	}()
	slots := make(chan struct{}, wikiJobWorkers)
	var jobs sync.WaitGroup
	defer jobs.Wait()
	ticker := time.NewTicker(wikiTickInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := r.tick(ctx, slots, &jobs); err != nil && ctx.Err() == nil {
			r.logger.Warn("Wiki 调度暂未完成", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.wake:
		}
	}
}

func (r *Runtime) initialize(ctx context.Context) error {
	if r.Store == nil || r.Store.DB == nil {
		return errors.New("Wiki 数据库尚未配置")
	}
	if err := r.Store.Initialize(ctx); err != nil {
		return err
	}
	if err := r.Engine.Knowledge.RepairGeneratedTitleSeparators(ctx); err != nil {
		return err
	}
	keys, err := r.outbox.Keys()
	if err != nil {
		return err
	}
	channels, err := r.Store.ListChannels(ctx)
	if err != nil {
		return err
	}
	r.setKnownChannels(channels)
	r.stateMu.Lock()
	for key := range keys {
		r.recovery[key] = true
	}
	r.stateMu.Unlock()
	_, err = r.flushOutbox(ctx)
	return err
}

func (r *Runtime) tick(ctx context.Context, slots chan struct{}, jobs *sync.WaitGroup) error {
	if !r.Ready() {
		err := r.initialize(ctx)
		r.stateMu.Lock()
		r.ready = err == nil
		r.startupError = ""
		if err != nil {
			r.startupError = "Wiki 启动失败"
			var schemaErr *WikiSchemaError
			if errors.As(err, &schemaErr) {
				r.startupError = schemaErr.Error()
			}
		}
		r.stateMu.Unlock()
		if err != nil {
			return err
		}
	}
	if empty, err := r.flushOutbox(ctx); !empty || err != nil {
		return err
	}
	now := time.Now().UTC()
	if now.Sub(r.lastRefresh) >= wikiRefreshInterval {
		if err := r.refreshChannels(ctx); err != nil {
			return err
		}
		r.lastRefresh = time.Now().UTC()
	}
	if now.Sub(r.lastMaintenance) >= wikiMaintenanceInterval {
		if err := r.Accounts.Cleanup(ctx); err != nil {
			return err
		}
		if err := r.Research.Prune(ctx); err != nil {
			return err
		}
		r.lastMaintenance = now
	}
	if err := r.scheduleChannels(ctx, now); err != nil {
		return err
	}
	for len(slots) < cap(slots) && ctx.Err() == nil {
		job, err := r.Store.ClaimJob(ctx)
		if err != nil || job == nil {
			return err
		}
		slots <- struct{}{}
		jobs.Go(func() {
			defer func() {
				<-slots
				r.Wake()
			}()
			r.runJob(ctx, job)
		})
	}
	return nil
}

func (r *Runtime) setKnownChannels(channels []*ChannelRecord) {
	known := make(map[string]bool, len(channels))
	for _, channel := range channels {
		known[channel.ID] = channel.Enabled
	}
	r.stateMu.Lock()
	r.known = known
	r.stateMu.Unlock()
}

func (r *Runtime) refreshChannels(ctx context.Context) error {
	channels, err := r.Store.ListChannels(ctx)
	if err != nil {
		return err
	}
	var refs []string
	if r.cfg != nil {
		refs = r.cfg.Channels()
	}
	for _, channel := range channels {
		if channel.Enabled {
			refs = append(refs, channel.ID)
		}
	}
	allowed := make(map[string]bool)
	verified := make(map[string]bool)
	seen := make(map[string]bool)
	user := r.userBot()
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := AliasKey(ref)
		if err != nil || seen[key] {
			continue
		}
		seen[key] = true
		old, err := r.Store.GetChannel(ctx, key)
		if err != nil {
			return err
		}
		if old != nil {
			allowed[old.ID] = true
			if key == old.ID && verified[old.ID] {
				continue
			}
		}
		if user == nil || !user.IsConnected() {
			continue
		}
		target := key
		if id, err := CanonicalChannelID(key); err == nil {
			target = "-100" + id
		}
		resolveCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		info, err := user.ResolveChannel(resolveCtx, target)
		cancel()
		if err != nil || info == nil {
			if old != nil {
				if err := r.invalidateAccess(ctx, old.ID); err != nil {
					return err
				}
			}
			r.logger.Info("Wiki 频道身份核实延后", "channel", key)
			continue
		}
		channel, err := r.Store.RegisterChannel(ctx, channelEntity(info), ref)
		if err != nil {
			return err
		}
		allowed[channel.ID] = true
		verified[channel.ID] = true
	}
	for _, channel := range channels {
		if !allowed[channel.ID] {
			if err := r.invalidateAccess(ctx, channel.ID); err != nil {
				return err
			}
		}
	}
	channels, err = r.Store.ListChannels(ctx)
	if err == nil {
		r.setKnownChannels(channels)
	}
	return err
}

func channelEntity(info *mtproto.ChannelInfo) ChannelEntity {
	return ChannelEntity{
		ID:              info.ID,
		Title:           info.Title,
		Username:        info.Username,
		ActiveUsernames: info.ActiveUsernames,
		Broadcast:       info.Broadcast,
		Restricted:      info.Restricted,
		NoForwards:      info.NoForwards,
	}
}

func (r *Runtime) invalidateAccess(ctx context.Context, channelID string) error {
	_, err := r.Store.DB.ExecContext(ctx,
		"UPDATE wiki_channels SET access_verified=FALSE, verified_until=NULL WHERE id=?", channelID)
	return err
}

func (r *Runtime) scheduleChannels(ctx context.Context, now time.Time) error {
	channels, err := r.Store.ListChannels(ctx)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		if _, err := r.Community.DispatchApproved(ctx, channel.ID); err != nil {
			return err
		}
		if channel.Config.Paused || channel.ActiveJob != "" {
			continue
		}
		if channel.PublishedRevision != "" && channel.AccessVerified {
			legacy, err := r.Store.HasLegacyCaptionMedia(ctx, channel.ID)
			if err != nil {
				return err
			}
			if legacy {
				if _, err := r.Store.EnqueueJob(ctx, nil, channel.ID, "albums", "caption_media_repair", "", false); err != nil {
					return err
				}
				continue
			}
		}
		if channel.Config.Frequency == "manual" {
			continue
		}
		dirty := channel.DirtyAt != nil &&
			now.Sub(*channel.DirtyAt) >= time.Duration(channel.Config.DebounceSeconds)*time.Second
		if !dirty && !scheduleDue(channel.Config, channel.ScheduledAt, now) {
			continue
		}
		kind := "update"
		if channel.ReconciledAt == nil || now.Sub(*channel.ReconciledAt) >= 7*24*time.Hour {
			kind = "reconcile"
		}
		err := r.Store.WithTx(ctx, func(tx *sql.Tx) error {
			if _, err := r.Store.EnqueueJob(ctx, tx, channel.ID, kind, "", "", false); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET scheduled_at=UTC_TIMESTAMP(6) WHERE id=?", channel.ID)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
