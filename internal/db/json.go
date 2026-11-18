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
	"bytes"
	"encoding/json"
	"strings"
)

// ParseJSONColumn 复刻 Python「值真值判定 + try json.loads / except: pass」
// 的读取语义（如 mysql.py get_summaries 对 params/result/content 的解析、
// memory_manager.py:376-390 对 keywords/topics 的解析）：
//   - 空值（NULL/空串）→ nil（对应 Python if row.get(col) 分支不进入）；
//   - 解析成功 → 解析后的任意 JSON 值；
//   - 解析失败 → 原始文本字符串（对应 except: pass 后值保持原 str）。
func ParseJSONColumn(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}
	return v
}

// UnmarshalJSONCompat 是 R13 的「双解析兼容」：先按目标类型直接解析；
// 失败则把列文本当作 JSON 字符串解码一次，再用其内容解析目标
// （兼容历史上被双重编码写入的 JSON 列）。两次都失败时返回最后一次错误。
func UnmarshalJSONCompat(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err == nil {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	return json.Unmarshal([]byte(s), v)
}

// EncodeJSON 等价 json.dumps(value, ensure_ascii=False)：
// UTF-8 直出不转义，且不转义 HTML 字符（Go 默认的 <、>、& 转义
// 与 Python 输出不一致，这里用 Encoder 显式关闭）。
// 返回值无结尾换行，可直接作为 SQL 参数。
func EncodeJSON(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// EncodeJSONMust 是 EncodeJSON 的宽松包装：编码失败返回 "{}"
// （调用点均为纯数据 map/list，实际不会失败；保留兜底避免散落错误分支）。
func EncodeJSONMust(v any) string {
	s, err := EncodeJSON(v)
	if err != nil {
		return "{}"
	}
	return s
}
