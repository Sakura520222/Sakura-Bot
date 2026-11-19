// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package vectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultSourceArchiveDir 为默认原文归档目录（对齐 MESSAGE_SOURCE_DIR 默认值）。
	DefaultSourceArchiveDir = "data/message_sources"

	// defaultReindexBatchSize 为离线重放归档的默认批大小。
	defaultReindexBatchSize = 32
)

// SourceRecord 表示单条频道消息的独立完整原文归档记录
// （对齐 core/ai/source_archive.py 与 scripts/reindex_message_sources.py）。
type SourceRecord struct {
	ChannelID       string         `json:"channel_id"`
	MessageID       int64          `json:"message_id"`
	Text            string         `json:"text"`
	CreatedAt       string         `json:"created_at"`
	ChannelName     string         `json:"channel_name,omitempty"`
	ChannelUsername string         `json:"channel_username,omitempty"`
	PostURL         string         `json:"post_url,omitempty"`
	SenderID        int64          `json:"sender_id,omitempty"`
	Extra           map[string]any `json:"-"` // 顶层扩展元数据，读写无损保留
}

// Validate 校验归档记录必填字段与时间戳格式（对齐 scripts/reindex_message_sources.py::validate_source）。
func (r SourceRecord) Validate() error {
	if strings.TrimSpace(r.ChannelID) == "" {
		return errors.New("归档记录缺少合法 channel_id")
	}
	if r.MessageID <= 0 {
		return fmt.Errorf("归档记录 message_id 非法: %d", r.MessageID)
	}
	if strings.TrimSpace(r.Text) == "" {
		return errors.New("归档记录缺少非空 text")
	}
	if r.CreatedAt == "" {
		return errors.New("归档记录缺少 created_at")
	}
	if _, ok := parseISODateTime(r.CreatedAt); !ok {
		return fmt.Errorf("归档记录 created_at 非法 ISO 时间: %q", r.CreatedAt)
	}
	return nil
}

// MarshalJSON 将 SourceRecord 编码为顶层扁平 JSON 对象（先铺 Extra，再由核心字段覆盖，
// 对齐 Python dict(metadata, channel_id=..., message_id=..., text=..., created_at=...)）。
func (r SourceRecord) MarshalJSON() ([]byte, error) {
	obj := make(map[string]any, len(r.Extra)+8)
	if r.Extra != nil {
		maps.Copy(obj, r.Extra)
	}
	obj["channel_id"] = r.ChannelID
	obj["message_id"] = r.MessageID
	obj["text"] = r.Text
	obj["created_at"] = r.CreatedAt
	if r.ChannelName != "" {
		obj["channel_name"] = r.ChannelName
	} else if _, exists := obj["channel_name"]; !exists {
		// 保持简洁，未提供时不额外注入空串
	}
	if r.ChannelUsername != "" {
		obj["channel_username"] = r.ChannelUsername
	}
	if r.PostURL != "" {
		obj["post_url"] = r.PostURL
	}
	if r.SenderID != 0 {
		obj["sender_id"] = r.SenderID
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\r\n"), nil
}

// UnmarshalJSON 从顶层 JSON 对象解码核心字段并把其余键保留在 Extra 中。
func (r *SourceRecord) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("归档内容不是合法 JSON 对象: %w", err)
	}
	if raw == nil {
		return errors.New("归档内容不能为 null")
	}

	var out SourceRecord
	extra := make(map[string]any)

	for k, v := range raw {
		switch k {
		case "channel_id":
			if err := json.Unmarshal(v, &out.ChannelID); err != nil {
				return fmt.Errorf("解析 channel_id 失败: %w", err)
			}
		case "message_id":
			id, err := decodeStrictInt64(v)
			if err != nil {
				return fmt.Errorf("解析 message_id 失败: %w", err)
			}
			out.MessageID = id
		case "text":
			if err := json.Unmarshal(v, &out.Text); err != nil {
				return fmt.Errorf("解析 text 失败: %w", err)
			}
		case "created_at":
			if err := json.Unmarshal(v, &out.CreatedAt); err != nil {
				return fmt.Errorf("解析 created_at 失败: %w", err)
			}
		case "channel_name":
			s, err := decodeOptionalString(v)
			if err != nil {
				return fmt.Errorf("解析 channel_name 失败: %w", err)
			}
			out.ChannelName = s
		case "channel_username":
			s, err := decodeOptionalString(v)
			if err != nil {
				return fmt.Errorf("解析 channel_username 失败: %w", err)
			}
			out.ChannelUsername = s
		case "post_url":
			s, err := decodeOptionalString(v)
			if err != nil {
				return fmt.Errorf("解析 post_url 失败: %w", err)
			}
			out.PostURL = s
		case "sender_id":
			if !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				if id, err := decodeStrictInt64(v); err == nil {
					out.SenderID = id
				} else if s, sErr := decodeOptionalString(v); sErr == nil && s != "" {
					if parsed, pErr := strconv.ParseInt(s, 10, 64); pErr == nil {
						out.SenderID = parsed
					}
				}
			}
		default:
			var val any
			if err := json.Unmarshal(v, &val); err != nil {
				return err
			}
			extra[k] = val
		}
	}
	if len(extra) > 0 {
		out.Extra = extra
	}
	*r = out
	return nil
}

// decodeStrictInt64 严格解码 JSON 整数（拒绝布尔值、字符串与带小数点的浮点数）。
func decodeStrictInt64(raw json.RawMessage) (int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] == '"' || bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false")) || bytes.Equal(trimmed, []byte("null")) {
		return 0, fmt.Errorf("期望整数，实际 %s", string(trimmed))
	}
	var num json.Number
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&num); err != nil {
		return 0, err
	}
	return num.Int64()
}

// decodeOptionalString 解码可为 null 的 JSON 字符串字段。
func decodeOptionalString(raw json.RawMessage) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}

// resolveArchiveDir 归一归档目录路径（为空时回退默认目录）。
func resolveArchiveDir(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return DefaultSourceArchiveDir
	}
	return dir
}

// SourcePath 计算原文归档文件路径：{dir}/{sha256("{channelID}:{messageID}")}.json
// （逐字节对齐 core/ai/source_archive.py::source_path）。
func SourcePath(dir, channelID string, messageID int64) string {
	base := resolveArchiveDir(dir)
	key := fmt.Sprintf("%s:%d", channelID, messageID)
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(base, hex.EncodeToString(sum[:])+".json")
}

// ArchiveSource 原子持久化单条原文归档：同目录创建临时文件 -> 写入 -> Sync() -> Close() -> os.Rename
// （对齐 core/ai/source_archive.py::archive_source，保证崩溃或重试绝不产生半写 JSON）。
func ArchiveSource(dir string, rec SourceRecord) error {
	if rec.CreatedAt == "" {
		rec.CreatedAt = formatISOUTC(time.Now().UTC())
	}
	if err := rec.Validate(); err != nil {
		return err
	}

	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("编码归档记录失败: %w", err)
	}

	target := SourcePath(dir, rec.ChannelID, rec.MessageID)
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("创建归档目录失败 (%s): %w", parent, err)
	}

	tmp, err := os.CreateTemp(parent, ".archive-*.tmp")
	if err != nil {
		return fmt.Errorf("创建归档临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入归档临时文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("刷盘归档临时文件失败: %w", err)
	}
	// Windows 下必须先关闭文件句柄再执行 Rename。
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭归档临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("原子替换归档文件失败: %w", err)
	}
	committed = true
	return nil
}

// ReadSource 读取并校验指定 (channelID, messageID) 的原文归档记录；文件不存在时返回包装 os.ErrNotExist 的错误。
func ReadSource(dir, channelID string, messageID int64) (*SourceRecord, error) {
	path := SourcePath(dir, channelID, messageID)
	return readSourceFile(path)
}

// readSourceFile 从指定 JSON 文件读取并校验 SourceRecord。
func readSourceFile(path string) (*SourceRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec SourceRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("解析归档文件 %s 失败: %w", filepath.Base(path), err)
	}
	if err := rec.Validate(); err != nil {
		return nil, fmt.Errorf("校验归档文件 %s 失败: %w", filepath.Base(path), err)
	}
	return &rec, nil
}

// DeleteSource 幂等删除指定消息的原文归档文件（文件不存在视为成功，对齐 unlink(missing_ok=True)）。
func DeleteSource(dir, channelID string, messageID int64) error {
	err := os.Remove(SourcePath(dir, channelID, messageID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// listArchiveJSONFiles 按文件名升序枚举归档目录下的所有 *.json 文件（跳过隐藏临时文件与子目录）。
func listArchiveJSONFiles(dir string) ([]string, error) {
	base := resolveArchiveDir(dir)
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		files = append(files, filepath.Join(base, name))
	}
	sort.Strings(files)
	return files, nil
}

// WalkSources 按文件名升序遍历目录中的有效原文归档记录：
//   - 单条损坏记录记告警跳过，不中断其余合法归档文件的遍历；
//   - 若回调 fn 返回非 nil 错误，则立即停止并返回该错误。
func WalkSources(dir string, fn func(SourceRecord) error) error {
	files, err := listArchiveJSONFiles(dir)
	if err != nil {
		return err
	}
	for _, path := range files {
		rec, err := readSourceFile(path)
		if err != nil {
			slog.Warn("跳过损坏的原文归档文件", "file", path, "error", err)
			continue
		}
		if err := fn(*rec); err != nil {
			return err
		}
	}
	return nil
}

// sourceToMessageItem 将归档记录无损转换为待向量化的 MessageItem（保留原始 created_at 时区字符串及扩展字段）。
func sourceToMessageItem(rec SourceRecord) MessageItem {
	meta := make(map[string]any, len(rec.Extra)+2)
	if rec.Extra != nil {
		maps.Copy(meta, rec.Extra)
	}
	meta["created_at"] = rec.CreatedAt
	if rec.PostURL != "" {
		meta["post_url"] = rec.PostURL
	}
	channelName := rec.ChannelName
	if channelName == "" {
		channelName = rec.ChannelID
	}
	return MessageItem{
		ChannelID:       rec.ChannelID,
		ChannelName:     channelName,
		ChannelUsername: rec.ChannelUsername,
		MessageID:       rec.MessageID,
		SenderID:        rec.SenderID,
		Text:            rec.Text,
		Metadata:        meta,
	}
}

// ReindexFromArchive 离线从原文归档目录重放并重建消息向量索引
// （对齐 scripts/reindex_message_sources.py）：
//   - 原归档目录只读访问，文件与扩展元数据保持逐字节不变；
//   - 单条损坏文件或单条嵌入失败互相隔离，不影响其余记录完成入库；
//   - 返回成功重建的消息归档条数；若存在损坏文件或失败条目，同时返回聚合错误供调用方感知。
func ReindexFromArchive(ctx context.Context, dir string, store *Store, embedFn EmbedFunc, batchSize int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if store == nil {
		return 0, errors.New("store 不能为空")
	}
	if store.ReadOnly() {
		return 0, ErrReadOnly
	}
	if embedFn == nil {
		return 0, errors.New("embedFn 不能为空")
	}
	if batchSize <= 0 {
		batchSize = defaultReindexBatchSize
	}

	files, err := listArchiveJSONFiles(dir)
	if err != nil {
		return 0, err
	}

	var (
		processed int
		errs      []error
		batch     = make([]MessageItem, 0, batchSize)
	)

	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := store.UpsertMessages(ctx, embedFn, batch); err == nil {
			processed += len(batch)
			batch = batch[:0]
			return nil
		}
		// 整批失败时逐条降级重试，确保单条坏数据不拖累同批其余合法归档。
		for _, item := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, itemErr := store.UpsertMessages(ctx, embedFn, []MessageItem{item}); itemErr != nil {
				errs = append(errs, fmt.Errorf("%s:%d 重建失败: %w", item.ChannelID, item.MessageID, itemErr))
			} else {
				processed++
			}
		}
		batch = batch[:0]
		return nil
	}

	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		rec, err := readSourceFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		batch = append(batch, sourceToMessageItem(*rec))
		if len(batch) >= batchSize {
			if err := flushBatch(); err != nil {
				return processed, err
			}
		}
	}
	if err := flushBatch(); err != nil {
		return processed, err
	}
	return processed, errors.Join(errs...)
}
