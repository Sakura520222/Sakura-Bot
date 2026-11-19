#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"

if [[ ! -f data/.env ]]; then
    cp data/.env.example data/.env
    printf '请填写 data/.env 中的 Telegram、MySQL 和 AI 配置，然后重新运行脚本。\n' >&2
    exit 1
fi

npm --prefix web ci
npm --prefix web run build
mkdir -p bin
version=$(tr -d '\r\n' < VERSION)
go build -trimpath -ldflags="-X github.com/Sakura520222/Sakura-Bot/internal/version.Version=$version" -o bin/ ./cmd/sakura-main ./cmd/sakura-qa
exec ./bin/sakura-main "$@"
