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
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 本文件复刻 memory_manager.py 关键词检索依赖的 Python 值语义：
//
//   - json.loads：R13 双解析的读取半边（memory_manager.py:376-390），
//     失败时保留原始文本；数字按 int/float 区分（json.Unmarshal 默认全
//     float64 会让 str(1) 变 "1.0"，与 Python str(1)=="1" 背离）；
//   - str()：Python str 对顶层字符串不加引号、对容器做 repr；
//   - ", ".join()：字符串按字符迭代等边界语义。

// parseJSONText 复刻 Python json.loads 的读取语义：
//   - 空字节（NULL/空串，对应 Python None/""）→ nil；
//   - 解析成功 → 解析值（json.Number 归一为 int64/float64）；
//   - 解析失败（含尾部多余内容）→ 原始文本字符串。
func parseJSONText(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(data)
	}
	// Python json.loads 对尾部垃圾（如 `[] xyz`）抛异常 → 保留原文本。
	if dec.More() {
		return string(data)
	}
	return pyNumberize(v)
}

// pyNumberize 递归把 json.Number 归一为 int64（整数字面量）或 float64，
// 对齐 Python json.loads 的 int/float 区分。
func pyNumberize(v any) any {
	switch x := v.(type) {
	case json.Number:
		if !strings.ContainsAny(x.String(), ".eE") {
			if i, err := strconv.ParseInt(x.String(), 10, 64); err == nil {
				return i
			}
		}
		f, err := x.Float64()
		if err != nil {
			return x.String()
		}
		return f
	case []any:
		for i, el := range x {
			x[i] = pyNumberize(el)
		}
		return x
	case map[string]any:
		for k, el := range x {
			x[k] = pyNumberize(el)
		}
		return x
	default:
		return v
	}
}

// pythonTruthy 复刻 Python 真值判定（if value:）。
func pythonTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

// pythonStr 复刻 Python str(value)：顶层字符串原样返回（不加引号），
// 容器按 repr 渲染（元素字符串带引号），None/bool 按字面量。
func pythonStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return pyFloatStr(x)
	case []any:
		parts := make([]string, len(x))
		for i, el := range x {
			parts[i] = pythonRepr(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		// Python 字典按插入序；Go map 无序，此处按键排序保证确定性。
		// （keywords/topics 实际形态为字符串列表，dict 仅属理论兜底。）
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, pythonRepr(k)+": "+pythonRepr(x[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", x)
	}
}

// pythonRepr 复刻 Python repr(value)：与 str 的差异仅在字符串加引号。
func pythonRepr(v any) string {
	if s, ok := v.(string); ok {
		return pyReprString(s)
	}
	return pythonStr(v)
}

// pyReprString 复刻 Python 字符串 repr：优先单引号，含单引号且不含双引号时
// 换双引号；转义反斜杠、引号与 \n\r\t，其余控制字符输出 \xNN。
func pyReprString(s string) string {
	quote := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		quote = "\""
	}
	var b strings.Builder
	b.WriteString(quote)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r == rune(quote[0]) {
				b.WriteByte('\\')
				b.WriteRune(r)
			} else if r < 0x20 || r == 0x7f {
				b.WriteString(fmt.Sprintf(`\x%02x`, r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteString(quote)
	return b.String()
}

// pyFloatStr 复刻 Python float 的 str：保证带小数点或指数（str(1.0)=="1.0"）。
// Go 'g' -1 与 Python repr 同为最短往返表示；指数阈值差异属病理输入偏差。
func pyFloatStr(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// pythonJoinComma 复刻 Python ", ".join(value) 的全部可观察边界：
//   - 字符串按字符迭代（', '.join("abc") == "a, b, c"）；
//   - 列表元素必须全为字符串，否则 Python 抛
//     TypeError: sequence item N: expected str instance, found <type>；
//   - 字典迭代键（键恒为字符串）；
//   - 其他真值类型 → TypeError: can only join an iterable。
//
// ok=false 时 errDetail 为供日志拼接的 TypeError 细节。
func pythonJoinComma(v any) (joined, errDetail string, ok bool) {
	switch x := v.(type) {
	case string:
		runes := []rune(x)
		parts := make([]string, len(runes))
		for i, r := range runes {
			parts[i] = string(r)
		}
		return strings.Join(parts, ", "), "", true
	case []any:
		parts := make([]string, 0, len(x))
		for i, el := range x {
			s, isStr := el.(string)
			if !isStr {
				return "", fmt.Sprintf("sequence item %d: expected str instance, found %s", i, pyTypeName(el)), false
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ", "), "", true
	case map[string]any:
		// 键顺序同 pythonStr：排序保证确定性（病理输入，仅兜底）。
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return strings.Join(keys, ", "), "", true
	default:
		return "", "can only join an iterable", false
	}
}

// pyTypeName 返回 Python 风格的类型名（用于复刻 TypeError 文案）。
func pyTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case string:
		return "str"
	case int64:
		return "int"
	case float64:
		return "float"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	default:
		return "object"
	}
}
