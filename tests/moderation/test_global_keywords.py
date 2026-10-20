# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
from copy import deepcopy
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import pytest
from pydantic import ValidationError

from core.commands import guard_commands
from core.config import ConfigChangedEvent
from core.config.persistence import ConfigConflictError, ConfigPersistenceError
from core.initializers import moderation_initializer
from core.moderation.config import DEFAULT_WORDS, GuardConfig, GuardGroup, migrate_nickname_keywords
from core.web_api.routes import guard as routes

from .conftest import ADMIN, CHAT, USER
from .test_config_api import api as api
from .test_config_api import config_store as config_store


def test_legacy_union_preserves_order_and_all_group_settings_without_mutating_input():
    source = {
        "groups": {
            str(CHAT): {"enabled": True, "nickname_keywords": ["甲", "乙", "甲"]},
            str(CHAT - 1): {"nickname_keywords": ["乙", "丙"], "nickname_action": "warn"},
            str(CHAT - 2): {"nickname_keywords": [], "nickname_filter": False},
        },
        "retention_days": 123,
    }
    before = deepcopy(source)
    config = GuardConfig.model_validate(source)
    assert config.nickname_keywords == ["甲", "乙", "丙"]
    assert source == before
    assert config.groups[str(CHAT)].enabled
    assert config.groups[str(CHAT - 1)].nickname_action == "warn"
    assert not config.groups[str(CHAT - 2)].nickname_filter
    assert all("nickname_keywords" not in group for group in config.model_dump()["groups"].values())


def test_missing_legacy_words_keep_old_defaults_but_explicit_empty_global_is_authoritative():
    assert (
        GuardConfig.model_validate({"groups": {str(CHAT): {}}}).nickname_keywords == DEFAULT_WORDS
    )
    assert (
        GuardConfig.model_validate(
            {"groups": {str(CHAT): {"nickname_keywords": []}}}
        ).nickname_keywords
        == []
    )
    assert (
        GuardConfig.model_validate(
            {"nickname_keywords": [], "groups": {str(CHAT): {"nickname_keywords": ["旧词"]}}}
        ).nickname_keywords
        == []
    )
    words = [f"词{i}" for i in range(1100)]
    groups = {
        str(CHAT - i): {"nickname_keywords": words[i * 100 : (i + 1) * 100]} for i in range(11)
    }
    assert GuardConfig.model_validate({"groups": groups}).nickname_keywords == words


@pytest.mark.parametrize("words", [None, "spam", [True], [""], [" "], ["a" * 129]])
def test_invalid_global_and_legacy_lists_are_rejected(words):
    for data in (
        {"nickname_keywords": words},
        {"groups": {str(CHAT): {"nickname_keywords": words}}},
    ):
        with pytest.raises(ValidationError):
            GuardConfig.model_validate(data)


def test_migration_only_changes_keyword_paths_and_preserves_concurrent_unrelated_edits(
    config_store,
):
    persistence, original = config_store
    snapshot = persistence.read()
    snapshot["sakura_guard"] = {
        "groups": {str(CHAT): {"nickname_keywords": ["词"], "enabled": True}}
    }
    persistence.save_changes(snapshot)
    snapshot = persistence.read()
    assert migrate_nickname_keywords(snapshot)
    concurrent = persistence.read()
    concurrent["custom"]["nested"] = [42]
    concurrent["sakura_guard"]["groups"][str(CHAT)]["nickname_action"] = "warn"
    persistence.save_changes(concurrent)
    saved = persistence.save_changes(snapshot)
    assert saved["sakura_guard"] == {
        "nickname_keywords": ["词"],
        "groups": {str(CHAT): {"enabled": True, "nickname_action": "warn"}},
    }
    assert saved["custom"]["nested"] == [42]
    assert saved["channels"] == original["channels"]
    assert not migrate_nickname_keywords(saved)
    assert persistence.backup_path.exists()


def test_legacy_edit_during_migration_is_not_lost(config_store):
    persistence, _ = config_store
    snapshot = persistence.read()
    snapshot["sakura_guard"] = {"groups": {str(CHAT): {"nickname_keywords": ["甲"]}}}
    persistence.save_changes(snapshot)
    migration = persistence.read()
    migrate_nickname_keywords(migration)
    concurrent = persistence.read()
    concurrent["sakura_guard"]["groups"][str(CHAT)]["nickname_keywords"] = ["乙"]
    persistence.save_changes(concurrent)
    with pytest.raises(ConfigConflictError):
        persistence.save_changes(migration)
    assert persistence.read()["sakura_guard"]["groups"][str(CHAT)]["nickname_keywords"] == ["乙"]


def test_migration_deduplicates_existing_global_words_without_resurrecting_legacy_words():
    snapshot = {
        "sakura_guard": {
            "nickname_keywords": ["新词", "新词"],
            "groups": {str(CHAT): {"nickname_keywords": ["旧词"]}},
        }
    }
    assert migrate_nickname_keywords(snapshot)
    assert snapshot["sakura_guard"]["nickname_keywords"] == ["新词"]
    assert "nickname_keywords" not in snapshot["sakura_guard"]["groups"][str(CHAT)]
    assert not migrate_nickname_keywords(snapshot)


def test_global_api_clears_persistently_and_new_groups_share_it(api, guard, config_store):
    persistence, original = config_store
    body = {"nickname_keywords": ["共享", "共享"], "previous": DEFAULT_WORDS}
    result = api.patch("/api/guard/nickname-keywords", json=body)
    assert result.status_code == 200
    assert result.json()["nickname_keywords"] == ["共享"]
    assert api.post("/api/guard/groups", json={"chat_id": CHAT}).status_code == 200
    result = api.get("/api/guard/config").json()
    assert result["config"]["nickname_keywords"] == ["共享"]
    assert "nickname_keywords" not in result["defaults"]
    assert "nickname_keywords" not in result["config"]["groups"][str(CHAT)]
    assert (
        api.patch(
            "/api/guard/nickname-keywords", json={"nickname_keywords": [], "previous": ["共享"]}
        ).status_code
        == 200
    )
    assert api.post("/api/guard/groups", json={"chat_id": CHAT - 1}).status_code == 200
    guard.reload(persistence.read())
    assert guard.config.nickname_keywords == []
    for key, value in original.items():
        assert persistence.read()[key] == value


def test_global_api_stale_edit_and_explicit_retry(api, guard, config_store):
    persistence, _ = config_store
    assert (
        api.patch(
            "/api/guard/nickname-keywords",
            json={"nickname_keywords": ["远端"], "previous": DEFAULT_WORDS},
        ).status_code
        == 200
    )
    body = {"nickname_keywords": ["草稿"], "previous": DEFAULT_WORDS}
    result = api.patch("/api/guard/nickname-keywords", json=body)
    assert result.status_code == 409
    assert result.json()["detail"]["current"] == {"nickname_keywords": ["远端"]}
    assert persistence.read()["sakura_guard"]["nickname_keywords"] == ["远端"]
    body["previous"] = ["远端"]
    assert api.patch("/api/guard/nickname-keywords", json=body).status_code == 200
    assert guard.config.nickname_keywords == ["草稿"]


@pytest.mark.parametrize("conflict", [False, True])
def test_global_api_handles_concurrent_commits(api, guard, config_store, monkeypatch, conflict):
    persistence, _ = config_store
    api.post("/api/guard/groups", json={"chat_id": CHAT})

    def concurrent_save(snapshot):
        other = persistence.read()
        if conflict:
            other["sakura_guard"]["nickname_keywords"] = ["同时保存"]
        else:
            other["sakura_guard"]["groups"][str(CHAT)]["nickname_action"] = "warn"
        persistence.save_changes(other)
        return persistence.save_changes(snapshot)

    monkeypatch.setattr(routes, "save_config_changes", concurrent_save)
    result = api.patch(
        "/api/guard/nickname-keywords",
        json={"nickname_keywords": ["页面"], "previous": DEFAULT_WORDS},
    )
    assert result.status_code == (409 if conflict else 200)
    if conflict:
        assert result.json()["detail"]["current"]["nickname_keywords"] == ["同时保存"]
    else:
        assert guard.group(CHAT).nickname_action == "warn"
        assert guard.config.nickname_keywords == ["页面"]


def test_global_api_rejects_invalid_unauthorized_and_stale_group_writes(api, guard, config_store):
    persistence, original = config_store
    for words in [None, [True], [""], ["x" * 129]]:
        assert (
            api.patch(
                "/api/guard/nickname-keywords",
                json={"nickname_keywords": words, "previous": DEFAULT_WORDS},
            ).status_code
            == 422
        )
    for actor in [USER, "dev"]:
        assert (
            api.patch(
                "/api/guard/nickname-keywords",
                json={"nickname_keywords": [], "previous": DEFAULT_WORDS},
                headers={"x-test-user": str(actor)},
            ).status_code
            == 403
        )
    assert (
        api.put(f"/api/guard/groups/{CHAT}", json={"nickname_keywords": ["旧界面"]}).status_code
        == 422
    )
    assert (
        api.patch(
            f"/api/guard/groups/{CHAT}/settings",
            json={
                "changes": {"nickname_keywords": ["旧界面"]},
                "previous": {"nickname_keywords": []},
            },
        ).status_code
        == 422
    )
    guard.default_admins = lambda: []
    assert (
        api.patch(
            "/api/guard/nickname-keywords",
            json={"nickname_keywords": [], "previous": DEFAULT_WORDS},
        ).status_code
        == 403
    )
    assert persistence.read() == original


def test_global_api_write_failure_preserves_runtime(api, guard, config_store, monkeypatch):
    persistence, original = config_store
    before = guard.config.nickname_keywords.copy()

    def reject(_):
        raise ConfigPersistenceError("保存失败")

    monkeypatch.setattr(routes, "save_config_changes", reject)
    assert (
        api.patch(
            "/api/guard/nickname-keywords",
            json={"nickname_keywords": [], "previous": DEFAULT_WORDS},
        ).status_code
        == 503
    )
    assert guard.config.nickname_keywords == before
    assert persistence.read() == original


async def test_matching_uses_global_words_and_preserves_group_actions_filters_and_exemptions(
    guard, store
):
    guard.config.nickname_keywords = ["共同词"]
    guard.config.groups[str(CHAT - 1)] = GuardGroup(enabled=True, nickname_action="warn")
    user = guard.client.users[USER]
    user.first_name = "共同词"
    assert (await guard.match(store, guard.group(CHAT), CHAT, user)).action == "ban"
    assert (await guard.match(store, guard.group(CHAT - 1), CHAT - 1, user)).action == "warn"
    user.first_name, user.username = "普通成员", "共同词"
    assert (await guard.match(store, guard.group(CHAT), CHAT, user)).target == "username"
    guard.group(CHAT - 1).nickname_filter = False
    assert await guard.match(store, guard.group(CHAT - 1), CHAT - 1, user) is None
    store.white.add((CHAT, USER))
    assert await guard.match(store, guard.group(CHAT), CHAT, user) is None
    store.white.clear()
    guard.config.nickname_keywords = []
    assert await guard.match(store, guard.group(CHAT), CHAT, user) is None


async def test_global_hot_reload_changes_matching_in_all_groups(guard, store):
    initializer = moderation_initializer.ModerationInitializer()
    initializer.service = guard
    config = {
        "sakura_guard": {"nickname_keywords": ["热更新"], "groups": {str(CHAT): {"enabled": True}}}
    }
    await initializer.on_config_changed(ConfigChangedEvent(config=config, version=1))
    user = guard.client.users[USER]
    user.first_name = "热更新"
    assert await guard.match(store, guard.group(CHAT), CHAT, user)
    config["sakura_guard"]["nickname_keywords"] = []
    await initializer.on_config_changed(ConfigChangedEvent(config=config, version=2))
    assert await guard.match(store, guard.group(CHAT), CHAT, user) is None


@pytest.mark.parametrize("command", ["/guard_addword", "/guard_delword"])
async def test_global_commands_work_in_private_chat_and_reject_group_reviewers(
    guard, config_store, monkeypatch, command
):
    persistence, _ = config_store
    monkeypatch.setattr(guard_commands, "get_guard", lambda: guard)
    guard.group(CHAT).reviewer_ids = [USER]
    event = SimpleNamespace(
        sender_id=USER, chat_id=CHAT, raw_text=f"{command} 测试词", reply=AsyncMock()
    )
    before = persistence.read()
    await guard_commands.handle_guard(event)
    assert "没有权限" in event.reply.await_args.args[0]
    assert persistence.read() == before
    event.sender_id, event.chat_id = ADMIN, ADMIN
    await guard_commands.handle_guard(event)
    assert ("测试词" in guard.config.nickname_keywords) is (command == "/guard_addword")
    assert "全局" in event.reply.await_args.args[0]
    # 旧版指定群 ID 的写法也修改同一份全局词表。
    event.raw_text = f"/guard_addword {CHAT} 兼容词"
    await guard_commands.handle_guard(event)
    assert "兼容词" in persistence.read()["sakura_guard"]["nickname_keywords"]


@pytest.mark.parametrize("fail_save", [False, True])
async def test_startup_migrates_legacy_config_and_write_failure_keeps_guard_active(
    guard, config_store, monkeypatch, fail_save
):
    persistence, _ = config_store
    snapshot = persistence.read()
    snapshot["sakura_guard"] = {
        "groups": {str(CHAT): {"enabled": True, "nickname_keywords": ["迁移词"]}}
    }
    persistence.save_changes(snapshot)
    guard.repository.initialize = AsyncMock()
    guard.start = AsyncMock()
    monkeypatch.setattr(moderation_initializer, "GuardRepository", lambda _: guard.repository)
    monkeypatch.setattr(moderation_initializer, "Moderator", lambda *_: guard)
    monkeypatch.setattr(
        moderation_initializer,
        "AppealWorkflow",
        lambda _: SimpleNamespace(routing_changed=MagicMock()),
    )
    monkeypatch.setattr(moderation_initializer, "read_config", persistence.read)

    def save(config):
        if fail_save:
            raise ConfigPersistenceError("磁盘写入失败")
        return persistence.save_changes(config)

    monkeypatch.setattr(moderation_initializer, "save_config_changes", save)
    initializer = moderation_initializer.ModerationInitializer()
    await initializer.initialize(guard.client, SimpleNamespace(pool=object()))
    assert initializer.service is guard
    guard.start.assert_awaited_once()
    assert guard.config.nickname_keywords == ["迁移词"]
    assert guard.group(CHAT).enabled
    assert ("nickname_keywords" in persistence.read()["sakura_guard"]) is not fail_save
