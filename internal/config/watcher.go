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
	"os"
	"sync"
	"time"
)

// DefaultWatchInterval 默认文件轮询周期（对齐 Python FileWatcher 的 500ms 防抖窗口）。
const DefaultWatchInterval = 500 * time.Millisecond

type watchedStat struct {
	exists bool
	ver    fileVersion
}

func statFileState(path string) watchedStat {
	fi, err := os.Stat(path)
	if err != nil {
		return watchedStat{exists: false}
	}
	return watchedStat{exists: true, ver: versionOf(fi)}
}

// FileWatcher 在后台轮询监控 config.json 与三类提示词文件（prompt.txt、poll_prompt.txt、qa_persona.txt）
// 的 stat 三元组变化，并在变更时触发校验重载或发布 PromptChangedEvent。
type FileWatcher struct {
	cfg *Config

	mu          sync.Mutex
	initialized bool
	cfgStat     watchedStat
	promptStats map[PromptType]watchedStat
}

// NewFileWatcher 创建绑定到 cfg 的文件监控器。
func NewFileWatcher(cfg *Config) *FileWatcher {
	return &FileWatcher{
		cfg:         cfg,
		promptStats: make(map[PromptType]watchedStat, 3),
	}
}

// NewFileWatcher 是 Config 上的便捷构造入口。
func (c *Config) NewFileWatcher() *FileWatcher {
	return NewFileWatcher(c)
}

func (w *FileWatcher) initBaselineLocked() {
	if w.initialized {
		return
	}
	w.cfgStat = statFileState(w.cfg.ConfigPath())
	for _, kind := range []PromptType{PromptSummary, PromptPoll, PromptQA} {
		path, err := w.cfg.PromptFilePath(kind)
		if err == nil {
			w.promptStats[kind] = statFileState(path)
			if content, ok := w.cfg.peekPromptIfExists(kind); ok {
				w.cfg.setLastPrompt(kind, content)
			}
		}
	}
	w.initialized = true
}

// Run 启动后台监控循环，直至 ctx 被取消（interval <= 0 时取 DefaultWatchInterval=500ms）。
func (w *FileWatcher) Run(ctx context.Context, interval time.Duration) {
	if w == nil || w.cfg == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultWatchInterval
	}

	w.mu.Lock()
	w.initBaselineLocked()
	w.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.PollOnce(ctx)
		}
	}
}

// PollOnce 执行单次 stat 三元组巡检并在发生变化时触发事件。
func (w *FileWatcher) PollOnce(ctx context.Context) {
	if w == nil || w.cfg == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	w.mu.Lock()
	if !w.initialized {
		w.initBaselineLocked()
		w.mu.Unlock()
		return
	}

	cfgPath := w.cfg.ConfigPath()
	curCfgStat := statFileState(cfgPath)
	cfgChanged := curCfgStat != w.cfgStat
	if cfgChanged {
		w.cfgStat = curCfgStat
	}

	var changedPrompts []struct {
		kind PromptType
		path string
	}
	for _, kind := range []PromptType{PromptSummary, PromptPoll, PromptQA} {
		path, err := w.cfg.PromptFilePath(kind)
		if err != nil {
			continue
		}
		curStat := statFileState(path)
		if curStat != w.promptStats[kind] {
			w.promptStats[kind] = curStat
			changedPrompts = append(changedPrompts, struct {
				kind PromptType
				path string
			}{kind: kind, path: path})
		}
	}
	w.mu.Unlock()

	if cfgChanged {
		_, _ = w.cfg.Reload(ctx)
		// 若因验证错误自动回滚重写了 config.json，同步最新 stat 以免下一轮重复触发。
		w.mu.Lock()
		w.cfgStat = statFileState(cfgPath)
		w.mu.Unlock()
	}

	for _, p := range changedPrompts {
		content, err := w.cfg.GetPrompt(p.kind)
		if err != nil {
			continue
		}
		// 若文件原本被删除且 GetPrompt 自动重建了默认文件，同步最新 stat。
		w.mu.Lock()
		w.promptStats[p.kind] = statFileState(p.path)
		w.mu.Unlock()

		if prev, hadPrev := w.cfg.getLastPrompt(p.kind); hadPrev && prev == content {
			continue
		}
		w.cfg.setLastPrompt(p.kind, content)

		w.cfg.EventBus().PublishPrompt(ctx, PromptChangedEvent{
			PromptType: p.kind,
			FilePath:   p.path,
			Content:    content,
			Timestamp:  time.Now(),
		})
	}
}
