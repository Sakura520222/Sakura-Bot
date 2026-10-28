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

import "context"

// 本文件定义工具执行器对检索组件的窄接口（Go 惯例：消费者包内定义，
// 由 main 装配实现）。行号出处为 Python 侧被移植方法的调用面。

// 结果字典的键约定（对齐 Python dict 的键，勿改名）：
//
//	result_id    请求内自增的内部引用 ID（int）
//	source       "message" | "summary"（可缺省，由启发式推断）
//	summary_id   仅总结类携带的 MySQL 总结记录 ID（int 或缺省）
//	message_id   仅消息类携带的真实 Telegram 消息 ID（int 或缺省）
//	summary_text 正文文本
//	metadata     map[string]any：channel_id / channel_name / created_at /
//	             message_id / channel_username 等
//	doc_id       原始文档 ID（summaries 为数字字符串，messages 为
//	             "channel_id:message_id"）
//	similarity / rerank_score  相似度 / 重排得分（float，可缺省）
//	channel_id / channel_name  元数据顶层的便捷键（可缺省）

// VectorStore 对齐 ToolExecutor 眼中的 core/ai/vector_store.py 调用面
// （agent_tools.py:339-385）。query 为查询文本——Python 版在 vector store
// 内部生成 embedding（vector_store.py:554-564），Go 侧由装配层组合
// embedder + vectors 实现；embedding 不可用/失败时返回空切片（对齐
// vector_store.py:558-564 的 [] 返回），接口返回 error 时执行器按工具
// 异常路径转 {"error": ...}（等价 Python 组件上抛异常）。
type VectorStore interface {
	// IsAvailable 对齐 vector_store.is_available（agent_tools.py:340）。
	IsAvailable() bool
	// IsMessagesAvailable 对齐 vector_store.is_messages_available。
	IsMessagesAvailable() bool
	// SearchSimilar 对齐 search_similar：仅总结 collection。
	// topK<=0、filterMetadata 为 nil、dateAfter/dateBefore 为空串的语义
	// 与 Python 同名参数一致。
	SearchSimilar(ctx context.Context, query string, topK int, filterMetadata map[string]any, dateAfter, dateBefore string) ([]map[string]any, error)
	// SearchMessages 对齐 search_messages：仅原始消息 collection。
	SearchMessages(ctx context.Context, query string, topK int, filterMetadata map[string]any, dateAfter, dateBefore string) ([]map[string]any, error)
	// SearchAll 对齐 search_all：两 collection 合并按相似度降序
	// （vector_store.py:406-464，逐条结果带 source 标记）。
	SearchAll(ctx context.Context, query string, topK int, filterMetadata map[string]any, dateAfter, dateBefore string) ([]map[string]any, error)
}

// MemoryManager 对齐 ToolExecutor 眼中的 core/ai/memory_manager.py 调用面
// （agent_tools.py:398-556）。返回行/字典的键与 Python 版一致；
// 组件内部捕获异常返回空值的行为（memory_manager.py 各方法的 try/except）
// 由实现方承担；本接口的 error 仅表示实现方选择上抛的异常，执行器将其
// 转为 {"error": ...}。字符串参数传空串等价 Python None；int 参数为 0 的
// 语义在各方法注明。
type MemoryManager interface {
	// SearchSummaries 对齐 memory_manager.search_summaries
	// （memory_manager.py:330-413）。topics 为 nil 等价 Python None；
	// timeRangeDays 执行器已应用工具层默认 90。
	SearchSummaries(ctx context.Context, keywords, topics []string, timeRangeDays int, channelID string, limit int) ([]map[string]any, error)
	// ListChannels 对齐 memory_manager.list_channels（:182-194），
	// 行经 _normalize_channel_row 归一化。
	ListChannels(ctx context.Context) ([]map[string]any, error)
	// ResolveChannel 对齐 memory_manager.resolve_channel（:196-281），
	// 返回 {success, channel_id, channel_name, match_type, channel|candidates, error?}。
	ResolveChannel(ctx context.Context, channelHint string) (map[string]any, error)
	// GetRecentSummaries 对齐 memory_manager.get_recent_summaries
	// （:300-328）。timeRangeDays 为 0 等价 Python None（不限时间范围）。
	GetRecentSummaries(ctx context.Context, channelID string, limit, timeRangeDays int) ([]map[string]any, error)
	// GetChannelStats 对齐 memory_manager.get_channel_stats（:283-298）。
	GetChannelStats(ctx context.Context, channelID string) (map[string]any, error)
	// GetChannelContext 对齐 memory_manager.get_channel_context（:146-180），
	// 返回给模型阅读的上下文描述字符串。
	GetChannelContext(ctx context.Context, channelID string) (string, error)
}

// Reranker 对齐 ToolExecutor 眼中的 core/ai/reranker.py 调用面
// （agent_tools.py:433-455）。candidates 为结果存储中的原始结果字典；
// 返回值必须为候选的浅拷贝并追加 rerank_score 键（对齐 reranker.py 语义），
// 不得修改入参。Python 侧经 run_in_executor 调用的同步阻塞在 Go 侧即
// 普通阻塞调用，ctx 用于取消。
type Reranker interface {
	// IsAvailable 对齐 reranker.is_available（agent_tools.py:434）。
	IsAvailable() bool
	// Rerank 对齐 reranker.rerank；topK<=0 由实现方按配置兜底
	// （对齐 `top_k or self.final_k`）。
	Rerank(ctx context.Context, query string, candidates []map[string]any, topK int) []map[string]any
}
