# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""助手启动时先初始化申诉存储，并优先路由申诉输入。"""

import os
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

from telegram.ext import CallbackQueryHandler, MessageHandler

os.environ.setdefault("QA_BOT_TOKEN", "123456:TEST_TOKEN")

import qa_bot


async def test_qa_startup_initializes_appeals_and_registers_priority_handlers(mocker):
    application = MagicMock()
    application.bot.set_my_commands = AsyncMock()
    builder = MagicMock()
    builder.token.return_value.build.return_value = application
    mocker.patch.object(qa_bot.Application, "builder", return_value=builder)
    appeal = SimpleNamespace(
        initialize=AsyncMock(), message=AsyncMock(), callback=AsyncMock(), deliver=AsyncMock()
    )
    mocker.patch("core.handlers.appeal_handler.AppealHandler", return_value=appeal)
    mocker.patch(
        "core.handlers.submission_handler.get_submission_handler", return_value=MagicMock()
    )
    pool = object()
    mocker.patch(
        "core.infrastructure.database.manager.get_db_manager",
        return_value=SimpleNamespace(pool=pool),
    )
    bot = qa_bot.QABot.__new__(qa_bot.QABot)
    bot.initialize_database = AsyncMock()
    bot.run()
    priority = [
        call.args[0]
        for call in application.add_handler.call_args_list
        if call.kwargs.get("group") == -1
    ]
    assert any(
        isinstance(handler, CallbackQueryHandler) and handler.callback is appeal.callback
        for handler in priority
    )
    assert any(
        isinstance(handler, MessageHandler) and handler.callback is appeal.message
        for handler in priority
    )
    await application.post_init(application)
    bot.initialize_database.assert_awaited_once()
    appeal.initialize.assert_awaited_once_with(pool)
    commands = application.bot.set_my_commands.call_args.args[0]
    assert {"appeal", "appeals", "cancel_appeal"} <= {command.command for command in commands}
    assert any(
        call.args[0] is appeal.deliver
        for call in application.job_queue.run_repeating.call_args_list
    )
    application.run_polling.assert_called_once()
