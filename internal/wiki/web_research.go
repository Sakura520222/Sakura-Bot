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
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	xhtml "golang.org/x/net/html"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/netpolicy"
)

const (
	// MaxFetchBodyBytes 是抓取公开网页正文的最大字节数（1.5 MiB）。
	MaxFetchBodyBytes = 1536 * 1024
	// MaxSearchProviderBodyBytes 是搜索 API 响应体上限（1 MiB）。
	MaxSearchProviderBodyBytes = 1024 * 1024
	// MaxFetchRedirects 是网页抓取允许的最大重定向跳数（3 次）。
	MaxFetchRedirects = 3
	// DefaultDailySearchBudget 是每日联网检索调用预算上限（200 次）。
	DefaultDailySearchBudget = 200
	// ResearchUserAgent 是公开检索出站 User-Agent。
	ResearchUserAgent = "Sakura-Bot/2.5 (https://github.com/Sakura520222/Sakura-Bot)"
)

// SearchProviderDefaults 定义全部 9 个内置搜索源及其官方 API 根地址（对齐 web_research.py:DEFAULTS）。
var SearchProviderDefaults = map[string]string{
	"langsearch": "https://api.langsearch.com",
	"serper":     "https://google.serper.dev",
	"mwmbl":      "https://api.mwmbl.org",
	"deepseek":   "https://api.deepseek.com",
	"tavily":     "https://api.tavily.com",
	"serpapi":    "https://serpapi.com",
	"wikipedia":  "https://www.wikipedia.org",
	"crossref":   "https://api.crossref.org",
	"github":     "https://api.github.com",
}

// DefaultProviderOrder 是默认搜索源优先级顺序。
var DefaultProviderOrder = []string{
	"langsearch", "serper", "mwmbl", "deepseek", "tavily",
	"serpapi", "wikipedia", "crossref", "github",
}

// FreeProviderIntervals 定义免密钥开放搜索源的最小请求间隔秒数。
var FreeProviderIntervals = map[string]time.Duration{
	"mwmbl":     1 * time.Second,
	"wikipedia": 1 * time.Second,
	"crossref":  1 * time.Second,
	"github":    6 * time.Second,
}

// GeneralWebProviders 标记具备全网通用检索能力的搜索源。
var GeneralWebProviders = map[string]bool{
	"langsearch": true,
	"serper":     true,
	"mwmbl":      true,
	"deepseek":   true,
	"tavily":     true,
	"serpapi":    true,
}

var (
	reCJKQuery = regexp.MustCompile(`[\x{3400}-\x{9fff}]`)
	reHTMLTags = regexp.MustCompile(`<[^>]*>`)
)

// SearchUnavailableError 表示搜索或抓取不可用（携带对齐 Python 路由的机器码）。
type SearchUnavailableError struct {
	Code string
}

func (e *SearchUnavailableError) Error() string {
	if e == nil || e.Code == "" {
		return "search_unavailable"
	}
	return e.Code
}

// ProviderSetting 表示单个搜索源开关与密钥。
type ProviderSetting struct {
	Enabled bool   `json:"enabled"`
	APIKey  string `json:"api_key"`
}

// SearchSettings 表示联网检索与网页读取完整配置（对齐 web_research.py:SearchSettings）。
type SearchSettings struct {
	Enabled     bool                       `json:"enabled"`
	Order       []string                   `json:"order"`
	Providers   map[string]ProviderSetting `json:"providers"`
	ResultLimit int                        `json:"result_limit"`
	Timeout     int                        `json:"timeout"`
	Concurrency int                        `json:"concurrency"`
	TTLHours    int                        `json:"ttl_hours"`
	DailyBudget int                        `json:"daily_budget,omitempty"`
}

// DefaultSearchSettings 返回默认联网检索设置。
func DefaultSearchSettings() SearchSettings {
	return SearchSettings{
		Enabled:     false,
		Order:       append([]string(nil), DefaultProviderOrder...),
		Providers:   map[string]ProviderSetting{},
		ResultLimit: 5,
		Timeout:     10,
		Concurrency: 3,
		TTLHours:    24,
		DailyBudget: DefaultDailySearchBudget,
	}
}

// ParseSearchSettings 解析并校验 wiki_search 配置（自动忽略旧版遗留统计字段）。
func ParseSearchSettings(raw []byte) (SearchSettings, error) {
	s := DefaultSearchSettings()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "{}" || string(trimmed) == "null" {
		return s, nil
	}
	var m map[string]any
	if err := json.Unmarshal(trimmed, &m); err != nil {
		return s, fmt.Errorf("invalid search settings json: %w", err)
	}
	delete(m, "daily_calls")
	delete(m, "daily_cost_units")
	if provs, ok := m["providers"].(map[string]any); ok {
		for _, pm := range provs {
			if obj, ok := pm.(map[string]any); ok {
				delete(obj, "base_url")
				delete(obj, "cost_units")
			}
		}
	}
	cleaned, _ := json.Marshal(m)
	if err := json.Unmarshal(cleaned, &s); err != nil {
		return s, err
	}
	if s.Providers == nil {
		s.Providers = map[string]ProviderSetting{}
	}
	if len(s.Order) == 0 {
		s.Order = append([]string(nil), DefaultProviderOrder...)
	}
	if s.DailyBudget <= 0 {
		s.DailyBudget = DefaultDailySearchBudget
	}
	if err := s.Validate(); err != nil {
		return s, err
	}
	return s, nil
}

// Validate 校验搜索源列表去重、支持集合与参数边界。
func (s *SearchSettings) Validate() error {
	seen := map[string]bool{}
	for _, name := range s.Order {
		if _, ok := SearchProviderDefaults[name]; !ok {
			return fmt.Errorf("unknown search provider: %q", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate search provider in order: %q", name)
		}
		seen[name] = true
	}
	for name, p := range s.Providers {
		if _, ok := SearchProviderDefaults[name]; !ok {
			return fmt.Errorf("unknown search provider: %q", name)
		}
		if len(p.APIKey) > 1024 {
			return fmt.Errorf("api_key too long for provider %q", name)
		}
	}
	if s.ResultLimit < 1 || s.ResultLimit > 10 {
		return errors.New("result_limit must be in [1, 10]")
	}
	if s.Timeout < 2 || s.Timeout > 30 {
		return errors.New("timeout must be in [2, 30]")
	}
	if s.Concurrency < 1 || s.Concurrency > 12 {
		return errors.New("concurrency must be in [1, 12]")
	}
	if s.TTLHours < 1 || s.TTLHours > 168 {
		return errors.New("ttl_hours must be in [1, 168]")
	}
	return nil
}

// MaskedSearchSettings 生成隐藏真实 API Key 的管理端响应字典（对齐 wiki_research.py:masked）。
func MaskedSearchSettings(s SearchSettings) map[string]any {
	provs := make(map[string]any, len(SearchProviderDefaults))
	for _, name := range DefaultProviderOrder {
		p := s.Providers[name]
		_, isFree := FreeProviderIntervals[name]
		provs[name] = map[string]any{
			"enabled":      p.Enabled,
			"has_key":      p.APIKey != "",
			"official_url": SearchProviderDefaults[name],
			"requires_key": !isFree,
			"general_web":  GeneralWebProviders[name],
		}
	}
	return map[string]any{
		"enabled":      s.Enabled,
		"order":        s.Order,
		"result_limit": s.ResultLimit,
		"timeout":      s.Timeout,
		"concurrency":  s.Concurrency,
		"ttl_hours":    s.TTLHours,
		"providers":    provs,
	}
}

// ExtractHTMLText 使用 golang.org/x/net/html 提取网页标题与干净正文（优先 <main>/<article>，剥离脚本样式导航与页脚）。
func ExtractHTMLText(rawHTML string) (title string, content string) {
	doc, err := xhtml.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "", strings.TrimSpace(PlainSnippet(rawHTML))
	}

	var (
		titleParts []string
		bodyParts  []string
		mainParts  []string
		inTitle    bool
		inMain     int
	)

	skipTags := map[string]bool{
		"script": true, "style": true, "template": true,
		"noscript": true, "nav": true, "footer": true, "aside": true,
	}
	blockStartTags := map[string]bool{
		"p": true, "br": true, "li": true, "h1": true, "h2": true, "h3": true,
		"h4": true, "h5": true, "h6": true, "blockquote": true, "pre": true,
		"tr": true, "section": true,
	}
	blockEndTags := map[string]bool{
		"p": true, "li": true, "h1": true, "h2": true, "h3": true,
		"h4": true, "h5": true, "h6": true, "blockquote": true, "pre": true,
		"tr": true, "main": true, "article": true,
	}

	appendPart := func(s string) {
		bodyParts = append(bodyParts, s)
		if inMain > 0 {
			mainParts = append(mainParts, s)
		}
	}

	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n == nil {
			return
		}
		switch n.Type {
		case xhtml.ElementNode:
			tag := strings.ToLower(n.Data)
			if skipTags[tag] {
				return
			}
			if tag == "title" {
				inTitle = true
			}
			if tag == "main" || tag == "article" {
				inMain++
			}
			if blockStartTags[tag] {
				appendPart("\n")
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			if tag == "title" {
				inTitle = false
			}
			if blockEndTags[tag] {
				appendPart("\n")
			}
			if (tag == "main" || tag == "article") && inMain > 0 {
				inMain--
			}
			return

		case xhtml.TextNode:
			if inTitle {
				titleParts = append(titleParts, n.Data)
			} else {
				appendPart(n.Data)
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	title = strings.Join(strings.Fields(strings.Join(titleParts, "")), " ")
	if runes := []rune(title); len(runes) > 300 {
		title = string(runes[:300])
	}

	chosen := bodyParts
	for _, p := range mainParts {
		if strings.TrimSpace(p) != "" {
			chosen = mainParts
			break
		}
	}
	var cleanLines []string
	for _, rawLine := range strings.Split(strings.Join(chosen, ""), "\n") {
		line := strings.Join(strings.Fields(rawLine), " ")
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}
	return title, strings.Join(cleanLines, "\n")
}

// PlainSnippet 剥离 HTML 标签并反转义字符实体。
func PlainSnippet(value string) string {
	return strings.TrimSpace(stdhtml.UnescapeString(reHTMLTags.ReplaceAllString(value, "")))
}

// InternalResearchToken 计算 /api/internal/wiki-research 共享认证令牌（对齐 sakura-qa/wiki_research.go:23）。
func InternalResearchToken(qaBotToken string) string {
	trimmed := strings.TrimSpace(qaBotToken)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("sakura-wiki-research:v1:" + trimmed))
	return hex.EncodeToString(sum[:])
}

// VerifyInternalBearer 以常量时间比对内部检索桥接的 Authorization: Bearer 令牌。
func VerifyInternalBearer(header, qaBotToken string) bool {
	trimmedToken := strings.TrimSpace(qaBotToken)
	if trimmedToken == "" || !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	providedHex := strings.TrimPrefix(header, "Bearer ")
	providedBytes, err := hex.DecodeString(providedHex)
	if err != nil || len(providedBytes) != sha256.Size {
		return false
	}
	expected := sha256.Sum256([]byte("sakura-wiki-research:v1:" + trimmedToken))
	return hmac.Equal(providedBytes, expected[:])
}

// ProviderHTTPRequest 描述构造后的单个搜索源 HTTP 请求。
type ProviderHTTPRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// BuildProviderRequest 为全部 9 个内置搜索源构造标准化请求（对齐 web_research.py:provider_request）。
func BuildProviderRequest(name, apiKey, query string, limit int) (*ProviderHTTPRequest, error) {
	base, ok := SearchProviderDefaults[name]
	if !ok {
		return nil, fmt.Errorf("unsupported provider %q", name)
	}
	if limit <= 0 {
		limit = 5
	}
	headers := map[string]string{"User-Agent": ResearchUserAgent}

	switch name {
	case "langsearch":
		headers["Authorization"] = "Bearer " + apiKey
		headers["Content-Type"] = "application/json"
		b, _ := json.Marshal(map[string]any{"query": query, "count": limit})
		return &ProviderHTTPRequest{Method: http.MethodPost, URL: base + "/v1/web-search", Headers: headers, Body: b}, nil

	case "serper":
		headers["X-API-KEY"] = apiKey
		headers["Content-Type"] = "application/json"
		b, _ := json.Marshal(map[string]any{"q": query, "num": limit})
		return &ProviderHTTPRequest{Method: http.MethodPost, URL: base + "/search", Headers: headers, Body: b}, nil

	case "mwmbl":
		u := base + "/api/v2/search/?" + url.Values{"q": {query}}.Encode()
		return &ProviderHTTPRequest{Method: http.MethodGet, URL: u, Headers: headers}, nil

	case "wikipedia":
		wikiLang := "en"
		if reCJKQuery.MatchString(query) {
			wikiLang = "zh"
		}
		u := fmt.Sprintf("https://%s.wikipedia.org/w/rest.php/v1/search/page?%s",
			wikiLang, url.Values{"q": {query}, "limit": {strconv.Itoa(limit)}}.Encode())
		return &ProviderHTTPRequest{Method: http.MethodGet, URL: u, Headers: headers}, nil

	case "crossref":
		q := url.Values{
			"query.bibliographic": {query},
			"rows":                {strconv.Itoa(limit)},
			"select":              {"title,URL,published,abstract"},
		}
		return &ProviderHTTPRequest{Method: http.MethodGet, URL: base + "/works?" + q.Encode(), Headers: headers}, nil

	case "github":
		q := url.Values{"q": {query}, "per_page": {strconv.Itoa(limit)}}
		return &ProviderHTTPRequest{Method: http.MethodGet, URL: base + "/search/repositories?" + q.Encode(), Headers: headers}, nil

	case "deepseek":
		headers["Authorization"] = "Bearer " + apiKey
		headers["Content-Type"] = "application/json"
		b, _ := json.Marshal(map[string]any{"query": query, "count": limit, "need_content": true})
		return &ProviderHTTPRequest{Method: http.MethodPost, URL: base + "/v1/tools/search", Headers: headers, Body: b}, nil

	case "tavily":
		headers["Authorization"] = "Bearer " + apiKey
		headers["Content-Type"] = "application/json"
		b, _ := json.Marshal(map[string]any{
			"query":                  query,
			"max_results":            limit,
			"include_raw_content":    "text",
			"include_published_date": true,
			"search_depth":           "basic",
		})
		return &ProviderHTTPRequest{Method: http.MethodPost, URL: base + "/search", Headers: headers, Body: b}, nil

	case "serpapi":
		q := url.Values{"engine": {"google"}, "q": {query}, "api_key": {apiKey}}
		return &ProviderHTTPRequest{Method: http.MethodGet, URL: base + "/search.json?" + q.Encode(), Headers: headers}, nil
	}
	return nil, fmt.Errorf("unsupported provider %q", name)
}

// NormalizedResults 解析并校验任一搜索源响应为统一公网结果列表（对齐 web_research.py:normalized_results）。
func NormalizedResults(name string, payload map[string]any, limit int, endpoint string) ([]map[string]any, error) {
	if payload == nil {
		return nil, &SearchUnavailableError{Code: "provider_error"}
	}
	if errVal, hasErr := payload["error"]; hasErr && errVal != nil && errVal != false && errVal != "" {
		return nil, &SearchUnavailableError{Code: "provider_error"}
	}

	var rows []map[string]any
	switch name {
	case "langsearch":
		if mapInt(payload, "code") != 200 {
			return nil, &SearchUnavailableError{Code: "provider_error"}
		}
		dataObj, _ := payload["data"].(map[string]any)
		webPages, _ := dataObj["webPages"].(map[string]any)
		vals, _ := webPages["value"].([]any)
		for _, item := range vals {
			if m, ok := item.(map[string]any); ok {
				rows = append(rows, map[string]any{
					"url":            m["url"],
					"title":          m["name"],
					"snippet":        m["snippet"],
					"content":        m["text"],
					"published_date": m["datePublished"],
				})
			}
		}

	case "serper":
		vals, ok := payload["organic"].([]any)
		if !ok {
			return nil, &SearchUnavailableError{Code: "provider_format"}
		}
		for _, item := range vals {
			if m, ok := item.(map[string]any); ok {
				rows = append(rows, m)
			}
		}

	case "mwmbl":
		vals, ok := payload["results"].([]any)
		if !ok {
			return nil, &SearchUnavailableError{Code: "provider_format"}
		}
		for _, item := range vals {
			if m, ok := item.(map[string]any); ok {
				rows = append(rows, map[string]any{
					"url":     m["url"],
					"title":   m["title"],
					"snippet": m["content"],
				})
			}
		}

	case "wikipedia":
		host := "en.wikipedia.org"
		if u, err := url.Parse(endpoint); err == nil && u.Hostname() != "" {
			host = u.Hostname()
		}
		vals, _ := payload["pages"].([]any)
		for _, item := range vals {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			key, ok := m["key"].(string)
			if !ok || key == "" {
				continue
			}
			rows = append(rows, map[string]any{
				"url":     fmt.Sprintf("https://%s/wiki/%s", host, url.PathEscape(key)),
				"title":   m["title"],
				"snippet": PlainSnippet(fmt.Sprint(orEmpty(m["excerpt"]))),
				"content": PlainSnippet(fmt.Sprint(orEmpty(m["description"]))),
			})
		}

	case "crossref":
		msgObj, _ := payload["message"].(map[string]any)
		vals, _ := msgObj["items"].([]any)
		for _, item := range vals {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			title := firstArrayString(m["title"])
			abs := PlainSnippet(fmt.Sprint(orEmpty(m["abstract"])))
			if abs == "" {
				abs = PlainSnippet(title)
			}
			rows = append(rows, map[string]any{
				"url":     m["URL"],
				"title":   title,
				"snippet": abs,
				"date":    extractCrossrefDate(m["published"]),
			})
		}

	case "github":
		vals, _ := payload["items"].([]any)
		for _, item := range vals {
			if m, ok := item.(map[string]any); ok {
				rows = append(rows, map[string]any{
					"url":     m["html_url"],
					"title":   m["full_name"],
					"snippet": m["description"],
				})
			}
		}

	case "serpapi":
		vals, ok := payload["organic_results"].([]any)
		if !ok {
			return nil, &SearchUnavailableError{Code: "provider_format"}
		}
		for _, item := range vals {
			if m, ok := item.(map[string]any); ok {
				rows = append(rows, m)
			}
		}

	default: // deepseek, tavily
		vals, ok := payload["results"].([]any)
		if !ok {
			return nil, &SearchUnavailableError{Code: "provider_format"}
		}
		for _, item := range vals {
			if m, ok := item.(map[string]any); ok {
				rows = append(rows, m)
			}
		}
	}

	seen := map[string]bool{}
	var out []map[string]any
	fetchedAt := FormatISO(NowUTC()) + "Z"
	maxScan := min(len(rows), 50)
	for _, row := range rows[:maxScan] {
		urlKey := "url"
		if name == "serper" || name == "serpapi" {
			urlKey = "link"
		}
		rawURL, _ := row[urlKey].(string)
		cleanURL, err := netpolicy.PublicURL(rawURL)
		if err != nil || seen[cleanURL] {
			continue
		}
		seen[cleanURL] = true

		snippetKey := "snippet"
		contentKey := "content"
		if name == "tavily" {
			snippetKey = "content"
			contentKey = "raw_content"
		}
		pubDate := fmt.Sprint(orEmpty(row["published_date"]))
		if pubDate == "" {
			pubDate = fmt.Sprint(orEmpty(row["date"]))
		}

		out = append(out, map[string]any{
			"url":          cleanURL,
			"title":        truncateRunesSimple(fmt.Sprint(orEmpty(row["title"])), 300),
			"provider":     name,
			"snippet":      truncateRunesSimple(fmt.Sprint(orEmpty(row[snippetKey])), 800),
			"content":      truncateRunesSimple(fmt.Sprint(orEmpty(row[contentKey])), 4500),
			"published_at": truncateRunesSimple(pubDate, 80),
			"fetched_at":   fetchedAt,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func orEmpty(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func firstArrayString(v any) string {
	switch arr := v.(type) {
	case []any:
		if len(arr) > 0 {
			return fmt.Sprint(orEmpty(arr[0]))
		}
	case []string:
		if len(arr) > 0 {
			return arr[0]
		}
	}
	return ""
}

func extractCrossrefDate(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	outer, ok := m["date-parts"].([]any)
	if !ok || len(outer) == 0 {
		return ""
	}
	inner, ok := outer[0].([]any)
	if !ok || len(inner) == 0 {
		return ""
	}
	parts := make([]string, 0, len(inner))
	for _, p := range inner {
		parts = append(parts, fmt.Sprint(p))
	}
	return strings.Join(parts, "-")
}

func truncateRunesSimple(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

// ResearchService 提供带 SSRF 防护、MySQL 缓存、每日调用配额与 9 源回退的联网检索及公开网页提取能力。
type ResearchService struct {
	store    *Store
	cfg      *config.Config
	resolver netpolicy.IPResolver

	activeMu    sync.Mutex
	activeCond  *sync.Cond
	activeCount int

	stripeLocks [32]sync.Mutex

	freeMu       sync.Mutex
	providerLock map[string]*sync.Mutex
	nextCallAt   map[string]time.Time
}

// NewResearchService 创建联网检索服务。
func NewResearchService(store *Store, cfg *config.Config) *ResearchService {
	rs := &ResearchService{
		store:        store,
		cfg:          cfg,
		resolver:     net.DefaultResolver,
		providerLock: make(map[string]*sync.Mutex, len(FreeProviderIntervals)),
		nextCallAt:   make(map[string]time.Time, len(FreeProviderIntervals)),
	}
	rs.activeCond = sync.NewCond(&rs.activeMu)
	for name := range FreeProviderIntervals {
		rs.providerLock[name] = &sync.Mutex{}
	}
	return rs
}

// CurrentSettings 从运行时配置读取最新 SearchSettings。
func (r *ResearchService) CurrentSettings() SearchSettings {
	if r == nil || r.cfg == nil {
		return DefaultSearchSettings()
	}
	raw, ok := r.cfg.SubSection("wiki_search")
	if !ok {
		return DefaultSearchSettings()
	}
	parsed, err := ParseSearchSettings(raw)
	if err != nil {
		return DefaultSearchSettings()
	}
	return parsed
}

func (r *ResearchService) httpClient(timeout time.Duration) *http.Client {
	return netpolicy.NewPublicHTTPClient(timeout, r.resolver)
}

func (r *ResearchService) acquireSlot(maxConcurrency int) {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	r.activeMu.Lock()
	for r.activeCount >= maxConcurrency {
		r.activeCond.Wait()
	}
	r.activeCount++
	r.activeMu.Unlock()
}

func (r *ResearchService) releaseSlot() {
	r.activeMu.Lock()
	r.activeCount--
	r.activeCond.Broadcast()
	r.activeMu.Unlock()
}

func (r *ResearchService) checkAndRecordAttempt(ctx context.Context, provider string, dailyBudget int) error {
	if r.store == nil || r.store.DB == nil {
		return nil
	}
	if dailyBudget > 0 {
		var used sql.NullInt64
		_ = r.store.DB.QueryRowContext(ctx, "SELECT SUM(calls) FROM wiki_search_usage WHERE day=UTC_DATE()").Scan(&used)
		if used.Int64 >= int64(dailyBudget) {
			return &SearchUnavailableError{Code: "daily_budget_exceeded"}
		}
	}
	_, err := r.store.DB.ExecContext(ctx, `
		INSERT INTO wiki_search_usage(day, provider, calls)
		VALUES (UTC_DATE(), ?, 1)
		ON DUPLICATE KEY UPDATE calls = calls + 1
	`, provider)
	return err
}

func (r *ResearchService) withCache(ctx context.Context, cacheKey string, settings SearchSettings, perform func() (map[string]any, error)) (map[string]any, error) {
	idx := 0
	if len(cacheKey) >= 4 {
		if v, err := strconv.ParseUint(cacheKey[:4], 16, 32); err == nil {
			idx = int(v % uint64(len(r.stripeLocks)))
		}
	}
	r.stripeLocks[idx].Lock()
	defer r.stripeLocks[idx].Unlock()

	if r.store != nil && r.store.DB != nil {
		var raw []byte
		err := r.store.DB.QueryRowContext(ctx, "SELECT result FROM wiki_search_cache WHERE cache_key=? AND expires_at > UTC_TIMESTAMP(6)", cacheKey).Scan(&raw)
		if err == nil && len(raw) > 0 {
			return decodeMapJSON(raw), nil
		}
	}

	r.acquireSlot(settings.Concurrency)
	result, err := func() (map[string]any, error) {
		defer r.releaseSlot()
		return perform()
	}()
	if err != nil {
		return nil, err
	}

	if r.store != nil && r.store.DB != nil {
		_, _ = r.store.DB.ExecContext(ctx, `
			INSERT INTO wiki_search_cache (cache_key, result, expires_at)
			VALUES (?, ?, DATE_ADD(UTC_TIMESTAMP(6), INTERVAL ? HOUR))
			ON DUPLICATE KEY UPDATE result=VALUES(result), expires_at=VALUES(expires_at)
		`, cacheKey, EncodeJSON(result), settings.TTLHours)
	}
	return result, nil
}

// Search 按优先级顺序查询已启用的搜索源并返回核实后的公开网页结果（对齐 web_research.py:search）。
func (r *ResearchService) Search(ctx context.Context, query string, override *SearchSettings, assistant bool) (map[string]any, error) {
	settings := r.CurrentSettings()
	if override != nil {
		settings = *override
	}
	cleanQuery := truncateRunesSimple(strings.Join(strings.Fields(query), " "), 128)
	if !settings.Enabled || cleanQuery == "" {
		return nil, &SearchUnavailableError{Code: "search_disabled_or_empty"}
	}
	key := Digest("search-v3", cleanQuery, settings, assistant)

	return r.withCache(ctx, key, settings, func() (map[string]any, error) {
		var order []string
		for _, name := range settings.Order {
			prov, exists := settings.Providers[name]
			if !exists || !prov.Enabled {
				continue
			}
			_, isFree := FreeProviderIntervals[name]
			if isFree || prov.APIKey != "" {
				order = append(order, name)
			}
		}
		if len(order) == 0 {
			return nil, &SearchUnavailableError{Code: "no_search_provider_configured"}
		}
		if assistant {
			sort.SliceStable(order, func(i, j int) bool {
				wi := GeneralWebProviders[order[i]]
				wj := GeneralWebProviders[order[j]]
				if wi != wj {
					return wi
				}
				return false
			})
		}

		failures := 0
		for _, name := range order {
			provReq, err := BuildProviderRequest(name, settings.Providers[name].APIKey, cleanQuery, settings.ResultLimit)
			if err != nil {
				failures++
				continue
			}
			payload, err := r.requestProvider(ctx, name, provReq, time.Duration(settings.Timeout)*time.Second, settings.DailyBudget)
			if err != nil {
				if errors.Is(err, &SearchUnavailableError{Code: "daily_budget_exceeded"}) {
					return nil, err
				}
				failures++
				continue
			}
			results, err := NormalizedResults(name, payload, settings.ResultLimit, provReq.URL)
			if err != nil {
				failures++
				continue
			}

			reachable := make([]bool, len(results))
			var wg sync.WaitGroup
			for i, res := range results {
				wg.Add(1)
				go func(idx int, u string) {
					defer wg.Done()
					reachable[idx] = r.Reachable(ctx, u)
				}(i, mapString(res, "url"))
			}
			wg.Wait()

			if assistant {
				var usable []any
				for i, res := range results {
					if reachable[i] || mapString(res, "snippet") != "" || mapString(res, "content") != "" {
						cp := cloneMap(res)
						cp["page_reachable"] = reachable[i]
						usable = append(usable, cp)
					}
				}
				if len(usable) > 0 {
					scope := name
					if GeneralWebProviders[name] {
						scope = "web"
					}
					return map[string]any{
						"results":  usable,
						"provider": name,
						"scope":    scope,
						"guidance": "按返回来源的覆盖范围作答。page_reachable=false 仅有搜索索引资料，不能声称读过原网页，也不能用空结果推断没有新闻。",
					}, nil
				}
			} else {
				var usable []any
				for i, res := range results {
					if reachable[i] {
						usable = append(usable, res)
					}
				}
				if len(usable) > 0 {
					return map[string]any{
						"results":  usable,
						"provider": name,
					}, nil
				}
			}
		}

		if failures == len(order) {
			return nil, &SearchUnavailableError{Code: "search_providers_unavailable"}
		}
		if assistant {
			hasWeb := false
			for _, name := range order {
				if GeneralWebProviders[name] {
					hasWeb = true
					break
				}
			}
			if !hasWeb {
				return nil, &SearchUnavailableError{Code: "no_general_web_provider"}
			}
		}
		return nil, &SearchUnavailableError{Code: "no_search_evidence"}
	})
}

func (r *ResearchService) requestProvider(ctx context.Context, name string, provReq *ProviderHTTPRequest, timeout time.Duration, dailyBudget int) (map[string]any, error) {
	doCall := func() (map[string]any, error) {
		if err := r.checkAndRecordAttempt(ctx, name, dailyBudget); err != nil {
			return nil, err
		}
		var bodyReader io.Reader
		if len(provReq.Body) > 0 {
			bodyReader = bytes.NewReader(provReq.Body)
		}
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		httpReq, err := http.NewRequestWithContext(reqCtx, provReq.Method, provReq.URL, bodyReader)
		if err != nil {
			return nil, err
		}
		for k, v := range provReq.Headers {
			httpReq.Header.Set(k, v)
		}

		cli := r.httpClient(timeout)
		resp, err := cli.Do(httpReq)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) && FreeProviderIntervals[name] > 0 {
				delaySec := 60
				if retryHdr := strings.TrimSpace(resp.Header.Get("Retry-After")); retryHdr != "" {
					if parsedSec, pErr := strconv.Atoi(retryHdr); pErr == nil && parsedSec > 0 {
						delaySec = min(3600, max(60, parsedSec))
					}
				}
				r.freeMu.Lock()
				r.nextCallAt[name] = time.Now().Add(time.Duration(delaySec) * time.Second)
				r.freeMu.Unlock()
			}
			return nil, &SearchUnavailableError{Code: "provider_unavailable"}
		}

		raw, err := readBoundedBody(resp.Body, MaxSearchProviderBodyBytes)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	}

	interval, isFree := FreeProviderIntervals[name]
	if !isFree {
		return doCall()
	}

	lk := r.providerLock[name]
	lk.Lock()
	defer lk.Unlock()

	r.freeMu.Lock()
	waitDur := time.Until(r.nextCallAt[name])
	r.freeMu.Unlock()

	if waitDur > interval+100*time.Millisecond {
		return nil, &SearchUnavailableError{Code: "provider_rate_limited"}
	}
	if waitDur > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitDur):
		}
	}

	r.freeMu.Lock()
	r.nextCallAt[name] = time.Now().Add(interval)
	r.freeMu.Unlock()
	return doCall()
}

// Fetch 安全抓取公开网页（最多 3 次公网重定向、上限 1.5 MiB、提取纯文本与标题，对齐 web_research.py:fetch）。
func (r *ResearchService) Fetch(ctx context.Context, rawURL string) (map[string]any, error) {
	settings := r.CurrentSettings()
	if !settings.Enabled {
		return nil, &SearchUnavailableError{Code: "fetch_disabled"}
	}
	cleanURL, err := netpolicy.PublicURL(rawURL)
	if err != nil {
		return nil, err
	}

	return r.withCache(ctx, Digest("fetch-v2", cleanURL), settings, func() (map[string]any, error) {
		if err := r.checkAndRecordAttempt(ctx, "fetch", settings.DailyBudget); err != nil {
			return nil, err
		}
		timeout := time.Duration(settings.Timeout) * time.Second
		fetchCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cli := r.httpClient(timeout)

		target := cleanURL
		for hop := 0; hop <= MaxFetchRedirects; hop++ {
			req, reqErr := http.NewRequestWithContext(fetchCtx, http.MethodGet, target, nil)
			if reqErr != nil {
				return nil, &SearchUnavailableError{Code: "fetch_unavailable"}
			}
			req.Header.Set("User-Agent", ResearchUserAgent)
			req.Header.Set("Accept", "text/html, text/plain;q=0.9, text/markdown;q=0.9, application/xhtml+xml;q=0.8")

			resp, doErr := cli.Do(req)
			if doErr != nil {
				return nil, &SearchUnavailableError{Code: "fetch_unavailable"}
			}

			if isRedirectStatus(resp.StatusCode) {
				loc := resp.Header.Get("Location")
				_ = resp.Body.Close()
				if loc == "" || hop == MaxFetchRedirects {
					return nil, &SearchUnavailableError{Code: "fetch_unavailable"}
				}
				resolved, rErr := resolveRedirectURL(target, loc)
				if rErr != nil {
					return nil, &SearchUnavailableError{Code: "fetch_unavailable"}
				}
				target = resolved
				continue
			}

			if resp.StatusCode != http.StatusOK {
				code := resp.StatusCode
				_ = resp.Body.Close()
				return nil, &SearchUnavailableError{Code: fmt.Sprintf("fetch_http_%d", code)}
			}

			ct := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
			switch ct {
			case "text/html", "application/xhtml+xml", "text/plain", "text/markdown":
			default:
				_ = resp.Body.Close()
				return nil, &SearchUnavailableError{Code: "fetch_unsupported_type"}
			}

			rawBytes, truncatedBytes, readErr := readUpToBytes(resp.Body, MaxFetchBodyBytes)
			_ = resp.Body.Close()
			if readErr != nil {
				return nil, &SearchUnavailableError{Code: "fetch_unavailable"}
			}

			text := string(rawBytes)
			title := ""
			if ct == "text/html" || ct == "application/xhtml+xml" {
				title, text = ExtractHTMLText(text)
			} else {
				text = strings.TrimSpace(text)
			}
			if text == "" {
				return nil, &SearchUnavailableError{Code: "fetch_no_text"}
			}

			runes := []rune(text)
			truncated := truncatedBytes || len(runes) > 12000
			if len(runes) > 12000 {
				text = string(runes[:12000])
			}
			return map[string]any{
				"url":        target,
				"provider":   "fetch",
				"title":      title,
				"content":    text,
				"truncated":  truncated,
				"fetched_at": FormatISO(NowUTC()) + "Z",
			}, nil
		}
		return nil, &SearchUnavailableError{Code: "fetch_unavailable"}
	})
}

// Reachable 快速探测公开链接是否可达且返回非空响应（最多 3 次公网重定向，5 秒超时）。
func (r *ResearchService) Reachable(ctx context.Context, rawURL string) bool {
	target, err := netpolicy.PublicURL(rawURL)
	if err != nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cli := r.httpClient(5 * time.Second)

	for hop := 0; hop <= MaxFetchRedirects; hop++ {
		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, nil)
		if err != nil {
			return false
		}
		req.Header.Set("User-Agent", ResearchUserAgent)
		resp, err := cli.Do(req)
		if err != nil {
			return false
		}
		if isRedirectStatus(resp.StatusCode) {
			loc := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if loc == "" || hop == MaxFetchRedirects {
				return false
			}
			resolved, rErr := resolveRedirectURL(target, loc)
			if rErr != nil {
				return false
			}
			target = resolved
			continue
		}
		var one [1]byte
		n, _ := resp.Body.Read(one[:])
		_ = resp.Body.Close()
		return (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) && n > 0
	}
	return false
}

// TestProvider 测试单个搜索源连通性与结果解析（供管理端连接测试使用）。
func (r *ResearchService) TestProvider(ctx context.Context, name, apiKey string) (map[string]any, error) {
	if _, ok := SearchProviderDefaults[name]; !ok {
		return nil, fmt.Errorf("unknown search provider: %q", name)
	}
	_, isFree := FreeProviderIntervals[name]
	if !isFree && strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("API key is required for this provider")
	}
	provReq, err := BuildProviderRequest(name, strings.TrimSpace(apiKey), "Sakura Bot", 3)
	if err != nil {
		return nil, err
	}
	payload, err := r.requestProvider(ctx, name, provReq, 10*time.Second, 0)
	if err != nil {
		return nil, err
	}
	results, err := NormalizedResults(name, payload, 3, provReq.URL)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":           true,
		"provider":     name,
		"result_count": len(results),
		"results":      results,
	}, nil
}

// GetUsage 查询最近 days 天的搜索源调用统计。
func (r *ResearchService) GetUsage(ctx context.Context, days int) ([]map[string]any, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return []map[string]any{}, nil
	}
	if days <= 0 || days > 90 {
		days = 30
	}
	return QueryRowsMap(ctx, r.store.DB, `
		SELECT DATE_FORMAT(day, '%Y-%m-%d') AS day, provider, calls, cost_units
		FROM wiki_search_usage
		WHERE day >= UTC_DATE() - INTERVAL ? DAY
		ORDER BY day DESC, provider
	`, days)
}

// Prune 清理过期的检索缓存、事实核查缓存与超过 90 天的调用统计（对齐 web_research.py:prune）。
func (r *ResearchService) Prune(ctx context.Context) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	for _, tbl := range []string{"wiki_search_cache", "wiki_fact_checks"} {
		if _, err := r.store.DB.ExecContext(ctx, "DELETE FROM "+tbl+" WHERE expires_at <= UTC_TIMESTAMP(6) LIMIT 1000"); err != nil {
			return err
		}
	}
	_, err := r.store.DB.ExecContext(ctx, "DELETE FROM wiki_search_usage WHERE day < UTC_DATE() - INTERVAL 90 DAY LIMIT 1000")
	return err
}

func isRedirectStatus(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func resolveRedirectURL(baseStr, location string) (string, error) {
	baseU, err := url.Parse(baseStr)
	if err != nil {
		return "", err
	}
	locU, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return netpolicy.PublicURL(baseU.ResolveReference(locU).String())
}

func readBoundedBody(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("response body exceeds limit")
	}
	return data, nil
}

func readUpToBytes(r io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}
