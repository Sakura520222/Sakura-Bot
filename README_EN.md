# 🌸 Sakura-Bot

[![Release](https://img.shields.io/github/v/release/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/releases)
[![License](https://img.shields.io/badge/License-AGPL--3.0-blue?style=flat-square)](LICENSE)
[![Python](https://img.shields.io/badge/python-3.13+-blue?style=flat-square&logo=python)](https://www.python.org/downloads/)
[![Code style: ruff](https://img.shields.io/endpoint?style=flat-square&url=https://raw.githubusercontent.com/astral-sh/ruff/main/assets/badge/v2.json)](https://github.com/astral-sh/ruff)
[![Docker](https://img.shields.io/badge/docker-20.10%2B-blue?style=flat-square&logo=docker)](https://www.docker.com/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/pulls)
[![Stars](https://img.shields.io/github/stars/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/stargazers)

Sakura-Bot is a Telegram channel management tool: AI summaries and polls, message forwarding, RAG question answering, subscriptions, submission review, and a browser-based admin panel, plus the Sakura Guard moderation module for group chats.

[中文文档](README.md) | [Documentation](https://sakura-bot.pyabunny.com/) | [Getting Started](wiki/User-Getting-Started.md) | [Command Reference](wiki/User-Commands.md) | [Wiki](wiki/Home.md) | [Report Issues](https://github.com/Sakura520222/Sakura-Bot/issues)

---

## Features

- **AI summaries**: scheduled per channel (daily, weekly, or multi-day) or on demand; summaries are stored in MySQL and indexed into a vector store
- **RAG Q&A**: agentic RAG with semantic search over both summaries and raw messages, reranking, multi-turn conversations, and post-link citations, with quota control
- **Forwarding**: keyword and regex filtering, footer templates, copy mode, and UserBot fallback when the bot cannot forward
- **Submissions and subscriptions**: users subscribe to summary push notifications, request summaries, or submit posts (anonymously if enabled) through the assistant bot for admin review
- **Polls**: AI-generated interactive polls with per-channel public/anonymous mode and destination, one-click regeneration
- **WebUI**: manage channels, schedules, forwarding rules, AI settings, MySQL data, vector stores, and system status from the browser; JWT authentication and instant hot reload on save
- **Sakura Guard**: opt-in per-group nickname and message rules, contact-card filtering, join verification, `@admin report`, admin review, blacklists/whitelists, and self-service ban appeals
- **Infrastructure**: multi-channel and multi-admin, Chinese/English interface, hot configuration reload with atomic rollback, hot switching between assistant bot runtimes, graceful shutdown

See the [command reference](wiki/User-Commands.md) for the full command list and the [user guide](wiki/User-Guide.md) for details on each feature.

## Quick Start

Requires **Python 3.13+** or **Docker 20.10+**, plus:

- A Telegram Bot Token from [@BotFather](https://t.me/BotFather)
- Telegram API credentials from [my.telegram.org](https://my.telegram.org)
- An OpenAI-compatible API key (e.g., [DeepSeek](https://platform.deepseek.com/), [OpenAI](https://platform.openai.com/))
- A MySQL database (MySQL is the only application database; provision it yourself — Compose does not include one)

Docker:

```bash
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot
cp data/.env.example data/.env   # edit .env with your credentials
docker compose up -d
```

Local:

```bash
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot
python -m venv .venv
source .venv/bin/activate        # Windows: .venv\Scripts\activate
pip install -r requirements.txt
cp data/.env.example data/.env   # edit .env with your credentials
python main.py
```

The first run requires phone-number authentication for Telegram; session files are reused afterwards. For the full configuration reference, enabling the assistant bot / UserBot / WebUI / Sakura Guard, and FAQs, see [Getting Started](wiki/User-Getting-Started.md).

In Docker deployments, sending `/update` to the bot updates the host repository, reinstalls dependencies, rebuilds the WebUI, and restarts. Updates use fast-forward merges and preserve local settings; they stop and ask for input on conflicts. Changes involving system dependencies or the Dockerfile require a host-side `docker compose up -d --build`.

### Assistant Bot Runtime

The assistant bot runs a Python implementation by default, with a lighter Go implementation (`qa-bot-go/`) available. Priority: `config.json` > the `QA_BOT_RUNTIME` environment variable (deployment-level default, see `data/.env.example`) > `python`. Admins can view or switch the runtime with the main-bot `/qa_runtime` command (alias `/qa_运行时`) or with one click on the WebUI "System" page; switching is persisted in `config.json` and restarts the assistant bot immediately (in-progress sessions are interrupted), falling back to Python automatically when the Go binary is missing. See [runtime footprint optimization](wiki/Developer-Runtime-Footprint-Optimization.md).

## Project Structure

```
Sakura-Bot/
├── core/               # core modules
│   ├── ai/             # AI engine (agentic RAG, vector store, embeddings, reranker)
│   ├── bootstrap/      # application bootstrap (initializer orchestration)
│   ├── commands/       # command handlers
│   ├── config/         # config hot reload (event bus, file watcher, atomic rollback)
│   ├── forwarding/     # channel message forwarding
│   ├── handlers/       # event handlers (UserBot, cross-bot, submission review)
│   ├── i18n/           # internationalization (zh-CN, en-US)
│   ├── infrastructure/ # database, logging, exceptions, utilities
│   ├── moderation/     # Sakura Guard moderation
│   ├── services/       # business services
│   ├── system/         # scheduler, error handling, process, shutdown
│   ├── telegram/       # Telegram clients
│   └── web_api/        # WebUI API (FastAPI, JWT auth)
├── data/               # config (.env, config.json), prompts, sessions, vector store
├── web/                # WebUI frontend (Vue 3 + Vite + Naive UI)
├── wiki/               # documentation site sources (VitePress)
├── tests/              # tests
├── main.py             # main entry point
└── qa_bot.py           # assistant bot entry point
```

## Tech Stack

| Technology | Purpose |
| --- | --- |
| Python 3.13+ | main language |
| Telethon / python-telegram-bot | Telegram clients (main bot / assistant bot) |
| OpenAI SDK | LLM calls (any OpenAI-compatible service) |
| FastAPI + Vue 3 | WebUI backend and frontend |
| MySQL (aiomysql) / ChromaDB | application database / vector store |
| APScheduler / watchdog | scheduling / config hot reload |
| Ruff / Docker / GitHub Actions | code quality / deployment / CI |

## Documentation

Documentation site: <https://sakura-bot.pyabunny.com/> (sources in [wiki/](wiki/Home.md), published automatically on push to `main`).

- Users: [Getting Started](wiki/User-Getting-Started.md), [Command Reference](wiki/User-Commands.md), [WebUI Guide](wiki/User-WebUI-Guide.md), [Sakura Guard](wiki/User-Sakura-Guard.md), [Assistant Bot Guide](wiki/User-QA-Bot-Guide.md), [RAG Quickstart](wiki/User-RAG-Quickstart.md)
- Developers: [Developer Getting Started](wiki/Developer-Getting-Started.md), [Architecture](wiki/Developer-Architecture.md), [Documentation Site Maintenance](wiki/Developer-Documentation-Site.md)

## Contributing

Issues and PRs are welcome. Target the `main` branch (`dev` is automation-maintained), follow [Conventional Commits](https://www.conventionalcommits.org/), and use Ruff for code style. See the [contributing guide](CONTRIBUTING.md) and [code of conduct](wiki/Code-of-Conduct.md).

## License

[AGPL-3.0](LICENSE). Modifications and network services must provide source code under the same license; derivatives must retain the origin: https://github.com/Sakura520222/Sakura-Bot . This project is for personal learning only; API costs and compliance are the user's responsibility.

## Acknowledgments

- [Telethon](https://github.com/LonamiWebs/Telethon) - Telegram MTProto client
- [python-telegram-bot](https://github.com/python-telegram-bot/python-telegram-bot) - Telegram Bot API library
- [DeepSeek](https://www.deepseek.com/) / [OpenAI](https://openai.com/) - LLM APIs
- [SiliconFlow](https://siliconflow.cn/) - embedding and reranker APIs
- All [contributors](https://github.com/Sakura520222/Sakura-Bot/graphs/contributors)

## Support

- Issues: [GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)
- Discussions: [GitHub Discussions](https://github.com/Sakura520222/Sakura-Bot/discussions)
- Email: [sakura520222@outlook.com](mailto:sakura520222@outlook.com)
