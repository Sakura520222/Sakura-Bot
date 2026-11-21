# Pull Request

> PR 标题必须符合 Conventional Commits，例如：`feat(ai): add summary pipeline`。

## 变更概述

请用 1-3 句话说明这个 PR 做了什么、为什么需要这些变更。

## 变更类型

- [ ] ✨ feat：新功能
- [ ] 🐛 fix：问题修复
- [ ] 📚 docs：文档更新
- [ ] ♻️ refactor：代码重构
- [ ] ⚡ perf：性能优化
- [ ] 🧪 test：测试相关
- [ ] 🔧 chore/build/ci：工程维护
- [ ] 🚑 hotfix：生产紧急修复
- [ ] 🚀 release：发布准备

## 影响范围

- [ ] Main Bot
- [ ] QA Bot
- [ ] UserBot
- [ ] AI / RAG
- [ ] Forwarding
- [ ] Config
- [ ] Database / Migration
- [ ] Web API / WebUI
- [ ] i18n
- [ ] Docker / Deploy

## 主要变更

- 
- 
- 

## 相关 Issues

```text
Closes #
Relates to #
```

## 测试情况

- [ ] 已运行 `go test ./...`
- [ ] 已运行 `go vet ./...`，`gofmt -l cmd internal` 无输出
- [ ] 涉及前端时已运行 `npm --prefix web run build`
- [ ] 已补充或更新测试
- [ ] 涉及数据库迁移
- [ ] 涉及配置结构变更
- [ ] 涉及用户可见文案，已同步中英文 i18n

## 风险与回滚

说明兼容性风险、迁移风险、配置变更、回滚方式；如无风险请填写“无”。

## 自检清单

- [ ] 分支从 `main` 创建并指向 `main`
- [ ] 没有提交真实 Token、Session、日志、数据库、缓存或本地配置
- [ ] 新增 Go 文件包含 AGPL-3.0 版权头
- [ ] 注释与日志使用中文
- [ ] 用户可见字符串已使用 i18n
- [ ] 文档已更新，或确认无需更新

## 额外说明

如有需要 Review 特别关注的点，请在此说明。
