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

import "time"

// parseISODateTime 复刻 Python datetime.fromisoformat（3.11+）对常用 ISO 8601
// 格式的解析语义，并按本项目约定把 naive 时间（无时区后缀）视为 UTC：
//   - 日期：YYYY-MM-DD（扩展）或 YYYYMMDD（基本）；
//   - 日期与时间之间允许任意单字符分隔符（'T'、空格等，Python 文档中的 *）；
//   - 时间：HH[:MM[:SS[.f...]]]（扩展）或 HH[MM[SS]]（紧凑）；
//   - 小数秒任意位数，超出微秒部分直接截断（Python 语义）；
//   - 时区：Z/z、±HH、±HH:MM[:SS]、±HH[MM[SS]]；带偏移的输入换算为 UTC 时刻；
//   - 校验失败（非法月/日/时/分/秒、残留字符、0000 年等）返回 ok=false，
//     对应 Python 捕获 ValueError 后的 None。
//
// 返回的时间统一为 UTC（naive 输入按 UTC 赋时区，与评审结论「naive 视为
// UTC」一致），因此可直接做时刻比较。
func parseISODateTime(s string) (t time.Time, ok bool) {
	if s == "" {
		return time.Time{}, false
	}

	// ── 日期部分 ──
	var year, month, day, i int
	switch {
	case len(s) >= 10 && s[4] == '-' && s[7] == '-': // 扩展格式 YYYY-MM-DD
		if !isDigits(s[0:4]) || !isDigits(s[5:7]) || !isDigits(s[8:10]) {
			return time.Time{}, false
		}
		year, month, day = atoiDigits(s[0:4]), atoiDigits(s[5:7]), atoiDigits(s[8:10])
		i = 10
	case len(s) >= 8 && isDigits(s[0:8]): // 基本格式 YYYYMMDD
		year, month, day = atoiDigits(s[0:4]), atoiDigits(s[4:6]), atoiDigits(s[6:8])
		i = 8
	default:
		return time.Time{}, false
	}
	// Python datetime 年份下限为 1（0000 年非法）。
	if year < 1 || month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) {
		return time.Time{}, false
	}

	hour, minute, second, nsec := 0, 0, 0, 0
	hasOffset, offsetSec := false, 0

	if i < len(s) {
		i++ // 任意单字符分隔符（Python 3.11+ 语义）

		// ── 小时（必有，两位数字）──
		if i+2 > len(s) || !isDigits(s[i:i+2]) {
			return time.Time{}, false
		}
		hour = atoiDigits(s[i : i+2])
		i += 2

		// ── 分钟/秒/小数秒：扩展（冒号）或紧凑（连续数字对）形式 ──
		switch {
		case i < len(s) && s[i] == ':':
			i++
			if i+2 > len(s) || !isDigits(s[i:i+2]) {
				return time.Time{}, false
			}
			minute = atoiDigits(s[i : i+2])
			i += 2
			if i < len(s) && s[i] == ':' {
				i++
				if i+2 > len(s) || !isDigits(s[i:i+2]) {
					return time.Time{}, false
				}
				second = atoiDigits(s[i : i+2])
				i += 2
				if i < len(s) && (s[i] == '.' || s[i] == ',') {
					f, n, ok := parseFrac(s[i+1:])
					if !ok {
						return time.Time{}, false
					}
					nsec = f
					i += 1 + n
				}
			}
		case i+2 <= len(s) && isDigits(s[i:i+2]):
			minute = atoiDigits(s[i : i+2])
			i += 2
			if i+2 <= len(s) && isDigits(s[i:i+2]) {
				second = atoiDigits(s[i : i+2])
				i += 2
				if i < len(s) && (s[i] == '.' || s[i] == ',') {
					f, n, ok := parseFrac(s[i+1:])
					if !ok {
						return time.Time{}, false
					}
					nsec = f
					i += 1 + n
				}
			}
		}

		// ── 时区后缀 ──
		if i < len(s) {
			switch s[i] {
			case 'Z', 'z':
				hasOffset = true
				i++
			case '+', '-':
				sign := 1
				if s[i] == '-' {
					sign = -1
				}
				off, n, ok := parseOffset(s[i+1:])
				if !ok {
					return time.Time{}, false
				}
				hasOffset = true
				offsetSec = sign * off
				i += 1 + n
			}
		}
		if i != len(s) {
			return time.Time{}, false // 存在未消费的残留字符 → 解析失败
		}
	}

	// Python 拒绝 hour>23 / minute>59 / second>59（不支持闰秒）。
	if hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, false
	}

	loc := time.UTC
	if hasOffset {
		loc = time.FixedZone("", offsetSec)
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, nsec, loc).UTC(), true
}

// parseOffset 解析 ISO 时区偏移：HH、HHMM、HHMMSS、HH:MM、HH:MM:SS。
// 返回偏移秒数与消耗的字符数。
func parseOffset(s string) (off, consumed int, ok bool) {
	if len(s) < 2 || !isDigits(s[0:2]) {
		return 0, 0, false
	}
	hours := atoiDigits(s[0:2])
	consumed = 2
	var minutes, seconds int
	if len(s) > consumed && s[consumed] == ':' {
		if len(s) < consumed+3 || !isDigits(s[consumed+1:consumed+3]) {
			return 0, 0, false
		}
		minutes = atoiDigits(s[consumed+1 : consumed+3])
		consumed += 3
		if len(s) > consumed && s[consumed] == ':' {
			if len(s) < consumed+3 || !isDigits(s[consumed+1:consumed+3]) {
				return 0, 0, false
			}
			seconds = atoiDigits(s[consumed+1 : consumed+3])
			consumed += 3
		}
	} else if len(s) >= consumed+2 && isDigits(s[consumed:consumed+2]) {
		minutes = atoiDigits(s[consumed : consumed+2])
		consumed += 2
		if len(s) >= consumed+2 && isDigits(s[consumed:consumed+2]) {
			seconds = atoiDigits(s[consumed : consumed+2])
			consumed += 2
		}
	}
	if hours > 23 || minutes > 59 || seconds > 59 {
		return 0, 0, false
	}
	return hours*3600 + minutes*60 + seconds, consumed, true
}

// parseFrac 解析小数秒数字串，超出微秒的位数截断（Python fromisoformat 语义）。
// 返回纳秒数与消耗的字符数。
func parseFrac(s string) (nsec, consumed int, ok bool) {
	n := 0
	for n < len(s) && isDigitByte(s[n]) {
		n++
	}
	if n == 0 {
		return 0, 0, false
	}
	use := n
	if use > 6 {
		use = 6 // 截断到微秒
	}
	micros := atoiDigits(s[:use])
	for i := use; i < 6; i++ {
		micros *= 10
	}
	return micros * 1000, n, true
}

// daysInMonth 返回指定年月的天数（含闰年规则）。
func daysInMonth(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	}
	return 0
}

// isDigits 报告 s 是否为非空纯十进制数字串。
func isDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigitByte(s[i]) {
			return false
		}
	}
	return true
}

// isDigitByte 报告 b 是否为 ASCII 十进制数字。
func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }

// atoiDigits 把已校验的十进制数字串转为整数（调用方须先经 isDigits 校验）。
func atoiDigits(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// parseISODateTimePtr 是 parseISODateTime 的指针包装：解析失败返回 nil
// （对应 Python 过滤逻辑中边界解析失败 → None → 忽略该边界）。
func parseISODateTimePtr(s string) *time.Time {
	t, ok := parseISODateTime(s)
	if !ok {
		return nil
	}
	return &t
}
