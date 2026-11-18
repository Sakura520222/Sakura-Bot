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
	"testing"
	"time"
)

// TestParseISODateTime 表驱动覆盖 datetime.fromisoformat 等价格式与非法输入。
// 期望值统一以 UTC 时刻表达（naive 输入按 UTC 赋时区）。
func TestParseISODateTime(t *testing.T) {
	utc := func(s string) time.Time {
		tm, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatalf("测试用例构造失败: %v", err)
		}
		return tm
	}
	cases := []struct {
		name  string
		input string
		want  time.Time
		ok    bool
	}{
		{"aware偏移", "2026-01-02T15:04:05+00:00", utc("2026-01-02T15:04:05Z"), true},
		{"Z后缀", "2026-01-02T15:04:05Z", utc("2026-01-02T15:04:05Z"), true},
		{"naive视为UTC", "2026-01-02T15:04:05", utc("2026-01-02T15:04:05Z"), true},
		{"空格分隔符", "2026-01-02 15:04:05", utc("2026-01-02T15:04:05Z"), true},
		{"仅日期", "2026-01-02", utc("2026-01-02T00:00:00Z"), true},
		{"无秒", "2026-01-02T15:04", utc("2026-01-02T15:04:00Z"), true},
		{"小数秒", "2026-01-02T15:04:05.5", utc("2026-01-02T15:04:05.5Z"), true},
		{"小数秒截断到微秒", "2026-01-02T15:04:05.1234567", utc("2026-01-02T15:04:05.123456Z"), true},
		{"正偏移换算UTC", "2026-01-02T15:04:05+08:00", utc("2026-01-02T07:04:05Z"), true},
		{"紧凑偏移", "2026-01-02T15:04:05-0800", utc("2026-01-02T23:04:05Z"), true},
		{"小时偏移", "2026-01-02T15:04:05+08", utc("2026-01-02T07:04:05Z"), true},
		{"基本格式", "20260102T150405", utc("2026-01-02T15:04:05Z"), true},
		{"闰年2月29", "2024-02-29T00:00:00", utc("2024-02-29T00:00:00Z"), true},
		{"小写z", "2026-01-02T15:04:05z", utc("2026-01-02T15:04:05Z"), true},
		{"空串", "", time.Time{}, false},
		{"非日期", "not-a-date", time.Time{}, false},
		{"非法月份", "2026-13-01T00:00:00", time.Time{}, false},
		{"非闰年2月29", "2026-02-29T00:00:00", time.Time{}, false},
		{"零年", "0000-01-01T00:00:00", time.Time{}, false},
		{"小时24", "2026-01-02T24:00:00", time.Time{}, false},
		{"孤立分隔符", "2026-01-02T", time.Time{}, false},
		{"残留字符", "2026-01-02T15:04:05 garbage", time.Time{}, false},
		{"悬空小数点", "2026-01-02T15:04:05.", time.Time{}, false},
		{"孤立时区号", "2026-01-02T15:04:05+", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseISODateTime(tc.input)
			if ok != tc.ok {
				t.Fatalf("parseISODateTime(%q) ok = %v, 期望 %v", tc.input, ok, tc.ok)
			}
			if tc.ok && !got.Equal(tc.want) {
				t.Fatalf("parseISODateTime(%q) = %v, 期望 %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestParseISODateTimePtr 验证边界解析失败返回 nil（忽略该边界）。
func TestParseISODateTimePtr(t *testing.T) {
	if got := parseISODateTimePtr("不是日期"); got != nil {
		t.Fatalf("不可解析边界应返回 nil，实际 %v", got)
	}
	if got := parseISODateTimePtr("2026-01-02"); got == nil {
		t.Fatal("可解析边界不应返回 nil")
	}
}
