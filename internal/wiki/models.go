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
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	SkippedAIReview    = "ai-review-disabled"
	ManualReviewPolicy = "manual-review"
	MessageMediaPrefix = "message:"

	DefaultChunkSize    = 5000
	DefaultChunkOverlap = 300
)

var (
	ErrUngroundedCitation = errors.New("citation does not match its source and offsets")

	reCanonicalChannel = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
	reChannelAlias     = regexp.MustCompile(`^[a-z][a-z0-9_]{3,63}$`)
	reHex64            = regexp.MustCompile(`^[a-f0-9]{64}$`)
	reHex32            = regexp.MustCompile(`^[a-f0-9]{32}$`)
	reValidSlug        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,180}$`)
	reSlugNonAlnum     = regexp.MustCompile(`[^a-z0-9]+`)
	reEmptyTitleCat    = regexp.MustCompile(`^[/／]\s+(\S)`)
)

var (
	validFactKinds = map[string]bool{
		"Topic": true, "Entity": true, "Event": true,
		"Claim": true, "FAQ": true, "Procedure": true,
	}
	validFactStatuses = map[string]bool{
		"current": true, "uncertain": true, "disputed": true, "superseded": true,
	}
	validRelationKinds = map[string]bool{
		"belongs_to": true, "depends_on": true, "discusses": true, "solves": true,
		"replaces": true, "supports": true, "contradicts": true, "evolves": true,
	}
	validClaimReviewStatuses = map[string]bool{
		"supported": true, "unsupported": true, "sensitive": true,
	}
	validFactCheckStatuses = map[string]bool{
		"verified": true, "partial": true, "disputed": true,
		"unverified": true, "outdated": true, "rejected": true,
	}
)

// WikiConfig 定义单频道 Wiki 配置契约（完整对齐 core/wiki/models.py:WikiConfig 全部字段、默认值与边界约束）。
type WikiConfig struct {
	Enabled               bool     `json:"enabled"`
	Public                bool     `json:"public"`
	Paused                bool     `json:"paused"`
	PublicationMode       string   `json:"publication_mode"`
	AIReviewEnabled       bool     `json:"ai_review_enabled"`
	FactCheck             bool     `json:"fact_check"`
	FactCheckRequired     bool     `json:"fact_check_required"`
	ProposalsEnabled      bool     `json:"proposals_enabled"`
	AnnotationsEnabled    bool     `json:"annotations_enabled"`
	ProposalNotifications bool     `json:"proposal_notifications"`
	Frequency             string   `json:"frequency"`
	Cron                  string   `json:"cron"`
	Timezone              string   `json:"timezone"`
	HistorySince          *string  `json:"history_since"`
	IncludeSummaries      bool     `json:"include_summaries"`
	IncludeImages         bool     `json:"include_images"`
	IncludeForwards       bool     `json:"include_forwards,omitempty"`
	MessageTypes          []string `json:"message_types"`
	Language              string   `json:"language"`
	Columns               []string `json:"columns"`
	ShowQuotes            bool     `json:"show_quotes"`
	ShowTelegramLinks     bool     `json:"show_telegram_links"`
	PublicBaseURL         string   `json:"public_base_url"`
	Description           string   `json:"description"`
	CoverURL              string   `json:"cover_url"`
	HiddenTopics          []string `json:"hidden_topics"`
	ExcludeKeywords       []string `json:"exclude_keywords,omitempty"`
	ExcludeRegex          []string `json:"exclude_regex,omitempty"`
	Temperature           float64  `json:"temperature"`
	TimeoutSeconds        int      `json:"timeout_seconds"`
	Retries               int      `json:"retries"`
	Concurrency           int      `json:"concurrency"`
	TokenBudget           int64    `json:"token_budget"`
	MaxOutputTokens       int      `json:"max_output_tokens"`
	BatchCharBudget       int      `json:"batch_char_budget,omitempty"`
	MaxBatchChunks        int      `json:"max_batch_chunks,omitempty"`
	MinMessageLength      int      `json:"min_message_length,omitempty"`
	MinEntitySources      int      `json:"min_entity_sources,omitempty"`
	ExtractionCacheDays   int      `json:"extraction_cache_days,omitempty"`
	RetentionPublications int      `json:"retention_publications"`
	InputCostPerMillion   *float64 `json:"input_cost_per_million"`
	OutputCostPerMillion  *float64 `json:"output_cost_per_million"`
	DebounceSeconds       int      `json:"debounce_seconds"`
}

// DefaultWikiConfig 返回与 Python WikiConfig() 完全一致的默认配置。
func DefaultWikiConfig() WikiConfig {
	return WikiConfig{
		Enabled:               false,
		Public:                false,
		Paused:                false,
		PublicationMode:       "review",
		AIReviewEnabled:       true,
		FactCheck:             false,
		FactCheckRequired:     false,
		ProposalsEnabled:      false,
		AnnotationsEnabled:    false,
		ProposalNotifications: false,
		Frequency:             "manual",
		Cron:                  "0 3 * * *",
		Timezone:              "Asia/Shanghai",
		HistorySince:          nil,
		IncludeSummaries:      true,
		IncludeImages:         true,
		IncludeForwards:       true,
		MessageTypes:          []string{"text", "image", "other"},
		Language:              "zh-CN",
		Columns:               []string{"pages", "graph", "timeline", "profile"},
		ShowQuotes:            false,
		ShowTelegramLinks:     true,
		PublicBaseURL:         "",
		Description:           "",
		CoverURL:              "",
		HiddenTopics:          []string{},
		Temperature:           0.2,
		TimeoutSeconds:        180,
		Retries:               3,
		Concurrency:           1,
		TokenBudget:           10_000_000,
		MaxOutputTokens:       65536,
		BatchCharBudget:       12000,
		MaxBatchChunks:        32,
		RetentionPublications: 20,
		DebounceSeconds:       120,
	}
}

// ParseWikiConfig 反序列化并严格校验频道配置 JSON（缺省字段自动填充默认值）。
func ParseWikiConfig(raw []byte) (WikiConfig, error) {
	cfg := DefaultWikiConfig()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "{}" || string(trimmed) == "null" {
		return cfg, nil
	}
	if err := json.Unmarshal(trimmed, &cfg); err != nil {
		return cfg, fmt.Errorf("解析 WikiConfig 失败: %w", err)
	}
	if cfg.HiddenTopics == nil {
		cfg.HiddenTopics = []string{}
	}
	if cfg.MessageTypes == nil {
		cfg.MessageTypes = []string{"text", "image", "other"}
	}
	if cfg.Columns == nil {
		cfg.Columns = []string{"pages", "graph", "timeline", "profile"}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// HistorySinceTime 解析 HistorySince 为 UTC 时间（未设置返回零值与 false）。
func (c WikiConfig) HistorySinceTime() (time.Time, bool) {
	if c.HistorySince == nil || strings.TrimSpace(*c.HistorySince) == "" {
		return time.Time{}, false
	}
	t, err := ParseUTCTime(*c.HistorySince)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Validate 执行全部字段边界与跨字段业务不变量校验（对齐 core/wiki/models.py:119-180）。
func (c *WikiConfig) Validate() error {
	if c.Public && !c.Enabled {
		return errors.New("Enable the Wiki before making it public")
	}
	if c.FactCheckRequired && !c.AIReviewEnabled {
		return errors.New("要求外部证据核实时须启用 AI 来源审校")
	}
	if c.PublicationMode != "review" && c.PublicationMode != "auto" {
		return fmt.Errorf("invalid publication_mode: %q", c.PublicationMode)
	}
	switch c.Frequency {
	case "manual", "daily", "weekly", "cron":
	default:
		return fmt.Errorf("invalid frequency: %q", c.Frequency)
	}
	if _, err := parseWikiCron(c.Cron); err != nil {
		return fmt.Errorf("invalid cron: %w", err)
	}
	if strings.TrimSpace(c.Timezone) == "" {
		c.Timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", c.Timezone, err)
	}
	if c.HistorySince != nil && strings.TrimSpace(*c.HistorySince) != "" {
		if _, err := ParseUTCTime(*c.HistorySince); err != nil {
			return fmt.Errorf("invalid history_since: %w", err)
		}
	}
	for _, mt := range c.MessageTypes {
		if mt != "text" && mt != "image" && mt != "other" {
			return fmt.Errorf("invalid message_type: %q", mt)
		}
	}
	langLen := utf8.RuneCountInString(c.Language)
	if langLen < 2 || langLen > 32 {
		return errors.New("language must be 2..32 characters")
	}
	for _, col := range c.Columns {
		switch col {
		case "pages", "graph", "timeline", "profile":
		default:
			return fmt.Errorf("invalid column: %q", col)
		}
	}
	if c.PublicBaseURL != "" {
		if len(c.PublicBaseURL) > 2048 {
			return errors.New("public_base_url exceeds 2048 characters")
		}
		u, err := url.Parse(c.PublicBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
			u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("Use the public WebUI origin without a path or credentials")
		}
		c.PublicBaseURL = strings.TrimRight(c.PublicBaseURL, "/")
	}
	if c.CoverURL != "" {
		if len(c.CoverURL) > 2048 {
			return errors.New("cover_url exceeds 2048 characters")
		}
		u, err := url.Parse(c.CoverURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return errors.New("Use an HTTPS image URL without credentials")
		}
	}
	if utf8.RuneCountInString(c.Description) > 280 {
		return errors.New("description exceeds 280 characters")
	}
	if len(c.HiddenTopics) > 100 {
		return errors.New("hidden_topics exceeds 100 items")
	}
	if math.IsNaN(c.Temperature) || c.Temperature < 0 || c.Temperature > 1 {
		return errors.New("temperature must be in [0, 1]")
	}
	if c.TimeoutSeconds < 10 || c.TimeoutSeconds > 1800 {
		return errors.New("timeout_seconds must be in [10, 1800]")
	}
	if c.Retries < 0 || c.Retries > 8 {
		return errors.New("retries must be in [0, 8]")
	}
	if c.Concurrency < 1 || c.Concurrency > 4 {
		return errors.New("concurrency must be in [1, 4]")
	}
	if c.TokenBudget < 1000 {
		return errors.New("token_budget must be >= 1000")
	}
	if c.MaxOutputTokens < 1024 || c.MaxOutputTokens > 262144 {
		return errors.New("max_output_tokens must be in [1024, 262144]")
	}
	if c.RetentionPublications < 2 || c.RetentionPublications > 200 {
		return errors.New("retention_publications must be in [2, 200]")
	}
	if c.DebounceSeconds < 10 || c.DebounceSeconds > 86400 {
		return errors.New("debounce_seconds must be in [10, 86400]")
	}
	if c.InputCostPerMillion != nil && (*c.InputCostPerMillion < 0 || math.IsNaN(*c.InputCostPerMillion)) {
		return errors.New("input_cost_per_million must be >= 0")
	}
	if c.OutputCostPerMillion != nil && (*c.OutputCostPerMillion < 0 || math.IsNaN(*c.OutputCostPerMillion)) {
		return errors.New("output_cost_per_million must be >= 0")
	}
	for _, pat := range c.ExcludeRegex {
		if _, err := regexp.Compile(pat); err != nil {
			return fmt.Errorf("invalid exclude_regex %q: %w", pat, err)
		}
	}
	return nil
}

// Citation 表示绑定到具体来源与分块字符坐标的原文引证（对齐 core/wiki/models.py:Citation）。
type Citation struct {
	SourceID string `json:"source_id"`
	ChunkID  string `json:"chunk_id"`
	Quote    string `json:"quote"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

// Fact 表示从频道原文提取并落地的单条结构化断言（对齐 core/wiki/models.py:Fact）。
type Fact struct {
	Entity          string           `json:"entity"`
	Kind            string           `json:"kind"`
	Aliases         []string         `json:"aliases,omitempty"`
	Topic           string           `json:"topic"`
	Key             string           `json:"key"`
	Text            string           `json:"text"`
	OccurredAt      *string          `json:"occurred_at,omitempty"`
	ValidUntil      *string          `json:"valid_until,omitempty"`
	Status          string           `json:"status"`
	Confidence      float64          `json:"confidence"`
	Evidence        []Citation       `json:"evidence"`
	SourceCreatedAt string           `json:"source_created_at,omitempty"`
	MediaGroup      string           `json:"media_group,omitempty"`
	AlbumMessages   []int64          `json:"album_messages,omitempty"`
	CommunityNotes  []map[string]any `json:"community_notes,omitempty"`
}

// Relation 表示两个实体之间的有向语义边（对齐 core/wiki/models.py:Relation）。
type Relation struct {
	Source     string     `json:"source"`
	Target     string     `json:"target"`
	Kind       string     `json:"kind"`
	Evidence   []Citation `json:"evidence"`
	Confidence float64    `json:"confidence"`
}

// Extraction 表示单次模型提取返回的断言与关系集合（对齐 core/wiki/models.py:Extraction）。
type Extraction struct {
	Facts     []Fact     `json:"facts"`
	Relations []Relation `json:"relations"`
}

// ClaimReview 表示独立来源审校中单条段落的审校结果（对齐 core/wiki/models.py:ClaimReview）。
type ClaimReview struct {
	Index      int    `json:"index"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	FactStatus string `json:"fact_status"`
	Citations  []int  `json:"citations,omitempty"`
}

// Review 表示独立来源审校批次决策（对齐 core/wiki/models.py:Review）。
type Review struct {
	Approved bool          `json:"approved"`
	Reason   string        `json:"reason"`
	Claims   []ClaimReview `json:"claims"`
}

// ProfileNarrative 表示频道画像叙述（对齐 core/wiki/models.py:ProfileNarrative）。
type ProfileNarrative struct {
	Description string `json:"description"`
	Style       string `json:"style"`
	Limitations string `json:"limitations"`
}

// Chunk 表示按 Unicode 码点切分的原文滑动窗口分块。
type Chunk struct {
	ID          string `json:"id"`
	SourceID    string `json:"source_id"`
	ContentHash string `json:"content_hash"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	Text        string `json:"text"`
}

// NowUTC 返回截断至微秒精度的当前 UTC 时间。
func NowUTC() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

// FormatISO 格式化为无时区后缀的 UTC ISO-8601 字符串（对齐 Python utc().isoformat()）。
func FormatISO(t time.Time) string {
	u := t.UTC().Truncate(time.Microsecond)
	base := u.Format("2006-01-02T15:04:05")
	micros := u.Nanosecond() / 1000
	if micros != 0 {
		return fmt.Sprintf("%s.%06d", base, micros)
	}
	return base
}

// ParseUTCTime 兼容解析 RFC3339、Python ISO 字符串与 MySQL DATETIME(6) 字符串并转为 UTC。
func ParseUTCTime(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, errors.New("empty time string")
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UTC().Truncate(time.Microsecond), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", raw)
}

// EncodeJSON 序列化为键有序且不转义 HTML 的紧凑 JSON 字符串（对齐 core/wiki/store.py:encode）。
func EncodeJSON(v any) string {
	normalized := normalizeJSONValue(v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(normalized)
	return strings.TrimSuffix(buf.String(), "\n")
}

func normalizeJSONValue(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return fmt.Sprint(v)
	}
	return out
}

// Digest 计算任意参数元组的确定性 SHA-256 十六进制摘要（64 字符，对齐 core/wiki/models.py:digest）。
func Digest(parts ...any) string {
	if parts == nil {
		parts = []any{}
	}
	raw := EncodeJSON(parts)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// StableID 生成指定长度前缀的确定性 SHA-256 十六进制 ID（length 范围 1..64）。
func StableID(length int, parts ...any) string {
	full := Digest(parts...)
	if length <= 0 || length >= 64 {
		return full
	}
	return full[:length]
}

// NewID 生成 32 位小写十六进制随机标识符（对齐 uuid4().hex）。
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return StableID(32, time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// CanonicalChannelID 将频道数字 ID（含可选 -100 前缀）规范化为无前缀正整数字符串（对齐 canonical_id）。
func CanonicalChannelID(value string) (string, error) {
	s := strings.TrimSpace(value)
	s = strings.TrimPrefix(s, "-100")
	if !reCanonicalChannel.MatchString(s) {
		return "", errors.New("Wiki channel identity must be a Telegram channel ID")
	}
	return s, nil
}

// AliasKey 将频道链接、@handle 或数字 ID 规范化为 wiki_channel_aliases 查询键（对齐 alias_key）。
func AliasKey(value string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"https://t.me/", "http://t.me/", "t.me/"} {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.TrimLeft(s, "@")
	s = strings.TrimRight(s, "/")
	if cid, err := CanonicalChannelID(s); err == nil {
		return cid, nil
	}
	if !reChannelAlias.MatchString(s) {
		return "", errors.New("Invalid channel alias")
	}
	return s, nil
}

// NameKey 执行 NFKC 归一化、小写折叠与空白压缩（对齐 core/wiki/models.py:name_key）。
func NameKey(value string) string {
	normalized := norm.NFKC.String(value)
	lowered := strings.ToLower(normalized)
	return strings.Join(strings.Fields(lowered), " ")
}

// EntityKey 计算实体名称经 NFKC 小写与空白折叠后的 SHA-256 十六进制键（64 字符）。
func EntityKey(name string) string {
	sum := sha256.Sum256([]byte(NameKey(name)))
	return hex.EncodeToString(sum[:])
}

// Slugify 生成符合 ^[a-z0-9_-]{1,180}$ 路由契约的词条 URL Slug；纯中文或符号标题回退至 item-<hash8>。
func Slugify(text string) string {
	normalized := strings.ToLower(norm.NFKC.String(strings.TrimSpace(text)))
	slug := reSlugNonAlnum.ReplaceAllString(normalized, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "item-" + EntityKey(text)[:8]
	}
	if len(slug) > 180 {
		slug = strings.TrimRight(slug[:180], "-")
		if slug == "" {
			return "item-" + EntityKey(text)[:8]
		}
	}
	return slug
}

// StripEmptyTitleCategory 移除缺失分类名时前导的孤立斜杠分隔符（对齐 strip_empty_title_category）。
func StripEmptyTitleCategory(title string) string {
	loc := reEmptyTitleCat.FindStringSubmatchIndex(title)
	if loc == nil {
		return title
	}
	return title[loc[2]:]
}

// Chunks 按确定性 Unicode 码点（rune）滑动窗口切分文本，步长 step = size - overlap。
func Chunks(text string, size, overlap int) ([]Chunk, error) {
	if size <= 0 {
		return nil, errors.New("chunk size must be positive")
	}
	if overlap < 0 || overlap >= size {
		return nil, errors.New("invalid chunk overlap")
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return nil, nil
	}
	step := size - overlap
	var out []Chunk
	for start := 0; start < len(runes); start += step {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, Chunk{
			Start: start,
			End:   end,
			Text:  string(runes[start:end]),
		})
		if end == len(runes) {
			break
		}
	}
	return out, nil
}

// BuildSourceChunks 为已持久化的原文构造带确定性 SHA-256 ID 的分块切片（对齐 models.py:chunks）。
func BuildSourceChunks(sourceID, body, contentHash string, size, overlap int) ([]Chunk, error) {
	if size <= 0 {
		size = DefaultChunkSize
	}
	if overlap < 0 {
		overlap = DefaultChunkOverlap
	}
	rawChunks, err := Chunks(body, size, overlap)
	if err != nil {
		return nil, err
	}
	for i := range rawChunks {
		rawChunks[i].ID = Digest(sourceID, contentHash, rawChunks[i].Start, rawChunks[i].End)
		rawChunks[i].SourceID = sourceID
		rawChunks[i].ContentHash = contentHash
	}
	return rawChunks, nil
}

// ValidateExtraction 对模型提取的 Facts 与 Relations 执行严格的原文锚定校验与轻微坐标偏移修复：
//   - 若引用的 source_id / chunk_id 与当前 chunk 冲突（非空且不相等），立即拒绝；
//   - 若 0 <= relStart < relEnd <= len(chunkRunes) 且切片与 quote 逐字符相等，原样通过；
//   - 若坐标缺失或轻微漂移，但在 chunk.Text 中存在唯一/最近邻精确原文匹配，自动修复为精确码点偏移；
//   - 若 quote 为空或未在 chunk.Text 中原文出现，返回 ErrUngroundedCitation。
func ValidateExtraction(ext *Extraction, chunk Chunk) (*Extraction, error) {
	if ext == nil {
		return &Extraction{}, nil
	}
	if len(ext.Facts) > 80 || len(ext.Relations) > 80 {
		return nil, errors.New("extraction exceeds maximum fact/relation count")
	}
	chunkRunes := []rune(chunk.Text)

	validateAndRepairCitations := func(citations []Citation) error {
		if len(citations) == 0 || len(citations) > 20 {
			return ErrUngroundedCitation
		}
		for i := range citations {
			ev := &citations[i]
			if ev.Quote == "" || utf8.RuneCountInString(ev.Quote) > 6000 {
				return ErrUngroundedCitation
			}
			if ev.SourceID != "" && chunk.SourceID != "" && ev.SourceID != chunk.SourceID {
				return ErrUngroundedCitation
			}
			if ev.ChunkID != "" && chunk.ID != "" && ev.ChunkID != chunk.ID {
				return ErrUngroundedCitation
			}
			ev.SourceID = chunk.SourceID
			ev.ChunkID = chunk.ID

			quoteRunes := []rune(ev.Quote)
			qLen := len(quoteRunes)
			if qLen == 0 || qLen > len(chunkRunes) {
				return ErrUngroundedCitation
			}

			// 1. 检查给定坐标是否直接精确命中
			if ev.Start >= chunk.Start && ev.End <= chunk.End && ev.End-ev.Start == qLen {
				relStart := ev.Start - chunk.Start
				relEnd := ev.End - chunk.Start
				if relEnd <= len(chunkRunes) && string(chunkRunes[relStart:relEnd]) == ev.Quote {
					continue
				}
			}

			// 2. 在当前 chunk.Text 中查找精确子串以修复轻微坐标漂移
			relPos := findClosestRuneSubstring(chunkRunes, quoteRunes, ev.Start-chunk.Start)
			if relPos < 0 {
				return ErrUngroundedCitation
			}
			ev.Start = chunk.Start + relPos
			ev.End = ev.Start + qLen
		}
		return nil
	}

	for i := range ext.Facts {
		f := &ext.Facts[i]
		f.Entity = strings.TrimSpace(f.Entity)
		f.Text = strings.TrimSpace(f.Text)
		if f.Entity == "" || utf8.RuneCountInString(f.Entity) > 160 || f.Text == "" || utf8.RuneCountInString(f.Text) > 16000 {
			return nil, errors.New("invalid fact entity or text")
		}
		if f.Kind == "" {
			f.Kind = "Topic"
		}
		if !validFactKinds[f.Kind] {
			return nil, fmt.Errorf("invalid fact kind: %q", f.Kind)
		}
		if f.Topic == "" {
			f.Topic = "其他"
		}
		if f.Key == "" {
			f.Key = "fact:" + StableID(16, f.Entity, f.Text)
		}
		if f.Status == "" {
			f.Status = "current"
		}
		if !validFactStatuses[f.Status] {
			return nil, fmt.Errorf("invalid fact status: %q", f.Status)
		}
		if math.IsNaN(f.Confidence) || f.Confidence < 0 || f.Confidence > 1 {
			return nil, errors.New("fact confidence out of range [0,1]")
		}
		if err := validateAndRepairCitations(f.Evidence); err != nil {
			return nil, err
		}
	}

	for i := range ext.Relations {
		r := &ext.Relations[i]
		r.Source = strings.TrimSpace(r.Source)
		r.Target = strings.TrimSpace(r.Target)
		if r.Source == "" || utf8.RuneCountInString(r.Source) > 160 ||
			r.Target == "" || utf8.RuneCountInString(r.Target) > 160 {
			return nil, errors.New("invalid relation endpoint")
		}
		if !validRelationKinds[r.Kind] {
			return nil, fmt.Errorf("invalid relation kind: %q", r.Kind)
		}
		if math.IsNaN(r.Confidence) || r.Confidence < 0 || r.Confidence > 1 {
			return nil, errors.New("relation confidence out of range [0,1]")
		}
		if err := validateAndRepairCitations(r.Evidence); err != nil {
			return nil, err
		}
	}

	return ext, nil
}

func findClosestRuneSubstring(haystack, needle []rune, hint int) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	bestPos := -1
	bestDist := int(^uint(0) >> 1)
	limit := len(haystack) - len(needle)
	for i := 0; i <= limit; i++ {
		if haystack[i] != needle[0] {
			continue
		}
		match := true
		for j := 1; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			if hint < 0 {
				return i
			}
			dist := i - hint
			if dist < 0 {
				dist = -dist
			}
			if dist < bestDist {
				bestDist = dist
				bestPos = i
				if dist == 0 {
					return i
				}
			}
		}
	}
	return bestPos
}

// UnifiedDiff 生成标准 Unified Diff 行序列（3 行上下文，对齐 Python difflib.unified_diff(..., lineterm="")）。
func UnifiedDiff(oldLines, newLines []string, fromFile, toFile string) []string {
	const contextLines = 3
	edits := computeDiffEdits(oldLines, newLines)

	hasChange := false
	for _, e := range edits {
		if e.op != ' ' {
			hasChange = true
			break
		}
	}
	if !hasChange {
		return nil
	}

	if fromFile == "" {
		fromFile = "before"
	}
	if toFile == "" {
		toFile = "after"
	}
	out := []string{
		"--- " + fromFile,
		"+++ " + toFile,
	}

	// 将邻近变更聚合成带 3 行上下文的 hunk
	type hunkRange struct{ start, end int }
	var hunks []hunkRange
	for i, e := range edits {
		if e.op == ' ' {
			continue
		}
		s := max(0, i-contextLines)
		end := min(len(edits), i+contextLines+1)
		if len(hunks) > 0 && s <= hunks[len(hunks)-1].end {
			hunks[len(hunks)-1].end = max(hunks[len(hunks)-1].end, end)
		} else {
			hunks = append(hunks, hunkRange{start: s, end: end})
		}
	}

	for _, h := range hunks {
		sub := edits[h.start:h.end]
		oldStart, newStart := sub[0].oldIdx+1, sub[0].newIdx+1
		oldCount, newCount := 0, 0
		for _, e := range sub {
			if e.op == ' ' || e.op == '-' {
				oldCount++
			}
			if e.op == ' ' || e.op == '+' {
				newCount++
			}
		}
		if oldCount == 0 {
			oldStart--
		}
		if newCount == 0 {
			newStart--
		}
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldCount, newStart, newCount))
		for _, e := range sub {
			out = append(out, string(e.op)+e.text)
		}
	}
	return out
}

type diffEdit struct {
	op     byte // ' ', '-', '+'
	text   string
	oldIdx int
	newIdx int
}

func computeDiffEdits(a, b []string) []diffEdit {
	n, m := len(a), len(b)
	// 快速剥离公共前后缀，大幅降低大文档编辑的 LCS 矩阵开销
	prefix := 0
	for prefix < n && prefix < m && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < (n-prefix) && suffix < (m-prefix) && a[n-1-suffix] == b[m-1-suffix] {
		suffix++
	}

	midA := a[prefix : n-suffix]
	midB := b[prefix : m-suffix]
	na, nb := len(midA), len(midB)

	var midEdits []diffEdit
	if na == 0 && nb == 0 {
		// 无中间差异
	} else if na*nb > 4_000_000 {
		// 超大块安全退化为整段替换，防止 O(N*M) 内存爆炸
		for i := 0; i < na; i++ {
			midEdits = append(midEdits, diffEdit{op: '-', text: midA[i], oldIdx: prefix + i, newIdx: prefix})
		}
		for j := 0; j < nb; j++ {
			midEdits = append(midEdits, diffEdit{op: '+', text: midB[j], oldIdx: prefix + na, newIdx: prefix + j})
		}
	} else {
		dp := make([]int, (na+1)*(nb+1))
		stride := nb + 1
		for i := na - 1; i >= 0; i-- {
			for j := nb - 1; j >= 0; j-- {
				if midA[i] == midB[j] {
					dp[i*stride+j] = dp[(i+1)*stride+(j+1)] + 1
				} else {
					down := dp[(i+1)*stride+j]
					right := dp[i*stride+(j+1)]
					if down >= right {
						dp[i*stride+j] = down
					} else {
						dp[i*stride+j] = right
					}
				}
			}
		}
		i, j := 0, 0
		for i < na || j < nb {
			if i < na && j < nb && midA[i] == midB[j] {
				midEdits = append(midEdits, diffEdit{op: ' ', text: midA[i], oldIdx: prefix + i, newIdx: prefix + j})
				i++
				j++
			} else if i < na && (j == nb || dp[(i+1)*stride+j] >= dp[i*stride+(j+1)]) {
				midEdits = append(midEdits, diffEdit{op: '-', text: midA[i], oldIdx: prefix + i, newIdx: prefix + j})
				i++
			} else {
				midEdits = append(midEdits, diffEdit{op: '+', text: midB[j], oldIdx: prefix + i, newIdx: prefix + j})
				j++
			}
		}
	}

	total := make([]diffEdit, 0, prefix+len(midEdits)+suffix)
	for k := 0; k < prefix; k++ {
		total = append(total, diffEdit{op: ' ', text: a[k], oldIdx: k, newIdx: k})
	}
	total = append(total, midEdits...)
	for k := 0; k < suffix; k++ {
		oi := n - suffix + k
		ni := m - suffix + k
		total = append(total, diffEdit{op: ' ', text: a[oi], oldIdx: oi, newIdx: ni})
	}
	return total
}

// UniqueStrings 去重并保持首次出现顺序。
func UniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, s := range items {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// SortedKeys 返回 map 的升序键列表。
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
