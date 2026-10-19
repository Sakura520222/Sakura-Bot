# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import pytest
from telethon.tl.types import InputMessageEntityMentionName, MessageEntityTextUrl

from core.i18n.i18n import set_language
from core.moderation.notification import notification_text

from .conftest import ADMIN, CHAT, USER
from .test_verification import begin
from .test_verification_links import start_case


def profile_reference(message, case):
    assert message["text"] == notification_text(case)
    mentions = [
        entity
        for entity in message["entities"]
        if isinstance(entity, InputMessageEntityMentionName)
    ]
    assert len(mentions) == 1
    mention = mentions[0]
    assert mention.user_id.user_id == USER
    assert mention.user_id.access_hash == 12345
    assert bytes(mention)  # 输入用户引用必须能真正序列化成 MTProto 请求。
    label = (
        message["text"]
        .encode("utf-16-le")[mention.offset * 2 : (mention.offset + mention.length) * 2]
        .decode("utf-16-le")
    )
    assert label == (case["payload"].get("name") or str(USER))
    assert all(
        not entity.url.startswith("tg://user?")
        for entity in message["entities"]
        if isinstance(entity, MessageEntityTextUrl)
    )


@pytest.mark.parametrize("language", ["zh-CN", "en-US"])
@pytest.mark.parametrize(
    "username,name",
    [
        (None, "🌸 无用户名 🧑‍💻"),
        ("alice", '<a href="https://evil.example">Alice</a> & **管理员**'),
        (None, ""),
        (None, "测试群"),  # 与前面的群名一致时，引用仍必须落在用户姓名上。
    ],
)
async def test_notification_name_opens_id_based_profile(guard, store, language, username, name):
    set_language(language)
    guard.client.users[USER].username = username
    guard.client.users[USER].first_name = name
    await begin(guard, store)
    case = start_case(store)
    for notice in await store.notices(case["id"]):
        message = guard.client.sent[(notice["target_id"], notice["message_id"])]
        profile_reference(message, case)
        links = {
            entity.url for entity in message["entities"] if isinstance(entity, MessageEntityTextUrl)
        }
        assert case["payload"]["chat_link"] in links
        assert case["payload"]["evidence_link"] in links


async def test_edit_preserves_profile_reference_and_existing_admin_controls(guard, store):
    guard.client.users[USER].username = None
    await begin(guard, store)
    case = start_case(store)
    before = await store.notices(case["id"])
    old_buttons = guard.client.sent[(ADMIN, before[0]["message_id"])]["buttons"]
    case["payload"]["evidence"]["reason"] = '<b>更新 & "原因"</b>'
    await store.update_case(case["id"], case["status"], payload=case["payload"])
    for notice in before:
        await store.notice(case["id"], notice["target_id"], notice["message_id"], None)
    await guard.notifier.sync(store, await store.case(case["id"]), guard.group(CHAT))
    for notice in await store.notices(case["id"]):
        message = guard.client.sent[(notice["target_id"], notice["message_id"])]
        profile_reference(message, case)
        assert message["buttons"] == old_buttons
    assert {(r["target_id"], r["message_id"]) for r in before} == {
        (r["target_id"], r["message_id"]) for r in await store.notices(case["id"])
    }


async def test_review_chat_fallback_contains_the_same_user_reference(guard, store):
    guard.group(CHAT).notification_ids = [ADMIN]
    guard.group(CHAT).review_chat_id = -1009999999999
    guard.client.users[USER].username = None
    guard.client.fail[ADMIN] = RuntimeError("private messages unavailable")
    await begin(guard, store)
    case = start_case(store)
    notice = next(row for row in await store.notices(case["id"]) if row["target_id"] != ADMIN)
    profile_reference(guard.client.sent[(notice["target_id"], notice["message_id"])], case)


async def test_inaccessible_user_does_not_prevent_admin_notification(guard, store):
    guard.client.users[USER].username = None
    guard.client.fail["profile"] = ValueError("user entity inaccessible")
    await begin(guard, store)
    case = start_case(store)
    for notice in await store.notices(case["id"]):
        assert not notice["error"]
        assert notice["synced_status"] == case["status"]
        message = guard.client.sent[(notice["target_id"], notice["message_id"])]
        assert message["text"] == notification_text(case)
        assert str(USER) in message["text"]
        assert not any(
            isinstance(entity, InputMessageEntityMentionName) for entity in message["entities"]
        )
