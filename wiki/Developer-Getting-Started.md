# 🚀 开发快速开始

本指南帮助开发者快速搭建 Sakura-Bot 的开发环境。

> **分类**: 开发者文档  
> **类型**: 教程  
> **难度**: ⭐  
> **更新时间**: 2026-10-31

[← 返回开发者文档](Developer-Guide.md) | [Wiki 首页](Home.md)

---

## 📋 环境要求

- **Python 3.13+**（Docker 与 CI 均使用 3.13）
- **Git**
- **MySQL**（必需，项目当前仅支持 MySQL）
- **Node.js 20+ / npm**（可选，用于 WebUI 前端开发与构建）
- **Go 1.27+**（可选，仅修改问答 Bot Go 版 `qa-bot-go/` 时需要）
- **Docker**（可选，用于容器化测试）

---

## 🐳 克隆项目

```bash
# Fork 并克隆项目
git clone https://github.com/YOUR_USERNAME/Sakura-Bot.git
cd Sakura-Bot

# 添加上游仓库
git remote add upstream https://github.com/Sakura520222/Sakura-Bot.git
```

---

## 🔧 开发环境设置

### 步骤 1：创建虚拟环境

```bash
# 使用 venv
python -m venv venv

# 激活虚拟环境
# Windows:
venv\Scripts\activate
# Linux/macOS:
source venv/bin/activate
```

### 步骤 2：安装依赖

```bash
# 安装项目依赖（含 pytest/ruff 等开发工具，无单独的开发依赖文件）
pip install -r requirements.txt
```

### 步骤 2.1：安装 WebUI 前端依赖（可选）

如果需要开发或本地构建 WebUI：

```bash
cd web
npm install
npm run build
cd ..
```

Docker 构建和项目更新流程会处理前端构建；仅修改 Python 后端时通常不需要手动执行。

### 步骤 3：配置环境变量

```bash
# 复制配置模板
cp data/.env.example data/.env

# 编辑配置文件
nano data/.env
```

填写必要的配置项（参考[用户快速开始](User-Getting-Started.md)）。

当前版本仅支持 MySQL，请至少配置以下数据库项：

```env
DATABASE_TYPE=mysql
MYSQL_HOST=localhost
MYSQL_PORT=3306
MYSQL_USER=your_mysql_user
MYSQL_PASSWORD=your_mysql_password
MYSQL_DATABASE=sakura_bot_db
```

### 步骤 4：安装 Pre-commit 钩子（推荐）

```bash
# 安装 pre-commit
pip install pre-commit

# 激活钩子
pre-commit install
```

---

## 🧪 运行测试

### 运行所有测试

```bash
pytest
```

### 运行特定测试

```bash
pytest tests/test_ai_client.py
```

### 查看覆盖率

`pytest.ini` 已默认启用覆盖率收集，直接运行 `pytest` 即可生成报告：

```bash
pytest --cov=core --cov-report=html
```

报告输出到 `htmlcov/` 与 `coverage.xml`。覆盖率目标与当前进展见[技术债务追踪](Developer-Technical-Debt.md)。

---

## 🔍 代码质量检查

### 使用 Ruff 脚本

```bash
# 完整模式：检查 → 自动修复 → 格式化
python run_ruff.py

# 只检查，不修改文件
python run_ruff.py --check

# 检查指定目录
python run_ruff.py --check core/
```

脚本会将日志写入 `logs/`；需要以退出码判断结果时（如 CI），请直接使用下方 Ruff 命令。

### 直接使用 Ruff

```bash
# 检查
ruff check .

# 修复
ruff check --fix .

# 格式化
ruff format .
```

详细说明请参考 [Ruff 脚本指南](Developer-Ruff-Script-Guide.md)。

---

## 🏃 运行项目

### 运行主 Bot

```bash
python main.py
```

主 Bot 会按配置自动管理问答 Bot 子进程。

### 运行问答 Bot

v2.2.0 起问答 Bot 缺省运行 **Go 版静态二进制**（`qa-bot-go/`，内存占用远低于 Python 版）。运行时解析优先级为 `config.json` 的 `qa_bot_runtime` > `QA_BOT_RUNTIME` 环境变量 > 缺省 `go`；Go 二进制缺失时自动回退 Python。管理员可通过 `/qa_runtime` 命令或 WebUI「系统」页热切换。

```bash
# Python 回退/调试入口（通常无需手动运行；运行时为 go 时由 main.py 管理 Go 子进程）
python qa_bot.py
```

> 注意：单独调试 `python qa_bot.py` 时，不要同时运行由 `main.py` 管理的问答 Bot 副本（同一 Token 会互相冲突）。

如需开发 `qa-bot-go/`，请安装 Go 1.27+ 工具链，构建与运行说明见 `qa-bot-go/README.md`。

### 使用 Docker 运行

```bash
# 构建镜像（fork / 本地源码部署；缺省 compose 配置直接拉取 GHCR 依赖镜像，可跳过构建）
docker compose build

# 启动服务
docker compose up

# 后台运行
docker compose up -d
```

缺省 `docker-compose.yml` 拉取 GHCR 依赖镜像（`ghcr.io/sakura520222/sakura-bot:main`，内含 Python 依赖、Go 二进制与前端产物），源码仍整体挂载，代码更新走 `git pull` 或 `/update` 命令。

---

## 🏗️ 项目结构

```
Sakura-Bot/
├── core/                      # 核心模块
│   ├── __init__.py           # 版本号等包级定义
│   ├── ai/                   # AI、RAG、双后端向量存储和工具调用
│   ├── bootstrap/            # 应用启动编排
│   ├── commands/             # 命令处理
│   ├── config/               # 配置管理、事件总线、热重载
│   ├── forwarding/           # 频道转发功能
│   ├── handlers/             # 事件处理器
│   ├── i18n/                 # 国际化文案
│   ├── infrastructure/       # 数据库、日志、异常和工具
│   ├── initializers/         # 初始化步骤（含命令集中注册）
│   ├── migrations/           # 数据库迁移脚本
│   ├── moderation/           # Sakura Guard 群管与封禁申诉
│   ├── services/             # 缓存、投票、投稿等领域服务
│   ├── system/               # 调度、进程、镜像更新和关闭管理
│   ├── telegram/             # Telegram 相关
│   ├── utils/                # 通用工具
│   └── web_api/              # FastAPI WebUI API
├── qa-bot-go/                 # 问答 Bot Go 版实现（缺省运行时）
├── web/                       # Vue 3 + Vite WebUI 前端
├── tests/                     # 测试代码
│   ├── conftest.py           # pytest 共享 fixtures
│   └── test_*.py             # 测试文件
├── scripts/                   # 部署脚本与向量迁移/回填工具
├── deploy/                    # 宿主机镜像部署器（systemd 单元）
├── data/                      # 数据文件
│   ├── .env                  # 环境变量
│   ├── config.json           # 配置文件
│   └── sessions/             # Telegram 会话
├── wiki/                      # 项目文档（VitePress 站点）
├── main.py                    # 主程序入口
├── qa_bot.py                  # 问答 Bot Python 回退入口
├── requirements.txt          # 依赖列表
├── pytest.ini                # pytest 配置
├── pyproject.toml            # 项目配置
└── run_ruff.py               # Ruff 脚本
```

---

## 📝 开发工作流

### 1. 创建功能分支

```bash
# 确保在 main 分支
git checkout main
git pull upstream main

# 创建功能分支
git checkout -b feature/your-feature-name
```

### 2. 编写代码

- 遵循 [PEP 8](https://peps.python.org/pep-0008/) 规范
- 添加类型提示
- 编写文档字符串（Google 风格）
- 编写单元测试

### 3. 本地测试

```bash
# 运行测试
pytest

# 代码检查（不修改文件）
python run_ruff.py --check
```

### 4. 提交代码

```bash
# 添加文件
git add .

# 提交（遵循 Conventional Commits）
git commit -m "feat(scope): 添加功能描述"
```

**提交信息格式：**
- `feat:` - 新功能
- `fix:` - Bug 修复
- `docs:` - 文档更新
- `style:` - 代码格式
- `refactor:` - 重构
- `test:` - 测试相关
- `chore:` - 构建/工具

### 5. 推送分支

```bash
git push origin feature/your-feature-name
```

### 6. 创建 Pull Request

在 GitHub 上创建 PR，描述您的更改。

---

## 🧩 开发最佳实践

### 1. 代码风格

```python
# ✅ 好的示例
async def fetch_user_data(user_id: int) -> dict[str, Any] | None:
    """获取用户数据。

    Args:
        user_id: 用户ID

    Returns:
        用户数据字典，如果不存在返回 None
    """
    try:
        return await database.get_user(user_id)
    except DatabaseError as e:
        logger.error(f"获取用户数据失败: {e}")
        return None


# ❌ 避免的示例
def getuser(id):
    # 获取用户
    return db.get(id)
```

### 2. 错误处理

```python
# ✅ 使用自定义异常
from core.exceptions import BotError, AIServiceError

try:
    result = await ai_client.analyze(text)
except AIServiceError as e:
    logger.error(f"AI分析失败: {e}")
    # 处理错误
```

### 3. 类型提示

```python
# ✅ 使用类型提示
from typing import Optional


def process_message(text: str, user_id: int) -> Optional[str]:
    """处理消息并返回结果。"""
    # ...
```

### 4. 文档字符串

```python
# ✅ Google 风格文档字符串
def calculate_summary(messages: list[Message]) -> str:
    """计算消息总结。

    Args:
        messages: 消息列表

    Returns:
        总结文本

    Raises:
        ValueError: 如果消息列表为空
    """
    if not messages:
        raise ValueError("消息列表不能为空")
    # ...
```

---

## 🐛 调试技巧

### 使用 pdb 调试

```python
import pdb

pdb.set_trace()  # 设置断点
```

### 使用日志

```python
import logging

logger = logging.getLogger(__name__)

logger.debug("调试信息")
logger.info("普通信息")
logger.warning("警告信息")
logger.error("错误信息")
```

### 查看日志

```bash
# 实时查看日志
tail -f logs/sakura-bot.log

# 查看特定级别日志
tail -f logs/sakura-bot.log | grep ERROR
```

---

## 📚 相关文档

- **[架构设计](Developer-Architecture.md)** - 系统架构说明
- **[Ruff 脚本指南](Developer-Ruff-Script-Guide.md)** - 代码质量工具
- **[贡献指南](../CONTRIBUTING.md)** - 如何贡献代码
- **[用户快速开始](User-Getting-Started.md)** - 用户部署指南

---

## 💬 获取帮助

- 📖 查阅 [开发者文档](Developer-Guide.md)
- 🔍 搜索 [GitHub Issues](https://github.com/Sakura520222/Sakura-Bot/issues)
- 💬 参与 [GitHub Discussions](https://github.com/Sakura520222/Sakura-Bot/discussions)
- 📧 发送邮件至 [sakura520222@outlook.com](mailto:sakura520222@outlook.com)

---

<div align="center">

**🌸 欢迎贡献代码！**

[⭐ Star](https://github.com/Sakura520222/Sakura-Bot) · [🍴 Fork](https://github.com/Sakura520222/Sakura-Bot/fork)

</div>