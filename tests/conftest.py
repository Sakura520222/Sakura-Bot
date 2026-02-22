"""Pytest 全局配置和 Fixtures

Copyright 2026 Sakura-Bot
本项目采用 AGPL-3.0 许可
"""

import asyncio
import json
import os
import tempfile
from collections.abc import Generator
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest

# ==================== 异步事件循环配置 ====================


@pytest.fixture(scope="session", autouse=True)
def clean_environment():
    """在测试会话开始时清空环境变量，但保留Python必要变量

    这防止 Pydantic Settings 从系统环境变量中读取真实配置
    """
    original_env = os.environ.copy()

    # 保留Python必要的环境变量（如PATH, SYSTEMROOT等）
    essential_vars = [
        "PATH",
        "SYSTEMROOT",
        "PYTHONPATH",
        "HOME",
        "USERPROFILE",
        "TEMP",
        "TMP",
        "APPDATA",
        "PATHEXT",
        "COMSPEC",
        "TERM",
    ]

    # 只清空非必要的变量
    vars_to_remove = [k for k in os.environ.keys() if k not in essential_vars]
    for var in vars_to_remove:
        del os.environ[var]

    yield

    # 测试结束后恢复原始环境变量
    os.environ.clear()
    os.environ.update(original_env)


@pytest.fixture(scope="session")
def event_loop() -> Generator[asyncio.AbstractEventLoop, None, None]:
    """创建一个 session 级别的事件循环，所有异步测试共享

    这避免了每个测试用例都创建/销毁事件循环的性能开销
    """
    loop = asyncio.get_event_loop_policy().new_event_loop()
    yield loop
    loop.close()


# ==================== 临时文件和目录 Fixtures ====================


@pytest.fixture
def temp_dir() -> Generator[Path, None, None]:
    """创建临时目录用于测试

    自动清理测试后产生的临时文件
    """
    with tempfile.TemporaryDirectory() as tmpdir:
        yield Path(tmpdir)


@pytest.fixture
def temp_config_file(temp_dir: Path) -> Generator[Path, None, None]:
    """创建临时配置文件"""
    config_file = temp_dir / "config.json"
    default_config = {
        "api_key": "test_api_key",
        "base_url": "https://api.test.com",
        "model": "test-model",
        "channels": ["@test_channel1", "@test_channel2"],
        "send_report_to_source": True,
        "enable_poll": True,
        "log_level": "INFO",
    }
    config_file.write_text(json.dumps(default_config, ensure_ascii=False), encoding="utf-8")
    yield config_file


@pytest.fixture
def temp_env_file(temp_dir: Path) -> Generator[Path, None, None]:
    """创建临时 .env 文件"""
    env_file = temp_dir / ".env"
    env_content = """
TELEGRAM_API_ID=123456
TELEGRAM_API_HASH=test_api_hash
TELEGRAM_BOT_TOKEN=123456:ABCDEF
LLM_API_KEY=test_llm_api_key
LLM_BASE_URL=https://api.test.com
LLM_MODEL=test-model
TARGET_CHANNEL=@test_channel
LANGUAGE=zh-CN
LOG_LEVEL=DEBUG
"""
    env_file.write_text(env_content, encoding="utf-8")
    yield env_file


# ==================== Mock Fixtures ====================


@pytest.fixture
def mock_telegram_client():
    """Mock Telegram 客户端"""
    client = AsyncMock()
    client.get_entity = AsyncMock()
    client.get_me = AsyncMock(return_value=MagicMock(id=123456, username="test_bot"))
    client.send_message = AsyncMock()
    client.get_messages = AsyncMock()
    client.download_media = AsyncMock()
    return client


@pytest.fixture
def mock_telegram_event():
    """Mock Telegram 事件对象"""
    event = MagicMock()
    event.chat_id = -1001234567890
    event.message = MagicMock()
    event.message.id = 12345
    event.message.message = "Test message"
    event.message.sender_id = 123456
    event.message.date = MagicMock()
    event.message.original_fwd = None
    event.original_update = MagicMock()
    event.original_update.chat_id = -1001234567890
    return event


@pytest.fixture
def mock_openai_client():
    """Mock OpenAI 客户端"""
    client = MagicMock()
    client.chat = MagicMock()
    client.chat.completions = MagicMock()
    client.chat.completions.create = MagicMock(return_value=MockOpenAIResponse())
    return client


@pytest.fixture
def mock_openai_response():
    """Mock OpenAI API 响应"""
    response = MagicMock()
    response.choices = [MagicMock()]
    response.choices[0].message.content = "Test AI response"
    response.usage = MagicMock()
    response.usage.prompt_tokens = 100
    response.usage.completion_tokens = 50
    response.usage.total_tokens = 150
    return response


# ==================== 数据库 Fixtures ====================


@pytest.fixture
async def temp_sqlite_db():
    """创建临时 SQLite 内存数据库

    使用 :memory: 模式，测试结束后自动清理
    """
    import aiosqlite

    # 创建内存数据库连接
    async with aiosqlite.connect(":memory:") as db:
        # 创建测试表结构
        await db.execute(
            """
            CREATE TABLE IF NOT EXISTS test_messages (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                channel_id TEXT NOT NULL,
                message_id INTEGER NOT NULL,
                content TEXT,
                timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
            )
        """
        )
        await db.execute(
            """
            CREATE TABLE IF NOT EXISTS test_summaries (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                channel_id TEXT NOT NULL,
                summary_text TEXT NOT NULL,
                timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
            )
        """
        )
        await db.commit()
        yield db
        # 数据库会在退出上下文时自动清理


@pytest.fixture
def mock_db_connection():
    """Mock 数据库连接"""
    conn = AsyncMock()
    conn.execute = AsyncMock()
    conn.commit = AsyncMock()
    conn.fetchone = AsyncMock(return_value=(1, "test_data"))
    conn.fetchall = AsyncMock(return_value=[(1, "test_data")])
    return conn


# ==================== AI 相关 Mock 类 ====================


class MockOpenAIResponse:
    """Mock OpenAI API 响应类"""

    def __init__(
        self, content="Test response", usage_prompt_tokens=100, usage_completion_tokens=50
    ):
        self.choices = [MagicMock()]
        self.choices[0].message.content = content
        self.choices[0].finish_reason = "stop"
        self.usage = MagicMock()
        self.usage.prompt_tokens = usage_prompt_tokens
        self.usage.completion_tokens = usage_completion_tokens
        self.usage.total_tokens = usage_prompt_tokens + usage_completion_tokens
        self.id = "chatcmpl-test123"
        self.object = "chat.completion"
        self.created = 1234567890
        self.model = "test-model"


class MockEmbeddingResponse:
    """Mock Embedding API 响应类"""

    def __init__(self, embedding_dim=1536):
        self.data = [MagicMock()]
        self.data[0].embedding = [0.1] * embedding_dim
        self.data[0].index = 0
        self.usage = MagicMock()
        self.usage.prompt_tokens = 10
        self.usage.total_tokens = 10


# ==================== 环境变量 Mock ====================


@pytest.fixture
def mock_env_vars():
    """Mock 环境变量

    在测试开始时设置环境变量
    注意：不需要恢复环境变量，因为 clean_environment 会处理
    """
    # 设置测试用环境变量
    test_env = {
        "TELEGRAM_API_ID": "123456",
        "TELEGRAM_API_HASH": "test_api_hash",
        "TELEGRAM_BOT_TOKEN": "123456:ABCDEF",
        "LLM_API_KEY": "test_llm_api_key",
        "LLM_BASE_URL": "https://api.test.com",
        "LLM_MODEL": "test-model",
        "TARGET_CHANNEL": "@test_channel",
        "LANGUAGE": "zh-CN",
        "LOG_LEVEL": "DEBUG",
    }

    os.environ.update(test_env)
    yield test_env

    # 不需要恢复环境变量，clean_environment fixture 会处理


# ==================== 配置 Mock ====================


@pytest.fixture
def mock_config():
    """Mock 配置对象"""
    config = {
        "api_key": "test_api_key",
        "base_url": "https://api.test.com",
        "model": "test-model",
        "channels": ["@test_channel1", "@test_channel2"],
        "send_report_to_source": True,
        "enable_poll": True,
        "log_level": "INFO",
        "summary_schedules": {
            "@test_channel1": {
                "frequency": "weekly",
                "days": ["mon"],
                "hour": 9,
                "minute": 0,
            }
        },
        "channel_poll_settings": {
            "@test_channel1": {
                "enabled": True,
                "send_to_channel": False,
            }
        },
    }
    return config


# ==================== 测试数据 Fixtures ====================


@pytest.fixture
def sample_messages():
    """示例消息数据"""
    return [
        {
            "id": 1,
            "content": "这是一条测试消息",
            "timestamp": "2026-01-01 10:00:00",
            "sender": "user1",
        },
        {
            "id": 2,
            "content": "这是另一条测试消息",
            "timestamp": "2026-01-01 11:00:00",
            "sender": "user2",
        },
        {
            "id": 3,
            "content": "第三条测试消息，包含更多内容",
            "timestamp": "2026-01-01 12:00:00",
            "sender": "user1",
        },
    ]


@pytest.fixture
def sample_summary():
    """示例总结文本"""
    return """
# 频道总结 - 2026年1月1日

## 核心要点
- 用户1和用户2进行了讨论
- 共有3条消息
- 讨论主题：测试相关内容

## 重要消息
- [消息1](https://t.me/test_channel/1)
- [消息2](https://t.me/test_channel/2)
"""


# ==================== Pytest 钩子函数 ====================


def pytest_configure(config):
    """Pytest 配置钩子"""
    # 注册自定义标记
    config.addinivalue_line("markers", "unit: 单元测试标记")
    config.addinivalue_line("markers", "integration: 集成测试标记")
    config.addinivalue_line("markers", "slow: 慢速测试标记")
    config.addinivalue_line("markers", "telegram: Telegram 相关测试")
    config.addinivalue_line("markers", "database: 数据库相关测试")
    config.addinivalue_line("markers", "asyncio: 异步测试")


@pytest.fixture(autouse=True)
def reset_singletons():
    """在每个测试后重置单例对象

    防止测试之间的状态污染
    """
    yield

    # 重置 Settings 单例
    try:
        import core.settings

        core.settings._settings = None
    except (ImportError, AttributeError):
        pass

    # 重置 Config 模块的全局变量
    try:
        import core.config

        if hasattr(core.config, "_bot_state"):
            core.config._bot_state = core.config.BOT_STATE_RUNNING
        if hasattr(core.config, "_scheduler_instance"):
            core.config._scheduler_instance = None
        if hasattr(core.config, "LINKED_CHAT_CACHE"):
            core.config.LINKED_CHAT_CACHE.clear()
    except (ImportError, AttributeError):
        pass


# ==================== 全局 Mock Fixtures ====================


@pytest.fixture
def mock_chat_completion():
    """Mock OpenAI chat completion 响应 - 全局可用"""
    response = MagicMock()
    response.choices = [MagicMock()]
    response.choices[0].message.content = "这是 AI 生成的测试总结"
    response.choices[0].index = 0
    response.choices[0].finish_reason = "stop"
    return response


@pytest.fixture
def mock_poll_response():
    """Mock OpenAI poll generation 响应 - 全局可用"""
    response = MagicMock()
    response.choices = [MagicMock()]
    response.choices[0].message.content = """```json
{
  "question": "测试投票问题 / Test Poll Question",
  "options": [
    "选项1 / Option 1",
    "选项2 / Option 2",
    "选项3 / Option 3",
    "选项4 / Option 4"
  ]
}
```"""
    return response


# ==================== 测试辅助函数 ====================


def create_mock_message(content: str, message_id: int = 1, sender_id: int = 123456):
    """创建 Mock 消息对象的辅助函数"""
    message = MagicMock()
    message.id = message_id
    message.message = content
    message.sender_id = sender_id
    message.date = MagicMock()
    message.original_fwd = None
    return message


def create_mock_channel(channel_id: str = "@test_channel", title: str = "Test Channel"):
    """创建 Mock 频道对象的辅助函数"""
    channel = MagicMock()
    channel.id = channel_id
    channel.title = title
    channel.username = channel_id.lstrip("@")
    channel.linked_chat_id = -1001234567890
    return channel
