# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project and Runtime

Sakura-Bot is an AGPL-3.0 Telegram channel-management application: AI summaries, polls, forwarding, RAG Q&A, subscriptions, submissions, and a web administration UI.

- Use **Python 3.13+** as documented in the READMEs and used by Docker/CI. Ruff targets `py311`; that is not the documented runtime requirement.
- Python dependencies, including test/lint tools, are in `requirements.txt`. The frontend uses Vue 3, TypeScript, Vite, Naive UI, and npm; Docker builds it with Node 20.
- Run backend commands from the repository root: runtime paths such as `data/config.json` are relative to the working directory.
- For local setup, copy `data/.env.example` to `data/.env` and configure Telegram credentials, an OpenAI-compatible LLM provider, and MySQL. Configure or disable optional QA Bot/UserBot integrations as appropriate.
- **MySQL is the only application database backend.** Compose does not start MySQL; provision it separately. ChromaDB is a separate persistent vector store, not a replacement for MySQL. Do not infer SQLite application support from old tests or CI environment values.

## Development Commands

Use the project's virtual environment. From the repository root:

```bash
python -m venv .venv
# Activate: source .venv/bin/activate (POSIX)
# Windows Git Bash: source .venv/Scripts/activate
python -m pip install -r requirements.txt

python main.py                    # Main bot; also manages optional QA subprocess and WebUI
python qa_bot.py                  # QA bot alone; do not also run a managed copy with the same token
```

### Lint and formatting

```bash
python -m ruff check .
python -m ruff format --check .

# Intentional source modifications
python -m ruff check . --fix
python -m ruff format .

pre-commit install
pre-commit run --all-files
```

`python run_ruff.py` finds a virtual environment and runs **check → fix → format**, writing logs under `logs/`. `python run_ruff.py --check core/` checks without fixing, but still writes logs. Use direct Ruff commands when exit status matters: the wrapper does not propagate checker failures. Pre-commit hooks can also modify files.

### Tests

```bash
python -m pytest tests/                                        # Full suite
python -m pytest tests/ -m "not slow and not integration"       # CI's filtered selection
python -m pytest tests/ -m integration                         # Integration selection
python -m pytest tests/core/config/test_event_bus.py            # Single file
python -m pytest tests/core/config/test_event_bus.py::test_event_bus_subscribe_and_publish
python -m pytest tests/test_ai_client.py -k test_analyze_with_messages
python -m pytest tests/core/config/test_event_bus.py --no-cov   # Focused run without coverage
```

`pytest.ini` already enables verbosity, short tracebacks, strict markers, asyncio auto mode, and coverage of `core/` (terminal, `htmlcov/`, `coverage.xml`). Registered feature markers are `unit`, `integration`, `slow`, `telegram`, and `database`. The filtered selection above is **not unit-only**: unmarked tests and other markers remain eligible.

Shared fixtures live in `tests/conftest.py`: temporary files, mocked services, session environment setup/cleanup, and per-test singleton resets. Environment fixtures run during fixture setup, **not before test-module collection imports**. Import-time initialization in settings/config/AI modules may therefore need mocking or test configuration before import; `data/.env` can override process variables. CI supplies MySQL-compatible test values at job level before collection, propagates pytest failures, and generates summaries from saved coverage data without rerunning tests. Some older tests still target legacy imports or synchronous AI APIs, so check them against the current implementation rather than treating them as API documentation.

### WebUI

```bash
npm --prefix web ci
npm --prefix web run dev          # Vite on port 3000; /api proxies to localhost:8080
npm --prefix web run build        # vue-tsc -b, then Vite; outputs web/dist/
npm --prefix web run preview      # Preview built frontend, not the integrated backend
```

The backend normally starts through `main.py` with WebUI enabled. Its default port is 8080; adjust the Vite proxy if that changes. There are no frontend lint/test npm scripts. `npm run build` is the available frontend type-check/build validation.

### Docker

```bash
docker compose up -d --build
docker compose logs -f
```

The image builds the frontend and bundles it with the Python backend. Compose loads `data/.env`, mounts `data/`, and publishes `${WEBUI_PORT:-8080}:8080`. Its healthcheck only tests that Python exits successfully, not bot/database health.

## Architecture and Boundaries

### Processes and lifecycle

- **Main process:** `main.py` handles settings validation, config watching/event-bus wiring, shutdown signals, and QA subprocess management. It delegates service startup to `core/bootstrap/app_bootstrap.py::AppBootstrap`, which coordinates the feature initializers in `core/initializers/`. Startup orders database initialization/migrations before Telegram clients, scheduling, handlers, forwarding, realtime RAG, and WebUI. Preserve that ordering when adding lifecycle-dependent services.
- **Main Bot and optional UserBot:** both use Telethon in the main process. UserBot is a real-account client for access the bot account lacks; forwarding and realtime ingestion select between these clients. It is not another subprocess.
- **QA Bot:** `qa_bot.py` uses python-telegram-bot with its own event loop. `core/system/process_manager.py` starts/stops it as a subprocess. Requests and notifications cross the process boundary through MySQL queues, handled by `mainbot_request_handler.py` and `mainbot_push_handler.py`; they do not share Python singletons. Scheduling polls requests, and the QA application polls pending notifications.
- **WebUI backend:** FastAPI/Uvicorn runs as an asyncio task in the main process through `WebAPIInitializer`. It shares the active bot/database/config services; launching the app factory alone does not initialize those services.

### Feature flows

- Telegram command handlers live under `core/commands/`, but their event registration is centralized in `core/initializers/command_registrar.py`. Menu metadata is separate in `core/bot_commands.py`. Adding a command can require both changes. The WebUI command catalog consumes that metadata, but execution is separately mapped in `core/web_api/routes/commands.py`.
- Summary flows fetch channel messages since the previous summary, exclude previously generated report/poll messages, invoke the AI client, persist summaries in MySQL and vectors in ChromaDB, send reports/polls, and enqueue subscriber notifications. Manual/request-driven orchestration is in `core/commands/summary_commands.py`; scheduled work is wired through the scheduler. Check both paths when changing summary behavior.
- `core/ai/qa_engine_v3.py::QAEngineV3` owns Q&A retrieval and conversation handling. Its streaming path first attempts agentic tool-calling; on failure it falls back to semantic/keyword retrieval, reciprocal-rank fusion, optional reranking, and answer generation. ChromaDB has separate summary and raw-message collections. Embeddings/reranking use SiliconFlow APIs; generation uses OpenAI-compatible clients.
- Realtime RAG listeners enqueue whitelisted new/edited Telegram messages for batched embedding and ChromaDB writes via `core/handlers/realtime_rag_handler.py`; deletion events remove message vectors. This is distinct from summary-vector persistence.
- The frontend calls typed API wrappers in `web/src/api/modules.ts` through the Axios client in `web/src/api/client.ts` (`/api` base URL and Bearer-token handling). Backend routes/schemas are in `core/web_api/`. `create_app()` serves `web/dist/` with SPA fallback when the build exists. Frontend route guards are in `web/src/router/index.ts`; backend authentication is enforced separately by middleware.

### Configuration and persistence

There are two configuration layers, not one interchangeable settings object:

1. `core/settings.py` builds Pydantic settings from environment values and loads **`data/.env` with `override=True`**. `get_settings()` is a lazy process-local singleton.
2. `data/config.json` holds runtime-editable settings. Legacy `core/config.py` implements loading/saving and runtime globals; the `core/config/` package loads it as `core._old_config` and re-exports its API. Both the file and package are intentional—do not remove one as a duplicate.

`FileWatcher → ConfigManager.reload_config() → validation/backup or rollback → ConfigChangedEvent → priority-ordered subscribers` is the hot-reload path. Subscribers update runtime globals, scheduler jobs, AI clients, and feature configuration. WebUI writes go through `core/web_api/deps.py::write_config()`, which also schedules an explicit reload using the injected main-process ConfigManager.

Mutable values such as `CHANNELS` are dynamically forwarded by `core.config.__getattr__`. Use `import core.config as config_module` and access `config_module.CHANNELS` at use time where hot reload matters; `from core.config import CHANNELS` holds a stale binding after replacement. Environment settings, runtime JSON globals, and subprocess state should not be assumed to update together.

`core/infrastructure/database/manager.py::get_db_manager()` creates the MySQL manager but does **not** initialize its asynchronous pool; callers initialize it in their own event loop. Main-process migrations are coordinated by `DatabaseInitializer`. Settings, database, vector-store, and handler factories commonly use process-local singletons.

## Repository Conventions

- Python: 4-space indentation, double quotes, formatter target of 100 columns. Ruff ignores `E501`, so 100 columns is not a hard lint limit. Imports are organized by Ruff/isort settings.
- Preserve the AGPL-3.0 copyright header when adding Python source. Comments, logs, and Google-style docstrings are conventionally Chinese. Match nearby handler naming: older handlers use `handle_`, while other feature commands use `cmd_`.
- Use `TYPE_CHECKING` guards for type-only imports where necessary to avoid the project's circular-import boundaries.
- User-facing strings must use `core/i18n/` lookups (`t()` or `get_text()`). Add keys to **both** `MESSAGE_ZH_CN` and `MESSAGE_EN_US` in `core/i18n/i18n.py`.
- Use Conventional Commits, e.g. `fix(forwarding): resolve media group deduplication race condition`. Branch from and target PRs to `main`; `dev` is automation-maintained/read-only per `CONTRIBUTING.md`.
