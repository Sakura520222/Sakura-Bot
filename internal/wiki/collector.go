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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

var (
	reSummaryMdLink = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	reSummaryStrip  = regexp.MustCompile(`[\s*_` + "`" + `#>]+`)
)

type summaryRef struct {
	sentAt time.Time
	text   string
}

// CollectorOption 配置 Collector。
type CollectorOption func(*Collector)

// WithMessageSourceDir 指定本地消息原文归档目录（默认 data/message_sources）。
func WithMessageSourceDir(dir string) CollectorOption {
	return func(c *Collector) {
		if strings.TrimSpace(dir) != "" {
			c.messageSourceDir = strings.TrimSpace(dir)
		}
	}
}

// WithVectorDBPath 指定本地 SQLite 向量库目录（默认 data/vectors）。
func WithVectorDBPath(dir string) CollectorOption {
	return func(c *Collector) {
		if strings.TrimSpace(dir) != "" {
			c.vectorDBPath = strings.TrimSpace(dir)
		}
	}
}

// WithImageEnqueuer 注入可选的异步图片下载/视觉识别入队回调（对应 ImagePipeline.CaptureMessage）。
func WithImageEnqueuer(fn func(ctx context.Context, channel *ChannelRecord, msg *mtproto.IncomingMessage)) CollectorOption {
	return func(c *Collector) {
		c.imageEnqueuer = fn
	}
}

// Collector 负责 3 阶段本地归档导入、Telegram 全量/增量历史拉取、墓碑对账、实时事件采集与 OCR 钩子联动。
type Collector struct {
	store            *Store
	clientProvider   func() mtproto.Messenger
	messageSourceDir string
	vectorDBPath     string
	imageEnqueuer    func(ctx context.Context, channel *ChannelRecord, msg *mtproto.IncomingMessage)
	logger           *slog.Logger

	refsMu      sync.RWMutex
	summaryRefs map[string]map[int64][]summaryRef
}

// compile-time check: *Collector implements media.WikiImageHook.
var _ media.WikiImageHook = (*Collector)(nil)

// NewCollector 创建 Wiki 来源采集器。
func NewCollector(store *Store, clientProvider func() mtproto.Messenger, opts ...CollectorOption) *Collector {
	msgDir := strings.TrimSpace(os.Getenv("MESSAGE_SOURCE_DIR"))
	if msgDir == "" {
		msgDir = "data/message_sources"
	}
	vecDir := strings.TrimSpace(os.Getenv("VECTOR_DB_PATH"))
	if vecDir == "" {
		vecDir = "data/vectors"
	}
	c := &Collector{
		store:            store,
		clientProvider:   clientProvider,
		messageSourceDir: msgDir,
		vectorDBPath:     vecDir,
		logger:           slog.Default(),
		summaryRefs:      make(map[string]map[int64][]summaryRef),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Collector) client() mtproto.Messenger {
	if c == nil || c.clientProvider == nil {
		return nil
	}
	return c.clientProvider()
}

// PolicyExclusion 判断单条消息是否被频道策略排除（对齐 collector.py:policy_exclusion）。
func PolicyExclusion(cfg WikiConfig, createdAt time.Time, category string) string {
	if since, ok := cfg.HistorySinceTime(); ok && !createdAt.IsZero() && createdAt.UTC().Before(since) {
		return "before_history_since"
	}
	if !slices.Contains(cfg.MessageTypes, category) {
		return "message_type"
	}
	return ""
}

// NormalizeSummaryComparisonText 剥离总结标题行与 Markdown 标记用于重复总结比对（对齐 collector.py:summary_text）。
func NormalizeSummaryComparisonText(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimLeft(lines[0], " \t"), "📋") {
		lines = lines[1:]
	}
	joined := strings.Join(lines, "\n")
	joined = reSummaryMdLink.ReplaceAllString(joined, "$1")
	return reSummaryStrip.ReplaceAllString(joined, "")
}

func (c *Collector) channelAliasSet(ctx context.Context, channelID string) (map[string]bool, error) {
	rows, err := c.store.exec(ctx).QueryContext(ctx, "SELECT alias FROM wiki_channel_aliases WHERE channel_id=?", channelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	keys := map[string]bool{channelID: true}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err == nil && a != "" {
			keys[a] = true
		}
	}
	return keys, rows.Err()
}

// PrepareSummaryRefs 扫描 summaries 表并建立本频道机器总结消息指纹映射，防止把机器人发回源频道的总结当作一手原文。
func (c *Collector) PrepareSummaryRefs(ctx context.Context, channelID string) error {
	keys, err := c.channelAliasSet(ctx, channelID)
	if err != nil {
		return err
	}
	refs := make(map[int64][]summaryRef)
	var after int64
	for {
		rows, err := c.store.DB.QueryContext(ctx, `
			SELECT id, channel_id, created_at, summary_text, summary_message_ids
			FROM summaries WHERE id > ? ORDER BY id LIMIT 100
		`, after)
		if err != nil {
			// 单测或轻量环境下 summaries 表可能未创建，忽略表不存在错误
			c.refsMu.Lock()
			c.summaryRefs[channelID] = refs
			c.refsMu.Unlock()
			return nil
		}
		count := 0
		for rows.Next() {
			count++
			var (
				id      int64
				chRaw   string
				created any
				sumText sql.NullString
				idsRaw  []byte
			)
			if err := rows.Scan(&id, &chRaw, &created, &sumText, &idsRaw); err != nil {
				continue
			}
			after = id
			ak, akErr := AliasKey(chRaw)
			if akErr != nil || !keys[ak] {
				continue
			}
			plain := NormalizeSummaryComparisonText(sumText.String)
			sentAt := scanNullableTime(created)
			if plain == "" || sentAt == nil {
				continue
			}
			for _, mid := range parseSummaryMessageIDs(idsRaw) {
				refs[mid] = append(refs[mid], summaryRef{sentAt: *sentAt, text: plain})
			}
		}
		_ = rows.Close()
		if count == 0 {
			break
		}
	}
	c.refsMu.Lock()
	c.summaryRefs[channelID] = refs
	c.refsMu.Unlock()
	return nil
}

func parseSummaryMessageIDs(raw []byte) []int64 {
	if len(raw) == 0 {
		return nil
	}
	var arr []any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	var out []int64
	for _, item := range arr {
		switch v := item.(type) {
		case float64:
			if v > 0 {
				out = append(out, int64(v))
			}
		case string:
			if id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && id > 0 {
				out = append(out, id)
			}
		}
	}
	return out
}

// IsSummary 判断给定消息是否为机器人自身发布的周期总结贴（对齐 collector.py:is_summary）。
func (c *Collector) IsSummary(channelID string, messageID int64, body string, createdAt time.Time) bool {
	plain := NormalizeSummaryComparisonText(body)
	if plain == "" {
		return false
	}
	c.refsMu.RLock()
	candidates := c.summaryRefs[channelID][messageID]
	c.refsMu.RUnlock()
	for _, ref := range candidates {
		diff := createdAt.UTC().Sub(ref.sentAt).Seconds()
		if diff < 0 {
			diff = -diff
		}
		if diff <= 600 {
			if plain == ref.text || (utf8.RuneCountInString(plain) >= 200 && strings.Contains(ref.text, plain)) {
				return true
			}
		}
	}
	return false
}

func (c *Collector) ensureSummaryRefs(ctx context.Context, channelID string) {
	c.refsMu.RLock()
	_, exists := c.summaryRefs[channelID]
	c.refsMu.RUnlock()
	if !exists {
		_ = c.PrepareSummaryRefs(ctx, channelID)
	}
}

func extractStaticMediaKey(msg *mtproto.IncomingMessage) string {
	if msg == nil || msg.Media == nil || !msg.Media.IsStaticImage() || msg.Media.MediaID == "" {
		return ""
	}
	if msg.Media.Kind == "photo" {
		return "photo:" + msg.Media.MediaID
	}
	return "document:" + msg.Media.MediaID
}

// CaptureMessage 将实时或历史拉取到的已核实 Telegram 消息写入 wiki_sources，并联动图片采集队列。
func (c *Collector) CaptureMessage(ctx context.Context, channel *ChannelRecord, msg *mtproto.IncomingMessage) (string, bool, error) {
	if channel == nil || msg == nil || msg.ID <= 0 {
		return "", true, nil
	}
	cfg := channel.Config
	body := msg.Text
	imgKey := extractStaticMediaKey(msg)
	category := "other"
	if imgKey != "" {
		category = "image"
	} else if body != "" {
		category = "text"
	}

	c.ensureSummaryRefs(ctx, channel.ID)
	msgID := int64(msg.ID)
	reason := PolicyExclusion(cfg, msg.Date, category)
	if reason == "" && body == "" && msg.Media == nil {
		reason = "service"
	}
	if reason == "" && !cfg.IncludeForwards && msg.FwdFrom != nil {
		reason = "forward"
	}
	if reason == "" && c.IsSummary(channel.ID, msgID, body, msg.Date) {
		reason = "generated_summary"
	}
	if reason == "" && cfg.MinMessageLength > 0 && imgKey == "" && utf8.RuneCountInString(strings.TrimSpace(body)) < cfg.MinMessageLength {
		reason = "short"
	}
	if reason == "" {
		for _, kw := range cfg.ExcludeKeywords {
			if kw != "" && strings.Contains(body, kw) {
				reason = "keyword"
				break
			}
		}
	}
	if reason == "" {
		for _, pat := range cfg.ExcludeRegex {
			if re, err := regexp.Compile(pat); err == nil && re.MatchString(body) {
				reason = "regex"
				break
			}
		}
	}

	excluded := reason != ""
	groupedStr := ""
	if msg.GroupedID != 0 {
		groupedStr = strconv.FormatInt(msg.GroupedID, 10)
	}

	sourceID, err := c.store.PutSource(ctx, PutSourceInput{
		ChannelID:       channel.ID,
		SourceType:      "message",
		ExternalID:      strconv.FormatInt(msgID, 10),
		Body:            body,
		CreatedAt:       msg.Date,
		EditedAt:        msg.EditDate,
		Verified:        true,
		MessageID:       &msgID,
		OriginalLength:  utf8.RuneCountInString(body),
		Excluded:        excluded,
		ExclusionReason: reason,
		MediaGroup:      groupedStr,
		Category:        category,
		MediaKey:        imgKey,
	})
	if err != nil {
		return "", excluded, err
	}

	if imgKey != "" && cfg.IncludeImages && !excluded {
		if c.imageEnqueuer != nil {
			c.imageEnqueuer(ctx, channel, msg)
		}
		_ = c.CaptureImage(ctx, channel.ID, msgID)
	}
	return sourceID, excluded, nil
}

// CaptureDelete 撤销已删除的 Telegram 消息列表。
func (c *Collector) CaptureDelete(ctx context.Context, channelID string, messageIDs []int64) error {
	return c.store.MarkMessageDeleted(ctx, channelID, messageIDs, "telegram")
}

// CaptureImage 从 message_images 与 image_assets 读取已完成视觉识别的结果并写入 source_type='image' 来源。
func (c *Collector) CaptureImage(ctx context.Context, channelID string, messageID int64) error {
	ch, err := c.store.GetChannel(ctx, channelID)
	if err != nil || ch == nil {
		return err
	}
	var (
		mediaID               string
		groupedID, ocr, descr sql.NullString
		sourceDate, updatedAt any
	)
	err = c.store.DB.QueryRowContext(ctx, `
		SELECT m.media_id, m.grouped_id, m.source_date, a.ocr, a.description, a.updated_at
		FROM message_images m
		JOIN image_assets a ON a.sha256 = m.sha256
		WHERE m.channel_id = ? AND m.message_id = ? AND m.deleted = FALSE AND a.recognized = TRUE
	`, ch.ID, messageID).Scan(&mediaID, &groupedID, &sourceDate, &ocr, &descr, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return nil
	}

	parentID := Digest(ch.ID, "message", strconv.FormatInt(messageID, 10))
	orig, err := c.store.GetSource(ctx, parentID)
	if err != nil || orig == nil {
		return err
	}
	if orig.Deleted || orig.Hidden || orig.Excluded || !orig.Verified || orig.MediaKey != mediaID {
		return nil
	}

	created := orig.CreatedAt
	if t := scanNullableTime(sourceDate); t != nil {
		created = *t
	}
	edited := orig.EditedAt
	if t := scanNullableTime(updatedAt); t != nil && t.After(edited) {
		edited = *t
	}
	group := orig.MediaGroup
	if group == "" {
		group = groupedID.String
	}
	reason := ""
	if !ch.Config.IncludeImages {
		reason = "images_disabled"
	}

	combined := media.CombinedText("", ocr.String, descr.String)
	_, err = c.store.PutSource(ctx, PutSourceInput{
		ChannelID:       ch.ID,
		SourceType:      "image",
		ExternalID:      strconv.FormatInt(messageID, 10),
		Body:            combined,
		CreatedAt:       created,
		EditedAt:        &edited,
		Verified:        true,
		MessageID:       &messageID,
		OriginalLength:  utf8.RuneCountInString(combined),
		Excluded:        !ch.Config.IncludeImages,
		ExclusionReason: reason,
		MediaGroup:      group,
		Category:        "image",
		MediaKey:        mediaID,
	})
	return err
}

// OnImageRecognized 实现 media.WikiImageHook，当后台视觉流水线完成某条频道消息图片识别时自动补录进 Wiki。
func (c *Collector) OnImageRecognized(ctx context.Context, channelID string, messageID int64) {
	cid, err := CanonicalChannelID(media.ChannelKey(channelID))
	if err != nil || messageID <= 0 {
		return
	}
	if err := c.CaptureImage(ctx, cid, messageID); err != nil {
		c.logger.Debug("Wiki image capture skipped", "channel_id", cid, "message_id", messageID, "error", err)
	}
}

// ImportLocal 执行 3 阶段本地热启动归档导入：
//  1. data/message_sources/*.json（verified=False）；
//  2. data/vectors/lite_index.sqlite3 messages_vectors 只读连接重组分片消息（verified=False）；
//  3. MySQL summaries 表周期总结（source_type='summary'，仅供主题规划，绝不作为公开证据）。
func (c *Collector) ImportLocal(ctx context.Context, job *JobRecord, channel *ChannelRecord) error {
	if err := c.store.ReportJob(ctx, job, "正在导入已有归档和总结", false, map[string]any{"stage": "import"}); err != nil {
		return err
	}
	_ = c.PrepareSummaryRefs(ctx, channel.ID)
	keys, err := c.channelAliasSet(ctx, channel.ID)
	if err != nil {
		return err
	}

	// Stage 1: data/message_sources/*.json
	if entries, dirErr := os.ReadDir(c.messageSourceDir); dirErr == nil {
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		job.Mu.Lock()
		archiveAfter := mapString(job.Cursor, "archive")
		job.Mu.Unlock()

		for idx, name := range names {
			if name <= archiveAfter {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			raw, readErr := os.ReadFile(filepath.Join(c.messageSourceDir, name))
			if readErr != nil {
				c.incImportFailure(job)
			} else {
				var rec map[string]any
				if jsonErr := json.Unmarshal(raw, &rec); jsonErr != nil {
					c.incImportFailure(job)
				} else if ak, akErr := AliasKey(mapString(rec, "channel_id")); akErr == nil && keys[ak] {
					mid := int64(mapInt(rec, "message_id"))
					text := mapString(rec, "text")
					created, tErr := ParseUTCTime(mapString(rec, "created_at"))
					if mid <= 0 || tErr != nil {
						c.incImportFailure(job)
					} else {
						var editedPtr *time.Time
						if edStr := mapString(rec, "edited_at"); edStr != "" {
							if et, eErr := ParseUTCTime(edStr); eErr == nil {
								editedPtr = &et
							}
						}
						_, _ = c.store.PutSource(ctx, PutSourceInput{
							ChannelID:      channel.ID,
							SourceType:     "message",
							ExternalID:     strconv.FormatInt(mid, 10),
							Body:           text,
							CreatedAt:      created,
							EditedAt:       editedPtr,
							Verified:       false,
							MessageID:      &mid,
							OriginalLength: utf8.RuneCountInString(text),
						})
					}
				}
			}
			job.Mu.Lock()
			job.Cursor["archive"] = name
			job.Mu.Unlock()
			if (idx+1)%25 == 0 {
				if err := c.store.CheckpointJob(ctx, job, "", false); err != nil {
					return err
				}
			}
		}
		if err := c.store.CheckpointJob(ctx, job, "", false); err != nil {
			return err
		}
	}

	// Stage 2: Read-only SQLite lite_index.sqlite3 messages_vectors
	sqliteFile := filepath.Join(c.vectorDBPath, "lite_index.sqlite3")
	if _, statErr := os.Stat(sqliteFile); statErr == nil {
		if vecErr := c.importFromVectorSQLite(ctx, sqliteFile, keys, job, channel); vecErr != nil {
			job.Mu.Lock()
			job.Progress["vector_import_unavailable"] = true
			job.Mu.Unlock()
			_ = c.store.CheckpointJob(ctx, job, "", false)
		}
	}

	// Stage 3: MySQL summaries
	for {
		job.Mu.Lock()
		sumAfter := mapInt64(job.Cursor, "summary")
		job.Mu.Unlock()

		rows, qErr := c.store.DB.QueryContext(ctx, `
			SELECT id, channel_id, summary_text, created_at, summary_message_ids
			FROM summaries WHERE id > ? ORDER BY id LIMIT 100
		`, sumAfter)
		if qErr != nil {
			break
		}
		count := 0
		for rows.Next() {
			count++
			var (
				id      int64
				chRaw   string
				sumText sql.NullString
				created any
				idsRaw  []byte
			)
			if err := rows.Scan(&id, &chRaw, &sumText, &created, &idsRaw); err != nil {
				continue
			}
			if ak, akErr := AliasKey(chRaw); akErr == nil && keys[ak] {
				ct := NowUTC()
				if t := scanNullableTime(created); t != nil {
					ct = *t
				}
				reason := ""
				if !channel.Config.IncludeSummaries {
					reason = "summaries_disabled"
				}
				_, _ = c.store.PutSource(ctx, PutSourceInput{
					ChannelID:       channel.ID,
					SourceType:      "summary",
					ExternalID:      strconv.FormatInt(id, 10),
					Body:            sumText.String,
					CreatedAt:       ct,
					Verified:        false,
					OriginalLength:  utf8.RuneCountInString(sumText.String),
					Excluded:        !channel.Config.IncludeSummaries,
					ExclusionReason: reason,
				})
				mids := parseSummaryMessageIDs(idsRaw)
				if len(mids) > 0 {
					job.Mu.Lock()
					existing, _ := job.Cursor["summary_message_ids"].([]any)
					for _, m := range mids {
						existing = append(existing, m)
					}
					job.Cursor["summary_message_ids"] = existing
					job.Mu.Unlock()
				}
			}
			job.Mu.Lock()
			job.Cursor["summary"] = id
			job.Mu.Unlock()
		}
		_ = rows.Close()
		if count == 0 {
			break
		}
		if err := c.store.CheckpointJob(ctx, job, "", false); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collector) incImportFailure(job *JobRecord) {
	job.Mu.Lock()
	job.Progress["import_failures"] = mapInt(job.Progress, "import_failures") + 1
	job.Mu.Unlock()
}

type vectorChunkPart struct {
	chunkIndex int
	doc        string
}

type vectorReconstructed struct {
	msgID   int64
	created time.Time
	parts   []vectorChunkPart
}

func buildReadOnlySQLiteDSN(path string) string {
	p := strings.ReplaceAll(path, "\\", "/")
	p = strings.ReplaceAll(p, "%", "%25")
	p = strings.ReplaceAll(p, "?", "%3F")
	p = strings.ReplaceAll(p, "#", "%23")
	return "file:" + p + "?mode=ro&_busy_timeout=5000"
}

func (c *Collector) importFromVectorSQLite(ctx context.Context, sqliteFile string, keys map[string]bool, job *JobRecord, channel *ChannelRecord) error {
	sqDB, err := sql.Open("sqlite", buildReadOnlySQLiteDSN(sqliteFile))
	if err != nil {
		return err
	}
	defer func() { _ = sqDB.Close() }()

	multiParts := make(map[int64]*vectorReconstructed)
	for {
		job.Mu.Lock()
		vecAfter := mapString(job.Cursor, "vector")
		job.Mu.Unlock()

		rows, err := sqDB.QueryContext(ctx, "SELECT id, document, metadata_json FROM messages_vectors WHERE id > ? ORDER BY id LIMIT 100", vecAfter)
		if err != nil {
			return err
		}
		type rawRow struct {
			id   string
			doc  string
			meta string
		}
		var batch []rawRow
		for rows.Next() {
			var r rawRow
			if err := rows.Scan(&r.id, &r.doc, &r.meta); err != nil {
				_ = rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		_ = rows.Close()
		if len(batch) == 0 {
			break
		}

		for _, r := range batch {
			meta := decodeMapJSON([]byte(r.meta))
			ak, akErr := AliasKey(mapString(meta, "channel_id"))
			mid := parseAnyInt64(meta["message_id"])
			created, tErr := ParseUTCTime(mapString(meta, "created_at"))
			if akErr == nil && keys[ak] && mid > 0 && tErr == nil {
				if _, hasChunkIdx := meta["chunk_index"]; hasChunkIdx {
					rec := multiParts[mid]
					if rec == nil {
						rec = &vectorReconstructed{msgID: mid, created: created}
						multiParts[mid] = rec
					}
					rec.parts = append(rec.parts, vectorChunkPart{
						chunkIndex: mapInt(meta, "chunk_index"),
						doc:        r.doc,
					})
				} else {
					midCopy := mid
					_, _ = c.store.PutSource(ctx, PutSourceInput{
						ChannelID:      channel.ID,
						SourceType:     "message",
						ExternalID:     strconv.FormatInt(midCopy, 10),
						Body:           r.doc,
						CreatedAt:      created,
						Verified:       false,
						MessageID:      &midCopy,
						OriginalLength: utf8.RuneCountInString(r.doc),
					})
				}
			}
			job.Mu.Lock()
			job.Cursor["vector"] = r.id
			job.Mu.Unlock()
		}
		if err := c.store.CheckpointJob(ctx, job, "", false); err != nil {
			return err
		}
	}

	// 写入多段分片重组消息（已存在的 message_sources 完整版不会被覆盖，除非分片拼接更完整）
	for mid, rec := range multiParts {
		sort.Slice(rec.parts, func(i, j int) bool {
			return rec.parts[i].chunkIndex < rec.parts[j].chunkIndex
		})
		var sb strings.Builder
		for i, p := range rec.parts {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(p.doc)
		}
		doc := sb.String()
		midCopy := mid
		existingID := Digest(channel.ID, "message", strconv.FormatInt(midCopy, 10))
		if existing, _ := c.store.GetSource(ctx, existingID); existing != nil && existing.Body != "" {
			continue
		}
		_, _ = c.store.PutSource(ctx, PutSourceInput{
			ChannelID:      channel.ID,
			SourceType:     "message",
			ExternalID:     strconv.FormatInt(midCopy, 10),
			Body:           doc,
			CreatedAt:      rec.created,
			Verified:       false,
			MessageID:      &midCopy,
			OriginalLength: utf8.RuneCountInString(doc),
		})
	}
	return nil
}

func parseAnyInt64(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case json.Number:
		n, _ := val.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		return n
	}
	return 0
}

// FetchHistory 通过 MTProto Messenger 扫描频道历史消息并标记为已核实（对齐 collector.py:history）。
func (c *Collector) FetchHistory(ctx context.Context, job *JobRecord, channel *ChannelRecord) error {
	cli := c.client()
	if cli == nil || !cli.IsConnected() {
		return errors.New("Wiki UserBot is offline")
	}

	markedRef := "-100" + channel.ID
	if chInfo, err := cli.ResolveChannel(ctx, markedRef); err == nil && chInfo != nil {
		updated, regErr := c.store.RegisterChannel(ctx, ChannelEntity{
			ID:              chInfo.ID,
			Title:           chInfo.Title,
			Username:        chInfo.Username,
			ActiveUsernames: chInfo.ActiveUsernames,
			Broadcast:       chInfo.Broadcast,
			Restricted:      chInfo.Restricted,
			NoForwards:      chInfo.NoForwards,
		}, channel.ID)
		if regErr == nil && updated != nil {
			channel = updated
		}
	}

	if err := c.store.ReportJob(ctx, job, "正在读取 Telegram 历史消息", false, map[string]any{"stage": "history"}); err != nil {
		return err
	}

	job.Mu.Lock()
	minID := mapInt(job.Cursor, "history_id")
	job.Mu.Unlock()

	var startTime time.Time
	if since, ok := channel.Config.HistorySinceTime(); ok && minID == 0 {
		startTime = since
	}

	lastCheckpoint := time.Now()
	processedInScan := 0
	visit := func(msg *mtproto.IncomingMessage) error {
		if msg == nil || msg.ID <= minID {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, excluded, capErr := c.CaptureMessage(ctx, channel, msg)
		if capErr != nil {
			return capErr
		}
		msgISO := FormatISO(msg.Date)
		job.Mu.Lock()
		job.Cursor["history_id"] = msg.ID
		job.Progress["enumerated"] = mapInt(job.Progress, "enumerated") + 1
		if excluded {
			job.Progress["excluded"] = mapInt(job.Progress, "excluded") + 1
		}
		if mapString(job.Progress, "earliest") == "" {
			job.Progress["earliest"] = msgISO
		}
		job.Progress["latest"] = msgISO
		job.Mu.Unlock()

		processedInScan++
		if processedInScan%50 == 0 || time.Since(lastCheckpoint) >= 2*time.Second {
			if err := c.store.CheckpointJob(ctx, job, "", false); err != nil {
				return err
			}
			_ = c.UpdateCoverage(ctx, job, "partial", "scanning")
			lastCheckpoint = time.Now()
		}
		return nil
	}
	if stream, ok := cli.(interface {
		WalkChannelHistoryAfter(context.Context, string, time.Time, int, func(*mtproto.IncomingMessage) error) error
	}); ok {
		if err := stream.WalkChannelHistoryAfter(ctx, markedRef, startTime, minID, visit); err != nil {
			return err
		}
	} else {
		messages, chInfo, err := cli.FetchChannelHistory(ctx, markedRef, startTime, nil)
		if err != nil {
			return err
		}
		if chInfo != nil && chInfo.ID > 0 {
			_, _ = c.store.RegisterChannel(ctx, channelEntity(chInfo), channel.ID)
		}
		for _, message := range messages {
			if err := visit(message); err != nil {
				return err
			}
		}
	}
	job.Mu.Lock()
	job.Progress["estimated_total"] = mapInt(job.Progress, "enumerated")
	job.Mu.Unlock()
	if err := c.store.CheckpointJob(ctx, job, "", false); err != nil {
		return err
	}
	return c.UpdateCoverage(ctx, job, "accessible", "all_currently_accessible_messages_enumerated")
}

// Reconcile 按 100 条批次向 Telegram 重新核对未确认或旧观察消息，自动更新编辑并墓碑化已删除帖子（对齐 collector.py:reconcile）。
func (c *Collector) Reconcile(ctx context.Context, job *JobRecord, channel *ChannelRecord) error {
	cli := c.client()
	if cli == nil || !cli.IsConnected() {
		return errors.New("Wiki UserBot is offline")
	}

	cutoff := db.MysqlDatetime6(job.CreatedAt)
	job.Mu.Lock()
	recAfter := mapInt64(job.Cursor, "reconcile_id")
	job.Mu.Unlock()

	var total int
	_ = c.store.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM wiki_sources
		WHERE channel_id=? AND source_type='message' AND deleted=FALSE AND message_id > ?
		  AND (verified=FALSE OR observed_at < ?)
	`, channel.ID, recAfter, cutoff).Scan(&total)

	if err := c.store.ReportJob(ctx, job, "正在核对未确认的历史记录", false, map[string]any{
		"stage":           "reconcile",
		"reconcile_total": total,
		"reconciled":      0,
	}); err != nil {
		return err
	}

	markedRef := "-100" + channel.ID
	for {
		job.Mu.Lock()
		recAfter = mapInt64(job.Cursor, "reconcile_id")
		job.Mu.Unlock()

		rows, err := c.store.DB.QueryContext(ctx, `
			SELECT message_id FROM wiki_sources
			WHERE channel_id=? AND source_type='message' AND deleted=FALSE AND message_id > ?
			  AND (verified=FALSE OR observed_at < ?)
			ORDER BY message_id LIMIT 100
		`, channel.ID, recAfter, cutoff)
		if err != nil {
			return err
		}
		var batchIDs []int64
		var intIDs []int
		for rows.Next() {
			var mid int64
			if err := rows.Scan(&mid); err == nil && mid > 0 {
				batchIDs = append(batchIDs, mid)
				intIDs = append(intIDs, int(mid))
			}
		}
		_ = rows.Close()
		if len(batchIDs) == 0 {
			break
		}

		fetched, err := cli.GetMessages(ctx, markedRef, intIDs)
		if err != nil {
			return err
		}
		byID := make(map[int64]*mtproto.IncomingMessage, len(fetched))
		for _, m := range fetched {
			if m != nil && m.ID > 0 && !m.Date.IsZero() {
				byID[int64(m.ID)] = m
			}
		}

		for _, mid := range batchIDs {
			if m, ok := byID[mid]; ok {
				if _, _, err := c.CaptureMessage(ctx, channel, m); err != nil {
					return err
				}
			} else {
				if err := c.store.MarkMessageDeleted(ctx, channel.ID, []int64{mid}, "telegram"); err != nil {
					return err
				}
			}
			job.Mu.Lock()
			job.Cursor["reconcile_id"] = mid
			job.Progress["reconciled"] = mapInt(job.Progress, "reconciled") + 1
			job.Mu.Unlock()
		}
		if err := c.store.CheckpointJob(ctx, job, "", false); err != nil {
			return err
		}
	}
	return c.UpdateCoverage(ctx, job, "accessible", "all_currently_accessible_messages_enumerated")
}

// RepairExclusions 重新核实频道内被标记为排除或未确认的历史记录（对齐 collector.py:repair_exclusions）。
func (c *Collector) RepairExclusions(ctx context.Context, channel *ChannelRecord) ([]int64, error) {
	_ = c.PrepareSummaryRefs(ctx, channel.ID)
	rows, err := c.store.DB.QueryContext(ctx, `
		SELECT id, message_id, created_at, category, verified
		FROM wiki_sources
		WHERE channel_id=? AND source_type='message' AND (excluded=TRUE OR verified=FALSE) AND deleted=FALSE
		ORDER BY message_id
	`, channel.ID)
	if err != nil {
		return nil, err
	}
	type inspectRow struct {
		id       string
		msgID    int64
		created  time.Time
		category string
		verified bool
	}
	known := make(map[string][]string)
	var inspect []inspectRow
	for rows.Next() {
		var r inspectRow
		var mid sql.NullInt64
		var createdRaw any
		if err := rows.Scan(&r.id, &mid, &createdRaw, &r.category, &r.verified); err != nil {
			_ = rows.Close()
			return nil, err
		}
		r.msgID = mid.Int64
		if t := scanNullableTime(createdRaw); t != nil {
			r.created = *t
		}
		reason := PolicyExclusion(channel.Config, r.created, r.category)
		if reason != "" && r.verified {
			known[reason] = append(known[reason], r.id)
		} else if r.msgID > 0 {
			inspect = append(inspect, r)
		}
	}
	_ = rows.Close()

	for reason, ids := range known {
		for start := 0; start < len(ids); start += 250 {
			end := min(len(ids), start+250)
			marks, iArgs := stringPlaceholders(ids[start:end])
			args := append([]any{reason}, iArgs...)
			_, _ = c.store.DB.ExecContext(ctx, "UPDATE wiki_sources SET exclusion_reason=? WHERE id IN ("+marks+")", args...)
		}
	}

	var restored []int64
	if len(inspect) > 0 {
		cli := c.client()
		if cli == nil || !cli.IsConnected() {
			return nil, errors.New("UserBot 未连接，旧排除记录暂未修改")
		}
		markedRef := "-100" + channel.ID
		for start := 0; start < len(inspect); start += 100 {
			batch := inspect[start:min(len(inspect), start+100)]
			intIDs := make([]int, len(batch))
			for i, b := range batch {
				intIDs[i] = int(b.msgID)
			}
			fetched, err := cli.GetMessages(ctx, markedRef, intIDs)
			if err != nil {
				return nil, err
			}
			byID := make(map[int64]*mtproto.IncomingMessage, len(fetched))
			for _, m := range fetched {
				if m != nil && m.ID > 0 && !m.Date.IsZero() {
					byID[int64(m.ID)] = m
				}
			}
			for _, item := range batch {
				if m, ok := byID[item.msgID]; ok {
					_, excluded, err := c.CaptureMessage(ctx, channel, m)
					if err == nil && !excluded {
						restored = append(restored, item.msgID)
					}
				} else {
					_, _ = c.store.DB.ExecContext(ctx, "UPDATE wiki_sources SET excluded=TRUE, exclusion_reason='unavailable' WHERE id=?", item.id)
				}
			}
		}
	}

	var excludedTotal sql.NullInt64
	_ = c.store.DB.QueryRowContext(ctx, "SELECT SUM(excluded) FROM wiki_sources WHERE channel_id=? AND source_type='message' AND deleted=FALSE", channel.ID).Scan(&excludedTotal)
	_, _ = c.store.DB.ExecContext(ctx, "UPDATE wiki_channels SET coverage=JSON_SET(coverage, '$.excluded', ?) WHERE id=?", excludedTotal.Int64, channel.ID)
	return restored, nil
}

// UpdateCoverage 重新统计频道可见来源覆盖指标并写入 wiki_channels.coverage。
func (c *Collector) UpdateCoverage(ctx context.Context, job *JobRecord, status, reason string) error {
	var (
		indexed          int64
		earliest, latest any
		excludedSum      sql.NullInt64
	)
	err := c.store.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(created_at), MAX(created_at), SUM(excluded)
		FROM wiki_sources
		WHERE channel_id=? AND source_type='message' AND deleted=FALSE
	`, job.ChannelID).Scan(&indexed, &earliest, &latest, &excludedSum)
	if err != nil {
		return err
	}

	job.Mu.Lock()
	enumerated := mapInt(job.Progress, "enumerated")
	estTotal := job.Progress["estimated_total"]
	failures := mapInt(job.Progress, "collection_failures")
	hasImportFailures := mapInt(job.Progress, "import_failures") > 0
	job.Mu.Unlock()

	if hasImportFailures {
		status = "partial"
		reason = "some_archived_sources_unreadable"
	}
	coverage := map[string]any{
		"indexed":         indexed,
		"earliest":        formatNullableTime(earliest),
		"latest":          formatNullableTime(latest),
		"excluded":        excludedSum.Int64,
		"status":          status,
		"reason":          reason,
		"enumerated":      enumerated,
		"estimated_total": estTotal,
		"failures":        failures,
		"missing_ranges":  []any{},
		"updated_at":      FormatISO(NowUTC()),
	}
	_, err = c.store.DB.ExecContext(ctx, "UPDATE wiki_channels SET coverage=? WHERE id=?", EncodeJSON(coverage), job.ChannelID)
	return err
}
