# RAG 快速启动

Sakura-Bot 的主 Bot 维护消息与总结索引。小妲助手结合语义检索、关键词、总结和原文详情回答频道问题，并根据资料附上来源链接。业务数据在 MySQL，向量索引在 SQLite。

## 配置 Embedding

在 `data/.env` 设置 Embedding 服务。Go 程序和 Docker 镜像已包含向量运行时。

```dotenv
EMBEDDING_API_KEY=YOUR_EMBEDDING_KEY
EMBEDDING_API_BASE=https://api.example.com/v1
EMBEDDING_MODEL=BAAI/bge-m3
EMBEDDING_DIMENSION=1024
VECTOR_DB_PATH=data/vectors
```

`EMBEDDING_API_BASE` 支持 API 基址，也支持以 `/embeddings` 结尾的完整地址。维度应与模型实际返回一致。更换 Embedding 模型时应重新生成相应索引，即使新模型的维度恰好相同。

### 配置可选重排序

```dotenv
RERANKER_API_KEY=YOUR_RERANKER_KEY
RERANKER_API_BASE=https://api.example.com/v1/rerank
RERANKER_MODEL=BAAI/bge-reranker-v2-m3
RERANKER_TOP_K=20
RERANKER_FINAL=5
```

重排序处理已召回的候选。配置应与实际接口兼容，最终数量应小于或等于候选数量。

## 启动与验证

1. 按[快速开始](User-Getting-Started.md)重启或启动服务，确认主 Bot 与助手可运行。
2. 添加测试频道，验证主 Bot 或 UserBot 能收到新消息。等待正文进入向量索引。在 WebUI「向量存储」中核对服务和索引状态。
3. 在小妲助手私聊提问，或发送 `/ask @频道名 问题` 限定频道。核对回答和原帖链接。
4. 对需要图片证据的频道开启[图片识别](images-pipeline.md)，等待 OCR 与描述写入消息索引。

消息历史语义查询需要对应的向量记录。最新总结类查询可以直接使用 MySQL 中的总结。Embedding 不可用时检查页面与日志中的服务错误，随后核对索引是否有对应频道和时间范围的记录。

## 从 Chroma 迁移

向量目录已包含 `lite_index.sqlite3` 时直接沿用 `VECTOR_DB_PATH`。旧版仍使用 Chroma 时，在停写窗口备份整个向量目录及 MySQL，再用目标 Go 镜像转换。

```bash
docker compose stop sakura-summary-bot
docker compose run --rm --no-deps sakura-summary-bot sakura-main --migrate-vectors
docker compose run --rm --no-deps sakura-summary-bot sakura-main --verify-vectors
docker compose up -d --no-build sakura-summary-bot
```

操作过程使用与正式部署相同的 `SAKURA_BOT_IMAGE` 和 `VECTOR_DB_PATH`。转换后先验证，再检查已有频道的消息及总结检索。保留旧向量目录及其备份，便于回滚核对。

[助手功能与权限](User-QA-Bot-Guide.md) · [RAG 开发参考](Developer-RAG-Feature-V3.md)
