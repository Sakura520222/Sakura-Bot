# 开发者文档

2.6.0 的 Go Module 位于仓库根目录。主 Bot、助手与开发工具使用同一依赖清单。Vue WebUI 和 VitePress 文档站分别位于 `web/` 和 `wiki/`。

## 开始开发

- 配置本地构建与独立测试库，按流程联调和提交。 [开发快速开始](Developer-Getting-Started.md)
- 查找程序入口、共享组件和数据流。 [系统架构](Developer-Architecture.md)
- 了解配置写入、并发保护和备份。 [配置持久化与恢复](Developer-Configuration-Persistence.md)
- 检查回归验证、镜像、发布附件与 Pages 构建。 [CI/CD 工作流](Developer-Workflow-Improvements.md)
- 本地预览并核对页面导航、内部链接与发布。 [文档站维护](Developer-Documentation-Site.md)

## 按模块查找

| 模块 | 阅读 |
| --- | --- |
| LLM、用途路由与 SSE | [AI 厂商、模型与用途](AI_PROFILES.md) · [系统架构](Developer-Architecture.md#ai-与定时总结) |
| 助手执行、回合与账本 | [助手架构](Developer-QA-Turns-Billing.md) |
| 消息、总结与向量搜索 | [RAG 实现](Developer-RAG-Feature-V3.md) |
| 图片任务和原帖关联 | [频道图片管道](images-pipeline.md) |
| 审核、入群验证与通知 | [Sakura Guard](User-Sakura-Guard.md) |
| 知识采集、增量与发布 | [Sakura Wiki](User-Sakura-Wiki.md) |

[Go 迁移执行记录](../docs/go-mainbot-execplan.md)保存迁移范围、验证结果和已知边界。旧版 Python 源码整体归档在仓库的 `legacy/python` 分支（最终版本 v2.5.2）；旧版运行记录见[历史文档](Archive.md)。
