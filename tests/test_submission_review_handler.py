# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
测试投稿审核强制署名流程。
"""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from core.handlers.submission_review_handler import SubmissionReviewHandler


@pytest.fixture
def handler():
    """创建投稿审核处理器实例。"""
    review_handler = SubmissionReviewHandler()
    review_handler.service = MagicMock()
    review_handler.repo = MagicMock()
    return review_handler


@pytest.mark.unit
def test_build_review_buttons_anonymous_contains_signed_approve(handler):
    """测试匿名投稿审核按钮包含署名通过。"""
    buttons = handler._build_review_buttons(12, can_force_signature=True)

    button_texts = [button.text for row in buttons for button in row]
    button_data = [button.data.decode() for row in buttons for button in row]

    assert "✍️ 署名通过" in button_texts
    assert "submission_signapprove_12" in button_data


@pytest.mark.unit
def test_build_review_buttons_non_anonymous_without_signed_approve(handler):
    """测试非匿名投稿审核按钮不包含署名通过。"""
    buttons = handler._build_review_buttons(12, can_force_signature=False)

    button_texts = [button.text for row in buttons for button in row]
    button_data = [button.data.decode() for row in buttons for button in row]

    assert "✍️ 署名通过" not in button_texts
    assert "submission_signapprove_12" not in button_data


@pytest.mark.unit
def test_build_review_buttons_restore_keeps_signed_approve(handler):
    """测试恢复原文阶段仍保留署名通过按钮。"""
    buttons = handler._build_review_buttons(12, show_restore=True, can_force_signature=True)

    button_texts = [button.text for row in buttons for button in row]
    button_data = [button.data.decode() for row in buttons for button in row]

    assert "✍️ 署名通过" in button_texts
    assert "🔄 恢复原文" in button_texts
    assert "submission_signapprove_12" in button_data


@pytest.mark.unit
def test_can_force_signature_requires_anonymous_and_submitter(handler):
    """测试仅匿名且有投稿者名称时允许强制署名。"""
    assert handler._can_force_signature({"is_anonymous": True, "submitter_name": "alice"})
    assert not handler._can_force_signature({"is_anonymous": False, "submitter_name": "alice"})
    assert not handler._can_force_signature({"is_anonymous": True, "submitter_name": None})
    assert not handler._can_force_signature(None)


@pytest.mark.unit
def test_build_publish_caption_forced_signature_uses_submitter(handler):
    """测试强制署名会覆盖匿名状态。"""
    caption = handler._build_publish_caption(
        {
            "id": 1,
            "title": "测试标题",
            "content": "测试正文",
            "is_anonymous": True,
            "signature_forced": True,
            "submitter_name": "alice",
        }
    )

    assert "**测试标题**" in caption
    assert "测试正文" in caption
    assert "—— 投稿者: @alice" in caption
    assert "投稿者: 匿名" not in caption


@pytest.mark.unit
def test_build_publish_caption_anonymous_without_force_keeps_anonymous(handler):
    """测试普通匿名通过仍显示匿名。"""
    caption = handler._build_publish_caption(
        {
            "id": 1,
            "title": "测试标题",
            "content": "测试正文",
            "is_anonymous": True,
            "signature_forced": False,
            "submitter_name": "alice",
        }
    )

    assert "—— 投稿者: 匿名" in caption
    assert "@alice" not in caption


@pytest.mark.unit
def test_build_publish_caption_forced_signature_without_name_falls_back_anonymous(handler):
    """测试强制署名但投稿者名称为空时回退匿名。"""
    caption = handler._build_publish_caption(
        {
            "id": 1,
            "title": "测试标题",
            "content": "测试正文",
            "is_anonymous": True,
            "signature_forced": True,
            "submitter_name": None,
        }
    )

    assert "—— 投稿者: 匿名" in caption


@pytest.mark.asyncio
@pytest.mark.unit
async def test_handle_callback_signed_approve_dispatches_forced_signature(handler):
    """测试署名通过回调会按强制署名审核。"""
    event = MagicMock()
    event.data = b"submission_signapprove_123"
    event.answer = AsyncMock()
    client = MagicMock()

    handler._handle_approve = AsyncMock()

    await handler.handle_callback(event, client)

    event.answer.assert_awaited_once()
    handler._handle_approve.assert_awaited_once_with(
        event,
        123,
        client,
        signature_forced=True,
    )


@pytest.mark.asyncio
@pytest.mark.unit
async def test_handle_approve_passes_forced_signature_and_notifies(handler):
    """测试署名通过会传递强制署名并通知投稿者。"""
    event = MagicMock()
    event.sender_id = 456
    event.edit = AsyncMock()
    client = MagicMock()
    submission = {
        "id": 123,
        "title": "测试标题",
        "signature_forced": True,
        "submitter_id": 789,
    }

    handler.service.approve_submission = AsyncMock(
        return_value={"success": True, "submission": submission, "message": "投稿已通过"}
    )
    handler._publish_to_channel = AsyncMock(return_value={"success": True, "channel_name": "test"})
    handler._notify_submitter = AsyncMock()

    await handler._handle_approve(event, 123, client, signature_forced=True)

    handler.service.approve_submission.assert_awaited_once_with(
        123,
        reviewed_by=456,
        signature_forced=True,
    )
    handler._notify_submitter.assert_awaited_once_with(
        submission,
        "approved",
        {"success": True, "channel_name": "test"},
        signature_forced=True,
    )
    event.edit.assert_awaited_once()
    assert "已按署名方式发布" in event.edit.await_args.args[0]


@pytest.mark.asyncio
@pytest.mark.unit
async def test_notify_submitter_forced_signature_adds_notice(handler):
    """测试强制署名通知包含说明。"""
    db = MagicMock()
    db.create_notification = AsyncMock()
    submission = {
        "id": 123,
        "title": "测试标题",
        "submitter_id": 789,
    }

    with patch("core.infrastructure.database.manager.get_db_manager", return_value=db):
        await handler._notify_submitter(
            submission,
            "approved",
            {"success": True, "channel_name": "test", "message_url": "https://t.me/test/1"},
            signature_forced=True,
        )

    db.create_notification.assert_awaited_once()
    kwargs = db.create_notification.await_args.kwargs
    assert kwargs["notification_type"] == "submission_approved"
    assert "管理员已将匿名投稿调整为署名发布" in kwargs["content"]["message"]
    assert "https://t.me/test/1" in kwargs["content"]["message"]


@pytest.mark.asyncio
@pytest.mark.unit
async def test_handle_ai_optimize_success_reuses_submission_lookup(handler):
    """测试 AI 优化成功时复用投稿查询结果构建按钮。"""
    event = MagicMock()
    event.edit = AsyncMock()
    handler.repo.get_submission = AsyncMock(
        return_value={"id": 123, "is_anonymous": True, "submitter_name": "alice"}
    )
    handler.service.ai_optimize = AsyncMock(
        return_value={
            "success": True,
            "optimized_title": "优化标题",
            "optimized_content": "优化正文",
        }
    )

    await handler._handle_ai_optimize(event, 123)

    handler.repo.get_submission.assert_awaited_once_with(123)
    assert event.edit.await_count == 2
    _, kwargs = event.edit.await_args
    button_data = [button.data.decode() for row in kwargs["buttons"] for button in row]
    assert "submission_signapprove_123" in button_data


@pytest.mark.asyncio
@pytest.mark.unit
async def test_handle_ai_optimize_failure_reuses_submission_lookup(handler):
    """测试 AI 优化失败时复用投稿查询结果构建按钮。"""
    event = MagicMock()
    event.edit = AsyncMock()
    handler.repo.get_submission = AsyncMock(
        return_value={"id": 123, "is_anonymous": True, "submitter_name": "alice"}
    )
    handler.service.ai_optimize = AsyncMock(return_value={"success": False, "message": "模型错误"})

    await handler._handle_ai_optimize(event, 123)

    handler.repo.get_submission.assert_awaited_once_with(123)
    _, kwargs = event.edit.await_args
    button_data = [button.data.decode() for row in kwargs["buttons"] for button in row]
    assert "submission_signapprove_123" in button_data
