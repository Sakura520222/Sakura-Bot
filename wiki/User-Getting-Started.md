# 快速开始

Sakura-Bot 2.6.0 使用 `sakura-main` 运行主 Bot 和 WebUI，使用 `sakura-qa` 运行独立的问答助手。以下步骤在所选 Go 版本的源码或发行附件目录内执行。

## 前置要求

- Docker Engine 与 Compose v2，或者 Go 1.27.4 及以上和 Node.js 22 及以上。
- 一个独立 MySQL 数据库，以及能够初始化、读取和升级本数据库应用表的账号。默认 Compose 从宿主机或其他机器连接 MySQL。
- 从 BotFather 获取主 Bot Token，在 Telegram 开发者平台获取 `TELEGRAM_API_ID` 和 `TELEGRAM_API_HASH`。启用小妲助手时另建一个 Bot 并保存其独立 Token。
- 可从运行环境访问的 OpenAI 兼容服务。Embedding 和 Reranker 可按需在配置检索时加入。

## 准备配置

全新安装时复制环境变量模板。升级时直接保留现有配置。

```bash
cp data/.env.example data/.env
```

编辑 `data/.env`，填写以下配置。

| 环境变量 | 用途 |
| --- | --- |
| `TELEGRAM_API_ID`、`TELEGRAM_API_HASH`、`TELEGRAM_BOT_TOKEN` | 主 Bot 授权和 MTProto 连接 |
| `REPORT_ADMIN_IDS` | 管理员的 Telegram 数字用户 ID，多个 ID 用逗号分隔 |
| `MYSQL_HOST`、`MYSQL_PORT`、`MYSQL_USER`、`MYSQL_PASSWORD`、`MYSQL_DATABASE` | 可从运行环境连接的 MySQL |
| `LLM_BASE_URL`、`LLM_API_KEY`、`LLM_MODEL` | 默认模型。可在 WebUI [按用途覆盖](AI_PROFILES.md) |
| `QA_BOT_ENABLED`、`QA_BOT_TOKEN` | 助手 Bot 开关和独立 Token |
| `USERBOT_ENABLED`、`USERBOT_SESSION_PATH` | 启用用户账号读取历史及实时更新，默认会话位于 `data/sessions/` |
| `WEBUI_ENABLED`、`WEBUI_DEV_MODE` | WebUI 开关和开发登录模式。正式服务使用 `WEBUI_DEV_MODE=false` |

Compose 中的 `localhost` 指容器自身。若 MySQL 与应用分开运行，填写容器可访问的地址。宿主机端口映射来自仓库根目录的 Compose 环境或 shell 环境。外部端口由 `WEBUI_PORT` 控制，容器端口由 `WEBUI_CONTAINER_PORT` 控制，默认都是 8080。应用的 `data/.env` 与 Compose 根目录的 `.env` 具有不同用途。

## Docker 部署

### 使用官方镜像

Git 克隆目录中可运行部署脚本，脚本快进更新源码并拉取匹配提交的镜像。用户指定 `SAKURA_BOT_IMAGE` 时，使用该镜像。

```bash
bash scripts/deploy.sh
docker compose logs -f sakura-summary-bot
```

从 GitHub Release 下载的自制 ZIP 或 tar.gz 使用附件内的 `RELEASE_REVISION` 匹配镜像。校验同版本的 `SHA256SUMS` 后，在附件目录运行以下命令。

```bash
export SAKURA_BOT_IMAGE="ghcr.io/sakura520222/sakura-bot:sha-$(cat RELEASE_REVISION)"
docker compose pull sakura-summary-bot
docker compose up -d --no-build sakura-summary-bot
```

[Release 附件安装与换包更新](../docs/release-archives.md)包含镜像鉴权、校验和更新步骤。

### 从源码构建镜像

```bash
export SAKURA_BOT_IMAGE=sakura-bot:local
docker compose build sakura-summary-bot
docker compose up -d --no-build sakura-summary-bot
```

### UserBot 首次登录

启用 UserBot 且没有有效会话时，用安装时相同的镜像执行以下命令。按终端提示输入手机号、验证码及可能需要的两步验证密码。

```bash
docker compose stop sakura-summary-bot
docker compose run --rm --no-deps sakura-summary-bot sakura-main --login-userbot
docker compose up -d --no-build sakura-summary-bot
```

旧版 Telethon SQLite 会话可以保留原路径，gotd/td 在同一目录生成 Go 会话文件。升级与回滚前都应保留原会话。

## 本地安装

在仓库根目录填写 `data/.env`。Linux/macOS 执行 `bash start.sh`，Windows 执行 `start.bat`。脚本构建 WebUI、主 Bot 和助手，然后启动主进程。构建产物位于 `bin/`。需登录 UserBot 时运行 `bin/sakura-main --login-userbot`，Windows 使用 `bin\sakura-main.exe --login-userbot`。

## 验证部署

1. 执行 `docker compose ps`，检查 `sakura-summary-bot`。用 `docker compose logs -f sakura-summary-bot` 查看启动与 MySQL、Telegram 连接日志。
2. 启用 WebUI 时，打开外部端口的 `/api/health` 和 `/login`，然后以[管理员身份登录](User-WebUI-Guide.md#登录与权限)。
3. 在 WebUI 添加测试频道。给需要向频道投递的机器人授予对应的发消息、发投票或置顶权限。检查 UserBot 是否能读取频道历史。
4. 在主 Bot 私聊发送 `/start`，选择语言并进入菜单。用 WebUI 或菜单生成测试总结，核对产出和任务结果。
5. 启用了助手时，在系统页核对运行状态，再与助手私聊验证问答。

## 更新、备份与回滚

- Git 克隆部署可在宿主机运行 `bash scripts/deploy.sh`。装有 systemd 的宿主机可用 `bash deploy/install-deployer.sh` 启用部署器，由主 Bot 的更新操作发起请求。
- 发行附件通过解压新附件并迁移数据目录更新。按新附件的 `RELEASE_REVISION` 切换镜像。
- 更新前保留镜像、Compose 配置、完整 MySQL 备份和数据目录。备份 SQLite 向量库时，在停写后归档数据目录，或使用 SQLite 的在线备份功能。
- 回滚时先停当前服务，再还原旧镜像及 Compose 配置。升级期间产生的新记录应按兼容性评估处理。

[2.6.0 升级说明](Release-2.6.0.md) · [Go 部署细节](../docs/go-deployment.md) · [RAG 与旧向量迁移](User-RAG-Quickstart.md)
