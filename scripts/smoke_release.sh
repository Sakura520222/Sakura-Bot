#!/usr/bin/env bash
# Verify each attachment in its own directory using fixture credentials only.
set -euo pipefail

root="${1:?Path to extracted attachments}"
image="${2:?Locally available image reference}"
revision="${3:?Expected Git revision}"

test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "$image")" = "$revision"

mysql_id=$(docker run --detach --rm --publish 127.0.0.1::3306 \
    -e MYSQL_ALLOW_EMPTY_PASSWORD=yes -e MYSQL_ROOT_HOST=% -e MYSQL_DATABASE=release_smoke \
    mysql:8.4.10)
trap 'docker rm -f "$mysql_id" >/dev/null' EXIT
mysql_address=$(docker port "$mysql_id" 3306/tcp)
mysql_port="${mysql_address##*:}"
for attempt in {1..60}; do
    if docker exec "$mysql_id" mysql --protocol=tcp -h 127.0.0.1 -u root release_smoke -Nse 'SELECT 1'; then
        break
    fi
    if [[ "$attempt" == 60 ]]; then
        docker logs "$mysql_id"
        exit 1
    fi
    sleep 2
done

for format in zip tar; do
    (
        cd "$root/$format"/Sakura-Bot-v*
        test "$(cat RELEASE_REVISION)" = "$revision"
        test ! -e .git
        test ! -e web/node_modules
        test ! -e web/dist
        cat > data/.env <<'EOF'
TELEGRAM_API_ID=123456
TELEGRAM_API_HASH=release_smoke
TELEGRAM_BOT_TOKEN=123456:release_smoke
LLM_API_KEY=release_smoke
QA_BOT_TOKEN=123457:release_smoke
QA_BOT_RUNTIME=go
EOF
        local_image="sakura-bot:release-smoke-$format"
        SAKURA_BOT_IMAGE="$local_image" docker compose build
        docker run --rm --network none --env-file data/.env "$local_image" \
            python -m scripts.release_archive_smoke

        test ! -e web/dist
        test "$(SAKURA_BOT_IMAGE="$image" docker compose config --images)" = "$image"
        docker run --rm --network none --env-file data/.env \
            --mount "type=bind,source=$PWD,target=/app" \
            --mount "type=bind,source=$PWD/data,target=/app/data" \
            "$image" bash -ec '
                python core/docker_runtime.py prepare
                test ! -e web/node_modules
                python -m scripts.release_archive_smoke
            '

        cd qa-bot-go
        MYSQL_TEST_DSN="root@tcp(127.0.0.1:$mysql_port)/release_smoke" \
            go test -count=1 -v ./internal/engine ./internal/handlers \
            -run '^(TestAnonymousDiscussionHistoryPersistsAcrossTurnsAndRestart|TestTelegramDiscussionFactsReachModelWithoutRewritingQuery|TestDiscussionScopeIgnoresOrdinaryReplyTarget|TestAIWorkerPreservesCommentWakeContext)$'
    )
done
