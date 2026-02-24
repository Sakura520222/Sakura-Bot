# Workflow 合并修复说明

## 问题描述

在 PR 合并后，GitHub Actions 的 Release 创建流程中存在以下问题：

1. **`pr-analyzer.yml`** 负责创建/更新 Release 描述
2. **`create-tag.yml`** 负责打包并上传附件
3. **问题**: `create-tag.yml` 使用 `workflow_run` 触发器监听 `pr-analyzer.yml` 的完成，但由于 GitHub Actions 的限制，由 `pull_request` 事件触发的 workflow 不会触发 `workflow_run` 事件

## 根本原因

```yaml
# 旧配置 - create-tag.yml
on:
  workflow_run:
    workflows: ["PR 自动分析并更新 Release"]
    types: [completed]
    branches: [main]
```

当 `pr-analyzer.yml` 由 `pull_request: [closed]` 触发时，它完成后不会触发 `workflow_run` 事件，导致 `create-tag.yml` 永远不会执行。

## 解决方案

### 合并为单一 Workflow

创建新的 **`release-on-pr-merge.yml`**，将两个 workflow 合并为一个，通过 `needs` 关键字控制执行顺序：

```yaml
jobs:
  analyze-and-update-release:
    # Job 1: 分析 PR 并创建/更新 Release 描述
    # ...
    
  build-and-upload-assets:
    # Job 2: 打包并上传附件
    needs: analyze-and-update-release  # 等待 Job 1 完成
    if: always() && needs.analyze-and-update-release.result == 'success'
```

### 关键优化点

#### 1. 使用合并后的 main 分支代码
```yaml
- name: 检出代码（使用合并后的 main 分支）
  uses: actions/checkout@v4
  with:
    fetch-depth: 0
    ref: main  # 确保使用的是合并后的 main 分支代码
```

#### 2. Tag 校验机制（防止竞态条件）
```yaml
- name: 等待 Release 创建并验证 Tag
  run: |
    # 等待最多 30 秒，确保 Release 创建完成
    MAX_RETRIES=6
    RETRY_COUNT=0
    
    while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
      # 检查 Tag 是否已经生效
      if git ls-remote --tags origin | grep -q "refs/tags/${TAG_NAME}$"; then
        echo "✅ Tag ${TAG_NAME} 已确认存在"
        break
      fi
      
      RETRY_COUNT=$((RETRY_COUNT + 1))
      echo "⏳ 等待 Tag 生效... ($RETRY_COUNT/$MAX_RETRIES)"
      sleep 5
    done
```

#### 3. 智能附件检测
```yaml
- name: 检查 Release 附件状态
  run: |
    # 检查是否有附件
    ASSETS=$(gh release view "$TAG_NAME" --json assets -q '.assets | length')
    
    if [ "$ASSETS" -eq 0 ]; then
      echo "ℹ️ Release 没有附件，将添加附件"
      echo "should_upload=true" >> $GITHUB_OUTPUT
    else
      echo "ℹ️ Release 已有附件，将跳过上传"
      echo "should_upload=false" >> $GITHUB_OUTPUT
    fi
```

## 文件变更

### 新增文件
- `.github/workflows/release-on-pr-merge.yml` - 合并后的 workflow

### 备份文件
- `.github/workflows/pr-analyzer.yml.bak` - 原 PR 分析 workflow 备份
- `.github/workflows/create-tag.yml.bak` - 原附件上传 workflow 备份

### 删除文件
- `.github/workflows/pr-analyzer.yml` - 已备份
- `.github/workflows/create-tag.yml` - 已备份

## 执行流程

```
PR 合并到 main
    ↓
[Job 1: analyze-and-update-release]
    ├─ 提取 PR 信息
    ├─ 分析 PR 类型
    ├─ 读取当前版本
    ├─ 检查 Release 是否存在
    ├─ 生成 Release 描述
    ├─ 创建或更新 Release
    ├─ 更新 CHANGELOG.md
    ├─ 发送 PR 评论
    └─ 输出: version, release_exists, pr_title, pr_number
    ↓
[Job 2: build-and-upload-assets] (needs: Job 1)
    ├─ 检出 main 分支代码
    ├─ 验证项目结构
    ├─ 等待并验证 Tag/Release
    ├─ 检查附件状态
    ├─ 创建发布资源包 (.tar.gz, .zip)
    ├─ 上传附件到 Release
    ├─ 发送 Telegram 通知
    └─ 生成发布摘要
```

## 优势

1. **消除竞态条件**: 通过 `needs` 确保 Job 1 完成后才执行 Job 2
2. **共享上下文**: 两个 job 在同一 workflow 中，可以轻松共享变量
3. **更好的日志**: 所有操作在一个 workflow run 中，便于调试
4. **原子性**: 要么全部成功，要么全部失败
5. **智能重试**: Tag 校验机制确保 Release 创建完成后再上传附件

## 测试验证

下次 PR 合并时，新的 workflow 将自动执行并完成以下任务：

1. ✅ 创建/更新 Release 描述
2. ✅ 打包项目文件
3. ✅ 上传附件（.tar.gz 和 .zip）
4. ✅ 发送 PR 评论通知
5. ✅ 发送 Telegram 通知
6. ✅ 生成 GitHub Actions 摘要

## 回滚方案

如果需要回滚到旧的分离方案：

```bash
cd e:\项目\Sakura-Bot
mv .github\workflows\pr-analyzer.yml.bak .github\workflows\pr-analyzer.yml
mv .github\workflows\create-tag.yml.bak .github\workflows\create-tag.yml
rm .github\workflows\release-on-pr-merge.yml
```

## 注意事项

1. **权限要求**: 确保 `GITHUB_TOKEN` 或 `MY_RELEASE_PAT` 有 `contents: write` 权限
2. **Tag 同步**: Job 2 会等待最多 30 秒以确保 Tag 生效
3. **附件去重**: 如果 Release 已有附件，Job 2 会跳过上传
4. **手动触发**: 保留了 `workflow_dispatch` 触发器，可手动执行

## 相关资源

- [GitHub Actions Workflow Run 事件限制](https://docs.github.com/en/actions/using-workflows/events-that-trigger-workflows#workflow_run)
- [GitHub Actions Job 依赖](https://docs.github.com/en/actions/using-jobs/using-jobs-in-a-workflow)
- [GitHub Release API](https://docs.github.com/en/rest/releases/releases)