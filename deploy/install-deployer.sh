#!/usr/bin/env bash
# 安装宿主机自动部署器（一次性）：/update 触及镜像层变更时自动拉取新镜像并重建容器。
# 用法：bash deploy/install-deployer.sh [仓库目录，默认 /root/Sakura-Bot]
set -euo pipefail

REPO_DIR="${1:-/root/Sakura-Bot}"

if [ ! -d "$REPO_DIR/.git" ]; then
    echo "错误：$REPO_DIR 不是 Sakura-Bot 仓库" >&2
    exit 1
fi
if ! command -v systemctl >/dev/null 2>&1; then
    echo "错误：需要 systemd" >&2
    exit 1
fi

# 以仓库实际路径渲染单元文件（.path 监听该仓库下的标记，.service 在该目录执行 deploy.sh）
mkdir -p /etc/systemd/system
sed "s#/root/Sakura-Bot#$REPO_DIR#g" "$REPO_DIR/deploy/sakura-image-deploy.path" \
    > /etc/systemd/system/sakura-image-deploy.path
sed "s#WorkingDirectory=/root/Sakura-Bot#WorkingDirectory=$REPO_DIR#" \
    "$REPO_DIR/deploy/sakura-image-deploy.service" \
    > /etc/systemd/system/sakura-image-deploy.service

systemctl daemon-reload
systemctl enable --now sakura-image-deploy.path

# 容器内据此标记判断 deployer 已就位（跳过容器内 pip/npm 重建）
mkdir -p "$REPO_DIR/logs"
touch "$REPO_DIR/data/.image_deployer_installed"

systemctl status sakura-image-deploy.path --no-pager | head -5
echo "✅ 自动部署器已安装：/update 检测到镜像层变更后会自动拉取新镜像并重建容器"
