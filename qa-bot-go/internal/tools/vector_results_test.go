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
	"testing"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/vectors"
)

// TestHitToResult 校验 vectors.Hit → 结果字典的 ID 解析
// （vector_store.py:620-662 移植）。
func TestHitToResult(t *testing.T) {
	cases := []struct {
		name          string
		hit           vectors.Hit
		source        string
		wantSummaryID any
		wantMessageID any
		wantSource    any
	}{
		{
			name:          "总结类数字doc_id",
			hit:           vectors.Hit{DocID: "123", Document: "正文", Similarity: 0.9},
			source:        "",
			wantSummaryID: int64(123), // 真实数据库总结 ID
			wantMessageID: nil,
			wantSource:    nil,
		},
		{
			name: "消息类冒号doc_id",
			hit: vectors.Hit{DocID: "-1001234567:42", Document: "消息",
				Metadata: map[string]any{"channel_id": "-1001234567"}, Similarity: 0.5},
			source:        "message",
			wantSummaryID: nil,
			wantMessageID: int64(42),
			wantSource:    "message",
		},
		{
			name: "metadata_message_id兜底_总结类同样适用",
			hit: vectors.Hit{DocID: "9", Document: "带元数据消息ID",
				Metadata: map[string]any{"message_id": float64(7)}, Similarity: 0.5},
			source:        "",
			wantSummaryID: int64(9),
			wantMessageID: int64(7),
			wantSource:    nil,
		},
		{
			name:          "字符串ID_哈希不resurrect",
			hit:           vectors.Hit{DocID: "abc_def:12", Document: "x", Similarity: 0.5},
			source:        "message",
			wantSummaryID: nil, // 非 md5/非数字 → summary_id 恒空（历史 Bug 红线）
			wantMessageID: int64(12),
			wantSource:    "message",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := HitToResult(c.hit, c.source)
			if r["summary_id"] != c.wantSummaryID {
				t.Errorf("summary_id = %v (%T), 期望 %v", r["summary_id"], r["summary_id"], c.wantSummaryID)
			}
			if r["message_id"] != c.wantMessageID {
				t.Errorf("message_id = %v, 期望 %v", r["message_id"], c.wantMessageID)
			}
			if r["source"] != c.wantSource {
				t.Errorf("source = %v, 期望 %v", r["source"], c.wantSource)
			}
			if r["similarity"] != c.hit.Similarity || r["doc_id"] != c.hit.DocID {
				t.Errorf("基础字段漂移: %v", r)
			}
		})
	}
}

// TestHitsToResultsEmptyNil 输入为空时返回空切片（JSON []）。
func TestHitsToResultsEmptyNil(t *testing.T) {
	if out := HitsToResults(nil, "message"); out == nil || len(out) != 0 {
		t.Errorf("nil 输入应得空切片, got %#v", out)
	}
}

// TestSortResultsBySimilarity 稳定降序：并列保持插入序（summaries 在前）。
func TestSortResultsBySimilarity(t *testing.T) {
	results := []map[string]any{
		{"doc_id": "s1", "similarity": 0.5},
		{"doc_id": "m1", "similarity": 0.9},
		{"doc_id": "s2", "similarity": 0.9}, // 与 m1 并列，但插入在后
		{"doc_id": "m2"},                    // 缺 similarity → 0
	}
	SortResultsBySimilarity(results)
	want := []string{"m1", "s2", "s1", "m2"}
	for i, id := range want {
		if results[i]["doc_id"] != id {
			t.Fatalf("排序结果 = %v, 期望 %v", results, want)
		}
	}
}
