// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// assemble_test.go：装配层纯逻辑的单测——GOMEMLIMIT 解析、配额管理器的
// 整体拒绝分支（评审 #10）与行转换辅助函数。

package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/handlers"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/memory"
)

// testLogger 输出到 io.Discard 的测试日志器。
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newQuotaTestConfig 构造带临时 config.json 的配置。
func newQuotaTestConfig(t *testing.T, configJSON string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	if configJSON != "" {
		if err := os.WriteFile(jsonPath, []byte(configJSON), 0o644); err != nil {
			t.Fatalf("写入临时配置失败: %v", err)
		}
	}
	cfg := config.New(filepath.Join(dir, ".env"), jsonPath)
	if err := cfg.Init(); err != nil {
		t.Fatalf("配置初始化失败: %v", err)
	}
	return cfg
}

// TestParseMemLimit 表驱动覆盖 GOMEMLIMIT 解析分支。
func TestParseMemLimit(t *testing.T) {
	cases := []struct {
		in    string
		want  int64
		fails bool
	}{
		{"96MiB", 96 << 20, false},
		{"96mib", 96 << 20, false},
		{" 1GiB ", 1 << 30, false},
		{"512KiB", 512 << 10, false},
		{"2048", 2048, false},
		{"0", 0, true},
		{"-5MiB", 0, true},
		{"abc", 0, true},
		{"", 0, true},
		{"12LiB", 0, true},
	}
	for i, c := range cases {
		got, err := parseMemLimit(c.in)
		if c.fails {
			if err == nil {
				t.Fatalf("case %d: %q 应解析失败", i, c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("case %d: %q 不应失败: %v", i, c.in, err)
		}
		if got != c.want {
			t.Fatalf("case %d: %q = %d, want %d", i, c.in, got, c.want)
		}
	}
}

// TestQuotaManagerInvalidConfigRejects 构造期整体拒绝（评审 #10）：
// qa_bot_quota 越界 = Python model_validate raise → 进程启动失败。
func TestQuotaManagerInvalidConfigRejects(t *testing.T) {
	cfg := newQuotaTestConfig(t, `{"qa_bot_quota": {"user_daily_limit": 0}}`)
	qm, err := newQuotaManager(cfg, db.New(cfg), testLogger())
	if err == nil {
		t.Fatalf("构造期应报配置校验错误")
	}
	if qm != nil {
		t.Fatalf("构造失败时应返回 nil 管理器")
	}
}

// TestQuotaCheckInvalidConfigRejects 整体拒绝分支：构造后配置非法时
// CheckQuota 必须在触碰数据库前拒绝（q.db 可安全为 nil）。
func TestQuotaCheckInvalidConfigRejects(t *testing.T) {
	cfg := newQuotaTestConfig(t, `{"qa_bot_quota": {"total_daily_limit": 99999999999}}`)
	qm := &quotaManager{cfg: cfg, log: testLogger()} // db 刻意为 nil
	res := qm.CheckQuota(context.Background(), 42)
	if res.Allowed {
		t.Fatalf("配置越界必须整体拒绝")
	}
	want := "⚠️ **系统错误**\n\n配额检查失败，请稍后再试。"
	if res.Message != want {
		t.Fatalf("拒绝文案不符: got %q, want %q", res.Message, want)
	}
	if res.Remaining != 0 || res.Used != 0 || res.IsAdmin {
		t.Fatalf("异常分支字段应零值: %+v", res)
	}

	if st := qm.GetUsageStatus(context.Background(), 42); st.Err == "" {
		t.Fatalf("GetUsageStatus 应携带 error")
	}
	// RefundQuota 不读限额配置（Python 同构），不在此覆盖。
}

// TestQuotaSystemErrorText 固定异常分支文案（逐字回归）。
func TestQuotaSystemErrorText(t *testing.T) {
	want := "⚠️ **系统错误**\n\n配额检查失败，请稍后再试。"
	if got := quotaSystemError().Message; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestSummaryToMap 验证 summaries 行转换：键集 = SELECT * 列名，NULL → nil。
func TestSummaryToMap(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := db.Summary{
		ID:                7,
		ChannelID:         "https://t.me/foo",
		ChannelName:       sql.NullString{String: "Foo", Valid: true},
		SummaryText:       "正文",
		MessageCount:      sql.NullInt64{Int64: 3, Valid: true},
		StartTime:         sql.NullTime{},
		EndTime:           sql.NullTime{Time: now, Valid: true},
		CreatedAt:         sql.NullTime{Time: now, Valid: true},
		AIModel:           sql.NullString{},
		SummaryType:       sql.NullString{String: "daily", Valid: true},
		SummaryMessageIDs: []any{int64(1)},
		PollMessageID:     sql.NullInt64{Int64: 9, Valid: true},
		Keywords:          []byte(`["k"]`),
	}
	m := summaryToMap(s)

	// 键集 = Python DictCursor 列名。
	for _, key := range []string{
		"id", "channel_id", "channel_name", "summary_text", "message_count",
		"start_time", "end_time", "created_at", "ai_model", "summary_type",
		"summary_message_ids", "poll_message_id", "button_message_id",
		"keywords", "topics", "sentiment", "entities",
	} {
		if _, ok := m[key]; !ok {
			t.Fatalf("缺少键 %q: %v", key, m)
		}
	}

	if m["id"] != int64(7) || m["channel_id"] != "https://t.me/foo" {
		t.Fatalf("基础字段不符: %v", m)
	}
	if m["channel_name"] != "Foo" || m["ai_model"] != nil {
		t.Fatalf("NullString 转换不符: channel_name=%v ai_model=%v", m["channel_name"], m["ai_model"])
	}
	if m["start_time"] != nil || m["end_time"] == nil {
		t.Fatalf("NullTime 转换不符: %v", m)
	}
	if m["keywords"] != `["k"]` || m["topics"] != nil {
		t.Fatalf("JSON 列应保留原始文本/nil: %v", m)
	}
	if m["summary_message_ids"] == nil {
		t.Fatalf("summary_message_ids 应透传 db 层解析结果")
	}
}

// TestChannelEntryToMaps 验证目录/统计行转换键集与值。
func TestChannelEntryToMaps(t *testing.T) {
	entry := memory.ChannelEntry{
		ChannelID:       "https://t.me/foo",
		ChannelName:     "Foo",
		LastSummaryTime: nil,
		SummaryCount:    2,
		MessageCount:    10,
	}
	m := channelEntryToMap(entry)
	if m["channel_id"] != "https://t.me/foo" || m["summary_count"] != int64(2) {
		t.Fatalf("目录行转换不符: %v", m)
	}

	stats := channelStatsToMap(memory.ChannelStatsEntry{
		ChannelID:        "https://t.me/foo",
		SummaryCount:     2,
		FirstSummaryTime: "2026-01-01T00:00:00",
	})
	if stats["first_summary_time"] != "2026-01-01T00:00:00" || stats["message_count"] != int64(0) {
		t.Fatalf("统计行转换不符: %v", stats)
	}
}

// TestFilterChannelID 过滤元数据提取。
func TestFilterChannelID(t *testing.T) {
	if got := filterChannelID(nil); got != "" {
		t.Fatalf("nil → 空串: %q", got)
	}
	if got := filterChannelID(map[string]any{"channel_id": "https://t.me/foo"}); got != "https://t.me/foo" {
		t.Fatalf("字符串透传: %q", got)
	}
	if got := filterChannelID(map[string]any{"channel_id": int64(5)}); got != "5" {
		t.Fatalf("非字符串 → %v 形态: %q", "%v", got)
	}
}

// TestMaxInt64。
func TestMaxInt64(t *testing.T) {
	if maxInt64(0, -1) != 0 || maxInt64(3, 2) != 3 || maxInt64(-2, -3) != -2 {
		t.Fatalf("maxInt64 语义错误")
	}
}

// TestHandlersUsageStatusShape 编译期确认装配返回形态与 handlers 一致
// （UsageStatus 字段名回归防线）。
func TestHandlersUsageStatusShape(t *testing.T) {
	st := handlers.UsageStatus{Err: "boom", Remaining: -1}
	if st.Err != "boom" || st.Remaining != -1 {
		t.Fatalf("UsageStatus 字段回归")
	}
}
