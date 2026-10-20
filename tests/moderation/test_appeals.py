# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""复核权限、恢复保护和助手消息路由的边界测试。"""

from contextlib import asynccontextmanager
from copy import deepcopy
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from telegram.ext import ApplicationHandlerStop

from core.handlers.appeal_handler import AppealHandler
from core.i18n.appeals import APPEAL_EN_US, APPEAL_ZH_CN
from core.moderation.appeal_inbox import AppealInbox
from core.moderation.appeal_ui import buttons, card
from core.moderation.appeals import AppealWorkflow, ban_context, source_signature
from core.moderation.matcher import Match
from core.moderation.moderator import GuardError

from .conftest import ADMIN, CHAT, USER


@pytest.fixture
async def appeal_boundary(guard):
    async with guard.repository.lock(USER) as source_store:
        case = await guard.punish(
            source_store,
            CHAT,
            guard.client.users[USER],
            Match("nickname", "nick", "reason", "original", "normalized"),
            "appeal",
        )
    appeal = {
        "id": "a" * 32,
        "case_id": case["id"],
        "chat_id": CHAT,
        "user_id": USER,
        "status": "executing",
        "revision": 3,
        "error": None,
        "payload": {
            "source": deepcopy(case),
            "source_signature": source_signature(case),
            "evidence": [{"text": "请复核这个封禁记录，我已修正昵称。", "files": []}],
            "decision": {
                "actor_id": ADMIN,
                "action": "approve",
                "reason": "已核实",
                "ban": ban_context(await guard.actions.permissions(CHAT, USER)),
            },
        },
    }
    store = SimpleNamespace(
        guard=source_store,
        get=AsyncMock(return_value=appeal),
        finish_approval=AsyncMock(),
        transition=AsyncMock(),
        execution_error=AsyncMock(),
        notices=AsyncMock(
            return_value=[
                {"kind": "reviewer", "target_id": ADMIN, "message_id": 90, "synced_revision": 3}
            ]
        ),
    )

    @asynccontextmanager
    async def lock(user_id):
        yield store

    repository = SimpleNamespace(lock=lock)
    workflow = AppealWorkflow(guard, repository)
    return guard, workflow, store, appeal


async def test_approval_only_finishes_after_telegram_unban(appeal_boundary):
    guard, workflow, store, appeal = appeal_boundary
    await workflow.execute(store, appeal)
    assert not guard.client.member_rights[CHAT, USER]["view_messages"]
    store.finish_approval.assert_awaited_once()
    store.transition.assert_not_awaited()


async def test_rpc_failure_keeps_intent_and_never_reports_completion(appeal_boundary):
    guard, workflow, store, appeal = appeal_boundary
    guard.client.fail["unban"] = PermissionError("missing Telegram rights")
    await workflow.execute(store, appeal)
    store.finish_approval.assert_not_awaited()
    store.execution_error.assert_awaited_once()
    assert guard.client.member_rights[CHAT, USER]["view_messages"]


async def test_revoked_reviewer_cannot_finish_a_durable_intent(appeal_boundary):
    guard, workflow, store, appeal = appeal_boundary
    guard.default_admins = lambda: []
    await workflow.execute(store, appeal)
    store.finish_approval.assert_not_awaited()
    assert store.transition.call_args.args[1] == "pending"
    assert store.transition.call_args.kwargs["error"] == "error.permission_denied"
    assert guard.client.member_rights[CHAT, USER]["view_messages"]


async def test_new_manual_ban_is_not_removed_by_old_intent(appeal_boundary):
    guard, workflow, store, appeal = appeal_boundary
    guard.client.ban_actors[CHAT, USER] = 12345
    await workflow.execute(store, appeal)
    store.finish_approval.assert_not_awaited()
    assert store.transition.call_args.kwargs["error"] == "guard.permissions_changed"
    assert guard.client.member_rights[CHAT, USER]["view_messages"]


async def test_updated_original_case_invalidates_intent(appeal_boundary):
    guard, workflow, store, appeal = appeal_boundary
    guard.repository.store.cases[appeal["case_id"]]["status"] = "allowed"
    await workflow.execute(store, appeal)
    assert store.transition.call_args.args[1] == "superseded"
    store.finish_approval.assert_not_awaited()


async def test_local_reviewer_cannot_remove_global_blacklist(appeal_boundary):
    guard, workflow, store, appeal = appeal_boundary
    guard.group(CHAT).reviewer_ids = [ADMIN]
    guard.default_admins = lambda: [11]
    appeal["payload"]["decision"]["action"] = "global"
    await workflow.execute(store, appeal)
    assert store.transition.call_args.kwargs["error"] == "appeal.global_admin_required"
    store.finish_approval.assert_not_awaited()


@pytest.mark.parametrize(
    "actor,revision,origin,error",
    [
        (9999, 3, (ADMIN, 90), "error.permission_denied"),
        (USER, 3, (ADMIN, 90), "error.permission_denied"),
        (ADMIN, 2, (ADMIN, 90), "appeal.stale"),
        (ADMIN, 3, (ADMIN, 91), "guard.invalid_callback"),
        (11, 3, (ADMIN, 90), "guard.invalid_callback"),
    ],
)
async def test_review_rechecks_actor_revision_and_delivered_message(
    appeal_boundary, actor, revision, origin, error
):
    _, workflow, store, appeal = appeal_boundary
    appeal["status"] = "pending"
    request = {
        "actor_id": actor,
        "revision": revision,
        "action": "approve",
        "origin_chat_id": origin[0],
        "origin_message_id": origin[1],
    }
    with pytest.raises(GuardError, match=error):
        await workflow.check_request(store, appeal, request)


def test_cards_escape_untrusted_material_and_use_bounded_callbacks(appeal_boundary):
    _, _, _, appeal = appeal_boundary
    appeal["status"] = "pending"
    appeal["payload"]["source"]["payload"]["chat_title"] = "<script>&群组"
    appeal["payload"]["evidence"][0]["text"] = '<a href="https://evil">证据</a>'
    text = card(appeal, reviewer=True)
    assert "<script>" not in text and "&lt;script&gt;" in text
    assert '<a href="https://evil">' not in text
    for row in buttons(appeal, reviewer=True).inline_keyboard:
        assert all(len(item.callback_data.encode()) <= 64 for item in row)
    assert APPEAL_EN_US.keys() == APPEAL_ZH_CN.keys()


def make_update(text, *, private=True):
    return SimpleNamespace(
        effective_user=SimpleNamespace(id=USER, is_bot=False),
        effective_chat=SimpleNamespace(
            id=USER if private else CHAT, type="private" if private else "supergroup"
        ),
        message=SimpleNamespace(
            text=text,
            caption=None,
            message_id=123,
            reply_text=AsyncMock(),
            photo=None,
            document=None,
            video=None,
        ),
    )


async def test_active_appeal_text_stops_before_qa_or_submission_handlers():
    entry = {
        "actor_id": USER,
        "token": "b" * 32,
        "appeal_id": "a" * 32,
        "mode": "draft",
        "payload": {"text": "理由", "files": []},
    }
    repository = SimpleNamespace(input=AsyncMock(return_value=entry))
    handler = AppealHandler(repository)
    handler.inbox.append = AsyncMock(return_value=entry)
    context = SimpleNamespace(bot=SimpleNamespace(send_message=AsyncMock()))
    with pytest.raises(ApplicationHandlerStop):
        await handler.message(make_update("这是我的申诉材料，请管理员核实。"), context)
    handler.inbox.append.assert_awaited_once()


async def test_normal_private_question_still_reaches_qa():
    repository = SimpleNamespace(input=AsyncMock(return_value=None))
    handler = AppealHandler(repository)
    await handler.message(make_update("今天有什么新闻？"), SimpleNamespace())


async def test_group_appeal_does_not_accept_evidence():
    repository = SimpleNamespace(input=AsyncMock())
    handler = AppealHandler(repository)
    update = make_update("/appeal", private=False)
    with pytest.raises(ApplicationHandlerStop):
        await handler.message(update, SimpleNamespace())
    repository.input.assert_not_awaited()
    update.message.reply_text.assert_awaited_once()


async def test_wrong_telegram_identity_cannot_open_another_accounts_appeal():
    with pytest.raises(GuardError):
        AppealInbox.own({"user_id": USER}, USER + 1)


async def test_unavailable_store_does_not_send_possible_evidence_to_qa():
    repository = SimpleNamespace(input=AsyncMock(side_effect=ConnectionError("database offline")))
    handler = AppealHandler(repository)
    update = make_update("这里是我的申诉证据。")
    with pytest.raises(ApplicationHandlerStop):
        await handler.message(update, SimpleNamespace())
    update.message.reply_text.assert_awaited_once()
