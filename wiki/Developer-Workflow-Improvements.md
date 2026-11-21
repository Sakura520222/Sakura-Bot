# CI/CD 工作流

## 变更验证

推送至 `main`，或向 `main` 发起 PR 时，CI 检查 Go 格式、单元测试、MySQL 集成、`go vet`、竞态及依赖漏洞。集成用例连接临时 MySQL，线上配置与生产数据不参与测试。同一 PR 的新推送会取消仍在排队的历史运行。

WebUI 流程运行单元测试、类型检查、构建和 Wiki、助手浏览器回归。Docker 流程先校验 Compose 端口绑定（`internal/composeconfig`），再从源码构建镜像，检查双 Go 二进制、静态资源与 HTTP 健康，并打包、校验两个发布附件。发布附件工具为仓库内的 Go 程序 `cmd/release-archive`，不依赖其他语言运行时。

## 发布产物

| 工作流 | 产物和触发范围 |
| --- | --- |
| [Docker 构建](../.github/workflows/docker-build.yml) | `main`、`v*` 标签和授权手工触发构建完整 Linux amd64 镜像并推送 GHCR。开发镜像带 `main` 或 `sha-...` 标签；`latest` 与语义化版本标签只随 `v*` 正式标签更新 |
| [Release](../.github/workflows/release.yml) | **`v*` 标签驱动**（保留手工触发兜底）。校验 `VERSION`、标签名与提交 SHA 一致后创建不可变 Release，上传 ZIP、tar.gz、SHA256SUMS，回下载校验并做安装冒烟 |
| [文档站](../.github/workflows/docs.yml) | 文档 PR 构建预览产物，`main` 上的文档变更构建后发布到 Pages |

普通 PR 合并只触发 CI 与 `main` 开发镜像构建，不会创建或更新正式 Release。正式发布流程：

1. 在 PR 中更新 `VERSION`、`internal/version/version.go`（及 WebUI `package.json` 两个 `version` 字段）与 `CHANGELOG.md`，合并到 `main`。
2. 推送与 `VERSION` 一致的标签：`git tag v<版本> && git push origin v<版本>`。
3. Release 工作流校验标签指向的提交、拉取同提交镜像、上传附件并通知。已带附件的 Release 与旧标签不可覆盖；发布失败可重跑工作流补全，但已发布的附件不会被替换。

官方镜像包含 Go 主 Bot、助手与 WebUI。自制发布附件内含 `RELEASE_REVISION`，可据此选择镜像并核对提交。详见[Release 附件安装与更新](../docs/release-archives.md)。

## 变更检查与正式发布

推送功能分支更新其 PR。以该提交的检查结果确认构建通过。正式站点的更新取决于 Pages 发布步骤，业务容器的更新取决于所选部署流程。旧版 Python 运行时不再参与任何 CI 流程；其源码归档在 `legacy/python` 分支。

[文档站维护](Developer-Documentation-Site.md) · [部署指南](User-Getting-Started.md)
