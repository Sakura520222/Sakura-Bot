# 系统架构

## 进程与通信

| 程序 | 责任 | Telegram 连接 |
| --- | --- | --- |
| `sakura-main` | 频道管理、总结、投票、转发、Guard、Wiki、WebUI 及助手进程管理 | gotd/td，主 Bot 与可选 UserBot 使用独立会话 |
| `sakura-qa` | 频道问答、评论助手、会话、计费、订阅、投稿与申诉 | Telegram Bot API，使用专用 Token |
| Vue WebUI | 频道与系统管理、公开 Wiki、账号交互 | 同一服务的 HTTP API |

两个程序共享 Go Module。Docker 将两个可执行程序和 WebUI 置于同一镜像，主进程可按配置启动和停止助手子进程。数据分别通过共享仓储和持久化接口访问。MySQL 运行锁防止同类进程同时接管同一业务实例。

## 组件与边界

| 领域 | 源码目录与职责 |
| --- | --- |
| 应用启动 | `internal/mainbot` 持有依赖装配、业务调度、事件绑定、停止与重启 |
| Telegram | `internal/mtproto` 负责 gotd/td 会话、频道历史、媒体和事件。 `internal/tg` 封装 Bot API |
| 入口交互 | `internal/botcmd` 管理主 Bot 的命令、按钮和参数输入。 `internal/handlers` 处理助手交互 |
| 持久化 | `internal/db` 使用 MySQL 保存业务记录。 `internal/vectors` 维护 SQLite WAL 索引 |
| AI | `internal/llm` 管理 SSE 与生成请求，`internal/embedder` 和 `internal/reranker` 各自处理结构化模型接口 |
| 频道业务 | `internal/summary`、`internal/poll`、`internal/forwarding`、`internal/media` 处理总结、互动、转发和图片 |
| 群管与百科 | `internal/guard` 和 `internal/wiki` 管理案件、验证、来源、增量生成和发布 |
| 问答 | `internal/engine`、`internal/tools`、`internal/rag` 处理生成、工具和检索 |
| WebUI | `internal/webapi` 提供 API、认证、公开 Wiki 和 SPA 静态服务 |
| 配置 | `internal/config` 管理运行配置、模型路由、备份和热重载 |

## Telegram 事件

主 Bot 和 UserBot 通过 gotd/td 分派消息、编辑、删除和成员事件。事件队列按会话分片，在同一会话中保持处理顺序。队列和后台 Worker 有明确容量，运行由 `context` 和关闭流程管理。

主 Bot 的操作菜单使用普通按钮文案，正文可使用 Telegram 原生 Rich Messages。Guard 的回调会复核原通知、会话和当前审核权限。用户 ID、频道标识和日志字段应区分 Telegram 的带符号 ID 与内部存储标识。

## AI 与定时总结

生成请求通过共用 SSE 层。等待完整结果的业务聚合有效分片并核对模型结束信息。助手消费分片进行预览和 Tool Calling。输出路由、用途思考等级和图片能力由[运行配置](AI_PROFILES.md)决定。

定时任务、Telegram 菜单与 WebUI 汇合到总结服务。WebUI 任务在应用进程生命周期内后台运行，可按任务编号查询。总结在历史读取时补入已持久化的 OCR 和描述。模型输出中断、超出预算或识图等待超限时返回相应任务结果。

## 数据一致性

- MySQL 保存频道业务、会话、计费、订阅、群管案件与 Wiki 数据。SQLite 向量索引保存消息和总结的检索表示。两者应分别备份。
- 图片文件按内容哈希存储，MySQL 保存对应原帖及识别结果。图像任务在写入前核对消息版本与租约。
- Wiki 生成使用来源哈希与实际存储的分块 ID，发布前再次验证证据。中断的任务从持久进度恢复。
- 配置通过文件锁、校验、历史快照和原子写入持久化。按[配置持久化约定](Developer-Configuration-Persistence.md)修改配置。

[回合与计费](Developer-QA-Turns-Billing.md) · [RAG 技术参考](Developer-RAG-Feature-V3.md) · [Go 迁移执行记录](../docs/go-mainbot-execplan.md)
