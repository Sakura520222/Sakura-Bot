# 🤝 贡献指南

感谢您对 Sakura-Bot 项目的关注！我们欢迎任何形式的贡献。

---

## 📋 目录

- [行为准则](#行为准则)
- [如何贡献](#如何贡献)
- [开发流程](#开发流程)
- [CI/CD 检查说明](#cicd-检查说明)
- [PR 标签规范](#pr-标签规范)
- [提交规范](#提交规范)
- [代码规范](#代码规范)
- [测试指南](#测试指南)
- [问题报告](#问题报告)
- [功能请求](#功能请求)

---

## 🌟 行为准则

### 我们的承诺

为了营造开放和包容的环境，我们承诺让每个人都能参与到项目中来，无论其经验水平、性别、性别认同和表达、性取向、残疾、个人外貌、体型、种族、民族、年龄、宗教或国籍如何。

### 我们的标准

积极行为包括：

- 使用友好和包容的语言
- 尊重不同的观点和经验
- 优雅地接受建设性批评
- 关注对社区最有利的事情
- 对其他社区成员表示同理心

不可接受的行为包括：

- 使用性化的语言或图像
- 恶意攻击或侮辱性评论
- 骚扰
- 未经许可发布他人的私人信息
- 其他不道德或不专业的行为

---

## 🚀 如何贡献

### 报告问题

如果您发现了 bug 或有功能建议，请：

1. 检查 [现有的 issues](../../issues) 确保问题未被报告
2. 使用合适的 [Issue 模板](.github/ISSUE_TEMPLATE/) 创建新 issue
3. 提供尽可能详细的信息

### 提交代码

1. **Fork 项目**
   ```bash
   # 在 GitHub 上点击 Fork 按钮
   ```

2. **克隆到本地**
   ```bash
   git clone https://github.com/YOUR_USERNAME/Sakura-Bot.git
   cd Sakura-Bot
   ```

3. **创建分支**
   ```bash
   git checkout -b feature/your-feature-name
   # 或
   git checkout -b fix/your-bug-fix
   ```

4. **进行更改**
   - 遵循[代码规范](#代码规范)
   - 添加必要的测试
   - 更新相关文档

5. **提交更改**
   ```bash
   git add .
   git commit -m "feat: 添加新功能描述"
   ```

6. **推送到您的 Fork**
   ```bash
   git push origin feature/your-feature-name
   ```

7. **创建 Pull Request**
   - 在 GitHub 上创建 PR
   - 使用 [PR 模板](.github/PULL_REQUEST_TEMPLATE.md)
   - 等待代码审查

---

## 🔄 开发流程

### 分支策略

- **main**: 唯一的长期分支（生产环境）- **PR 的目标分支**
- **feature/***: 新功能分支（从 main 创建）
- **fix/***: 问题修复分支（从 main 创建）
- **hotfix/***: 紧急修复分支（从 main 创建）
- **docs/***: 文档更新分支（从 main 创建）
- **test/***: 测试相关分支（从 main 创建）
- **refactor/***: 代码重构分支（从 main 创建）
- **chore/***: 构建/工具更新分支（从 main 创建）
- **legacy/python**: 已归档的 Python 旧版（最后版本 v2.5.2），只读保留，不接受日常开发

> **重要说明**：所有 PR 提交到 `main` 分支。项目不再维护 `dev` 分支。

### 工作流程

#### 标准开发流程

1. 从 `main` 分支创建功能分支
   ```bash
   git checkout main
   git pull origin main
   git checkout -b feature/your-feature-name
   ```

2. 在功能分支上开发
   - 遵循[代码规范](#代码规范)
   - 编写有意义的提交信息
   - 添加必要的测试

3. 本地预检查（推荐）
   ```bash
   # Go 格式与静态检查
   gofmt -l cmd internal
   go vet ./...

   # 运行测试（无 MySQL 时集成用例自动跳过）
   go test ./...

   # WebUI（如涉及前端）
   npm --prefix web run build
   ```

4. 提交到您的 Fork
   ```bash
   git add .
   git commit -m "feat(scope): 描述您的更改"
   git push origin feature/your-feature-name
   ```

5. 创建 Pull Request
   - 在 GitHub 上创建 PR 到 `main` 分支
   - 使用 [PR 模板](.github/PULL_REQUEST_TEMPLATE.md)
   - 添加合适的 PR 标签（见[PR 标签规范](#pr-标签规范)）
   - 等待 CI 检查通过
   - 通过代码审查后合并到 main

> **发布说明**：合并 PR 只触发 CI 与 `main` 开发镜像构建，不会创建正式 Release。正式版本由维护者推送与 `VERSION` 一致的 `v*` 标签发布，发布与附件不可变。

#### 紧急修复流程

对于生产环境的紧急问题，流程与标准开发流程相同：

1. 从 `main` 创建 `hotfix` 分支
   ```bash
   git checkout main
   git pull origin main
   git checkout -b hotfix/urgent-fix
   ```

2. 快速修复并测试

3. 创建 PR 到 `main` 分支
   - 使用更紧急的 PR 标签（如 `bug`、`critical`）
   - 通过快速审查后合并

---

## 🔍 CI/CD 检查说明

所有 Pull Request 都会自动运行以下 CI 检查，请确保您的代码通过所有检查。

### 检查项目

#### 1. Go 检查 ✅ 必须通过

- **gofmt** - `cmd/` 与 `internal/` 必须已格式化
- **go test** - 单元测试与 MySQL 集成测试（CI 提供临时 MySQL）
- **go vet** - 静态检查
- **go test -race** - 并发竞争检测

#### 2. 依赖漏洞扫描 ✅ 必须通过

- **govulncheck** - 检查 Go 依赖的已知漏洞

#### 3. WebUI 验证 ✅ 必须通过

- 单元测试、`vue-tsc` 类型检查与生产构建
- Wiki 与助手的 Playwright 浏览器回归

#### 4. Docker 构建测试 ✅ 必须通过

- Compose 端口绑定校验（`internal/composeconfig`）
- 完整镜像构建、镜像冒烟（双 Go 二进制、WebUI、健康检查）
- 两个发布附件（ZIP/tar.gz）的打包、校验与安装冒烟

### 本地预检查

在提交 PR 前，您可以在本地运行相同的检查：

```bash
# 1. 格式与静态检查（必须通过）
gofmt -l cmd internal
go vet ./...

# 2. 运行测试
go test ./...

# 3. 带独立 MySQL 的集成测试（可选）
MYSQL_TEST_DSN='root@tcp(127.0.0.1:3306)/sakura_go_test' go test ./...

# 4. WebUI（如涉及前端）
npm --prefix web ci
npm --prefix web run build
```

### CI 失败处理

如果您的 PR 未通过 CI 检查：

1. **查看失败原因**
   - 在 PR 页面点击 "Details" 查看具体错误
   - 查看 GitHub Actions 日志

2. **修复问题**
   - 根据错误信息修复代码
   - 运行本地预检查确认修复

3. **更新 PR**
   ```bash
   # 修复后提交新更改
   git add .
   git commit -m "fix: 修复 CI 检查失败的问题"
   git push origin feature/your-feature-name
   ```
   CI 会自动重新运行

4. **常见问题**

   | 问题 | 原因 | 解决方法 |
   |------|------|----------|
   | gofmt 检查失败 | 文件未格式化 | 运行 `gofmt -w <file>` |
   | go vet 失败 | 静态检查发现问题 | 按 vet 提示修复代码 |
   | 测试失败 | 代码逻辑错误或测试用例问题 | 本地运行 `go test ./... -run TestXxx -v` 查看详细错误 |
   | 集成测试失败 | MySQL 用例断言不一致 | 用 `MYSQL_TEST_DSN` 指向独立测试库复现 |
   | Docker 构建失败 | Dockerfile 错误或依赖问题 | 检查依赖和构建配置 |
   | 依赖漏洞扫描失败 | Go 依赖存在已知漏洞 | 运行 `govulncheck ./...` 并升级依赖 |

---

## PR 标签规范

创建 Pull Request 时，请添加适当的标签以帮助分类和审查。

### 类型标签

| 标签 | 用途 | 示例 |
|------|------|------|
| `enhancement` | 新功能 | 添加新的 AI 模型支持 |
| `bug` | 问题修复 | 修复总结生成错误 |
| `documentation` | 文档更新 | 更新 README 说明 |
| `refactor` | 代码重构 | 重构错误处理模块 |
| `performance` | 性能优化 | 优化数据库查询性能 |
| `dependencies` | 依赖更新 | 升级 Go/npm 依赖版本 |
| `testing` | 测试相关 | 添加单元测试 |
| `ci` | CI 配置 | 更新 GitHub Actions |

### 其他标签

| 标签 | 用途 |
|------|------|
| `good first issue` | 适合新手的问题 |
| `help wanted` | 需要帮助 |
| `wontfix` | 不予修复 |
| `duplicate` | 重复问题 |
| `question` | 疑问 |

### 如何添加标签

1. 在 PR 页面右侧找到 "Labels"
2. 点击 "Edit" 选择合适的标签
3. 保存更改

> 提示：PR 标签不是强制的，但添加标签可以帮助维护者更快地处理您的 PR。

---

## 📝 提交规范

我们使用 [Conventional Commits](https://www.conventionalcommits.org/) 规范。

### 格式

```
<type>(<scope>): <subject>

<body>

<footer>
```

### Type 类型

- `feat`: 新功能
- `fix`: 问题修复
- `docs`: 文档更新
- `style`: 代码格式（不影响功能）
- `refactor`: 代码重构
- `perf`: 性能优化
- `test`: 测试相关
- `chore`: 构建/工具更新
- `build`: 构建系统
- `ci`: CI 配置
- `revert`: 回退提交

### 示例

```bash
# 新功能
git commit -m "feat(ai): 添加新的 AI 模型支持"

# 问题修复
git commit -m "fix(summary): 修复汇总消息格式错误"

# 文档更新
git commit -m "docs(readme): 更新安装说明"

# 代码重构
git commit -m "refactor(botcmd): 优化错误处理逻辑"
```

### 详细的 Body

```bash
git commit -m "feat(qa): 添加问答历史记录功能

- 实现问答历史持久化存储
- 添加历史查询命令
- 支持历史记录导出

Closes #123"
```

---

## 代码规范

### Go 代码风格

- 使用 `gofmt` 格式化全部 Go 源码（`gofmt -w cmd internal`）
- 新文件保留简短的 AGPL-3.0 版权头（参见现有源文件）
- 注释、日志与文档约定使用中文
- 用户可见字符串使用 `internal/i18n` 的文案键（中英双语同时添加，zh-CN 为回退语言）

```bash
# 检查格式
gofmt -l cmd internal

# 格式化
gofmt -w cmd internal

# 静态检查
go vet ./...
```

### 代码结构

```
Sakura-Bot/
├── cmd/              # 程序入口：sakura-main、sakura-qa、release-archive
├── internal/         # 业务领域与共享基础设施
├── web/              # Vue 3 WebUI
├── wiki/             # VitePress 文档站源文件
├── data/             # 运行配置、提示词与会话（含 .env.example 模板）
├── deploy/           # 宿主机部署器 systemd 单元
├── scripts/          # 部署与冒烟脚本
└── docs/             # 面向部署与发布的专项文档
```

### 命名规范

- **包名**: 简短小写单词
- **导出标识符**: `CapitalizedWords`
- **未导出标识符**: `lowerCamelCase`
- **常量**: `CapitalizedWords` 或 `lowerCamelCase`（遵循 Go 惯例）

### WebUI 代码风格

- TypeScript + Vue 3 组合式 API，类型检查通过 `npm --prefix web run build`（`vue-tsc`）验证
- 前端 API 封装位于 `web/src/api/`，与后端 `internal/webapi` 的路由/模式保持同步

---

## 🧪 测试指南

### 运行测试

```bash
# 运行所有测试（MySQL 集成用例在未设置 MYSQL_TEST_DSN 时自动跳过）
go test ./...

# 运行特定包
go test ./internal/config -v

# 运行特定测试
go test ./internal/engine -run TestAnonymousDiscussionHistoryPersistsAcrossTurnsAndRestart -v

# 带独立 MySQL 的集成测试（测试库会被创建和删除，勿指向生产数据）
MYSQL_TEST_DSN='root@tcp(127.0.0.1:3306)/sakura_go_test' go test ./...

# 并发竞争检测
go test -race ./...

# WebUI 单元测试与构建
npm --prefix web test
npm --prefix web run build
```

### 编写测试

```go
func TestSummarizeChannel(t *testing.T) {
    // 常规用例使用标准库 testing；依赖外部服务的用例在环境缺失时 t.Skip。
    got := SummarizeChannel(...)
    if got != want {
        t.Errorf("SummarizeChannel() = %q, want %q", got, want)
    }
}
```

涉及 MySQL 的用例通过 `MYSQL_TEST_DSN` 获取连接，并在测试内创建/清理独立数据库；参见 `internal/db` 现有测试的组织方式。

---

## 🐛 问题报告

### 报告 Bug

请使用 [Bug Report 模板](.github/ISSUE_TEMPLATE/bug_report.yml) 并提供：

1. 清晰的描述和复现步骤
2. 预期行为和实际行为
3. 环境信息（程序版本、操作系统、部署方式等）
4. 相关日志和错误信息
5. 截图（如果适用）

### 功能请求

请使用 [Feature Request 模板](.github/ISSUE_TEMPLATE/feature_request.yml) 并说明：

1. 功能描述和使用场景
2. 详细的实现建议
3. 可能的替代方案
4. 优先级

---

## 📚 文档贡献

文档同样重要！您可以：

1. 修正错别字和错误
2. 改进现有文档的清晰度
3. 添加使用示例
4. 翻译文档

文档站源文件在 `wiki/` 目录，本地验证：

```bash
npm --prefix wiki ci
npm --prefix wiki run docs:build
```

构建会检查内部链接；新增页面需要同步更新 `wiki/.vitepress/config.mts` 的侧边栏。

---

## 🎯 Pull Request 检查清单

提交 PR 前，请确认：

- [ ] 代码符合项目规范
- [ ] 包含必要的测试
- [ ] 所有测试通过
- [ ] 更新了相关文档
- [ ] 遵循提交信息规范
- [ ] PR 描述清晰完整
- [ ] 添加了适当的标签

---

## 💬 获取帮助

- 📖 查看[文档站](https://sakura-bot.pyabunny.com/)
- 💬 参与 [Discussions](../../discussions)
- 🐛 查看 [Issues](../../issues)

---

## 📜 许可证

贡献的代码将采用项目的 [AGPL-3.0](LICENSE) 许可证。

---

## 🌈 致谢

感谢所有贡献者！您的贡献让 Sakura-Bot 变得更好。

[贡献者列表](../../graphs/contributors)
