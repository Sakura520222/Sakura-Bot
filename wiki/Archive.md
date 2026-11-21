# 历史文档

这里保留旧版本的发布说明和实现记录。部署、开发与排障采用 [2.6.0 升级说明](Release-2.6.0.md)和[开发者文档](Developer-Guide.md)里的现行步骤。

## 旧版 Python 源码

自 2.6.0 起主运行时与助手均为 Go 实现，仓库不再保留 Python 源码。旧版 Python 实现完整归档在仓库的 `legacy/python` 分支（基于 v2.5.2，即 Python 版最后发布），包含全部历史提交，仅作历史对照与回滚参考，不再接受修复或功能开发。

## 旧版本发布说明

- [v2.4.8 关联讨论群与匿名管理员](Release-2.4.8.md)
- [v2.4.2 回合与积分](Release-2.4.2.md)

## 迁移前开发记录

- [Python 主入口模块拆分](Developer-Module-Split-Summary.md)
- [代码重构总结](Developer-Refactoring-Summary.md)
- [错误处理增强](Developer-Error-Handling-Improvements.md)
- [早期问答 Bot 实施](Developer-QA-Bot-Implementation.md)
- [查询改写增强](Developer-Query-Rewrite-Enhancement.md)
- [运行时占用评估](Developer-Runtime-Footprint-Optimization.md)
- [技术债务记录](Developer-Technical-Debt.md)
- [Ruff 脚本指南](Developer-Ruff-Script-Guide.md)
- [早期 CI 测试修复](Developer-CI-Test-Fix.md)
- [工作流合并修复](Developer-Workflow-Merge-Fix.md)
