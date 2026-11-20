# Sakura-Bot Go 版部署

## 准备配置

Docker 部署需要 Docker Engine 和 Compose v2。MySQL 由宿主机或外部服务提供。将 `data/.env.example` 复制到 `data/.env` 并填写以下信息。

- 主 Bot 的 `TELEGRAM_BOT_TOKEN`、`TELEGRAM_API_ID`、`TELEGRAM_API_HASH` 和管理员 ID `REPORT_ADMIN_IDS`。
- MySQL 的 `MYSQL_HOST`、`MYSQL_PORT`、`MYSQL_USER`、`MYSQL_PASSWORD`、`MYSQL_DATABASE`。使用可从容器访问的地址。
- OpenAI 兼容服务的 API Key、URL 和模型。助手需要独立的 Telegram Token `QA_BOT_TOKEN` 和 `QA_BOT_ENABLED=true`。

复用已有环境时，保留 `data/.env`、`data/config.json`、提示词、自定义数据路径和原数据库配置。容器将 `data/` 和 `logs/` 挂载到固定路径。从宿主机环境或仓库根目录的 `.env` 设置 Compose 的 `WEBUI_PORT` 来修改外部端口。容器端口默认 8080。如果 `data/.env` 中已有自定义的 WebUI 端口，将 Compose 的 `WEBUI_CONTAINER_PORT` 设置为相同值。

### WebUI 访问与端口绑定

Compose 默认只将 WebUI 发布到宿主机 `127.0.0.1:8080`。以下变量放在仓库根目录的 `.env` 或宿主机环境中；`data/.env` 用于容器应用配置，不参与 Compose 的端口插值。

```dotenv
WEBUI_BIND_ADDRESS=127.0.0.1
WEBUI_PORT=8080
WEBUI_CONTAINER_PORT=8080
```

`WEBUI_BIND_ADDRESS` 未设置或为空时均使用 `127.0.0.1`。需要局域网访问时可显式设置为宿主机的局域网 IP；`0.0.0.0` 会监听所有 IPv4 接口，使用前应确认访问控制。IPv6 回环地址可写为 `[::1]`。

- Nginx、Caddy 或 Cloudflare Tunnel 在宿主机运行时，上游使用 `http://127.0.0.1:8080`，修改外部端口后同步调整上游。
- 代理或 cloudflared 在容器中运行时，将其加入同一 Docker 网络，上游使用 `http://sakura-summary-bot:8080`（自定义时使用 `WEBUI_CONTAINER_PORT`）；容器中的 `127.0.0.1` 指向该容器自身。

容器内的 `WEBUI_HOST` 保持 `0.0.0.0`，以便 Docker 转发和同网络代理访问。健康检查仍从容器内访问 `127.0.0.1:$WEBUI_PORT/api/health`，这里的端口由 `WEBUI_CONTAINER_PORT` 设置。启用 WebUI 仍需在 `data/.env` 中设置 `WEBUI_ENABLED=true`。

修改绑定地址或端口后，先执行 `docker compose config` 核对解析出的 `host_ip`、`published` 和 `target`，再执行 `docker compose up -d --no-build` 重建容器；仅执行 `docker compose restart` 不会更新端口映射。用 `docker compose port sakura-summary-bot 8080` 检查发布地址（自定义时替换为容器端口）。Docker 发布端口的流量可能经过 FORWARD 链，不能只依赖 nftables 的 INPUT 规则限制访问。

## 启动镜像

从 Git 克隆目录部署时，执行以下命令。

```bash
bash scripts/deploy.sh
docker compose logs -f sakura-summary-bot
```

脚本以快进方式更新工作区，拉取与 Git 提交匹配的完整镜像并重建容器。明确配置 `SAKURA_BOT_IMAGE` 时，脚本拉取指定镜像。若 GHCR 需要鉴权，在宿主机运行 `docker login ghcr.io`。使用 GitHub 下载附件时按附件内的 `RELEASE_REVISION` 选取镜像。[Release 附件安装和更新](release-archives.md)

在 fork 或修改源码后，可以本地构建。

```bash
export SAKURA_BOT_IMAGE=sakura-bot:local
docker compose build
docker compose up -d --no-build
```

### UserBot 首次授权

已有 Telethon 会话可通过原来的会话路径直接复用。gotd/td 读取原 `.session` 文件并在同目录生成 `.gotd.json`。启用 UserBot 且缺少有效授权时，停止容器后登录并按提示输入手机号、验证码及两步验证密码。

```bash
docker compose stop sakura-summary-bot
docker compose run --rm --no-deps sakura-summary-bot sakura-main --login-userbot
docker compose up -d --no-build
```

执行时沿用安装时的 `SAKURA_BOT_IMAGE`，使授权和启动使用同一镜像。已启动的容器可通过 `docker inspect --format '{{.Config.Image}}' sakura-bot` 查询镜像引用。完成后检查 `data/sessions/`，确认 Go 会话文件存在。

### 更新和检查

`docker compose ps` 显示容器与健康状态。启用 WebUI 时，访问 `/api/health` 检查 HTTP 服务，访问 `/login` 验证页面资源，再以管理员身份核对频道、调度、转发、Guard、Wiki 和助手状态。保留原有 Telegram Token 时，更新前停止旧容器以避免两个进程争抢更新。

使用 systemd 的 Git 克隆部署可通过 `bash deploy/install-deployer.sh` 启用宿主机部署器。Bot 的 `/update` 命令提交部署请求，宿主机执行 `scripts/deploy.sh`，日志写入 `logs/image-deploy.log`。部署结束后，部署器单元把结果写入 `data/.image_deploy_result`，由容器内的 Bot 回报到发起 `/update` 的对话：容器被重建时由新进程的启动钩子回报，版本无变化或部署失败时由原进程回报。也可以在宿主机手动运行更新脚本。

本地源码运行时，`/update check` 查询 Git 上游，`/update` 快进同步代码并重新构建两个程序和 WebUI。构建产物写入 `bin/.updates/`，成功后才切换进程。

## 从旧版升级与回滚

1. 记录当前镜像、Compose 文件和配置，在维护窗口停止写入后备份 MySQL 与完整的 `data/`，包括 SQLite WAL、导出文件和 Telegram 会话。若配置了目录外的路径，也备份对应目录。
2. 在独立测试 MySQL 与数据副本上启动 Go 镜像，核对频道、用户、积分、计费、订阅、向量检索和 Wiki 的存量记录。原 SQLite 向量目录沿用 `VECTOR_DB_PATH`。
3. 在生产迁移窗口停旧服务，使用原 MySQL 和数据目录启动已验证的镜像。验证主 Bot、UserBot 和助手分别授权后，再检查实时事件、总结、转发和 WebUI。
4. 如需回滚，先停 Go 进程。用旧镜像及其原先的 Compose 配置恢复服务。涉及升级后写入的数据时，先评估增量记录和旧版本的兼容性，再决定恢复哪一版备份。

从 v2.6.0 升级或为其应用此补丁时，核对现有代理的上游地址，并保留根目录 `.env` 中的端口设置。仅更新镜像不会改变旧 Compose 的发布地址，必须同时更新 Compose 并重建容器。此变更不修改数据、会话或向量库；回滚到旧 Compose 时也应保留显式的宿主机绑定地址。

### Chroma 向量库转换

旧向量目录中包含 `chroma.sqlite3` 时，在停止所有写入进程并备份整个目录后，执行一次离线转换。目标 `lite_index.sqlite3` 使用相同的 `VECTOR_DB_PATH`。

```bash
docker compose stop sakura-summary-bot
docker compose run --rm --no-deps sakura-summary-bot sakura-main --migrate-vectors
docker compose run --rm --no-deps sakura-summary-bot sakura-main --verify-vectors
docker compose up -d --no-build
```

执行时沿用安装目标版本的 `SAKURA_BOT_IMAGE`。本地源码部署可在仓库根目录对 `bin/sakura-main` 使用相同参数。已使用 `lite_index.sqlite3` 的环境可以沿用原目录。保留原 Chroma 目录和应用数据库备份，直到频道检索和助手问答验证完毕。

## 本地源码运行

安装 Go 1.27.4 或更高版本，以及 Node.js 22 或更高版本。编辑 `data/.env`，在 Linux 或 macOS 执行 `bash start.sh`，在 Windows 执行 `start.bat`。构建后可在仓库根目录直接执行 `bin/sakura-main` 或 `bin\sakura-main.exe`，添加 `--login-userbot` 可首次授权。启用助手后，主进程管理同目录的 `sakura-qa` 子进程。
