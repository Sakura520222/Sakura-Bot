// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package wiki

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultEventOutboxDir 是实时频道事件本地持久缓冲目录（对齐 WIKI_EVENT_DIR）。
const DefaultEventOutboxDir = "data/wiki_events"

// OutboxEvent 表示写入本地磁盘缓冲区的实时 Telegram 频道变更事件。
type OutboxEvent struct {
	Kind       string  `json:"kind"` // "delete" | "message" | "upsert"
	ChannelID  string  `json:"channel_id"`
	IDs        []int64 `json:"ids,omitempty"`
	MessageID  int64   `json:"message_id,omitempty"`
	MediaKey   string  `json:"media_key,omitempty"`
	MediaGroup string  `json:"media_group,omitempty"`
	Category   string  `json:"category,omitempty"`
	Body       string  `json:"body,omitempty"`
	CreatedAt  string  `json:"created_at,omitempty"`
	EditedAt   *string `json:"edited_at,omitempty"`
}

// OutboxItem 表示待重放的磁盘缓冲记录及其 SHA-256 确认键。
type OutboxItem struct {
	Key   string
	Event OutboxEvent
}

// EventOutbox 在 MySQL 短暂不可达时持久化实时频道增删事件，恢复后优先重放删除事件（对齐 core/wiki/outbox.py）。
type EventOutbox struct {
	root string
}

// NewEventOutbox 创建本地磁盘事件发件箱。
func NewEventOutbox(root string) *EventOutbox {
	r := strings.TrimSpace(root)
	if r == "" {
		if env := strings.TrimSpace(os.Getenv("WIKI_EVENT_DIR")); env != "" {
			r = env
		} else {
			r = DefaultEventOutboxDir
		}
	}
	return &EventOutbox{root: r}
}

// Root 返回本地缓冲目录路径。
func (o *EventOutbox) Root() string {
	if o == nil || o.root == "" {
		return DefaultEventOutboxDir
	}
	return o.root
}

// Put 将事件经同目录 .tmp 临时文件 fsync + 原子重命名为 {sha256}.json 落盘。
func (o *EventOutbox) Put(record OutboxEvent) (string, error) {
	root := o.Root()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create outbox dir: %w", err)
	}
	key := Digest(record)
	dest := filepath.Join(root, key+".json")
	if _, err := os.Stat(dest); err == nil {
		return key, nil
	}

	raw := []byte(EncodeJSON(record))
	tmp, err := os.CreateTemp(root, ".wiki-event-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create outbox temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write outbox temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("sync outbox temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close outbox temp file: %w", err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return "", fmt.Errorf("rename outbox file: %w", err)
	}
	cleanup = false
	return key, nil
}

// Keys 返回当前磁盘上所有已持久化的 64 位十六进制事件键集合。
func (o *EventOutbox) Keys() (map[string]bool, error) {
	root := o.Root()
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	keys := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".json")
		if reHex64.MatchString(stem) {
			keys[stem] = true
		}
	}
	return keys, nil
}

// Pending 读取最多 limit 条待重放事件，并确保所有 delete 事件排在 upsert/message 事件之前。
func (o *EventOutbox) Pending(limit int, filterKeys map[string]bool) ([]OutboxItem, error) {
	if limit <= 0 {
		limit = 100
	}
	root := o.Root()
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var deletes, messages []OutboxItem
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".json")
		if !reHex64.MatchString(stem) {
			continue
		}
		if filterKeys != nil && !filterKeys[stem] {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(root, entry.Name()))
		if readErr != nil {
			continue
		}
		var ev OutboxEvent
		if jsonErr := json.Unmarshal(data, &ev); jsonErr != nil {
			continue
		}
		item := OutboxItem{Key: stem, Event: ev}
		if ev.Kind == "delete" {
			deletes = append(deletes, item)
			if len(deletes) == limit {
				return deletes, nil
			}
		} else if len(messages) < limit {
			messages = append(messages, item)
		}
	}
	return append(deletes, messages[:min(len(messages), limit-len(deletes))]...), nil
}

// Acknowledge 校验事件键格式后幂等移除已成功落库的磁盘缓冲文件。
func (o *EventOutbox) Acknowledge(key string) error {
	if !reHex64.MatchString(key) {
		return errors.New("invalid outbox key")
	}
	err := os.Remove(filepath.Join(o.Root(), key+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
