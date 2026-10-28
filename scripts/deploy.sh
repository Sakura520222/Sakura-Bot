#!/usr/bin/env bash
# 一键部署：拉取最新镜像并重启（镜像由 GitHub Actions 构建并推送 GHCR）。
# 用法：bash scripts/deploy.sh
# 首次部署前：克隆本仓库、配置 data/.env；若镜像拉取被拒见 README「部署」一节。
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> 拉取最新镜像"
docker compose pull

echo "==> 重启服务"
docker compose up -d --remove-orphans

echo "==> 清理悬空镜像"
docker image prune -f >/dev/null

docker compose ps
echo "==> 部署完成：$(date '+%F %T')"
