# GitHub Actions 工作流改进说明

## 改进概述

对 `.github/workflows/create-tag.yml` 进行了重要改进，解决了Release附件打包和更新的问题。

## 改进内容

### 1. 新增手动触发选项

工作流现在支持通过 `workflow_dispatch` 手动触发，并提供以下配置选项：

#### 参数说明

- **force_update** (默认: `false`)
  - 是否强制更新已存在的Release附件
  - 当设置为 `true` 时，即使Release已存在也会更新附件文件
  - 适用于修复发布包或重新打包的场景

- **include_tests** (默认: `true`)
  - 是否包含 `tests/` 测试目录
  - 可根据需要选择是否打包测试代码

- **include_wiki** (默认: `true`)
  - 是否包含 `wiki/` 文档目录
  - 可根据需要选择是否打包Wiki文档

### 2. 改进的版本检查逻辑

```yaml
- 检查Release是否存在
- 如果存在且force_update=true → 执行更新流程（SHOULD_UPDATE=true）
- 如果存在且force_update=false → 跳过创建（SHOULD_UPDATE=false）
- 如果不存在 → 正常创建新Release（SHOULD_CREATE=true）
```

### 3. 增强的打包功能

#### 新增文件包含

打包过程现在包含以下额外文件：

**配置文件：**
- `pyproject.toml` - 项目配置
- `pytest.ini` - pytest配置
- `.gitignore` - Git忽略规则
- `CONTRIBUTING.md` - 贡献指南

**可选目录：**
- `tests/` - 测试代码（可选）
- `wiki/` - 项目文档（可选）

#### 打包清单

每次打包会生成 `packaging_manifest.txt` 清单文件，包含：
- 打包配置信息
- 所有包含的文件列表
- 压缩包文件大小

示例输出：
```
📋 打包配置:
- 版本: 1.5.9
- 包含测试: true
- 包含Wiki: true

📦 包含文件:
- main.py
- qa_bot.py
- core/ (所有Python文件)
- pyproject.toml
- pytest.ini
- .gitignore
- tests/
- wiki/
...

🗜️ 正在创建压缩包...
✅ 资源包创建完成
- Sakura-Bot-v1.5.9.tar.gz: 2.5M
- Sakura-Bot-v1.5.9.zip: 2.6M
```

### 4. 新增Release更新流程

当 `force_update=true` 时，工作流会执行以下步骤：

1. **生成发布信息** - 获取版本号和标签
2. **创建发布资源包** - 重新打包所有文件
3. **上传发布资源** - 使用 `--clobber` 参数覆盖已有附件
4. **发送通知** - 通过Telegram通知管理员（仅更新附件）
5. **生成摘要** - 在Actions页面显示更新摘要

### 5. 改进的通知机制

- **新建Release**：发送完整的新版本发布通知
- **更新Release**：发送附件更新通知，明确说明仅更新了附件

## 使用场景

### 场景1：正常发布新版本

```bash
# 自动触发（push到main分支）
git push origin main
```

工作流会：
- 检查版本号（从main.py读取）
- 如果Release不存在 → 创建新Release并打包
- 如果Release已存在 → 跳过创建

### 场景2：更新已存在的Release附件

```bash
# 手动触发工作流
1. 访问 GitHub → Actions → "自动创建 Release"
2. 点击 "Run workflow"
3. 选择分支：main
4. 设置参数：
   - force_update: true
   - include_tests: true (可选)
   - include_wiki: true (可选)
5. 点击 "Run workflow"
```

工作流会：
- 检查版本号
- 即使Release已存在也继续执行
- 重新打包所有文件
- 上传并覆盖已有附件
- 发送更新通知

### 场景3：自定义打包内容

```bash
# 手动触发，排除tests和wiki
1. 访问 GitHub → Actions → "自动创建 Release"
2. 设置参数：
   - force_update: false (或true)
   - include_tests: false
   - include_wiki: false
3. 运行工作流
```

## 技术细节

### 版本号获取

工作流从 `main.py` 中读取版本号：

```python
__version__ = "1.5.9"
```

### 打包流程

```bash
1. 创建发布目录：Sakura-Bot-v{VERSION}
2. 复制所有必要文件
3. 设置可执行权限（.sh脚本）
4. 创建tar.gz和zip压缩包
5. 计算文件大小
6. 生成打包清单
```

### 上传策略

使用 `gh release upload` 命令的 `--clobber` 参数：

```bash
gh release upload "$RELEASE_NAME" "*.tar.gz" "*.zip" --clobber
```

`--clobber` 参数会覆盖已有的同名文件，实现更新功能。

## 注意事项

1. **版本号一致性**：确保 `main.py` 中的 `__version__` 与预期的版本号一致
2. **权限要求**：工作流需要 `contents: write` 权限来创建/更新Release
3. **通知配置**：Telegram通知需要配置 `TELEGRAM_BOT_TOKEN` 和 `TELEGRAM_ADMIN_CHANNEL_ID`
4. **文件大小**：包含tests和wiki会增加发布包大小，请根据需要选择

## 未来改进方向

- [ ] 添加签名验证功能
- [ ] 支持多种压缩格式（如7z）
- [ ] 添加自动版本号递增功能
- [ ] 支持预发布版本（prerelease）
- [ ] 添加发布包校验和（SHA256）

## 相关文档

- [GitHub Actions文档](https://docs.github.com/en/actions)
- [gh release上传文档](https://cli.github.com/manual/gh_release_upload)
- [项目主README](../README.md)

## 更新日志

- **2026-02-20**: 初始版本
  - 添加force_update参数
  - 添加include_tests和include_wiki参数
  - 改进打包内容完整性
  - 添加打包清单功能
  - 实现Release附件更新机制