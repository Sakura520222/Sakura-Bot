// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package i18n 内嵌中英双语文案并提供与 Python core/i18n/i18n.py
// 逐语义对齐的 t(key, kwargs) 查询。
//
// 文案数据在 messages_zh.gen.go / messages_en.gen.go 中全量内嵌
// （MESSAGE_ZH_CN / MESSAGE_EN_US，含 appeal.*），由一次性提取脚本生成。
// 查询语义对齐 i18n.py:1984-2003：
//  1. 当前语言缺 key → 回退 zh-CN；
//  2. 仍缺 → 返回 key 本身；
//  3. 插值手写 {name} 扫描：任一命名 token 缺失或模板非法 → 返回原模板
//     （对齐 str.format 抛 KeyError/ValueError 被 catch 返回原文，评审 R14）。
package i18n

import (
	"strings"
	"sync/atomic"
)

// 支持的语言（对齐 I18nManager._supported_languages）。
const (
	LangZhCN = "zh-CN"
	LangEnUS = "en-US"
)

// currentLanguage 进程内当前语言；默认 zh-CN（对齐 Python 单例初值）。
var currentLanguage atomic.Value // 实际存储 string

func init() {
	currentLanguage.Store(LangZhCN)
}

// GetLanguage 返回当前语言代码。
func GetLanguage() string {
	return currentLanguage.Load().(string)
}

// SetLanguage 设置当前语言；不支持的语言返回 false 并保持不变
// （对齐 I18nManager.set_language，i18n.py:1942-1958）。
func SetLanguage(language string) bool {
	if language != LangZhCN && language != LangEnUS {
		return false
	}
	currentLanguage.Store(language)
	return true
}

// GetText 获取翻译文本；kwargs 为 nil 或空时原样返回模板
// （对齐 Python `if kwargs: ... else 原文`）。
func GetText(key string, kwargs map[string]string) string {
	return T(key, kwargs)
}

// T 是 GetText 的简写别名（对齐 i18n.py:2045 的 t()）。
func T(key string, kwargs map[string]string) string {
	message, ok := dictFor(GetLanguage())[key]

	// 当前语言没有该 key，回退到中文（i18n.py:1986-1989）。
	if !ok {
		message, ok = MessagesZhCN[key]
	}

	// 回退后仍不存在，返回 key 本身（i18n.py:1991-1994）。
	if !ok {
		return key
	}

	// 支持变量插值；任一 token 缺失或模板非法时返回原模板（R14）。
	if len(kwargs) == 0 {
		return message
	}
	if out, ok := formatTemplate(message, kwargs); ok {
		return out
	}
	return message
}

// dictFor 返回指定语言的字典；未知语言视为空（触发回退链）。
func dictFor(lang string) map[string]string {
	if lang == LangEnUS {
		return MessagesEnUS
	}
	return MessagesZhCN
}

// formatTemplate 复刻 Python str.format 的 {name} 插值与 {{ }} 转义，
// 只支持命名 token（现有文案不含位置参数）：
//   - {{ → 字面 { ；}} → 字面 }；
//   - {name} 或 {name:spec} / {name!conv} 取 name 对应值；
//   - 孤立 { 或 }、空 token、含嵌套 { 的字段、kwargs 缺失该名字 → 返回 (原模板, false)。
func formatTemplate(tmpl string, kwargs map[string]string) (string, bool) {
	var b strings.Builder
	// 预估容量，减少扩容。
	b.Grow(len(tmpl) + 32)

	for i := 0; i < len(tmpl); {
		c := tmpl[i]
		switch c {
		case '{':
			// "{{" → 字面 '{'。
			if i+1 < len(tmpl) && tmpl[i+1] == '{' {
				b.WriteByte('{')
				i += 2
				continue
			}
			end := strings.IndexByte(tmpl[i:], '}')
			if end < 0 {
				// Python: 单个 '{' 未闭合 → ValueError → 返回原文。
				return tmpl, false
			}
			field := tmpl[i+1 : i+end]
			if strings.ContainsRune(field, '{') {
				// 嵌套字段（格式说明符再嵌套）现有文案未使用，保守返回原文。
				return tmpl, false
			}
			name := field
			// 去掉 !conversion 与 :format_spec 部分（现有文案未使用，防御性支持）。
			if idx := strings.IndexAny(field, "!:"); idx >= 0 {
				name = field[:idx]
			}
			if name == "" {
				// Python: "{}" 自动位置编号在仅传 kwargs 时 → IndexError → 返回原文。
				return tmpl, false
			}
			v, ok := kwargs[name]
			if !ok {
				// Python: KeyError → 返回原文（R14 的核心语义）。
				return tmpl, false
			}
			b.WriteString(v)
			i += end + 1
		case '}':
			// "}}" → 字面 '}'。
			if i+1 < len(tmpl) && tmpl[i+1] == '}' {
				b.WriteByte('}')
				i += 2
				continue
			}
			// Python: 单个 '}' → ValueError "Single '}' encountered" → 返回原文。
			return tmpl, false
		default:
			// UTF-8 多字节字符按字节拷贝安全（{ 与 } 不会出现在多字节序列中间）。
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), true
}
