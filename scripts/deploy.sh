#!/usr/bin/env bash
# 一键部署：源码快进更新 + 拉取依赖镜像并重启。
# 镜像由 GitHub Actions 构建推送 GHCR，提供 Python 依赖、Go 二进制与前端产物；
# 业务代码仍从挂载的源码加载——日常代码更新只走 git pull，依赖层不变则秒级完成。
# 用法：bash scripts/deploy.sh
# 首次部署前：克隆本仓库、配置 data/.env；若镜像拉取被拒见 README「部署」一节。
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> 更新源码（快进合并，保留本地配置）"
git pull --ff-only || echo "!! 源码有本地分叉，请手动处理后再部署（已跳过 git pull）"

echo "==> 拉取最新依赖镜像"
docker compose pull

echo "==> 重启服务"
docker compose up -d --remove-orphans

echo "==> 清理悬空镜像"
docker image prune -f >/dev/null

docker compose ps
echo "==> 部署完成：$(date '+%F %T')"
