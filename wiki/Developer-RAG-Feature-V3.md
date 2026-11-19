# RAG 实现与维护

## 写入路径

1. 主 Bot 及 UserBot 的新消息、编辑、删除进入 `internal/rag`。
2. 消息及可用的 OCR/描述保留来源元数据，然后分块并批量生成 Embedding。
3. `internal/vectors` 使用 SQLite WAL 持久化 float32 向量、文档和元数据。消息编辑更新其索引，删除撤销对应记录。
4. 总结先保存至 MySQL，再写入总结向量。写入失败保留 MySQL 记录并记录错误。总结索引 ID 已存在时，以当前 MySQL 记录更新该索引。

图片处理复用[频道图片管道](images-pipeline.md)。归档、索引和业务表各自保留自己的职责，备份与恢复需要对应数据。

## 查询与工具

助手通过 `internal/tools` 执行频道检索。语义查询计算向量后在消息或总结索引中召回结果。关键词查询、最近总结、来源详情和频道目录使用对应的持久化信息。可选的 Reranker 对初步召回候选进行重排序。

- `semantic_search` 查询语义相关内容。
- `keyword_search` 按文字关键词查询。
- `get_recent_summaries` 读取近期总结。
- `get_source_detail` 继续读取已有来源详情。

工具结果携带频道和可用来源链接，模型应将这些结果作为资料引用。公开 Wiki 工具读取已发布且通过权限核对的 MySQL 内容，与消息 RAG 并行。

## 模型、维度和数据维护

Embedding 模型和 `EMBEDDING_DIMENSION` 必须匹配现有索引。模型变更时需要重建对应集合。Embedding 或 Reranker 的连接错误应按真实服务状态报告。空召回和上游失败应在调用结果中能区分。

旧 Chroma 转换、环境变量及验证见 [RAG 快速启动](User-RAG-Quickstart.md)。向量任务及 WebUI 集成应在独立测试库运行。

```bash
go test ./internal/vectors ./internal/embedder ./internal/reranker ./internal/rag ./internal/tools
go test ./cmd/sakura-qa
```
