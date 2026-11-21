# 🌸 Sakura-Bot

[![Release](https://img.shields.io/github/v/release/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/releases)
[![License](https://img.shields.io/badge/License-AGPL--3.0-blue?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27+-blue?style=flat-square&logo=go)](https://go.dev/dl/)
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
- **AI purpose routing**: Independent OpenAI-compatible profiles for summaries, assistant answers, vision and polls, with masked keys, model discovery/manual IDs and connection/call tests. [Configuration guide](wiki/AI_PROFILES.md).
- **Sakura Guard**: opt-in per-group nickname and message rules, contact-card filtering, join verification, `@admin report`, admin review, blacklists/whitelists, and self-service ban appeals
- **Sakura Wiki**: public channel knowledge at `/wiki` in the existing WebUI, with cited articles, an interactive graph, a timeline, message-based profiles, reviewed publication and shared MySQL snapshots for the Go assistant. Disabled by default. [User and administrator guide](docs/sakura-wiki.md). The repository's `wiki/` remains the project documentation.
- **Infrastructure**: multi-channel and multi-admin, Chinese/English interface, hot configuration reload with atomic rollback, an independent assistant process, graceful shutdown

See the [command reference](wiki/User-Commands.md) for the full command list and the [user guide](wiki/User-Guide.md) for details on each feature.

## Quick Start

For the project's ZIP or tar.gz release attachments, see [installing and updating from a release archive](docs/release-archives.md).

Use Docker Engine with Compose v2, or Go 1.27+ with Node.js 22+. Provision a Telegram bot token, Telegram API credentials, OpenAI-compatible service credentials and a MySQL database. [Configuration and credentials](wiki/User-Getting-Started.md)

```bash
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot
cp data/.env.example data/.env   # Configure Telegram, MySQL and AI
bash scripts/deploy.sh
```

GitHub Actions publishes a complete image with the main bot, assistant bot and WebUI. The deployment script fast-forwards the checkout, pulls the image built for that commit and recreates the container. The `data/` and `logs/` directories remain on the host. [Deployment, upgrade and rollback](docs/go-deployment.md)

For local development, run `bash start.sh` on Linux or macOS or `start.bat` on Windows. The script builds both Go binaries and WebUI, then starts the main bot. If UserBot is enabled and has no authorized session, follow the initial login steps in the deployment guide.

## Project Structure

```text
cmd/sakura-main/   Main bot entry point
cmd/sakura-qa/     Assistant bot entry point
internal/          Domain modules and shared infrastructure
web/               Vue 3, Vite and Naive UI
data/              Configuration, prompts, sessions and vector indexes
wiki/              Project documentation site
```

The main bot uses gotd/td for MTProto and shares the Go Telegram Bot API client with the assistant. MySQL stores application data. The vector index uses SQLite WAL and float32 embeddings. Both processes share the OpenAI-compatible client, embedding, reranking, configuration, logging and media processing packages.

## Documentation

Documentation site: <https://sakura-bot.pyabunny.com/> (sources in [wiki/](wiki/Home.md), published automatically on push to `main`).

- Users: [Getting Started](wiki/User-Getting-Started.md), [Command Reference](wiki/User-Commands.md), [WebUI Guide](wiki/User-WebUI-Guide.md), [Sakura Guard](wiki/User-Sakura-Guard.md), [Assistant Bot Guide](wiki/User-QA-Bot-Guide.md), [RAG Quickstart](wiki/User-RAG-Quickstart.md)
- Developers: [Developer Getting Started](wiki/Developer-Getting-Started.md), [Architecture](wiki/Developer-Architecture.md), [Documentation Site Maintenance](wiki/Developer-Documentation-Site.md)

## Contributing

Issues and PRs are welcome. Target the `main` branch, follow [Conventional Commits](https://www.conventionalcommits.org/), and format Go code with gofmt. See the [contributing guide](CONTRIBUTING.md) and [code of conduct](wiki/Code-of-Conduct.md).

## License

[AGPL-3.0](LICENSE). Modifications and network services must provide source code under the same license; derivatives must retain the origin: https://github.com/Sakura520222/Sakura-Bot . This project is for personal learning only; API costs and compliance are the user's responsibility.

## Acknowledgments

- [gotd/td](https://github.com/gotd/td) - Telegram MTProto client
- [go-telegram/bot](https://github.com/go-telegram/bot) - Telegram Bot API library
- [DeepSeek](https://www.deepseek.com/) / [OpenAI](https://openai.com/) - LLM APIs
- [SiliconFlow](https://siliconflow.cn/) - embedding and reranker APIs
- All [contributors](https://github.com/Sakura520222/Sakura-Bot/graphs/contributors)

## Support

- Issues: [GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)
- Discussions: [GitHub Discussions](https://github.com/Sakura520222/Sakura-Bot/discussions)
- Email: [sakura520222@outlook.com](mailto:sakura520222@outlook.com)
