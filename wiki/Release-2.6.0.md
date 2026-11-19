# v2.6.0 Go 版升级说明

## 运行方式

主 Bot 和助手在统一 Go Module 中开发，分别使用 `sakura-main` 和 `sakura-qa`。主 Bot 的 Telegram 连接和 UserBot 使用 gotd/td，助手使用 Telegram Bot API。WebUI 后端由主 Bot 的 Go HTTP 服务提供，页面继续使用 Vue。

生产镜像包含两个 Go 程序和构建后的 WebUI。业务记录位于 MySQL，向量索引使用 SQLite WAL 和 float32。

## 使用变化

- 主 Bot 首次 `/start` 提供个人语言选择。分类菜单使用纯文本标签，斜杠命令可打开操作面板。按提示输入参数并点击确认。
- WebUI 手动总结作为后台任务运行。同一频道已有 WebUI 总结任务时复用原任务，页面查询进度与结果。总结不会因最初的浏览器请求断开而随之取消。
- 文字、图文和工具生成共用 OpenAI 兼容 SSE 客户端。各用途可单独选择模型、图片能力和思考等级。模型列表、Embedding 和 Reranker 使用各自的接口。
- 频道总结使用已入库的 OCR 和图片描述。新图片仍在识别时，系统先等待同批任务。超出等待窗口时保留总结进度并提示稍后重试。
- Sakura Guard 的验证卡和案件通知使用 Rich Messages。通知会使用可解析的群名与公开用户名，并为可解析的用户提供可打开的资料链接。
- Wiki 增量更新保留已有来源与分块 ID，复用模型缓存，并在任务继续执行时补建缺失分块。包含实时进度修复的镜像还会在任务页区分模型思考与接收正文，并显示相应的字符统计。

## 升级顺序

1. 记录当前版本、镜像、Compose 配置和持久化目录。停写后备份 MySQL、数据目录、SQLite 文件及其 WAL、Telegram 会话、提示词和自定义目录。
2. 在独立 MySQL 与数据副本上验证目标镜像，检查频道、订阅、积分、计费、群管记录、Wiki 和向量检索。
3. 停止旧服务后用目标镜像接管原数据。同一个 Telegram Token 对应的服务实例应在维护窗口交替切换。已有的 Telethon SQLite 会话可由 Go 客户端导入。
4. 已经使用 `lite_index.sqlite3` 的环境可复用同一向量目录。仍在使用 Chroma 的环境按[离线向量转换](User-RAG-Quickstart.md#从-chroma-迁移)执行。
5. 核对 WebUI 登录、主 Bot 菜单、UserBot 连接、助手进程、频道总结、转发、群管与 Wiki 任务。保留完整备份和可用的旧镜像。

回滚前停止 Go 服务，恢复旧镜像和对应的 Compose 配置。对于升级后产生的数据，先依据备份和兼容性核对需要恢复的范围。安装、更新与回滚命令见[快速开始](User-Getting-Started.md)。

[历史更新记录](Archive.md) · [仓库变更日志](../CHANGELOG.md)
