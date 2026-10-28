// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	_ "modernc.org/sqlite" // 注册 "sqlite" 驱动（画像查询集成测试用）

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
)

// fakeStore 是 Store 窄接口的假实现：注入固定目录/总结/统计，
// 并记录 GetSummaries 的查询参数供断言。
type fakeStore struct {
	channels []contracts.ChannelInfo
	chErr    error

	summaries   []db.Summary
	sumErr      error
	gotChannel  string
	gotLimit    int
	gotOffset   int
	gotStart    *time.Time
	gotEnd      *time.Time
	gotStartNil bool
	gotEndNil   bool

	stats    db.ChannelSummaryStats
	statsErr error
}

func (f *fakeStore) GetAllChannels(ctx context.Context) ([]contracts.ChannelInfo, error) {
	if f.chErr != nil {
		return nil, f.chErr
	}
	return f.channels, nil
}

func (f *fakeStore) GetSummaries(ctx context.Context, channelID string, limit, offset int, startDate, endDate *time.Time) ([]db.Summary, error) {
	f.gotChannel, f.gotLimit, f.gotOffset = channelID, limit, offset
	f.gotStartNil, f.gotEndNil = startDate == nil, endDate == nil
	if startDate != nil {
		v := *startDate
		f.gotStart = &v
	}
	if endDate != nil {
		v := *endDate
		f.gotEnd = &v
	}
	if f.sumErr != nil {
		return nil, f.sumErr
	}
	return f.summaries, nil
}

func (f *fakeStore) GetChannelSummaryStats(ctx context.Context, channelID string) (db.ChannelSummaryStats, error) {
	if f.statsErr != nil {
		return db.ChannelSummaryStats{}, f.statsErr
	}
	return f.stats, nil
}

// resolveFixture 是 resolve_channel 测试的公共目录
// （db.GetAllChannels 聚合 + 合并后的产物形态）。
func resolveFixture() *fakeStore {
	return &fakeStore{channels: []contracts.ChannelInfo{
		{ChannelID: "https://t.me/foo_channel", ChannelName: "科技频道"},
		{ChannelID: "https://t.me/bar", ChannelName: "bar 频道"},
		{ChannelID: "https://t.me/zzz", ChannelName: "闲聊杂谈"},
		{ChannelID: "https://t.me/chan2", ChannelName: "chan2 频道"},
	}}
}

// TestResolveChannelEmptyHint 空提示分支（memory_manager.py:207-209）。
func TestResolveChannelEmptyHint(t *testing.T) {
	m := New(resolveFixture(), nil)
	got, err := json.Marshal(m.ResolveChannel(context.Background(), "  "))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"success":false,"error":"频道提示为空","candidates":[]}`
	if string(got) != want {
		t.Fatalf("空提示结果不符:\n got %s\nwant %s", got, want)
	}
}

// TestResolveChannelExact 覆盖 exact 分支的各类候选来源：
// 完整链接（大小写不敏感）、@用户名、URL 尾段、标准化链接。
func TestResolveChannelExact(t *testing.T) {
	cases := []struct {
		name string
		hint string
		want string
	}{
		{
			"大小写不同的完整链接经 hint_lower 命中",
			"HTTPS://T.ME/FOO_CHANNEL",
			`{"success":true,"channel_id":"https://t.me/foo_channel","channel_name":"科技频道","match_type":"exact","channel":{"channel_id":"https://t.me/foo_channel","channel_name":"科技频道","last_summary_time":null,"summary_count":0,"message_count":0}}`,
		},
		{
			"@频道名命中 channel_name 候选",
			"@科技频道",
			`{"success":true,"channel_id":"https://t.me/foo_channel","channel_name":"科技频道","match_type":"exact","channel":{"channel_id":"https://t.me/foo_channel","channel_name":"科技频道","last_summary_time":null,"summary_count":0,"message_count":0}}`,
		},
		{
			"URL 尾段命中",
			"bar",
			`{"success":true,"channel_id":"https://t.me/bar","channel_name":"bar 频道","match_type":"exact","channel":{"channel_id":"https://t.me/bar","channel_name":"bar 频道","last_summary_time":null,"summary_count":0,"message_count":0}}`,
		},
		{
			"尾部斜杠在 normalize 时剥除",
			"https://t.me/foo_channel/",
			`{"success":true,"channel_id":"https://t.me/foo_channel","channel_name":"科技频道","match_type":"exact","channel":{"channel_id":"https://t.me/foo_channel","channel_name":"科技频道","last_summary_time":null,"summary_count":0,"message_count":0}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(resolveFixture(), nil)
			got, err := json.Marshal(m.ResolveChannel(context.Background(), c.hint))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != c.want {
				t.Fatalf("exact 结果不符:\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

// TestResolveChannelFuzzy 模糊唯一命中（memory_manager.py:239-244）。
func TestResolveChannelFuzzy(t *testing.T) {
	m := New(resolveFixture(), nil)
	res := m.ResolveChannel(context.Background(), "科技")
	if !res.Success || res.MatchType != "fuzzy" || res.ChannelID != "https://t.me/foo_channel" {
		t.Fatalf("fuzzy 结果不符: %+v", res)
	}
}

// TestResolveChannelMultiple 多候选分支：错误文案 + 候选列表（:257-262）。
func TestResolveChannelMultiple(t *testing.T) {
	m := New(resolveFixture(), nil)
	res := m.ResolveChannel(context.Background(), "chan")
	if res.Success || res.Error != "找到多个匹配频道，请提供更完整的频道链接或名称" {
		t.Fatalf("多候选结果不符: %+v", res)
	}
	cands, ok := res.Candidates.([]ChannelEntry)
	if !ok || len(cands) != 2 {
		t.Fatalf("候选数量/类型不符: %#v", res.Candidates)
	}
	if cands[0].ChannelID != "https://t.me/foo_channel" || cands[1].ChannelID != "https://t.me/chan2" {
		t.Fatalf("候选顺序不符: %+v", cands)
	}
}

// TestResolveChannelCandidatesTruncated 候选截断到 10（matches[:10]）。
func TestResolveChannelCandidatesTruncated(t *testing.T) {
	fs := &fakeStore{}
	for i := 0; i < 11; i++ {
		fs.channels = append(fs.channels, contracts.ChannelInfo{
			ChannelID:   "https://t.me/user" + string(rune('a'+i)) + "_target",
			ChannelName: "频道" + string(rune('a'+i)),
		})
	}
	m := New(fs, nil)
	res := m.ResolveChannel(context.Background(), "target")
	cands, ok := res.Candidates.([]ChannelEntry)
	if !ok || len(cands) != 10 {
		t.Fatalf("候选应截断为 10: %#v", res.Candidates)
	}
}

// TestResolveChannelNormalizedFallback 目录未命中但输入形如 Telegram 标识
// （memory_manager.py:264-275）：channel 仅含两个键。
func TestResolveChannelNormalizedFallback(t *testing.T) {
	cases := []struct{ hint, id, name string }{
		{"@ghost", "https://t.me/ghost", "ghost"},
		{"t.me/xyz", "https://t.me/xyz", "xyz"},
		{"https://t.me/abc", "https://t.me/abc", "abc"},
		{"telegram.me/def", "https://t.me/def", "def"},
	}
	for _, c := range cases {
		m := New(resolveFixture(), nil)
		got, err := json.Marshal(m.ResolveChannel(context.Background(), c.hint))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		want := `{"success":true,"channel_id":"` + c.id + `","channel_name":"` + c.name +
			`","match_type":"normalized","channel":{"channel_id":"` + c.id + `","channel_name":"` + c.name + `"}}`
		if string(got) != want {
			t.Fatalf("normalized 兜底不符 (%s):\n got %s\nwant %s", c.hint, got, want)
		}
	}
}

// TestResolveChannelNotFound 无匹配且非 Telegram 标识（memory_manager.py:277）。
func TestResolveChannelNotFound(t *testing.T) {
	m := New(resolveFixture(), nil)
	got, err := json.Marshal(m.ResolveChannel(context.Background(), "不存在的名字"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"success":false,"error":"未找到匹配频道","candidates":[]}`
	if string(got) != want {
		t.Fatalf("未找到结果不符:\n got %s\nwant %s", got, want)
	}
}

// TestResolveChannelEmptyDirectory 空目录（合并后无频道）同样走兜底分支。
func TestResolveChannelEmptyDirectory(t *testing.T) {
	m := New(&fakeStore{}, nil)
	res := m.ResolveChannel(context.Background(), "@alone")
	if !res.Success || res.MatchType != "normalized" || res.ChannelID != "https://t.me/alone" {
		t.Fatalf("空目录兜底不符: %+v", res)
	}
}

// TestListChannelsNormalize 目录标准化（_normalize_channel_row）：
// datetime → isoformat、None → null、顺序保持 db 层排序结果。
func TestListChannelsNormalize(t *testing.T) {
	fs := &fakeStore{channels: []contracts.ChannelInfo{
		{
			ChannelID: "https://t.me/live", ChannelName: "LIVE",
			LastSummaryTime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			SummaryCount:    2, MessageCount: 10,
		},
		{ChannelID: "https://t.me/cool", ChannelName: "cool"}, // config 补零行
	}}
	m := New(fs, nil)
	got, err := json.Marshal(m.ListChannels(context.Background()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"channel_id":"https://t.me/live","channel_name":"LIVE","last_summary_time":"2026-01-02T03:04:05","summary_count":2,"message_count":10},` +
		`{"channel_id":"https://t.me/cool","channel_name":"cool","last_summary_time":null,"summary_count":0,"message_count":0}]`
	if string(got) != want {
		t.Fatalf("目录 JSON 不符:\n got %s\nwant %s", got, want)
	}
}

// TestListChannelsErrorSwallowed 目录查询异常 → 记日志返回空列表（Python []）。
func TestListChannelsErrorSwallowed(t *testing.T) {
	m := New(&fakeStore{chErr: errors.New("db down")}, nil)
	got := m.ListChannels(context.Background())
	if got == nil || len(got) != 0 {
		t.Fatalf("异常应返回空列表: %#v", got)
	}
}

// searchFixture 构造关键词检索的总结行
// （Keywords/Topics 为原始 JSON 列文本，对齐 DictCursor 的 str/None）。
func searchFixture() []db.Summary {
	return []db.Summary{
		{ID: 1, ChannelID: "https://t.me/aaa", SummaryText: "今天讨论 AI 与生活",
			Keywords: []byte(`["机器学习", "深度学习"]`), Topics: []byte(`["科技", "AI"]`)},
		{ID: 2, ChannelID: "https://t.me/bbb", SummaryText: "美食探店记",
			Keywords: []byte(`["美食"]`), Topics: []byte(`["生活"]`)},
		{ID: 3, ChannelID: "https://t.me/ccc", SummaryText: "纯文本",
			Keywords: []byte(`这不是JSON`)}, // Topics NULL
	}
}

// TestSearchSummariesNoFilter 无关键词无主题 → 直接返回全部并透传时间窗参数。
func TestSearchSummariesNoFilter(t *testing.T) {
	fs := &fakeStore{summaries: searchFixture()}
	m := New(fs, nil)
	before := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	got := m.SearchSummaries(context.Background(), nil, nil, 7, "", 10, &before)
	if len(got) != 3 {
		t.Fatalf("应返回全部 3 条: %d", len(got))
	}
	// 日期窗口：end = date_before，start = end - 7 天。
	if fs.gotStartNil || fs.gotEndNil || fs.gotLimit != 10 || fs.gotOffset != 0 {
		t.Fatalf("查询参数不符: start=%v end=%v limit=%d offset=%d", fs.gotStart, fs.gotEnd, fs.gotLimit, fs.gotOffset)
	}
	if !fs.gotEnd.Equal(before) || fs.gotStart.Sub(before) != -7*24*time.Hour {
		t.Fatalf("时间窗不符: start=%v end=%v", fs.gotStart, fs.gotEnd)
	}
}

// TestSearchSummariesKeywordMatch 关键词分支：正文命中 / keywords 列命中
// / 大小写不敏感 / 解析失败保留原始文本（R13）。
// 注意 Python 语义：topics 为空时 topic_match 恒为 True（整行放行），
// 关键词过滤用例必须同时给非空 topics 才有区分度。
func TestSearchSummariesKeywordMatch(t *testing.T) {
	cases := []struct {
		name     string
		keywords []string
		topics   []string
		wantIDs  []int64
	}{
		{"正文子串命中", []string{"AI"}, []string{"无"}, []int64{1}},
		{"关键词列命中（Python str 列表形态）", []string{"机器学习"}, []string{"无"}, []int64{1}},
		{"大小写不敏感", []string{"ai"}, []string{"无"}, []int64{1}},
		{"解析失败保留原始文本参与匹配", []string{"不是"}, []string{"无"}, []int64{3}},
		{"无命中", []string{"不存在词"}, []string{"无"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &fakeStore{summaries: searchFixture()}
			m := New(fs, nil)
			got := m.SearchSummaries(context.Background(), c.keywords, c.topics, 7, "", 10, nil)
			var ids []int64
			for _, s := range got {
				ids = append(ids, s.ID)
			}
			if len(ids) == 0 && len(c.wantIDs) == 0 {
				return
			}
			if !reflect.DeepEqual(ids, c.wantIDs) {
				t.Fatalf("命中不符: got %v want %v", ids, c.wantIDs)
			}
		})
	}
}

// TestSearchSummariesTopicMatch 主题分支与组合条件（keyword_match OR topic_match）。
func TestSearchSummariesTopicMatch(t *testing.T) {
	cases := []struct {
		name     string
		keywords []string
		topics   []string
		wantIDs  []int64
	}{
		// keywords 空切片（Python []，非 None）+ 主题非空 → 仅按主题过滤。
		{"主题命中", []string{}, []string{"生活"}, []int64{2}},
		// keywords=None（Go nil）+ 主题非空 → Python TypeError 吞错返回空。
		{"keywords=None 报 TypeError", nil, []string{"科技"}, nil},
		{"NULL 主题行按空列表处理", []string{"zzz"}, []string{"科技"}, []int64{1}},
		{"关键词或主题任一命中", []string{"美食"}, []string{"科技"}, []int64{1, 2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &fakeStore{summaries: searchFixture()}
			m := New(fs, nil)
			got := m.SearchSummaries(context.Background(), c.keywords, c.topics, 7, "", 10, nil)
			var ids []int64
			for _, s := range got {
				ids = append(ids, s.ID)
			}
			if !reflect.DeepEqual(ids, c.wantIDs) {
				t.Fatalf("命中不符: got %v want %v", ids, c.wantIDs)
			}
		})
	}
}

// TestSearchSummariesNoneKeywordsTypeError keywords=None + 非空 topics
// → Python TypeError 吞错路径（返回空）。
func TestSearchSummariesNoneKeywordsTypeError(t *testing.T) {
	fs := &fakeStore{summaries: searchFixture()}
	m := New(fs, nil)
	got := m.SearchSummaries(context.Background(), nil, []string{"科技"}, 7, "", 10, nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("None keywords + topics 应返回空列表: %#v", got)
	}
}

// TestSearchSummariesLimit 截断 filtered[:limit]。
func TestSearchSummariesLimit(t *testing.T) {
	fs := &fakeStore{summaries: searchFixture()}
	m := New(fs, nil)
	got := m.SearchSummaries(context.Background(), []string{}, []string{"科技", "生活"}, 7, "", 1, nil)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("截断不符: %#v", got)
	}
}

// TestSearchSummariesErrorSwallowed 检索异常 → 记日志返回空列表。
func TestSearchSummariesErrorSwallowed(t *testing.T) {
	m := New(&fakeStore{sumErr: errors.New("db down")}, nil)
	got := m.SearchSummaries(context.Background(), []string{"x"}, nil, 7, "", 10, nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("异常应返回空列表: %#v", got)
	}
}

// TestGetRecentSummaries 时间范围参数语义（timeRangeDays=0 → None/None）。
func TestGetRecentSummaries(t *testing.T) {
	fs := &fakeStore{summaries: searchFixture()}
	m := New(fs, nil)

	got := m.GetRecentSummaries(context.Background(), "https://t.me/aaa", 5, 0)
	if len(got) != 3 || !fs.gotStartNil || !fs.gotEndNil {
		t.Fatalf("不限时间参数不符: startNil=%v endNil=%v n=%d", fs.gotStartNil, fs.gotEndNil, len(got))
	}

	_ = m.GetRecentSummaries(context.Background(), "", 5, 7)
	if fs.gotStartNil || fs.gotEndNil || fs.gotStart.Sub(*fs.gotEnd) != -7*24*time.Hour {
		t.Fatalf("7 天窗口不符: start=%v end=%v", fs.gotStart, fs.gotEnd)
	}
}

// TestGetRecentSummariesErrorSwallowed 异常 → 空列表。
func TestGetRecentSummariesErrorSwallowed(t *testing.T) {
	m := New(&fakeStore{sumErr: errors.New("db down")}, nil)
	got := m.GetRecentSummaries(context.Background(), "", 5, 3)
	if got == nil || len(got) != 0 {
		t.Fatalf("异常应返回空列表: %#v", got)
	}
}

// TestGetChannelStats 统计行标准化（时间 isoformat / NULL → null）与异常 → nil。
func TestGetChannelStats(t *testing.T) {
	fs := &fakeStore{stats: db.ChannelSummaryStats{
		ChannelID: "https://t.me/aaa", ChannelName: "AAA",
		SummaryCount: 5, MessageCount: 9,
		LastSummaryTime:  sql.NullTime{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true},
		FirstSummaryTime: sql.NullTime{},
	}}
	m := New(fs, nil)
	got, err := json.Marshal(m.GetChannelStats(context.Background(), "https://t.me/aaa"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"channel_id":"https://t.me/aaa","channel_name":"AAA","summary_count":5,"message_count":9,"last_summary_time":"2026-01-02T03:04:05","first_summary_time":null}`
	if string(got) != want {
		t.Fatalf("统计 JSON 不符:\n got %s\nwant %s", got, want)
	}

	m2 := New(&fakeStore{statsErr: errors.New("db down")}, nil)
	if m2.GetChannelStats(context.Background(), "x") != nil {
		t.Fatalf("异常应返回 nil（Python {}）")
	}
}

// profileDDL 与 channel_profiles 建表列序一致（mysql.py:201-213），
// 供位置扫描的 SELECT * 集成验证（SQLite 内存库）。
const profileDDL = `CREATE TABLE channel_profiles (
	channel_id VARCHAR(255) PRIMARY KEY,
	channel_name VARCHAR(255),
	style VARCHAR(50) DEFAULT 'neutral',
	topics JSON,
	keywords_freq JSON,
	tone VARCHAR(50),
	avg_message_length DECIMAL(10,2) DEFAULT 0,
	total_summaries INT DEFAULT 0,
	last_updated DATETIME DEFAULT CURRENT_TIMESTAMP
)`

// TestGetChannelProfileAndContext 经 SQLite 内存库验证画像扫描与
// get_channel_context 全部分支（含 NULL/坏 JSON/空列表边界）。
func TestGetChannelProfileAndContext(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(profileDDL); err != nil {
		t.Fatalf("create table: %v", err)
	}
	inserts := []string{
		`INSERT INTO channel_profiles (channel_id, channel_name, style, topics, keywords_freq, total_summaries)
		 VALUES ('https://t.me/aaa', 'AAA', 'tech', '["AI","生活"]', '{"k":1}', 3)`,
		// 显式 NULL 行：省略列会取建表 DEFAULT（'neutral'/0），与 MySQL 行为一致。
		`INSERT INTO channel_profiles (channel_id, channel_name, style, topics, keywords_freq, tone, avg_message_length, total_summaries, last_updated)
		 VALUES ('https://t.me/nullrow', 'NR', NULL, NULL, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO channel_profiles (channel_id, channel_name, style, topics) VALUES ('https://t.me/rawtopics', 'RT', 'casual', 'abc')`,
		`INSERT INTO channel_profiles (channel_id, channel_name, style, topics) VALUES ('https://t.me/emptytopics', 'ET', 'neutral', '[]')`,
	}
	for _, q := range inserts {
		if _, err := sqlDB.Exec(q); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	m := New(&fakeStore{}, sqlDB)
	ctx := context.Background()
	cases := []struct{ channelID, want string }{
		{"", "这是一个多频道总结系统。"},
		{"https://t.me/aaa", "频道特点: 技术专业\n常讨论主题: AI, 生活\n已总结次数: 3"},
		{"https://t.me/nullrow", "频道特点: 中立\n已总结次数: None"},
		{"https://t.me/rawtopics", "频道特点: 轻松闲聊\n常讨论主题: a, b, c\n已总结次数: 0"},
		{"https://t.me/emptytopics", "频道特点: 中立客观\n已总结次数: 0"},
		{"https://t.me/missing", "频道: missing"},
	}
	for _, c := range cases {
		got := m.GetChannelContext(ctx, c.channelID)
		if got != c.want {
			t.Fatalf("上下文不符 (%q):\n got %q\nwant %q", c.channelID, got, c.want)
		}
	}

	// 画像扫描结构断言：JSON 解析与 NULL 语义。
	p, err := m.getChannelProfile(ctx, "https://t.me/aaa")
	if err != nil || p == nil {
		t.Fatalf("画像不应为空: %v, %+v", err, p)
	}
	if p.ChannelName.String != "AAA" || p.Style.String != "tech" || !p.TotalSummaries.Valid || p.TotalSummaries.Int64 != 3 {
		t.Fatalf("画像标量列不符: %+v", p)
	}
	wantTopics := []any{"AI", "生活"}
	if !reflect.DeepEqual(p.Topics, wantTopics) {
		t.Fatalf("topics 解析不符: %#v", p.Topics)
	}
	if !reflect.DeepEqual(p.KeywordsFreq, map[string]any{"k": int64(1)}) {
		t.Fatalf("keywords_freq 解析不符: %#v", p.KeywordsFreq)
	}
	if p, _ := m.getChannelProfile(ctx, "https://t.me/nullrow"); p == nil || p.Topics != nil || p.TotalSummaries.Valid {
		t.Fatalf("NULL 行语义不符: %+v", p)
	}
	if p, _ := m.getChannelProfile(ctx, "https://t.me/rawtopics"); p == nil || p.Topics != "abc" {
		t.Fatalf("坏 JSON 应保留原始文本: %#v", p)
	}
}

// TestPythonStr 复刻 Python str() 的表驱动用例。
func TestPythonStr(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "None"},
		{true, "True"},
		{false, "False"},
		{int64(3), "3"},
		{2.5, "2.5"},
		{float64(1), "1.0"},
		{"abc", "abc"},                  // 顶层字符串不加引号
		{[]any{"a", "b"}, "['a', 'b']"}, // 列表元素 repr
		{[]any{}, "[]"},
		{[]any{"it's", `a"b`}, "[\"it's\", 'a\"b']"},
		{[]any{"x\n"}, `['x\n']`},
		{[]any{nil, true, int64(1), 2.5}, "[None, True, 1, 2.5]"},
		{map[string]any{"b": int64(1), "a": []any{true}}, "{'a': [True], 'b': 1}"},
	}
	for _, c := range cases {
		if got := pythonStr(c.in); got != c.want {
			t.Fatalf("pythonStr(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseJSONText R13 双解析读取语义：空值/成功/失败/尾部垃圾。
func TestParseJSONText(t *testing.T) {
	if v := parseJSONText(nil); v != nil {
		t.Fatalf("空值应为 nil: %#v", v)
	}
	// Python：空白串 json.loads 抛异常且原值为真 → 保留原始文本。
	if v := parseJSONText([]byte(`  `)); v != `  ` {
		t.Fatalf("空白应保留原始文本: %#v", v)
	}
	got := parseJSONText([]byte(`["a", 1, 2.5, null]`))
	want := []any{"a", int64(1), 2.5, nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("解析不符: %#v", got)
	}
	if v := parseJSONText([]byte(`这不是JSON`)); v != "这不是JSON" {
		t.Fatalf("失败应保留原始文本: %#v", v)
	}
	if v := parseJSONText([]byte(`["a"] 垃圾`)); v != `["a"] 垃圾` {
		t.Fatalf("尾部垃圾应保留原始文本: %#v", v)
	}
	if v := parseJSONText([]byte(`3`)); v != int64(3) {
		t.Fatalf("整数应为 int64: %#v", v)
	}
}

// TestPythonJoinComma join 边界：字符串按字符、非字符串元素报 TypeError、不可迭代报错。
func TestPythonJoinComma(t *testing.T) {
	if s, detail, ok := pythonJoinComma([]any{"AI", "生活"}); !ok || s != "AI, 生活" || detail != "" {
		t.Fatalf("join 列表不符: %q %q %v", s, detail, ok)
	}
	if s, _, ok := pythonJoinComma("abc"); !ok || s != "a, b, c" {
		t.Fatalf("join 字符串按字符不符: %q", s)
	}
	_, detail, ok := pythonJoinComma([]any{"a", int64(1)})
	if ok || detail != "sequence item 1: expected str instance, found int" {
		t.Fatalf("非字符串元素错误不符: %q %v", detail, ok)
	}
	if _, detail, ok := pythonJoinComma(int64(5)); ok || detail != "can only join an iterable" {
		t.Fatalf("不可迭代错误不符: %q %v", detail, ok)
	}
}
