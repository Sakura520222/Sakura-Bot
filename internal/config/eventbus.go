// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package config

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// 事件优先级常量（数值越小越先执行，对齐 core/config/event_bus.py:15-18）。
const (
	PriorityCritical = 0
	PriorityHigh     = 10
	PriorityNormal   = 50
	PriorityLow      = 100

	defaultSubscriberTimeout  = 30 * time.Second
	criticalSubscriberTimeout = 60 * time.Second
)

// ConfigChangedEvent 表示配置更新成功事件（对齐 core/config/events.py:ConfigChangedEvent）。
type ConfigChangedEvent struct {
	Config        map[string]any
	OldConfig     map[string]any
	Version       int64
	ChangedFields []string
	Timestamp     time.Time
}

// String 屏蔽原始 Config 字典，防止日志格式化意外打印 API Key 等敏感凭据。
func (e ConfigChangedEvent) String() string {
	return fmt.Sprintf("ConfigChangedEvent{Version:%d, ChangedFields:%v}", e.Version, e.ChangedFields)
}

// HasChanged 检查给定字段（或前缀路径）是否在本次事件中发生变更；ChangedFields 为空时视为全量变更。
func (e ConfigChangedEvent) HasChanged(field string) bool {
	if len(e.ChangedFields) == 0 {
		return true
	}
	prefix := field + "."
	for _, f := range e.ChangedFields {
		if f == field || len(f) > len(prefix) && f[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// PromptChangedEvent 表示提示词文件变更事件（对齐 core/config/events.py:PromptChangedEvent）。
type PromptChangedEvent struct {
	PromptType PromptType
	FilePath   string
	Content    string
	Timestamp  time.Time
}

// ConfigValidationErrorEvent 表示配置文件验证失败（及自动回滚）事件。
type ConfigValidationErrorEvent struct {
	Error      string
	ConfigPath string
	RolledBack bool
	Timestamp  time.Time
}

type configSub struct {
	id       uint64
	priority int
	handler  func(ctx context.Context, ev ConfigChangedEvent)
}

type promptSub struct {
	id       uint64
	priority int
	handler  func(ctx context.Context, ev PromptChangedEvent)
}

type validationSub struct {
	id       uint64
	priority int
	handler  func(ctx context.Context, ev ConfigValidationErrorEvent)
}

// EventBus 支持按优先级顺序分发配置与提示词变更事件，并为每个订阅者提供超时与 panic 隔离。
type EventBus struct {
	mu             sync.RWMutex
	nextID         atomic.Uint64
	configSubs     []configSub
	promptSubs     []promptSub
	validationSubs []validationSub
}

// NewEventBus 创建新的配置事件总线。
func NewEventBus() *EventBus {
	return &EventBus{}
}

// EventBus 返回与 Config 关联的事件总线（延迟初始化，兼容直接构造的 Config 字面量）。
func (c *Config) EventBus() *EventBus {
	c.busMu.Lock()
	defer c.busMu.Unlock()
	if c.bus == nil {
		c.bus = NewEventBus()
	}
	return c.bus
}

// SetEventBus 替换 Config 关联的事件总线。
func (c *Config) SetEventBus(bus *EventBus) {
	c.busMu.Lock()
	defer c.busMu.Unlock()
	c.bus = bus
}

// Subscribe 在 Config 关联的 EventBus 上订阅 ConfigChangedEvent。
func (c *Config) Subscribe(priority int, handler func(ctx context.Context, ev ConfigChangedEvent)) (unsubscribe func()) {
	return c.EventBus().Subscribe(priority, handler)
}

// Subscribe 注册 ConfigChangedEvent 订阅者，priority 越小越先执行（同优先级保持注册顺序）。
// 返回的闭包可幂等取消该订阅。
func (b *EventBus) Subscribe(priority int, handler func(ctx context.Context, ev ConfigChangedEvent)) (unsubscribe func()) {
	if b == nil || handler == nil {
		return func() {}
	}
	id := b.nextID.Add(1)
	b.mu.Lock()
	b.configSubs = append(b.configSubs, configSub{id: id, priority: priority, handler: handler})
	sort.SliceStable(b.configSubs, func(i, j int) bool {
		return b.configSubs[i].priority < b.configSubs[j].priority
	})
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, s := range b.configSubs {
			if s.id == id {
				b.configSubs = append(b.configSubs[:i], b.configSubs[i+1:]...)
				break
			}
		}
	}
}

// SubscribePrompt 注册 PromptChangedEvent 订阅者。
func (b *EventBus) SubscribePrompt(priority int, handler func(ctx context.Context, ev PromptChangedEvent)) (unsubscribe func()) {
	if b == nil || handler == nil {
		return func() {}
	}
	id := b.nextID.Add(1)
	b.mu.Lock()
	b.promptSubs = append(b.promptSubs, promptSub{id: id, priority: priority, handler: handler})
	sort.SliceStable(b.promptSubs, func(i, j int) bool {
		return b.promptSubs[i].priority < b.promptSubs[j].priority
	})
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, s := range b.promptSubs {
			if s.id == id {
				b.promptSubs = append(b.promptSubs[:i], b.promptSubs[i+1:]...)
				break
			}
		}
	}
}

// SubscribeValidationError 注册 ConfigValidationErrorEvent 订阅者。
func (b *EventBus) SubscribeValidationError(priority int, handler func(ctx context.Context, ev ConfigValidationErrorEvent)) (unsubscribe func()) {
	if b == nil || handler == nil {
		return func() {}
	}
	id := b.nextID.Add(1)
	b.mu.Lock()
	b.validationSubs = append(b.validationSubs, validationSub{id: id, priority: priority, handler: handler})
	sort.SliceStable(b.validationSubs, func(i, j int) bool {
		return b.validationSubs[i].priority < b.validationSubs[j].priority
	})
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, s := range b.validationSubs {
			if s.id == id {
				b.validationSubs = append(b.validationSubs[:i], b.validationSubs[i+1:]...)
				break
			}
		}
	}
}

// Publish 按优先级升序顺序执行所有 ConfigChangedEvent 订阅者。
func (b *EventBus) Publish(ctx context.Context, ev ConfigChangedEvent) {
	if b == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.RLock()
	subs := make([]configSub, len(b.configSubs))
	copy(subs, b.configSubs)
	b.mu.RUnlock()

	for _, s := range subs {
		timeout := defaultSubscriberTimeout
		if s.priority <= PriorityHigh {
			timeout = criticalSubscriberTimeout
		}
		subCtx, cancel := context.WithTimeout(ctx, timeout)
		evCopy := ConfigChangedEvent{
			Config:        deepCopyMap(ev.Config),
			OldConfig:     deepCopyMap(ev.OldConfig),
			Version:       ev.Version,
			ChangedFields: append([]string(nil), ev.ChangedFields...),
			Timestamp:     ev.Timestamp,
		}
		safeInvoke(s.priority, func() {
			s.handler(subCtx, evCopy)
		})
		cancel()
	}
}

// PublishPrompt 按优先级升序顺序执行所有 PromptChangedEvent 订阅者。
func (b *EventBus) PublishPrompt(ctx context.Context, ev PromptChangedEvent) {
	if b == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.RLock()
	subs := make([]promptSub, len(b.promptSubs))
	copy(subs, b.promptSubs)
	b.mu.RUnlock()

	for _, s := range subs {
		timeout := defaultSubscriberTimeout
		if s.priority <= PriorityHigh {
			timeout = criticalSubscriberTimeout
		}
		subCtx, cancel := context.WithTimeout(ctx, timeout)
		safeInvoke(s.priority, func() {
			s.handler(subCtx, ev)
		})
		cancel()
	}
}

// PublishValidationError 按优先级升序顺序执行所有 ConfigValidationErrorEvent 订阅者。
func (b *EventBus) PublishValidationError(ctx context.Context, ev ConfigValidationErrorEvent) {
	if b == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.RLock()
	subs := make([]validationSub, len(b.validationSubs))
	copy(subs, b.validationSubs)
	b.mu.RUnlock()

	for _, s := range subs {
		subCtx, cancel := context.WithTimeout(ctx, defaultSubscriberTimeout)
		safeInvoke(s.priority, func() {
			s.handler(subCtx, ev)
		})
		cancel()
	}
}

// Close 清空全部订阅。
func (b *EventBus) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.configSubs = nil
	b.promptSubs = nil
	b.validationSubs = nil
}

func safeInvoke(priority int, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("配置事件订阅者发生 panic，已隔离继续执行", "priority", priority, "panic", r)
		}
	}()
	fn()
}

// CalculateChangedFields 计算新旧配置之间的差异键集合（对齐 core/config/events.py:calculate_changed_fields）。
func CalculateChangedFields(oldCfg, newCfg map[string]any) []string {
	if len(oldCfg) == 0 {
		return nil
	}
	set := make(map[string]struct{})
	collectDiff("", oldCfg, newCfg, set)
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func collectDiff(prefix string, left, right map[string]any, out map[string]struct{}) {
	keys := make(map[string]struct{}, len(left)+len(right))
	for k := range left {
		keys[k] = struct{}{}
	}
	for k := range right {
		keys[k] = struct{}{}
	}
	for k := range keys {
		lv, lok := left[k]
		rv, rok := right[k]
		path := prefix + k
		if lok != rok {
			out[path] = struct{}{}
			continue
		}
		lm, lIsMap := lv.(map[string]any)
		rm, rIsMap := rv.(map[string]any)
		if lIsMap && rIsMap {
			collectDiff(path+".", lm, rm, out)
			continue
		}
		if !configsEqualValue(lv, rv) {
			out[path] = struct{}{}
		}
	}
}
