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

- **main**: 主分支，稳定版本（生产环境）- **PR 的目标分支**
- **dev**: 开发分支，从 main 自动同步
- **feature/***: 新功能分支（从 main 创建）
- **fix/***: 问题修复分支（从 main 创建）
- **hotfix/***: 紧急修复分支（从 main 创建）
- **docs/***: 文档更新分支（从 main 创建）
- **test/***: 测试相关分支（从 main 创建）
- **refactor/***: 代码重构分支（从 main 创建）
- **chore/***: 构建/工具更新分支（从 main 创建）

> **重要说明**：所有 PR 应该提交到 `main` 分支。main 分支的更改会自动同步到 dev 分支。

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
   # 代码风格检查
   ruff check .
   
   # 导入排序检查（仅供参考）
   isort --check-only --diff .
   
   # 运行测试
   pytest tests/ -v
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

6. 自动同步到 dev
   - PR 合并到 main 后，会自动触发 sync-branches 工作流
   - main 分支的更改会自动同步到 dev 分支

> **工作流说明**：
> - 所有 PR 应该提交到 `main` 分支
> - main 分支的更改会自动同步到 dev 分支
> - dev 分支是只读的，由自动化工作流维护

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

4. 自动同步到 dev
   - 合并后自动触发同步工作流

---

## 🔍 CI/CD 检查说明

所有 Pull Request 都会自动运行以下 CI 检查，请确保您的代码通过所有检查。

### 检查项目

#### 1. 代码质量检查 ✅ 必须通过

- **Ruff 快速检查** - 主要代码风格检查
  - 检查代码错误（E）、格式问题（F）、警告（W）、导入排序（I）
  - 忽略行长度限制（E501）
  
- **导入排序检查** - 仅供参考，不影响 CI 通过
  - 使用 `isort` 检查导入语句排序
  
- **代码复杂度检查** - 仅供参考，不影响 CI 通过
  - 使用 `pylint` 检查代码复杂度
  - 目标分数：7.0+

#### 2. 安全漏洞扫描 ⚠️ 警告

- **Safety 扫描** - 检查依赖包已知漏洞
- **Pip-audit 审计** - 审计依赖包安全性

> 注意：安全扫描失败会显示警告，但不会阻止合并。请在合并前修复安全问题。

#### 3. Docker 构建测试 ✅ 必须通过

- 验证 Dockerfile 是否正确
- 测试镜像构建是否成功
- 确保容器能够正常启动

#### 4. 单元测试 ✅ 必须通过

- 运行 `tests/` 目录下的所有测试
- 生成代码覆盖率报告
- 目标：保持较高的测试覆盖率

### 本地预检查

在提交 PR 前，您可以在本地运行相同的检查：

```bash
# 1. 代码风格检查（必须通过）
pip install ruff
ruff check . --select=E,F,W,I --ignore=E501

# 2. 自动修复可修复的问题
ruff check . --fix

# 3. 导入排序（可选）
pip install isort
isort .

# 4. 运行测试
pip install pytest pytest-cov pytest-asyncio
pytest tests/ -v --cov=core

# 5. Docker 构建测试
docker build -t sakura-bot:test .
docker run --rm sakura-bot:test python --version
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
   | Ruff 检查失败 | 代码风格不符合规范 | 运行 `ruff check . --fix` 自动修复 |
   | Docker 构建失败 | Dockerfile 错误或依赖问题 | 检查依赖和构建配置 |
   | 测试失败 | 代码逻辑错误或测试用例问题 | 本地运行 `pytest -v` 查看详细错误 |
   | 安全漏洞警告 | 依赖包存在已知漏洞 | 更新依赖包到安全版本 |

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
| `dependencies` | 依赖更新 | 升级 Python 依赖版本 |
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
git commit -m "refactor(core): 优化错误处理逻辑"
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

### Python 代码风格

我们遵循 [PEP 8](https://pep8.org/) 规范，使用以下工具：

#### Ruff (主要检查工具)

```bash
# 安装 ruff
pip install ruff

# 检查代码
ruff check .

# 自动修复
ruff check . --fix
```

#### isort (导入排序)

```bash
# 安装 isort
pip install isort

# 检查导入排序
isort --check-only .

# 自动修复
isort .
```

### 代码结构

```
Sakura-Bot/
├── core/              # 核心模块
├── tests/            # 测试文件
├── data/             # 数据文件
├── wiki/             # 文档
├── main.py           # 入口文件
├── qa_bot.py         # QA Bot 入口
└── requirements.txt  # 依赖列表
```

### 命名规范

- **模块/包**: `lowercase_with_underscores`
- **类**: `CapitalizedWords`
- **函数/方法**: `lowercase_with_underscores`
- **常量**: `UPPERCASE_WITH_UNDERSCORES`
- **私有方法**: `_leading_underscore`

### 文档字符串

使用 Google 风格的 docstrings：

```python
def summarize_channel(channel_id: str, messages: List[str]) -> str:
    """生成频道消息汇总
    
    Args:
        channel_id: 频道 ID
        messages: 消息列表
    
    Returns:
        汇总文本
    
    Raises:
        ValueError: 如果频道 ID 无效
    """
    pass
```

---

## 🧪 测试指南

### 运行测试

```bash
# 安装测试依赖
pip install pytest pytest-cov pytest-asyncio

# 运行所有测试
pytest

# 运行特定测试文件
pytest tests/test_module.py

# 生成覆盖率报告
pytest --cov=core --cov-report=html
```

### 编写测试

```python
# tests/test_example.py
import pytest


def test_example():
    """测试示例"""
    result = function_to_test()
    assert result == expected_value


@pytest.mark.asyncio
async def test_async_example():
    """异步测试示例"""
    result = await async_function()
    assert result is not None
```

---

## 🐛 问题报告

### 报告 Bug

请使用 [Bug Report 模板](.github/ISSUE_TEMPLATE/bug_report.md) 并提供：

1. 清晰的描述和复现步骤
2. 预期行为和实际行为
3. 环境信息（Python 版本、操作系统等）
4. 相关日志和错误信息
5. 截图（如果适用）

### 功能请求

请使用 [Feature Request 模板](.github/ISSUE_TEMPLATE/feature_request.md) 并说明：

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

- 📖 查看 [Wiki](../wiki)
- 💬 参与 [Discussions](../../discussions)
- 🐛 查看 [Issues](../../issues)

---

## 📜 许可证

贡献的代码将采用项目的 [AGPL-3.0](LICENSE) 许可证。

---

## 🌈 致谢

感谢所有贡献者！您的贡献让 Sakura-Bot 变得更好。

[贡献者列表](../../graphs/contributors)