#!/usr/bin/env bash
# Update the checkout and run the complete image built from that revision.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

if [[ -f RELEASE_REVISION ]]; then
    printf '%s\n' 'Release 源码附件请按 docs/release-archives.md 更新。' >&2
    exit 1
fi

git pull --ff-only

# 已安装过宿主机部署器时，随仓库模板刷新 systemd 单元（幂等），避免单元文件
# 与仓库实现漂移后 /update 的回报协议失效。
if systemctl list-unit-files 2>/dev/null | grep -q '^sakura-image-deploy\.'; then
    if ! bash deploy/install-deployer.sh >/dev/null 2>&1; then
        printf '%s\n' '警告：刷新宿主机部署器单元失败，继续部署。' >&2
    fi
fi

if [[ -z "${SAKURA_BOT_IMAGE:-}" ]]; then
    default_image=ghcr.io/sakura520222/sakura-bot:main
    configured_image=$(docker compose config --images)
    if [[ "$configured_image" == "$default_image" ]]; then
        revision=$(git rev-parse HEAD)
        export SAKURA_BOT_IMAGE="${default_image%:*}:sha-$revision"
        # The image workflow may still be building the new commit.
        for attempt in {1..6}; do
            if docker compose pull; then
                break
            fi
            if [[ "$attempt" == 6 ]]; then
                exit 1
            fi
            sleep 10
        done
        label=$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "$SAKURA_BOT_IMAGE")
        if [[ "$label" != "$revision" ]]; then
            printf '镜像版本 %s 与源码版本 %s 不匹配。\n' "$label" "$revision" >&2
            exit 1
        fi
    else
        docker compose pull
    fi
else
    docker compose pull
fi

docker compose up -d --no-build --remove-orphans
docker compose ps
