#!/usr/bin/env bash
# Run only the local HTTP server with disposable configuration and no external network.
set -euo pipefail

image="${1:?Locally available image reference}"
version="${2:?Expected version}"
data_dir="${3:?Temporary data directory}"

mkdir -p "$data_dir"
printf '%s\n' 'existing prompt' > "$data_dir/prompt.txt"

test "$(docker run --rm --network none "$image" sakura-main --version)" = "$version"
docker run --rm --network none "$image" sh -ec '
    test -x /usr/local/bin/sakura-main
    test -x /usr/local/bin/sakura-qa
    test -s /app/web/dist/index.html
    test -s /app/third-party/opencc/LICENSE
    test -s /app/third-party/opencc/NOTICE
    for executable in python python3 pip pip3; do
        if command -v "$executable"; then
            exit 1
        fi
    done
'

container=$(docker run --detach --network none \
    --mount "type=bind,source=$data_dir,target=/app/data" \
    -e WEBUI_ENABLED=true "$image" sakura-main --web-only)
cleanup() {
    docker rm -f "$container" >/dev/null
}
trap cleanup EXIT

for attempt in {1..30}; do
    if docker exec "$container" curl -fsS http://127.0.0.1:8080/api/health >/dev/null; then
        break
    fi
    if [[ "$attempt" == 30 ]]; then
        docker logs "$container"
        exit 1
    fi
    sleep 1
done

docker exec "$container" curl -fsS http://127.0.0.1:8080/login > "$data_dir/login.html"
grep -q 'id="app"' "$data_dir/login.html"
test "$(cat "$data_dir/prompt.txt")" = 'existing prompt'
test -s "$data_dir/poll_prompt.txt"
test -s "$data_dir/config.json"
docker stop -t 15 "$container" >/dev/null
test "$(docker wait "$container")" = 0
