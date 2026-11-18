#!/usr/bin/env bash
# Verify both release attachment formats with an isolated test database.
set -euo pipefail

root="${1:?Path to extracted attachments}"
image="${2:?Locally available image reference}"
revision="${3:?Expected Git revision}"
smoke_image="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/smoke_image.sh"

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
        cp data/.env.example data/.env

        version=$(tr -d '\r\n' < VERSION)
        local_image="sakura-bot:release-smoke-$format"
        SAKURA_BOT_IMAGE="$local_image" docker compose build
        bash "$smoke_image" "$local_image" "$PWD/.smoke-local"

        test "$(SAKURA_BOT_IMAGE="$image" docker compose config --images)" = "$image"
        bash "$smoke_image" "$image" "$PWD/.smoke-published"

        MYSQL_TEST_DSN="root@tcp(127.0.0.1:$mysql_port)/release_smoke" \
            go test -count=1 -v ./internal/engine ./internal/handlers \
            -run '^(TestAnonymousDiscussionHistoryPersistsAcrossTurnsAndRestart|TestTelegramDiscussionFactsReachModelWithoutRewritingQuery|TestDiscussionScopeIgnoresOrdinaryReplyTarget|TestAIWorkerPreservesCommentWakeContext)$'
    )
done
