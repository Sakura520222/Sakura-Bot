# 🚀 开发快速开始

本指南帮助开发者快速搭建 Sakura-Bot 的开发环境。

> **分类**: 开发者文档  
> **类型**: 教程  
> **难度**: ⭐  
> **更新时间**: 2026-02-25

[← 返回开发者文档](Developer-Guide.md) | [Wiki 首页](Home.md)

---

## 📋 环境要求

- **Python 3.11+**（推荐 3.13）
- **Git**
- **PostgreSQL/MySQL**（可选，用于测试）
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
# 安装项目依赖
pip install -r requirements.txt

# 安装开发依赖
pip install -r requirements-dev.txt  # 如果存在
```

### 步骤 3：配置环境变量

```bash
# 复制配置模板
cp data/.env.example data/.env

# 编辑配置文件
nano data/.env
```

填写必要的配置项（参考[用户快速开始](User-Getting-Started.md)）。

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

```bash
pytest --cov=core --cov-report=html
```

覆盖率目标：**≥ 90%**

---

## 🔍 代码质量检查

### 使用 Ruff 脚本

```bash
# 检查代码
python run_ruff.py

# 自动修复
python run_ruff.py --fix

# 格式化代码
python run_ruff.py --format

# 一键完成
python run_ruff.py --all
```

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

### 运行问答 Bot

```bash
python qa_bot.py
```

### 使用 Docker 运行

```bash
# 构建镜像
docker-compose build

# 启动服务
docker-compose up

# 后台运行
docker-compose up -d
```

---

## 🏗️ 项目结构

```
Sakura-Bot/
├── core/                      # 核心模块
│   ├── __init__.py
│   ├── ai_client.py          # AI 客户端
│   ├── config.py             # 配置管理
│   ├── database.py           # 数据库操作
│   ├── conversation_manager.py  # 会话管理
│   ├── intent_parser.py      # 意图解析
│   ├── memory_manager.py     # 记忆管理
│   ├── qa_engine_v3.py       # 问答引擎
│   ├── vector_store.py       # 向量存储
│   ├── reranker.py           # 重排序
│   ├── command_handlers/     # 命令处理
│   ├── telegram/             # Telegram 相关
│   └── utils/                # 工具函数
├── tests/                     # 测试代码
│   ├── __init__.py
│   ├── conftest.py           # pytest 配置
│   └── test_*.py             # 测试文件
├── data/                      # 数据文件
│   ├── .env                  # 环境变量
│   ├── config.json           # 配置文件
│   └── sessions/             # Telegram 会话
├── wiki/                      # 项目文档
├── main.py                    # 主程序入口
├── qa_bot.py                 # 问答 Bot 入口
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

# 代码检查
python run_ruff.py --all
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
import pdb; pdb.set_trace()  # 设置断点
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
tail -f logs/sakura_bot.log

# 查看特定级别日志
tail -f logs/sakura_bot.log | grep ERROR
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