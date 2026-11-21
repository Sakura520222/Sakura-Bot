# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project and Runtime

Sakura-Bot is an AGPL-3.0 Telegram channel-management application: AI summaries, polls, forwarding, RAG Q&A, Sakura Guard group moderation, Sakura Wiki, subscriptions, submissions, and a web administration UI. The runtime is **Go-only**; the archived Python implementation lives on the `legacy/python` branch (v2.5.2 was its final release) and is not maintained.

- Go version follows `go.mod` (currently 1.27). WebUI uses Node 22+, Vue 3, TypeScript, Vite, Naive UI. The VitePress docs site (`wiki/`) builds with Node 24 in CI.
- **MySQL is the only application database backend.** Vector search uses a SQLite WAL index (`internal/vectors`); it is not a MySQL replacement. Docker Compose does not start MySQL — provision it separately.
- Run backend commands from the repository root: runtime paths such as `data/` and `logs/` are relative to the working directory.
- Local setup: copy `data/.env.example` to `data/.env` and configure Telegram credentials, an OpenAI-compatible LLM provider, and MySQL.

## Development Commands

```bash
bash start.sh          # POSIX: build both binaries + WebUI, then run the main bot
start.bat              # Windows equivalent
go run ./cmd/sakura-main --web-only   # Static WebUI + /api/health only, no Telegram or MySQL
```

### Checks

```bash
gofmt -l cmd internal            # Must print nothing
go vet ./...
go test ./...                    # MySQL integration cases skip without MYSQL_TEST_DSN
go test -race ./...              # Race suite; set MYSQL_TEST_DSN="" to skip MySQL cases
go test ./internal/engine -run TestXxx -v   # Single package / single test
MYSQL_TEST_DSN='root@tcp(127.0.0.1:3306)/sakura_go_test' go test ./...
```

MySQL integration tests create and drop their own test database; never point `MYSQL_TEST_DSN` at production data. CI additionally runs `govulncheck` and the race suite with `MYSQL_TEST_DSN=""`.

### WebUI and docs site

```bash
npm --prefix web ci
npm --prefix web run dev          # Vite dev server; /api proxies to the Go backend
npm --prefix web test             # Unit tests (Node built-in runner over web/tests/*.test.ts)
npm --prefix web run test:guard   # One suite only
npm --prefix web run build        # vue-tsc type-check + production build
npm --prefix web run test:wiki:browser        # Playwright Wiki regression (needs local build)
npm --prefix web run test:assistant:browser

npm --prefix wiki ci
npm --prefix wiki run docs:build  # VitePress build; fails on broken internal links
```

Unit tests use Node's built-in test runner via `node --experimental-strip-types --test` (no vitest/jest); browser regressions use `playwright-core`.

### Release tooling

```bash
go run ./cmd/release-archive build  --directory <dir> [--extract-to <dir>]  # Pack + verify ZIP/tar.gz + SHA256SUMS
go run ./cmd/release-archive verify --directory <dir> [--extract-to <dir>]
go test ./internal/composeconfig   # Compose port-binding checks (skips without Docker)
bash scripts/smoke_image.sh <image> <version> <data-dir>
bash scripts/smoke_release.sh <extracted-root> <image> <revision>
```

### Docker

```bash
docker compose up -d --build
docker compose logs -f
```

The image bundles both Go binaries and the built WebUI; it contains no Python runtime. `scripts/deploy.sh` fast-forwards the checkout, pulls the `sha-<revision>` image and recreates containers.

## Architecture and Boundaries

### Processes and lifecycle

- **`cmd/sakura-main`** (via `internal/mainbot`) is the main bot: channel management, summaries, polls, forwarding, Guard, Wiki, WebUI, and assistant process supervision. It speaks MTProto through `internal/mtproto` (gotd/td) with separate sessions for the bot and the optional UserBot, and hosts the HTTP API (`internal/webapi`) as an in-process server.
- **`cmd/sakura-qa`** is the assistant bot (Q&A, comment assistant, billing, submissions): Telegram Bot API with its own token, started/stopped as a subprocess by the main bot. Cross-process coordination goes through MySQL, not shared memory.
- **Vue WebUI** (`web/`) is served by the Go backend from `web/dist` (override with `SAKURA_WEB_DIST`). It calls typed API wrappers in `web/src/api/` over `/api`.

### Package map

| Area | Packages |
| --- | --- |
| Startup/wiring | `internal/mainbot` |
| Telegram | `internal/mtproto` (MTProto), `internal/tg` (Bot API) |
| Commands | `internal/botcmd` (main bot), `internal/handlers` (assistant) |
| Persistence | `internal/db` (MySQL), `internal/vectors` (SQLite index) |
| AI | `internal/llm`, `internal/embedder`, `internal/reranker`, `internal/engine`, `internal/rag`, `internal/tools`, `internal/memory` (channel/summary context for assistant tools) |
| Channel business | `internal/summary`, `internal/poll`, `internal/forwarding`, `internal/media` (image pipeline), `internal/submission` (review/publish workflow), `internal/welcome` (comment-area welcome) |
| Assistant users | `internal/qauser` (registration, summary subscriptions, cross-bot requests) |
| Guard / Wiki | `internal/guard`, `internal/wiki` |
| HTTP | `internal/webapi` (admin API, public wiki, auth, SPA) |
| Config | `internal/config` (validation, hot reload, atomic save) |
| Scheduling / process | `internal/scheduler` (cron/interval jobs), `internal/process` (PID files, image-update deployer coordination) |
| Cross-cutting | `internal/i18n`, `internal/logging`, `internal/filelock`, `internal/version`, `internal/contracts` (shared narrow interfaces), `internal/netpolicy` (outbound HTTP/SSRF guard), `internal/links` (t.me post-link builder) |

### Configuration

Environment (`data/.env`, loaded by the process) supplies credentials; `data/config.json` holds runtime-editable settings with file locking, validation, backups and hot reload (`internal/config`). WebUI writes go through the same validated path. Treat both layers as independent: changing one does not update the other.

### Versioning and releases

- The authoritative version is `VERSION`; `internal/version/version.go` embeds the same value and is what binaries report. Bump both together (plus `web/package.json` + `web/package-lock.json` whose `version` fields feed the WebUI footer) and add a `CHANGELOG.md` entry when releasing.
- Releases are **`v*` tag driven** (`.github/workflows/release.yml`): the tag must match `VERSION` and point at the release commit; published releases and assets are immutable. Merging a PR only runs CI and builds the `main` dev image (`docker-build.yml`); `latest` follows stable tags only.

## Repository Conventions

- Go: gofmt formatting, Chinese comments/logs. Preserve the short AGPL-3.0 copyright header on new Go files.
- User-facing strings use `internal/i18n` lookups (`Text`/`TLang`/`T`); keys live in `messages_zh.gen.go` and `messages_en.gen.go` — add to both languages, zh-CN is the fallback.
- Commit messages follow Conventional Commits, e.g. `fix(forwarding): resolve media group deduplication race condition`. Branch from and target PRs to `main`; there is no `dev` branch.
- Docs live in two trees: `wiki/*.md` is the VitePress docs site (deployed to GitHub Pages on merge to `main`; build fails on broken internal links), while `docs/` holds deployment/release-focused guides. Keep user/developer docs in sync with behavior changes.
