# 从 Release 附件安装和更新

从 [GitHub Releases](https://github.com/Sakura520222/Sakura-Bot/releases) 下载自制附件 `Sakura-Bot-vX.Y.Z.zip` 或 `Sakura-Bot-vX.Y.Z.tar.gz`，并下载同一版本的 `SHA256SUMS`。在下载目录执行 `sha256sum --ignore-missing -c SHA256SUMS`，确认已下载的附件校验通过，再解压到独立目录。GitHub 自动生成的 **Source code** 压缩包使用 GitHub 自己的打包流程。

自制附件包含项目源码、依赖清单和配置模板。Docker 构建会生成主 Bot、助手 Bot 和 WebUI，匹配提交的官方镜像也提供这三项产物。附件安装支持下文的两种 Docker Compose 路径。

## 配置

需要 Docker Engine、Docker Compose v2、MySQL，以及快速开始中列出的 [Telegram 和 AI 凭证](../wiki/User-Getting-Started.md)。预构建官方镜像支持 Linux amd64，其他平台可以选择本地构建，并使用项目支持的目标架构。

在解压出的目录中复制 `data/.env.example` 到 `data/.env`，按快速开始填写配置。使用助手时，填写 `QA_BOT_TOKEN`，并设置 `QA_BOT_ENABLED=true`。需要公开 Wiki 时，按 [Wiki 手册](sakura-wiki.md)配置。

## 使用匹配的官方镜像

`RELEASE_REVISION` 记录了附件对应的 Git 提交。使用该提交的镜像标签启动，避免部署到滚动更新的 `:main` 镜像。

```bash
export SAKURA_BOT_IMAGE="ghcr.io/sakura520222/sakura-bot:sha-$(cat RELEASE_REVISION)"
docker compose pull
docker compose up -d --no-build
```

如需固定镜像内容，可将 `SAKURA_BOT_IMAGE` 设置为拉取时显示的 `ghcr.io/sakura520222/sakura-bot@sha256:…`，并确认该镜像的 `org.opencontainers.image.revision` 标签与 `RELEASE_REVISION` 一致。若 GHCR 要求身份验证，执行 `docker login ghcr.io` 后重试。

## 从附件构建镜像

```bash
export SAKURA_BOT_IMAGE="sakura-bot:local-$(cat RELEASE_REVISION)"
docker compose build
docker compose up -d --no-build
```

服务启动后可打开 WebUI 的 `/login` 和已启用的 `/wiki`。已配置的助手可通过管理命令 `/qa_status` 或 WebUI 系统页确认运行状态。首次授权 UserBot 请参阅[部署指南](go-deployment.md)。

## 更新到新附件

1. 下载并校验新版本，解压到新目录。
2. 备份现有 `data/` 和 MySQL 数据库，使用旧目录原有的镜像设置执行 `docker compose down`。
3. 将旧目录的 `data/` 完整复制到新目录，并按需保留 `logs/`，保留 `data/.env`、配置、提示词、会话和其他持久化数据。根目录的 Compose 环境变量（例如 `WEBUI_PORT`）也需保留，并更新镜像引用。
4. 在新目录按上述任一镜像路径操作，使用新附件的 `RELEASE_REVISION`。

附件无需 Git。请使用上述换包方式更新。`/update`、`scripts/deploy.sh` 和 `deploy/install-deployer.sh` 用于 Git 克隆部署。
