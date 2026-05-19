# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
测试 QA Bot 投稿流程兜底处理。
"""

import os
from unittest.mock import AsyncMock, MagicMock

import pytest

os.environ.setdefault("QA_BOT_TOKEN", "123456:TEST_TOKEN")

import qa_bot
from core.handlers.submission_handler import SubmissionHandler
from core.telegram.keyboards import SUBMIT_MENU_CANCEL, SUBMIT_MENU_SKIP_CONTENT


@pytest.fixture
def bot(mocker):
    """创建屏蔽外部依赖的 QA Bot 实例。"""
    mocker.patch("qa_bot.get_quota_manager", return_value=MagicMock())
    mocker.patch("qa_bot.get_qa_engine_v3", return_value=MagicMock())
    mocker.patch("qa_bot.get_conversation_manager", return_value=MagicMock())
    mocker.patch("qa_bot.get_qa_user_system", return_value=MagicMock())
    return qa_bot.QABot()


def make_update(user_id: int = 123, text: str = SUBMIT_MENU_CANCEL):
    """构造最小 Telegram Update mock。"""
    message = MagicMock()
    message.text = text
    message.reply_text = AsyncMock()

    user = MagicMock()
    user.id = user_id
    user.username = "tester"
    user.first_name = "Test"

    update = MagicMock()
    update.effective_user = user
    update.message = message
    return update


@pytest.mark.unit
@pytest.mark.asyncio
async def test_handle_stray_submission_button_replies_main_menu(bot):
    """测试会话外投稿按钮不会进入 RAG 查询。"""
    update = make_update(text=SUBMIT_MENU_SKIP_CONTENT)

    await bot.handle_stray_submission_button(update, MagicMock())

    update.message.reply_text.assert_awaited_once()
    args, kwargs = update.message.reply_text.await_args
    assert "当前没有进行中的投稿" in args[0]
    assert kwargs["reply_markup"] is not None


@pytest.mark.unit
@pytest.mark.asyncio
async def test_cancel_submission_outside_conversation_clears_state(bot, mocker):
    """测试会话外取消命令会清理投稿状态。"""
    update = make_update(user_id=456, text="/cancel_submit")
    submission_handler = MagicMock()
    submission_handler.clear_user_state = MagicMock()
    mocker.patch(
        "core.handlers.submission_handler.get_submission_handler", return_value=submission_handler
    )

    await bot.cancel_submission_outside_conversation(update, MagicMock())

    submission_handler.clear_user_state.assert_called_once_with(456)
    update.message.reply_text.assert_awaited_once()
    args, kwargs = update.message.reply_text.await_args
    assert "当前没有进行中的投稿" in args[0]
    assert kwargs["reply_markup"] is not None


@pytest.mark.unit
@pytest.mark.asyncio
async def test_start_submission_resets_existing_state(mocker):
    """测试重复开始投稿会重置旧状态并进入标题阶段。"""
    mocker.patch(
        "core.handlers.submission_handler.get_submission_service", return_value=MagicMock()
    )
    mocker.patch("core.handlers.submission_handler.get_submission_repo", return_value=MagicMock())
    handler = SubmissionHandler()
    handler._user_states[123] = {"title": "旧标题"}
    update = make_update(user_id=123, text="/submit")

    result = await handler.start_submission(update, MagicMock())

    assert result == 0
    assert handler._user_states[123] == {
        "title": None,
        "content": None,
        "is_anonymous": False,
        "media_files": [],
    }
    update.message.reply_text.assert_awaited_once()


@pytest.mark.unit
@pytest.mark.asyncio
async def test_receive_title_expired_state_clears_user_state(mocker):
    """测试标题阶段状态丢失时会清理用户投稿状态。"""
    mocker.patch(
        "core.handlers.submission_handler.get_submission_service", return_value=MagicMock()
    )
    mocker.patch("core.handlers.submission_handler.get_submission_repo", return_value=MagicMock())
    handler = SubmissionHandler()
    clear_user_state = mocker.patch.object(handler, "clear_user_state")
    update = make_update(user_id=789, text="标题")

    result = await handler.receive_title(update, MagicMock())

    assert result == -1
    clear_user_state.assert_called_once_with(789)
    update.message.reply_text.assert_awaited_once()
