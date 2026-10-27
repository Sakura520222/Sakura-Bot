// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package tools

import (
	"sort"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/vectors"
)

// 本文件的导出函数是 main 装配层实现 VectorStore 窄接口时的辅助件：
// 把 internal/vectors 的检索命中转换为工具层的结果字典（契约见 interfaces.go），
// 移植 core/ai/vector_store.py:620-662 的结果格式化与 :464 的合并排序语义。

// HitToResult 把单条向量检索命中转换为结果字典
// （对齐 vector_store.py:620-662 的"结果形状与 doc_id 解析规则自 chroma 版
// 逐行移植，语义不得改动"）。
//
// doc_id 区分两种形态：
//   - summaries collection：数字 ID，即真实的数据库总结 ID → summary_id；
//   - messages collection："channel_id:message_id" → 解析出真实 message_id。
//
// 历史 Bug：曾把字符串 ID 的 md5 哈希当作 summary_id 返回，模型会把这个
// 内部哈希当成消息 ID 拼进链接。现在 summary_id 仅在纯数字 doc_id 时填充，
// 消息文档解析出真实 message_id（metadata.message_id 兜底）。
//
// source 为该结果的来源标记（"summary"/"message"，search_all 打标语义）；
// 传空串表示不打标（search_similar/search_messages 不带 source）。
func HitToResult(hit vectors.Hit, source string) map[string]any {
	summaryID, messageID := parseDocIDs(hit.DocID, hit.Metadata)

	result := map[string]any{
		"summary_id":   summaryID, // 消息类结果为 nil（对齐 Python None）
		"message_id":   messageID, // 总结类结果为 nil
		"summary_text": hit.Document,
		"metadata":     hit.Metadata,
		"similarity":   hit.Similarity,
		"doc_id":       hit.DocID, // 保留原始 ID
	}
	if source != "" {
		result["source"] = source
	}
	return result
}

// HitsToResults 批量转换（source 语义同 HitToResult）。
func HitsToResults(hits []vectors.Hit, source string) []map[string]any {
	out := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		out = append(out, HitToResult(hit, source))
	}
	return out
}

// parseDocIDs 移植 vector_store.py:632-650 的 ID 解析：
//   - doc_id 为纯数字 → summary_id = 该整数；否则按正则 ":(\d+)$"
//     提取 message_id；
//   - message_id 仍缺失时回退 metadata.message_id（入库时显式保存的
//     真实消息 ID；对总结类 doc_id 同样适用——与 Python 判定顺序一致）。
func parseDocIDs(docID string, metadata map[string]any) (summaryID, messageID any) {
	if id, err := pyInt(docID); err == nil {
		summaryID = int64(id)
	} else if m := messageIDSuffix.FindStringSubmatch(docID); m != nil {
		if id, err := pyInt(m[1]); err == nil {
			messageID = int64(id)
		}
	}
	if messageID == nil && metadata != nil {
		// 对齐 Python 真值判定：metadata.message_id 为 0/空串时不回退
		if raw := metadata["message_id"]; truthy(raw) {
			if id, err := pyInt(raw); err == nil {
				messageID = int64(id)
			}
		}
	}
	return summaryID, messageID
}

// SortResultsBySimilarity 把合并后的结果按 similarity 降序稳定排序
// （对齐 vector_store.py:464 `all_results.sort(key=..., reverse=True)`：
// Python 稳定排序在 reverse 下并列元素保持原序——先 summaries 后 messages
// 的插入序；Go 的 SliceStable 配 > 比较器等价）。缺失 similarity 视为 0。
func SortResultsBySimilarity(results []map[string]any) {
	sort.SliceStable(results, func(i, j int) bool {
		return pyFloatOrZero(results[i]["similarity"]) > pyFloatOrZero(results[j]["similarity"])
	})
}

// pyFloatOrZero 安全读取相似度数值，缺失/非法按 0（对齐
// `x.get("similarity", 0)`）。
func pyFloatOrZero(v any) float64 {
	if f, err := pyFloat(v); err == nil {
		return f
	}
	return 0
}
