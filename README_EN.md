# 🌸 Sakura-Bot

[![Release](https://img.shields.io/github/v/release/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/releases)
[![License](https://img.shields.io/badge/License-AGPL--3.0%20%2B%20Non--Commercial-blue?style=flat-square)](LICENSE)
[![Python](https://img.shields.io/badge/python-3.13+-blue?style=flat-square&logo=python)](https://www.python.org/downloads/)
[![Code style: black](https://img.shields.io/badge/code%20style-black-000000?style=flat-square)](https://github.com/psf/black)
[![Docker](https://img.shields.io/badge/docker-20.10%2B-blue?style=flat-square&logo=docker)](https://www.docker.com/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/pulls)
[![Stars](https://img.shields.io/github/stars/Sakura520222/Sakura-Bot?style=flat-square)](https://github.com/Sakura520222/Sakura-Bot/stargazers)

> **An intelligent Telegram channel management tool powered by AI** 🤖✨

[中文文档](README.md) | [Documentation](wiki) | [Report Bug](https://github.com/Sakura520222/Sakura-Bot/issues) | [Request Feature](https://github.com/Sakura520222/Sakura-Bot/issues)

---

## 📖 About

Sakura Channel Summary Assistant is a sophisticated Telegram channel management solution that leverages cutting-edge AI technology to automatically monitor, analyze, and summarize channel content. Designed for channel administrators, it provides an efficient way to stay updated with channel discussions through intelligent automation.

### ✨ Key Highlights

- 🎯 **AI-Powered Summarization** - Advanced language models extract key insights from conversations
- 🔍 **Intelligent Q&A System** - RAG-based natural language Q&A with semantic search and history queries
- ⏰ **Flexible Scheduling** - Daily, weekly, or multi-day automated summaries
- 🌐 **Multi-Channel Support** - Manage multiple channels simultaneously
- 🤖 **Customizable AI Configuration** - Support for OpenAI-compatible APIs (DeepSeek, OpenAI, etc.)
- 📊 **Interactive Polls** - Engage your community with AI-generated polls
- 📝 **History Management** - Track, export, and analyze all summaries

---

## 🚀 Quick Start

### Prerequisites

- **Python 3.13+** or **Docker 20.10+**
- **Telegram Bot Token** from [@BotFather](https://t.me/BotFather)
- **Telegram API credentials** from [my.telegram.org](https://my.telegram.org)
- **OpenAI-compatible API Key** (e.g., [DeepSeek](https://platform.deepseek.com/), [OpenAI](https://platform.openai.com/))

### 🐳 Docker Deployment (Recommended)

```bash
# Clone the repository
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot

# Configure environment variables
cp data/.env.example data/.env
# Edit data/.env with your credentials

# Start the service
docker-compose up -d

# View logs
docker-compose logs -f
```

### 💻 Local Installation

```bash
# Clone the repository
git clone https://github.com/Sakura520222/Sakura-Bot.git
cd Sakura-Bot

# Install dependencies
pip install -r requirements.txt

# Configure environment
cp data/.env.example data/.env
# Edit data/.env with your credentials

# Run the application
python main.py
```

---

## 🎨 Features

### Core Capabilities

| Feature | Description | Status |
|---------|-------------|--------|
| **🤖 AI Summarization** | Advanced AI models analyze and extract key points from channel messages | ✅ |
| **🔍 Auto Monitoring** | Automatically fetches and organizes messages from monitored channels | ✅ |
| **⏰ Flexible Scheduling** | Support for daily, weekly, and multi-day automatic summary frequencies | ✅ |
| **⚡ Manual Trigger** | Generate summaries on-demand via admin commands | ✅ |

### AI & Configuration

| Feature | Description | Status |
|---------|-------------|--------|
| **🔧 Custom AI Config** | Support for multiple OpenAI-compatible API services | ✅ |
| **🎯 Custom Prompts** | Tailor summary style with customizable AI prompts | ✅ |
| **🎯 Poll Prompts** | Configure AI-generated poll content independently | ✅ |

### Channel Management

| Feature | Description | Status |
|---------|-------------|--------|
| **👥 Multi-Admin** | Configure multiple admin IDs for report distribution | ✅ |
| **🌐 Multi-Channel** | Monitor and summarize multiple channels simultaneously | ✅ |
| **📝 Message Splitting** | Intelligently handle long summaries with automatic segmentation | ✅ |
| **⏱️ Smart Timestamps** | Track summary time to only fetch new messages efficiently | ✅ |
| **🕐 Per-Channel Schedule** | Configure automatic summary times for each channel individually | ✅ |

### Advanced Features

| Feature | Description | Status |
|---------|-------------|--------|
| **🔍 Intelligent Q&A System** | RAG-based natural language Q&A with semantic search and history queries | ✅ |
| **🧠 Vector Storage** | ChromaDB-powered vector storage for semantic search | ✅ |
| **🎯 Reranking** | BGE-Reranker for precise result reordering, improving accuracy | ✅ |
| **💬 Multi-Turn Conversations** | Context-aware dialogue, AI understands pronoun references and conversation history | ✅ |
| **🛡️ Error Recovery** | Intelligent retry mechanism, health checks, and graceful shutdown | ✅ |
| **📊 Interactive Polls** | Auto-generate polls in discussion groups after summaries | ✅ |
| **🎯 Per-Channel Polls** | Configure poll settings independently for each channel | ✅ |
| **🔄 Poll Regeneration** | Admin can regenerate polls with a single button click | ✅ |
| **📜 History Tracking** | Automatic database storage with query, export, and statistics | ✅ |
| **🌍 Internationalization** | Complete multi-language support system, all modules internationalized | ✅ |

---

## 📋 Usage

### Command Reference

#### Basic Commands

| Command | Aliases | Description | Example |
|---------|---------|-------------|---------|
| `/start` | `/开始` | View welcome message and introduction | `/start` |
| `/help` | `/帮助` | Display complete command list and usage | `/help` |

#### Core Functions

| Command | Aliases | Description | Example |
|---------|---------|-------------|---------|
| `/summary` | `/立即总结` | Generate immediate weekly summary | `/summary` |

#### AI Configuration

| Command | Aliases | Description | Example |
|---------|---------|-------------|---------|
| `/showprompt` | `/查看提示词` | View current AI prompt | `/showprompt` |
| `/setprompt` | `/设置提示词` | Set custom AI prompt | `/setprompt` |
| `/showaicfg` | `/查看AI配置` | View current AI configuration | `/showaicfg` |
| `/setaicfg` | `/设置AI配置` | Configure custom AI settings | `/setaicfg` |

#### Channel Management

| Command | Aliases | Description | Example |
|---------|---------|-------------|---------|
| `/showchannels` | `/查看频道列表` | List all configured channels | `/showchannels` |
| `/addchannel` | `/添加频道` | Add channel to monitoring list | `/addchannel https://t.me/example` |
| `/deletechannel` | `/删除频道` | Remove channel from monitoring | `/deletechannel https://t.me/example` |

#### Schedule Configuration

| Command | Aliases | Description | Example |
|---------|---------|-------------|---------|
| `/showchannelschedule` | `/查看频道时间配置` | View channel schedule settings | `/showchannelschedule` |
| `/setchannelschedule` | `/设置频道时间配置` | Configure automatic summary time | `/setchannelschedule` |
| `/deletechannelschedule` | `/删除频道时间配置` | Remove channel schedule | `/deletechannelschedule` |
| `/clearsummarytime` | `/清除总结时间` | Clear last summary timestamp | `/clearsummarytime` |
| `/setsendtosource` | `/设置报告发送回源频道` | Toggle source channel reporting | `/setsendtosource` |

#### Poll Configuration

| Command | Aliases | Description | Example |
|---------|---------|-------------|---------|
| `/channelpoll` | `/查看频道投票配置` | View channel poll settings | `/channelpoll` |
| `/setchannelpoll` | `/设置频道投票配置` | Configure channel poll settings | `/setchannelpoll` |
| `/deletechannelpoll` | `/删除频道投票配置` | Remove channel poll configuration | `/deletechannelpoll` |

#### System Control

| Command | Aliases | Description | Example |
|---------|---------|-------------|---------|
| `/pause` | `/暂停` | Pause all scheduled tasks | `/pause` |
| `/resume` | `/恢复` | Resume all scheduled tasks | `/resume` |
| `/restart` | `/重启` | Restart the bot | `/restart` |
| `/shutdown` | `/关机` | Shutdown the bot completely | `/shutdown` |

#### Debug & Logs

| Command | Aliases | Description | Example |
|---------|---------|-------------|---------|
| `/showloglevel` | `/查看日志级别` | View current log level | `/showloglevel` |
| `/setloglevel` | `/设置日志级别` | Set log level | `/setloglevel` |
| `/clearcache` | `/清除缓存` | Clear discussion group ID cache | `/clearcache` |
| `/changelog` | `/更新日志` | View update changelog | `/changelog` |

#### History Management

| Command | Aliases | Description | Example |
|---------|---------|-------------|---------|
| `/history` | `/历史` | View historical summaries | `/history` |
| `/export` | `/导出` | Export history records | `/export channel1 csv` |
| `/stats` | `/统计` | View statistics | `/stats` |

#### Language Settings

| Command | Aliases | Description | Example |
|---------|---------|-------------|---------|
| `/language` | `/语言` | View or switch interface language | `/language` / `/language en-US` |

### Configuration Example

Create or edit `data/.env`:

```env
# ===== Telegram Configuration =====
TELEGRAM_API_ID=your_api_id_here
TELEGRAM_API_HASH=your_api_hash_here
TELEGRAM_BOT_TOKEN=your_bot_token_here

# ===== Language Configuration =====
LANGUAGE=en-US  # Interface language: en-US (English) or zh-CN (Simplified Chinese)

# ===== AI Configuration (OpenAI-compatible APIs) =====

```env
# ===== Telegram Configuration =====
TELEGRAM_API_ID=your_api_id_here
TELEGRAM_API_HASH=your_api_hash_here
TELEGRAM_BOT_TOKEN=your_bot_token_here

# ===== AI Configuration (OpenAI-compatible APIs) =====
# Option 1: DeepSeek (Recommended)
LLM_API_KEY=your_deepseek_api_key
LLM_BASE_URL=https://api.deepseek.com
LLM_MODEL=deepseek-chat

# Option 2: OpenAI
# LLM_API_KEY=your_openai_api_key
# LLM_BASE_URL=https://api.openai.com/v1
# LLM_MODEL=gpt-4o

# ===== Admin Configuration =====
REPORT_ADMIN_IDS=your_admin_id_here,another_admin_id_here

# ===== Log Level =====
LOG_LEVEL=INFO

# ===== Poll Feature =====
ENABLE_POLL=True

# ===== RAG Intelligent Q&A System Configuration (Optional) =====
# Embedding Model Configuration (Required for vector search)
EMBEDDING_API_KEY=your_siliconflow_api_key
EMBEDDING_API_BASE=https://api.siliconflow.cn/v1
EMBEDDING_MODEL=BAAI/bge-large-zh-v1.5
EMBEDDING_DIMENSION=1024

# Reranker Configuration (Optional, for improving retrieval accuracy)
RERANKER_API_KEY=your_siliconflow_api_key
RERANKER_API_BASE=https://api.siliconflow.cn/v1/rerank
RERANKER_MODEL=BAAI/bge-reranker-v2-m3
RERANKER_TOP_K=20
RERANKER_FINAL=5

# Vector Database Configuration
VECTOR_DB_PATH=data/vectors

# Q&A Bot Configuration
QA_BOT_ENABLED=True
QA_BOT_TOKEN=your_qa_bot_token_here  # Get from @BotFather
```

> **Note**: The RAG system requires additional API keys. We recommend using [SiliconFlow](https://siliconflow.cn/) for Embedding and Reranker services. For detailed configuration, please refer to the [RAG Quick Start Guide](wiki/RAG_QUICKSTART.md).

---

## 🏗️ Project Structure

```
Sakura-Bot/
│
├── 📁 core/                          # Core modules
│   ├── ai_client.py                  # AI client module
│   ├── command_handlers/             # Command handlers
│   ├── telegram/                     # Telegram client
│   └── utils/                        # Utility functions
│
├── 📁 data/                          # Data directory
│   ├── .env                          # Environment configuration
│   ├── config.json                   # AI configuration
│   ├── prompt.txt                    # Summary prompt
│   ├── poll_prompt.txt               # Poll prompt
│   ├── summaries.db                  # SQLite database
│   └── sessions/                     # Telegram sessions
│
├── 📁 wiki/                          # Documentation
├── 📁 .github/                       # GitHub workflows
│
├── 📄 main.py                        # Entry point
├── 📄 requirements.txt               # Dependencies
├── 📄 docker-compose.yml             # Docker Compose config
├── 📄 Dockerfile                     # Docker image build
└── 📄 README.md                      # This file
```

---

## 🔧 Tech Stack

| Technology | Purpose | Version |
|------------|---------|---------|
| **Python** | Main language | 3.13+ |
| **Telethon** | Telegram API client | 1.34+ |
| **OpenAI SDK** | AI API integration | 1.0+ |
| **APScheduler** | Task scheduling | 3.10+ |
| **python-dotenv** | Environment management | 1.0+ |
| **Docker** | Containerization | 20.10+ |

---

## ❓ FAQ

### Do I need to login on first run?

Yes, first run requires Telegram authentication (phone + verification code). Session files will be generated for subsequent runs.

### How to get Telegram API credentials?

1. Visit [my.telegram.org](https://my.telegram.org)
2. Login to your Telegram account
3. Click "API development tools"
4. Create an app to get `api_id` and `api_hash`

### Which AI services are supported?

All OpenAI-compatible APIs, including:
- **DeepSeek** (Recommended, cost-effective)
- **OpenAI** official API
- Any third-party OpenAI-compatible service

### How to backup data?

```bash
# Backup data directory
tar -czf backup-$(date +%Y%m%d).tar.gz data/
```

---

## 🤝 Contributing

We welcome contributions! Please check our [Contributing Guidelines](wiki/CODE_OF_CONDUCT.md) and [Code of Conduct](wiki/CODE_OF_CONDUCT.md).

### How to Contribute

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/AmazingFeature`)
3. Commit your changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

---

## 📄 License

This project is licensed under **GNU Affero General Public License Version 3.0 (AGPL-3.0) with Non-Commercial restrictions**.

### Key Points

- **AGPL-3.0**: Requires source code disclosure for modifications and network services
- **Non-Commercial**: Prohibits commercial use, paid subscriptions, or SaaS products
- **Attribution**: All derivatives must retain original project links and author credits
- **API Responsibility**: Users are responsible for API costs and legal compliance

### Important Notice

- This project is for **personal learning only**, commercial use is prohibited
- When using code or derivatives, must cite the original repository
- Network service providers must provide source code per AGPL-3.0
- Project source: https://github.com/Sakura520222/Sakura-Bot

See [LICENSE](LICENSE) for the full license text.

---

## 🙏 Acknowledgments

- [Telethon](https://github.com/LonamiWebs/Telethon) - Powerful Telegram MTProto API framework
- [OpenAI](https://openai.com/) - Leading AI research and API services
- [DeepSeek](https://www.deepseek.com/) - Cost-effective AI API provider
- All [contributors](https://github.com/Sakura520222/Sakura-Bot/graphs/contributors) who helped improve this project

---

## 📞 Support

- 📧 Email: [sakura520222@outlook.com](mailto:sakura520222@outlook.com)
- 🐛 Issues: [GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)
- 💬 Discussions: [GitHub Discussions](https://github.com/Sakura520222/Sakura-Bot/discussions)

---

<div align="center">

**🌸 Sakura Channel Summary Assistant** · Making Channel Management Smarter

Made with ❤️ by [Sakura520222](https://github.com/Sakura520222)

[⭐ Star](https://github.com/Sakura520222/Sakura-Bot) · [🍴 Fork](https://github.com/Sakura520222/Sakura-Bot/fork) · [📖 Documentation](wiki) · [🐛 Report Issues](https://github.com/Sakura520222/Sakura-Bot/issues)

</div>