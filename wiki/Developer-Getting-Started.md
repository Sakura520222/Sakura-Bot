# 开发快速开始

## 环境

- Go 版本以根目录 `go.mod` 为准。2.6.0 当前要求 Go 1.27.4。
- WebUI 使用 Node.js 22 或以上。VitePress 文档站的构建工作流使用 Node.js 24。
- MySQL 集成测试使用独立实例。Docker 构建与镜像冒烟需要 Docker Engine 和 Compose v2。

在所选 Go 版本源码根目录安装依赖并构建 WebUI。

```bash
go mod download
npm --prefix web ci
npm --prefix web run build
```

## 本地启动

全新环境先从 `data/.env.example` 初始化 `data/.env`，为主 Bot、助手、数据库和 AI 使用开发凭据。Linux/macOS 运行 `bash start.sh`，Windows 运行 `start.bat`。脚本构建 `bin/sakura-main`、`bin/sakura-qa` 与 WebUI，并启动主进程。

仅调试静态 WebUI 和 HTTP 健康检查时，可以使用以下 Go 入口。

```bash
go run ./cmd/sakura-main --web-only
```

该模式不启动 Telegram 业务。正常启动流程见[部署指南](User-Getting-Started.md)。

编辑 Vue 源码时，按需运行 `npm --prefix web run dev`。Vite 的 `/api` 代理配置位于 `web/vite.config.ts`。正常 Go 服务默认从 `web/dist` 加载静态文件，可用 `SAKURA_WEB_DIST` 指定产物路径。

## 代码目录

```text
cmd/sakura-main/    Go 主 Bot 入口
cmd/sakura-qa/      Go 助手入口
internal/mainbot/  启动与业务组件装配
internal/mtproto/  gotd/td 会话、历史、事件与 Rich Messages 适配
internal/webapi/   管理、公开 Wiki、认证和静态资源路由
internal/config/   配置、校验、热重载及保存
internal/llm/      OpenAI 兼容 SSE 客户端
internal/db/       MySQL 仓储
internal/vectors/  SQLite WAL 向量索引
web/               Vue WebUI
wiki/              VitePress 站点
```

按[系统架构](Developer-Architecture.md)查找各业务模块。Python 源文件保留用于旧版行为对照和迁移回归。

## 本地检查

```bash
go test ./...
go vet ./...
go test -race ./...
npm --prefix web test
npm --prefix web run build
```

Go 集成用例通过 `MYSQL_TEST_DSN` 使用独立测试库。未配置时，相关用例跳过。测试会创建并删除测试数据库，确保账号和实例不指向生产数据。运行竞态检查时应具有当前平台支持的 C 工具链及 CGO 环境。

```bash
npm --prefix wiki ci
npm --prefix wiki run docs:build
```

对涉及 UI 的变更补充浏览器验证。CI 会运行 Wiki 与助手的浏览器回归，详情见 [CI/CD 工作流](Developer-Workflow-Improvements.md)。

## 贡献与部署

业务改动优先沿相同领域的正常入口验证。提交前使用 `gofmt` 格式化改动的 Go 文件。说明变更范围、数据兼容行为、测试环境和结果。部署前后的健康检查与迁移窗口见 [2.6.0 升级说明](Release-2.6.0.md)。

[贡献指南](../CONTRIBUTING.md) · [仓库迁移记录](../docs/go-mainbot-execplan.md)
