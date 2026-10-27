// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
)

// TestBuildDSN 冻结 DSN 规格逐字段校验（pool.go BuildDSN）。
func TestBuildDSN(t *testing.T) {
	got := BuildDSN(MySQLConfig{
		Host: "127.0.0.1", Port: "3306",
		User: "sakura_bot", Password: "secret",
		Database: "sakura_bot_db", Charset: "utf8mb4",
	})
	for _, want := range []string{
		"@tcp(127.0.0.1:3306)/sakura_bot_db?",
		"parseTime=true",
		"loc=UTC",
		"timeout=30s",
		"readTimeout=60s",
		"writeTimeout=60s",
		"time_zone=%27%2B00%3A00%27", // '+00:00' 的百分号编码（规格冻结）
		"charset=utf8mb4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("DSN 缺少 %q: %s", want, got)
		}
	}
}

// TestMySQLConfigFromEnv 校验 data/.env 覆盖进程环境（settings.py override=True
// 语义）。默认值断言不可靠（进程环境可能已含 MYSQL_*），只测覆盖路径。
func TestMySQLConfigFromEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "MYSQL_HOST=db.example\n" +
		"MYSQL_PORT=3307\n" +
		"MYSQL_USER=u1\n" +
		"MYSQL_PASSWORD=p1\n" +
		"MYSQL_DATABASE=d1\n" +
		"MYSQL_CHARSET=utf8\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatalf("写临时 .env 失败: %v", err)
	}

	cfg := MySQLConfigFromEnv(config.NewEnv(envFile))
	if cfg.Host != "db.example" || cfg.Port != "3307" || cfg.User != "u1" ||
		cfg.Password != "p1" || cfg.Database != "d1" || cfg.Charset != "utf8" {
		t.Fatalf(".env 覆盖不符: %+v", cfg)
	}
}

// TestPythonISOFormat 校验 Python datetime.isoformat 语义：
// 微秒为 0 无小数段；非 0 恒 6 位补零。
func TestPythonISOFormat(t *testing.T) {
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "2026-01-02T03:04:05"},
		{time.Date(2026, 1, 2, 3, 4, 5, 100_000_000, time.UTC), "2026-01-02T03:04:05.100000"},
		{time.Date(2026, 1, 2, 3, 4, 5, 1_000, time.UTC), "2026-01-02T03:04:05.000001"},
	}
	for _, c := range cases {
		if got := PythonISOFormat(c.in); got != c.want {
			t.Fatalf("PythonISOFormat(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMysqlDatetimeLayouts 校验 DATETIME / DATETIME(6) 参数格式与配额日键。
func TestMysqlDatetimeLayouts(t *testing.T) {
	ts := time.Date(2026, 6, 7, 8, 9, 10, 123_456_000, time.UTC)
	if got := MysqlDatetime(ts); got != "2026-06-07 08:09:10" {
		t.Fatalf("MysqlDatetime = %q", got)
	}
	if got := MysqlDatetime6(ts); got != "2026-06-07 08:09:10.123456" {
		t.Fatalf("MysqlDatetime6 = %q", got)
	}
	if got := QuotaDate(ts); got != "2026-06-07" {
		t.Fatalf("QuotaDate = %q", got)
	}
	// QuotaDate 应先归一 UTC 再取日键（Python datetime.now(UTC) 语义）。
	utc8 := time.FixedZone("UTC+8", 8*3600)
	if got := QuotaDate(time.Date(2026, 6, 8, 2, 0, 0, 0, utc8)); got != "2026-06-07" {
		t.Fatalf("QuotaDate 非UTC输入 = %q", got)
	}
}

// TestParseJSONColumn 覆盖 R13 读取语义：空值 nil、解析成功、失败保留原文。
func TestParseJSONColumn(t *testing.T) {
	if v := ParseJSONColumn(nil); v != nil {
		t.Fatalf("空值应为 nil: %v", v)
	}
	if v := ParseJSONColumn([]byte("")); v != nil {
		t.Fatalf("空串应为 nil: %v", v)
	}
	if v, ok := ParseJSONColumn([]byte(`["a","b"]`)).([]any); !ok || len(v) != 2 {
		t.Fatalf("数组解析不符: %v", ParseJSONColumn([]byte(`["a","b"]`)))
	}
	// 双编码列：解析一次得到内层字符串（Python json.loads 同样行为）。
	if v, ok := ParseJSONColumn([]byte(`"[1,2]"`)).(string); !ok || v != "[1,2]" {
		t.Fatalf("双编码列应解为字符串: %v", ParseJSONColumn([]byte(`"[1,2]"`)))
	}
	// 非法 JSON：保留原文（except: pass）。
	if v, ok := ParseJSONColumn([]byte("{bad")).(string); !ok || v != "{bad" {
		t.Fatalf("非法 JSON 应保留原文: %v", ParseJSONColumn([]byte("{bad")))
	}
}

// TestUnmarshalJSONCompat 覆盖双解析兼容：直解失败时按字符串再解一次。
func TestUnmarshalJSONCompat(t *testing.T) {
	var list []any
	if err := UnmarshalJSONCompat([]byte(`[1,2]`), &list); err != nil || len(list) != 2 {
		t.Fatalf("直解失败: %v %v", list, err)
	}
	// 双编码：目标 []any 直解失败 → 解出字符串 → 再解内层数组。
	var inner []any
	if err := UnmarshalJSONCompat([]byte(`"[\"x\"]"`), &inner); err != nil || len(inner) != 1 {
		t.Fatalf("双解析失败: %v %v", inner, err)
	}
	if err := UnmarshalJSONCompat([]byte("not json"), &inner); err == nil {
		t.Fatal("两次都失败应返回错误")
	}
}

// TestEncodeJSON 校验 ensure_ascii=False 等价（UTF-8 直出、不转义 HTML）。
func TestEncodeJSON(t *testing.T) {
	got := EncodeJSONMust(map[string]any{"k": "中文<a>&"})
	if got != `{"k":"中文<a>&"}` {
		t.Fatalf("EncodeJSON 输出不符: %s", got)
	}
	if strings.Contains(got, `\u`) { // Go 的 HTML 转义形式是 \u003c 等，检查 \u 已覆盖
		t.Fatalf("不应产生 ASCII 转义/HTML 转义: %s", got)
	}
	// nil 约定为空串（调用方据此写 SQL NULL，等价 Python 的 params=None 分支）。
	if EncodeJSONMust(nil) != "" {
		t.Fatalf("nil 应为空串（SQL NULL）: %q", EncodeJSONMust(nil))
	}
}

// TestParseSummaryMessageIDs 覆盖 _parse_summary_rows 语义。
func TestParseSummaryMessageIDs(t *testing.T) {
	if v := parseSummaryMessageIDs(nil); len(v.([]any)) != 0 {
		t.Fatalf("空值应为空列表: %v", v)
	}
	if v := parseSummaryMessageIDs([]byte(`[1,2,3]`)); len(v.([]any)) != 3 {
		t.Fatalf("解析不符: %v", v)
	}
	if v := parseSummaryMessageIDs([]byte("bad")); len(v.([]any)) != 0 {
		t.Fatalf("解析失败应为空列表: %v", v)
	}
}

// TestLastPathSegment 覆盖 split("/")[-1] 等价。
func TestLastPathSegment(t *testing.T) {
	cases := map[string]string{
		"https://t.me/foo":  "foo",
		"https://t.me/a/b":  "b",
		"plain":             "plain",
		"https://t.me/foo/": "",
	}
	for in, want := range cases {
		if got := lastPathSegment(in); got != want {
			t.Fatalf("lastPathSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPyStr 覆盖 Python None 日志渲染等价。
func TestPyStr(t *testing.T) {
	if pyStr(nil) != "None" || pyStrStr("") != "None" {
		t.Fatal("nil/空串应渲染为 None")
	}
	v := "u"
	if pyStr(&v) != "u" || pyStrStr("s") != "s" {
		t.Fatal("非空值应原样渲染")
	}
}
