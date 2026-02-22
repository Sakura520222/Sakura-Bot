# Ruff 一键运行脚本使用指南

## 概述

`run_ruff.py` 是一个便捷的 Python 脚本，用于自动运行 Ruff 代码检查和格式化工具。它会自动检测并激活项目的 venv 环境，并将所有运行日志保存到 `logs/` 目录。

## 功能特性

- ✅ **自动检测 venv**：自动查找并使用项目 venv 中的 ruff
- 📝 **日志记录**：每次运行都会生成带时间戳的日志文件
- 🔧 **多种模式**：支持检查、修复、格式化和完整模式
- 🎯 **灵活指定**：可以检查特定的文件或目录
- 🖥️ **跨平台**：支持 Windows、Linux 和 macOS

## 使用方法

### 基本用法

```bash
# 只检查代码问题（不修改文件）
python run_ruff.py

# 自动修复可修复的问题
python run_ruff.py --fix

# 格式化代码
python run_ruff.py --format

# 完整模式：检查 → 修复 → 格式化
python run_ruff.py --all
```

### 检查特定路径

```bash
# 检查特定目录
python run_ruff.py --check core/

# 检查特定文件
python run_ruff.py --check main.py

# 检查多个路径
python run_ruff.py --check core/ main.py
```

### 查看帮助

```bash
python run_ruff.py --help
```

## 运行模式说明

### 1. 检查模式（默认）
- 只检查代码问题，不修改任何文件
- 适合在提交代码前使用
- 命令：`python run_ruff.py`

### 2. 修复模式
- 自动修复 Ruff 可以修复的问题
- 不会修改格式问题
- 命令：`python run_ruff.py --fix`

### 3. 格式化模式
- 根据 `pyproject.toml` 中的配置格式化代码
- 统一代码风格
- 命令：`python run_ruff.py --format`

### 4. 完整模式
- 依次执行：检查 → 修复 → 格式化
- 适合一键完成所有代码优化
- 命令：`python run_ruff.py --all`

## 日志文件

所有运行日志都保存在 `logs/` 目录下，文件名格式：

```
logs/
├── ruff_check_20240222_143025.log
├── ruff_fix_20240222_143530.log
├── ruff_format_20240222_143645.log
└── ...
```

日志文件内容包括：
- 运行时间戳
- 使用的模式
- Ruff 的输出结果
- 错误信息（如果有）

## venv 检测

脚本会按以下顺序查找 venv：

1. `./venv/`
2. `./.venv/`
3. `./env/`

如果找不到 venv，会使用系统的 Python。

## 配置

Ruff 的配置在项目根目录的 `pyproject.toml` 文件中：

```toml
[tool.ruff]
target-version = "py311"
line-length = 100

[tool.ruff.lint]
select = ["E", "F", "W", "I", "UP", "ASYNC"]
extend-select = ["B"]

[tool.ruff.format]
quote-style = "double"
indent-style = "space"
```

如需修改 Ruff 的行为，请编辑此配置文件。

## 工作流程建议

### 日常开发

```bash
# 1. 开发完成后，检查代码
python run_ruff.py

# 2. 如果有问题，自动修复
python run_ruff.py --fix

# 3. 手动修复无法自动处理的问题

# 4. 格式化代码
python run_ruff.py --format
```

### 快速一键处理

```bash
# 一键完成所有操作
python run_ruff.py --all
```

### 提交前检查

```bash
# 检查特定目录或文件
python run_ruff.py --check core/ main.py
```

## 常见问题

### Q: 脚本提示找不到 ruff？
A: 确保已激活 venv 并安装了依赖：
```bash
pip install -r requirements.txt
```

### Q: 日志文件太多怎么办？
A: 可以定期清理 `logs/` 目录，或添加到 `.gitignore`（已添加）。

### Q: 如何忽略某些文件或目录？
A: 在 `pyproject.toml` 中配置 `exclude` 或 `per-file-ignores`。

### Q: 脚本修改了代码但我不满意？
A: 可以使用 Git 撤销更改：
```bash
git checkout -- .
```

## 输出状态码

- `0`：运行成功，没有发现问题
- `1`：发现问题或运行出错

可以结合 CI/CD 使用：
```bash
python run_ruff.py && echo "代码检查通过" || echo "代码检查失败"
```

## Pre-commit 钩子（自动检查）

### 什么是 Pre-commit？

Pre-commit 是一个在 Git 提交前自动运行代码检查的工具，可以防止有问题的代码进入版本库。

### 安装和配置

```bash
# 1. 安装 pre-commit
pip install pre-commit

# 2. 激活钩子
pre-commit install
```

### 使用方式

**正常提交（自动检查）：**
```bash
git add .
git commit -m "your message"
# Pre-commit 会自动运行 Ruff 检查和格式化
```

**跳过检查（紧急情况）：**
```bash
git commit --no-verify -m "your message"
```

### Pre-commit 钩子配置

项目的 pre-commit 配置在 `.pre-commit-config.yaml` 中：

```yaml
repos:
  - repo: https://github.com/astral-sh/ruff-pre-commit
    rev: v0.1.0
    hooks:
      - id: ruff          # 代码检查
      - id: ruff-format   # 代码格式化
```

### 手动运行所有钩子

```bash
# 在所有文件上运行
pre-commit run --all-files

# 在特定文件上运行
pre-commit run --files main.py
```

### 更新钩子版本

```bash
# 更新到最新版本
pre-commit autoupdate

# 清除缓存
pre-commit clean
```

## CI/CD 集成

项目已配置 GitHub Actions CI，会在每次 push 和 PR 时自动运行：

- ✅ Ruff 代码检查
- ✅ Ruff 格式化检查
- ✅ 安全漏洞扫描
- ✅ 单元测试

**CI 配置文件**：`.github/workflows/ci.yml`

## 三层防护体系

```
1️⃣ Pre-commit (本地提交前)
   ↓
2️⃣ run_ruff.py (本地手动运行)
   ↓
3️⃣ GitHub Actions CI (远程自动运行)
```

### 推荐工作流

```bash
# 1. 开发代码
# ... 编写代码 ...

# 2. 提交前（Pre-commit 自动运行）
git add .
git commit -m "feat: 添加新功能"
# 如果有问题，自动修复后重新提交

# 3. 推送到远程（触发 CI）
git push

# 4. CI 自动运行检查
# 查看 GitHub Actions 的运行结果
```

## 注意事项

1. **备份重要代码**：在使用 `--fix` 或 `--format` 前，建议提交或备份代码
2. **查看日志**：如果遇到问题，查看 `logs/` 目录中的日志文件
3. **版本控制**：日志目录已添加到 `.gitignore`，不会被提交到 Git
4. **团队协作**：建议在提交 PR 前运行 `python run_ruff.py`
5. **Pre-commit**：首次安装后需要激活，之后每次提交都会自动运行

## 相关文档

- [Ruff 官方文档](https://docs.astral.sh/ruff/)
- [Pre-commit 官方文档](https://pre-commit.com/)
- [项目配置说明](../pyproject.toml)
- [贡献指南](../CONTRIBUTING.md)
- [CI 工作流配置](../.github/workflows/ci.yml)
