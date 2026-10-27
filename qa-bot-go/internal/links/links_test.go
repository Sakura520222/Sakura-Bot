// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package links

import "testing"

// TestBuildTelegramPostLink 覆盖 telegram_links.py:88-125 的三大分支与边界：
// 数字 ID → 私有链接；合法 username → 公开链接；其余输入一律为空。
func TestBuildTelegramPostLink(t *testing.T) {
	cases := []struct {
		name      string
		channelID string
		messageID int64
		want      string
	}{
		// ── 分支一：数字频道 ID → t.me/c/<id>/<mid> ──
		{"裸数字ID", "123456", 789, "https://t.me/c/123456/789"},
		{"BotAPI-100前缀", "-1001234567", 5, "https://t.me/c/1234567/5"},
		{"c/路径", "c/12345", 7, "https://t.me/c/12345/7"},
		{"c/路径带消息段", "c/12345/999", 7, "https://t.me/c/12345/7"},
		{"t.me/c链接", "https://t.me/c/123456/42", 3, "https://t.me/c/123456/3"},

		// ── 分支二：合法 username → t.me/<u>/<mid> ──
		{"裸username", "sakura_channel", 2, "https://t.me/sakura_channel/2"},
		{"@username", "@foo_bar", 1, "https://t.me/foo_bar/1"},
		{"最短4位username", "abcd", 9, "https://t.me/abcd/9"},
		{"最长32位username", "a" + string(repeat('b', 31)), 1, "https://t.me/a" + string(repeat('b', 31)) + "/1"},
		{"t.me链接带帖子路径", "https://t.me/food/999", 3, "https://t.me/food/3"},
		{"裸username带帖子路径", "food/123", 4, "https://t.me/food/4"},
		{"telegram.me链接", "telegram.me/food", 3, "https://t.me/food/3"},
		{"http前缀", "http://t.me/food", 3, "https://t.me/food/3"},
		{"带首尾斜杠", "/food/", 6, "https://t.me/food/6"},

		// ── 分支三：非法输入一律为空 ──
		{"空频道", "", 1, ""},
		{"纯空白", "   ", 1, ""},
		{"邀请链接", "+AbCdEf", 1, ""},
		{"邀请链接带host", "https://t.me/+AbCdEf", 1, ""},
		{"username过短3位", "foo", 1, ""},
		{"username超长33位", "a" + string(repeat('b', 32)), 1, ""},
		{"username数字开头", "1abcd", 1, ""},
		{"username含连字符", "ab-cd", 1, ""},
		{"c/后非数字", "c/12a", 1, ""},
		{"裸-100无ID段", "-100", 1, ""},
		{"负数ID", "-200", 1, ""},
		{"乱字符", "!!!", 1, ""},
		{"消息ID为0", "food", 0, ""},
		{"消息ID为负", "food", -1, ""},
		{"数字频道但消息ID非法", "12345", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildTelegramPostLink(tc.channelID, tc.messageID)
			if got != tc.want {
				t.Fatalf("BuildTelegramPostLink(%q, %d) = %q, want %q", tc.channelID, tc.messageID, got, tc.want)
			}
		})
	}
}

// repeat 生成 n 个字节 c 组成的 string（测试辅助）。
func repeat(c byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return b
}
