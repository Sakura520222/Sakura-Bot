# Sakura-Bot 主 Bot Go 迁移 ExecPlan

## 验收目标

在 `feature/go-mainbot-migration` 交付同一 Go Module 内的主 Bot 和助手 Bot。用隔离的 MySQL、SQLite、Telegram 测试账号和临时配置核对旧数据和业务契约，验证完成后移除 Python 生产运行时与旧入口。

## 进度

- [x] 2026-11-18 审计工作区。基线为 `9fab944`，分支已存在，之前的迁移代码以未提交文件保留。根 Go Module 已包含迁移后的助手，以及主 Bot 的 MTProto、总结、投票、转发、RAG、Guard、Wiki 和管理命令包。
- [x] 2026-11-18 核对初始构建。基线缺少主 Bot 入口和 WebUI 路由装配，Wiki 有三处编译错误。基线 Go 测试启动后，助手入口包通过。
- [x] 装配主 Bot、共用配置和数据库生命周期，接入定时任务、Bot/UserBot 事件、媒体和跨 Bot 通信。Windows 主程序构建并通过 HTTP 冒烟检查。
- [x] 对照 FastAPI 业务路由清单验证 Go 路由注册，233 个显式方法与路径均匹配。补充 WebUI 登录、Wiki 认证、公开资源、配置更新和管理操作的回归测试。
- [ ] 校验所有路由的数据契约和依赖外部服务的业务闭环。
- [ ] 验证所有领域功能。覆盖 MTProto 登录和会话迁移、回溯、实时事件、群管、Wiki 任务和数据重建。
- [x] 更新双二进制构建、启动脚本、Go 容器产物、镜像部署器、发行归档和 GitHub Actions。工作分支未部署。
- [x] 在 Linux Docker 29.9.3 执行生产镜像最终阶段和容器冒烟。构建输入为独立交叉编译的 Linux amd64 Go 二进制和 Vue 产物。
- [ ] 执行完整的 Docker 多阶段源码构建，记录 GitHub Actions 结果。
- [x] 运行全包测试、vet、race、前端构建和容器冒烟；记录相同条件下的 Python/Go 性能数据。

## 决策

- 复用助手的 `config`、`db`、`logging`、`llm`、`embedder`、`reranker`、`tg`、`media` 和 `vectors`。主 Bot 的 MTProto 会话通过 gotd/td 管理，保留原 Telethon 文件，写入独立 gotd 会话。
- 主 Bot 与助手各有入口和进程，使用不同 Telegram 会话和 MySQL 单实例锁，共用业务数据。
- WebUI 沿用 Vue 产物。独立的 `--web-only` 入口用于静态资源和 HTTP 健康检查，正常入口装配实际业务。
- UserBot 首次验证通过 `sakura-main --login-userbot` 完成，服务启动只读取已有授权。
- Windows 验证使用 Go 1.27.3、Node.js、LLVM Clang 和独立的 MySQL 8.4.13。Go 测试使用工作区内的临时目录，解决 Windows AppContainer 默认临时目录的文件重命名限制。

## 运行架构

`cmd/sakura-main` 通过 `internal/mainbot` 装配主 Bot。gotd/td 以独立会话运行 Bot 和 UserBot，分派新消息、编辑、删除、群成员事件和按钮回调。频道事件进入总结、转发、Wiki 和 RAG，群事件进入 Guard，图片任务由 MySQL 任务表驱动。WebUI 通过 Go HTTP 服务访问同一业务组件。定时任务、图片处理和事件处理都有限制并发的工作队列。

`cmd/sakura-qa` 继续走 Go Telegram Bot API SDK，保留 Agentic RAG、多轮对话、工具、积分、计费和评论链路。两进程复用共享包和 MySQL 业务表，通过独立的 MySQL 运行锁约束实例数。主 Bot 写入 SQLite WAL 向量索引，助手通过版本号读取更新。部署、配置和回滚步骤见 [Go 部署指南](go-deployment.md)。

每个 gotd 会话使用文件锁，原 Telethon SQLite 会话保留供回滚。gotd 会把凭据、实体和更新位置分别写到同一会话名对应的独立文件。已有 SQLite 向量库直接沿用，旧 Chroma 库通过停机迁移命令转换和核对。

## 验证与剩余风险

- Go WebUI 的健康检查和 Vue 的 `/login` 直达路由均返回 HTTP 200。
- Vue 类型检查与生产构建通过。44 项前端测试通过，Wiki 和助手的浏览器回归通过。Wiki 移动端滚动用例在 Windows Chrome 上两次超时后，使用项目锁定的 Playwright Chromium 复测通过。
- `go test ./...` 在隔离 MySQL 8.4.13 上通过，包括消息级计费、签到、退款、独立进程锁、评论回填、旧 Guard 验证通知恢复和 Wiki 删除事件重放。全包 `go vet ./...`、Linux amd64 双二进制交叉构建及 `go mod verify` 通过。Windows Clang 最终 `go test -race -p 1 -count=1 ./...` 通过。前一轮并行 race 的配置包单次失败，单包和整仓串行复测通过。LLVM 有非致命的 LNK4217 链接警告。
- Linux 候选镜像 `sakura-bot:go-proof-tggtq85n` 使用未改动的 Dockerfile 最终阶段构建。在无外网、无端口映射的容器内验证两个 Go 可执行文件、版本、现有配置保留、全新配置初始化、无 Python/pip，以及 SIGTERM 后退出码 0。完整多阶段源码构建已配置在 CI 中，尚无远程运行结果。
- 用户授权在 `linux-server` 备份并直接验证。完整备份位于 `/root/Sakura-Bot-backups/pre-go-migration-20261118T125105Z`，包括 MySQL 单事务转储、4 个 SQLite 在线备份、运行文件、旧镜像、已跟踪改动和 SHA-256 清单。4 个 SQLite 快照均通过 `PRAGMA quick_check`，压缩归档通过 `gzip -t`，并已用 `sha256sum -c` 复核备份清单。
- 用备份会话在服务器执行只读 Telegram 验证。gotd 成功导入主 Bot 和 UserBot 的 Telethon 会话，验证账号身份、读取 5 条频道历史消息并完成 Guard 成员权限 RPC。SQLite 副本可读取 290 条摘要向量和 17,035 条消息向量。
- 将 MySQL 转储恢复到无外网、无端口映射的独立 MySQL 8.4.10 容器，运行主 Bot、Guard、Wiki 和助手计费的 Go 初始化。比对的 9 张表行数一致，覆盖摘要、用户、配额、对话、转发、图片资产和任务、帖子与评论。验证容器和测试数据已清理。生产容器健康，镜像 ID 与备份时一致。
- 最终验收还包括 GitHub Actions 实际通过和真实 Telegram 写操作链路，例如新入群验证、群管动作、投稿发布、投票和外部 AI 响应。Python 源代码与回归测试仍可用于这些验收和旧运行时回滚。
- 旧 Guard 待办缺少关联案件或授权凭据时保留为待人工复核，具备原始授权记录的旧待办可恢复运行。

## WebUI 性能

同一台 Windows 主机，旧分支版本使用 CPython 3.14.7、FastAPI 和 Uvicorn，迁移版使用 Go 1.27.3 的 `--web-only`。两者使用相同的 Vue 产物和隔离配置，各启动三次。每轮预热 10 次，再通过同一连接串行访问 `/api/health` 100 次。以下为三轮中位数，延迟取各轮 p50 的中位数。

| 指标 | Python | Go |
| --- | ---: | ---: |
| 进程启动到健康检查可用 | 7,985.54 ms | 196.68 ms |
| 健康检查延迟 | 1.7803 ms | 0.2253 ms |
| 请求完成后的工作集 | 166.96 MiB | 29.36 MiB |
| 请求完成后的 Windows 私有内存 | 877.48 MiB | 58.72 MiB |

测量范围为本机 WebUI 服务，使用静态资源和健康检查接口。
