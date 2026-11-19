# CI/CD 工作流

## 变更验证

推送至 `main`、`dev`，或向这些分支发起 PR 时，CI 检查 Go 格式、单元测试、MySQL 集成、`go vet`、竞态及依赖漏洞。集成用例连接临时 MySQL，线上配置与生产数据不参与测试。

WebUI 流程运行单元测试、类型检查、构建和 Wiki、助手浏览器回归。Docker 流程从源码构建镜像，检查双 Go 二进制、静态资源与 HTTP 健康，验证两个自制发布附件。Python 迁移回归用来核对旧版行为和数据兼容。

## 发布产物

| 工作流 | 产物和触发范围 |
| --- | --- |
| [Docker 构建](../.github/workflows/docker-build.yml) | `main`、版本标签和授权手工触发构建完整 Linux amd64 镜像并推送 GHCR。镜像提供分支或版本标签及 `sha-...` 标签 |
| [Release](../.github/workflows/release-on-pr-merge.yml) | 合并到 `main` 或授权手工触发，依据版本与提交更新 Release |
| [文档站](../.github/workflows/docs.yml) | 文档 PR 构建预览产物，`main` 构建后发布到 Pages |

官方镜像包含 Go 主 Bot、助手与 WebUI。自制发布附件内含 `RELEASE_REVISION`，可据此选择镜像并核对提交。详见[Release 附件安装与更新](../docs/release-archives.md)。

## 变更检查与正式发布

推送功能分支更新其 PR。以该提交的检查结果确认构建通过。正式站点的更新取决于 Pages 发布步骤，业务容器的更新取决于所选部署流程。

[文档站维护](Developer-Documentation-Site.md) · [部署指南](User-Getting-Started.md)
