"""
测试 MySQL 数据库日期时间格式修复

验证 datetime 对象正确传递给 MySQL，不使用 isoformat() 字符串
"""

from datetime import UTC, datetime
from unittest.mock import AsyncMock, MagicMock

import pytest

from core.database_mysql import MySQLManager


@pytest.fixture
def mysql_manager():
    """创建 MySQL 管理器实例"""
    manager = MySQLManager(
        host="localhost",
        port=3306,
        user="test_user",
        password="test_pass",
        database="test_db",
    )
    return manager


@pytest.mark.asyncio
async def test_register_user_datetime_format(mysql_manager):
    """测试用户注册时 datetime 格式正确"""
    # Mock 连接池和游标
    mock_pool = MagicMock()
    mock_conn = MagicMock()
    mock_cursor = MagicMock()

    mock_pool.acquire = MagicMock(return_value=mock_conn)
    mock_conn.__aenter__ = AsyncMock(return_value=mock_conn)
    mock_conn.__aexit__ = AsyncMock()
    mock_conn.cursor = MagicMock(return_value=mock_cursor)
    mock_cursor.__aenter__ = AsyncMock(return_value=mock_cursor)
    mock_cursor.__aexit__ = AsyncMock()

    # Mock execute 返回 None（用户不存在）
    mock_cursor.fetchone = AsyncMock(return_value=None)
    mock_cursor.execute = AsyncMock()
    mock_conn.commit = AsyncMock()

    mysql_manager.pool = mock_pool

    # 执行注册
    result = await mysql_manager.register_user(
        user_id=123456, username="testuser", first_name="Test", is_admin=False
    )

    assert result is True

    # 验证 execute 被调用两次（检查用户 + 插入用户）
    assert mock_cursor.execute.call_count == 2

    # 获取 INSERT 语句的调用参数
    insert_call = mock_cursor.execute.call_args_list[1]
    query = insert_call[0][0]
    params = insert_call[0][1]

    # 验证 SQL 语句
    assert "INSERT INTO users" in query
    assert "registered_at" in query
    assert "last_active" in query

    # 验证参数类型：应该是 datetime 对象，不是字符串
    assert isinstance(params[3], datetime), "registered_at 应该是 datetime 对象"
    assert isinstance(params[4], datetime), "last_active 应该是 datetime 对象"

    # 验证 datetime 对象有时区信息
    assert params[3].tzinfo == UTC, "datetime 应该是 UTC 时区"
    assert params[4].tzinfo == UTC, "datetime 应该是 UTC 时区"


@pytest.mark.asyncio
async def test_register_user_update_existing(mysql_manager):
    """测试更新已存在用户时 datetime 格式正确"""
    # Mock 连接池和游标
    mock_pool = MagicMock()
    mock_conn = MagicMock()
    mock_cursor = MagicMock()

    mock_pool.acquire = MagicMock(return_value=mock_conn)
    mock_conn.__aenter__ = AsyncMock(return_value=mock_conn)
    mock_conn.__aexit__ = AsyncMock()
    mock_conn.cursor = MagicMock(return_value=mock_cursor)
    mock_cursor.__aenter__ = AsyncMock(return_value=mock_cursor)
    mock_cursor.__aexit__ = AsyncMock()

    # Mock execute 返回已注册时间（用户已存在）
    existing_time = datetime(2026, 1, 1, 0, 0, 0, tzinfo=UTC)
    mock_cursor.fetchone = AsyncMock(return_value=(existing_time,))
    mock_cursor.execute = AsyncMock()
    mock_conn.commit = AsyncMock()

    mysql_manager.pool = mock_pool

    # 执行注册（应该更新）
    result = await mysql_manager.register_user(
        user_id=123456, username="testuser", first_name="Test", is_admin=False
    )

    assert result is True

    # 获取 UPDATE 语句的调用参数
    update_call = mock_cursor.execute.call_args_list[1]
    query = update_call[0][0]
    params = update_call[0][1]

    # 验证 SQL 语句
    assert "UPDATE users" in query
    assert "last_active" in query

    # 验证参数类型：应该是 datetime 对象
    assert isinstance(params[2], datetime), "last_active 应该是 datetime 对象"
    assert params[2].tzinfo == UTC, "datetime 应该是 UTC 时区"


@pytest.mark.asyncio
async def test_update_user_activity_datetime_format(mysql_manager):
    """测试更新用户活跃时间时 datetime 格式正确"""
    # Mock 连接池和游标
    mock_pool = MagicMock()
    mock_conn = MagicMock()
    mock_cursor = MagicMock()

    mock_pool.acquire = MagicMock(return_value=mock_conn)
    mock_conn.__aenter__ = AsyncMock(return_value=mock_conn)
    mock_conn.__aexit__ = AsyncMock()
    mock_conn.cursor = MagicMock(return_value=mock_cursor)
    mock_cursor.__aenter__ = AsyncMock(return_value=mock_cursor)
    mock_cursor.__aexit__ = AsyncMock()

    mock_cursor.execute = AsyncMock()
    mock_conn.commit = AsyncMock()

    mysql_manager.pool = mock_pool

    # 执行更新
    result = await mysql_manager.update_user_activity(user_id=123456)

    assert result is True

    # 获取调用参数
    call_args = mock_cursor.execute.call_args
    query = call_args[0][0]
    params = call_args[0][1]

    # 验证 SQL 语句
    assert "UPDATE users" in query
    assert "last_active" in query

    # 验证参数类型：应该是 datetime 对象
    assert isinstance(params[0], datetime), "last_active 应该是 datetime 对象"
    assert params[0].tzinfo == UTC, "datetime 应该是 UTC 时区"


@pytest.mark.asyncio
async def test_update_channel_profile_datetime_format(mysql_manager):
    """测试更新频道画像时 datetime 格式正确"""
    # Mock 连接池和游标
    mock_pool = MagicMock()
    mock_conn = MagicMock()
    mock_cursor = MagicMock()

    mock_pool.acquire = MagicMock(return_value=mock_conn)
    mock_conn.__aenter__ = AsyncMock(return_value=mock_conn)
    mock_conn.__aexit__ = AsyncMock()
    mock_conn.cursor = MagicMock(return_value=mock_cursor)
    mock_cursor.__aenter__ = AsyncMock(return_value=mock_cursor)
    mock_cursor.__aexit__ = AsyncMock()

    # Mock 用户不存在
    mock_cursor.fetchone = AsyncMock(return_value=None)
    mock_cursor.execute = AsyncMock()
    mock_conn.commit = AsyncMock()

    mysql_manager.pool = mock_pool

    # 执行更新（新建）
    await mysql_manager.update_channel_profile(
        channel_id="https://t.me/test",
        channel_name="Test Channel",
        keywords=["AI", "Python"],
        topics=["技术"],
        sentiment="positive",
    )

    # 验证 execute 被调用了3次（查询现有 + 查询统计 + 插入/更新）
    assert mock_cursor.execute.call_count >= 3

    # 获取最后一次调用（INSERT 或 UPDATE）
    last_call = mock_cursor.execute.call_args_list[-1]
    query = last_call[0][0]
    params = last_call[0][1]

    # 验证 SQL 语句包含 last_updated
    assert "last_updated" in query

    # 找到 last_updated 参数的位置
    # INSERT 或 UPDATE 语句的最后一个参数是 last_updated
    last_updated_param = params[-1]

    # 验证参数类型：应该是 datetime 对象
    assert isinstance(last_updated_param, datetime), "last_updated 应该是 datetime 对象"
    assert last_updated_param.tzinfo == UTC, "datetime 应该是 UTC 时区"


def test_datetime_not_isoformat_string():
    """验证 datetime 对象不会被错误转换为 isoformat 字符串"""
    now = datetime.now(UTC)

    # 正确：datetime 对象
    assert isinstance(now, datetime)
    assert now.tzinfo == UTC

    # 错误：不应该使用 isoformat() 作为数据库参数
    # isoformat 会生成字符串如 "2026-02-24T12:50:02+00:00"
    # MySQL DATETIME 不接受这种格式
    iso_string = now.isoformat()
    assert isinstance(iso_string, str)
    assert "+" in iso_string or "Z" in iso_string  # 包含时区信息

    # 验证区别
    assert now != iso_string
    assert type(now) is not type(iso_string)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
