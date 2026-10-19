# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import json
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from core.commands import guard_commands
from core.config import AsyncIOEventBus, ConfigChangedEvent
from core.config.persistence import ConfigPersistenceError, ConfigStore
from core.initializers.moderation_initializer import ModerationInitializer
from core.moderation.config import GuardGroup
from core.moderation.moderator import GuardError
from core.web_api.auth import WebUIUser
from core.web_api.routes import guard as routes

from .conftest import ADMIN, CHAT, USER


@pytest.fixture
def config_store(tmp_path, monkeypatch):
    path = tmp_path / "config.json"
    original = {
        "channels": ["@existing"],
        "api_key": "test-secret",
        "forwarding": {"enabled": True, "rules": [{"id": "existing"}]},
        "custom": {"nested": [1, 2]},
    }
    path.write_text(json.dumps(original), encoding="utf-8")
    store = ConfigStore(path)
    monkeypatch.setattr(guard_commands, "read_config", store.read)
    monkeypatch.setattr(guard_commands, "save_config_changes", store.save_changes)
    monkeypatch.setattr(routes, "read_config", store.read)
    monkeypatch.setattr(routes, "save_config_changes", store.save_changes)
    return store, original


async def test_guard_config_save_preserves_other_settings_and_backups(guard, config_store):
    persistence, original = config_store
    await guard_commands.save_group(
        guard, CHAT, GuardGroup(enabled=True, nickname_keywords=["custom"])
    )
    saved = persistence.read()
    for key, value in original.items():
        assert saved[key] == value
    assert persistence.backup_path.exists()
    assert guard.group(CHAT).nickname_keywords == ["custom"]


async def test_invalid_configuration_is_rejected_without_mutating_live_settings(
    guard, config_store
):
    persistence, _ = config_store
    snapshot = persistence.read()
    snapshot["sakura_guard"] = {"groups": {str(CHAT): {"enabled": "false"}}}
    with pytest.raises(ConfigPersistenceError):
        persistence.save_changes(snapshot)
    assert "sakura_guard" not in persistence.read()
    assert guard.group(CHAT).enabled


async def test_hot_reload_updates_rules_and_missing_section_disables_every_group(guard):
    bus = AsyncIOEventBus()
    initializer = ModerationInitializer(bus)
    initializer.service = guard
    await bus.subscribe(ConfigChangedEvent, initializer.on_config_changed)
    await bus.publish(
        ConfigChangedEvent(
            config={
                "sakura_guard": {
                    "groups": {str(CHAT): {"enabled": True, "nickname_keywords": ["new-word"]}}
                }
            },
            version=1,
        )
    )
    assert guard.group(CHAT).nickname_keywords == ["new-word"]
    await bus.publish(ConfigChangedEvent(config={}, version=2))
    assert not guard.group(CHAT).enabled
    await initializer.shutdown()
    assert not bus._subscribers.get(ConfigChangedEvent)


@pytest.fixture
def api(guard, monkeypatch):
    monkeypatch.setattr(routes, "get_guard", lambda: guard)
    app = FastAPI()

    @app.middleware("http")
    async def authenticated(request, call_next):
        request.state.user = WebUIUser(user_id=request.headers.get("x-test-user", str(ADMIN)))
        return await call_next(request)

    app.include_router(routes.router, prefix="/api/guard")
    with TestClient(app) as client:
        yield client


def test_api_current_admin_authorization_is_rechecked(api, guard):
    assert api.get("/api/guard/config").status_code == 200
    assert api.get("/api/guard/config", headers={"x-test-user": str(USER)}).status_code == 403
    assert api.get("/api/guard/config", headers={"x-test-user": "dev"}).status_code == 403
    assert api.get("/api/guard/config", headers={"x-test-user": "admin"}).status_code == 200
    guard.default_admins = lambda: []
    assert api.get("/api/guard/config").status_code == 403


def test_api_config_validates_types_and_preserves_existing_fields(api, config_store):
    persistence, original = config_store
    assert api.put(f"/api/guard/groups/{CHAT}", json={"enabled": "false"}).status_code == 422
    assert api.put(f"/api/guard/groups/{CHAT}", json=GuardGroup().model_dump()).status_code == 200
    saved = persistence.read()
    for key, value in original.items():
        assert saved[key] == value


@pytest.mark.parametrize(
    "field",
    [
        "enabled",
        "verify_join",
        "nickname_filter",
        "message_filter",
        "block_bot_messages",
        "share_global_blacklist",
        "ban_any_traditional",
        "allow_group_admin_review",
        "verification_notice_cleanup",
    ],
)
@pytest.mark.parametrize("value", [True, False])
def test_group_switch_saves_immediately_without_overwriting_other_settings(
    api, guard, config_store, field, value
):
    persistence, original = config_store
    group = GuardGroup(
        enabled=True,
        nickname_keywords=["已保存关键词"],
        message_keywords=["已保存消息词"],
        verification_timeout=180,
        trusted_bot_ids=[67890],
    )
    setattr(group, field, not value)
    assert api.put(f"/api/guard/groups/{CHAT}", json=group.model_dump()).status_code == 200
    expected = {**group.model_dump(), field: value}
    response = api.patch(f"/api/guard/groups/{CHAT}", json={"field": field, "value": value})
    assert response.status_code == 200
    assert response.json()["group"] == expected
    saved = persistence.read()
    assert saved["sakura_guard"]["groups"][str(CHAT)] == expected
    assert guard.group(CHAT).model_dump() == expected
    guard.reload(saved)
    assert api.get("/api/guard/config").json()["config"]["groups"][str(CHAT)] == expected
    for key, original_value in original.items():
        assert saved[key] == original_value


@pytest.mark.parametrize(
    "body",
    [
        {"field": "enabled", "value": "true"},
        {"field": "enabled", "value": 1},
        {"field": "enabled", "value": None},
        {"field": "enabled"},
        {"field": "unknown", "value": True},
        {"field": "nickname_keywords", "value": True},
        {"field": "enabled", "value": True, "nickname_keywords": ["未保存草稿"]},
    ],
)
def test_group_switch_rejects_untyped_or_unrelated_fields(api, config_store, body):
    persistence, original = config_store
    response = api.patch(f"/api/guard/groups/{CHAT}", json=body)
    assert response.status_code == 422
    assert persistence.read() == original


def test_group_switch_requires_existing_group_valid_id_and_current_admin(api, guard, config_store):
    persistence, original = config_store
    body = {"field": "enabled", "value": True}
    assert api.patch(f"/api/guard/groups/{CHAT}", json=body).status_code == 404
    for chat_id in [0, 123, -(2**63)]:
        assert api.patch(f"/api/guard/groups/{chat_id}", json=body).status_code == 422
    for actor in [str(USER), "dev"]:
        assert (
            api.patch(
                f"/api/guard/groups/{CHAT}", json=body, headers={"x-test-user": actor}
            ).status_code
            == 403
        )
    guard.default_admins = lambda: []
    assert api.patch(f"/api/guard/groups/{CHAT}", json=body).status_code == 403
    assert persistence.read() == original


def test_group_switch_validation_failure_preserves_disk_and_live_state(
    api, guard, config_store, monkeypatch
):
    persistence, _ = config_store
    assert api.post("/api/guard/groups", json={"chat_id": CHAT}).status_code == 200
    before = persistence.read()
    monkeypatch.setattr(
        guard,
        "validate_group",
        AsyncMock(side_effect=GuardError("guard.permissions_required")),
    )
    response = api.patch(f"/api/guard/groups/{CHAT}", json={"field": "enabled", "value": True})
    assert response.status_code == 409
    assert persistence.read() == before
    assert guard.group(CHAT).enabled is False


def test_group_switch_write_failure_reports_error_without_changing_live_state(
    api, guard, config_store, monkeypatch
):
    persistence, _ = config_store
    assert api.post("/api/guard/groups", json={"chat_id": CHAT}).status_code == 200
    before = persistence.read()

    def reject_write(snapshot):
        raise ConfigPersistenceError("保存配置失败，请重试")

    monkeypatch.setattr(routes, "save_config_changes", reject_write)
    response = api.patch(
        f"/api/guard/groups/{CHAT}", json={"field": "block_bot_messages", "value": True}
    )
    assert response.status_code == 503
    assert response.json()["detail"] == "保存配置失败，请重试"
    assert persistence.read() == before
    assert guard.group(CHAT).block_bot_messages is False


def test_group_switch_merges_concurrent_changes_during_permission_check(
    api, guard, config_store, monkeypatch
):
    persistence, _ = config_store
    assert api.post("/api/guard/groups", json={"chat_id": CHAT}).status_code == 200

    async def concurrent_edit(chat_id, group):
        snapshot = persistence.read()
        snapshot["sakura_guard"]["groups"][str(CHAT)]["nickname_keywords"] = ["并发保存"]
        snapshot["custom"]["nested"] = [3, 4]
        persistence.save_changes(snapshot)

    monkeypatch.setattr(guard, "validate_group", concurrent_edit)
    response = api.patch(
        f"/api/guard/groups/{CHAT}", json={"field": "block_bot_messages", "value": True}
    )
    assert response.status_code == 200
    saved = persistence.read()
    assert saved["sakura_guard"]["groups"][str(CHAT)]["nickname_keywords"] == ["并发保存"]
    assert saved["custom"]["nested"] == [3, 4]
    assert guard.group(CHAT).block_bot_messages is True
    assert guard.group(CHAT).nickname_keywords == ["并发保存"]


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("nickname_keywords", ["新增昵称词"]),
        ("message_keywords", ["新增消息词"]),
        ("verification_timeout", 180),
        ("join_requests", "approve"),
        ("message_action", "mute"),
        ("mute_seconds", 900),
        ("trusted_bot_ids", [12345]),
        ("reviewer_ids", [23456]),
        ("notification_ids", [34567]),
        ("nickname_action", "warn"),
        ("bot_message_action", "ban"),
        ("verification_max_attempts", 5),
        ("verification_timeout_action", "kick"),
        ("verification_wrong_action", "allow"),
        ("verification_message_mode", "result"),
        ("verification_admin_actions", []),
        ("join_request_match_action", "manual"),
        ("notification_enabled", False),
        ("verification_notice_cleanup", False),
        ("notification_events", ["verification_end", "admin_action"]),
        ("review_chat_id", CHAT - 1),
        ("review_chat_id", None),
        (
            "rules",
            [
                {
                    "id": "new_rule",
                    "target": "message",
                    "kind": "keyword",
                    "value": "测试",
                    "action": "delete",
                }
            ],
        ),
        ("enabled", True),
    ],
)
def test_field_autosave_persists_only_edited_setting(api, guard, config_store, field, value):
    persistence, original = config_store
    assert api.post("/api/guard/groups", json={"chat_id": CHAT}).status_code == 200
    before = guard.group(CHAT).model_dump()
    response = api.patch(
        f"/api/guard/groups/{CHAT}/settings",
        json={"changes": {field: value}, "previous": {field: before[field]}},
    )
    assert response.status_code == 200
    expected = {**before, field: value}
    assert response.json()["group"] == expected
    assert guard.group(CHAT).model_dump() == expected
    saved = persistence.read()
    assert saved["sakura_guard"]["groups"][str(CHAT)] == expected
    for key, original_value in original.items():
        assert saved[key] == original_value


@pytest.mark.parametrize(
    "body",
    [
        {"changes": {}, "previous": {}},
        {"changes": {"enabled": True}, "previous": {}},
        {"changes": {"enabled": True}, "previous": {"verify_join": False}},
        {"changes": {"nickname_keywords": [""]}, "previous": {"nickname_keywords": []}},
        {"changes": {"trusted_bot_ids": ["123"]}, "previous": {"trusted_bot_ids": []}},
        {"changes": {"verification_timeout": None}, "previous": {"verification_timeout": 120}},
        {"changes": {"verification_timeout": 3}, "previous": {"verification_timeout": 120}},
        {"changes": {"unknown": True}, "previous": {"unknown": False}},
        {"changes": {"enabled": "true"}, "previous": {"enabled": False}},
        {"changes": {"enabled": True}, "previous": {"enabled": False}, "extra": True},
        {
            "changes": {"rules": [{"id": "rule", "kind": "regex", "value": "("}]},
            "previous": {"rules": []},
        },
    ],
)
def test_invalid_autosave_preserves_configuration(api, config_store, body):
    persistence, original = config_store
    response = api.patch(f"/api/guard/groups/{CHAT}/settings", json=body)
    assert response.status_code == 422
    assert persistence.read() == original


def test_autosave_rejects_stale_field_then_accepts_explicit_retry(api, guard, config_store):
    persistence, _ = config_store
    assert api.post("/api/guard/groups", json={"chat_id": CHAT}).status_code == 200
    previous = guard.group(CHAT).nickname_keywords
    changed = persistence.read()
    changed["sakura_guard"]["groups"][str(CHAT)]["nickname_keywords"] = ["其他入口保存"]
    persistence.save_changes(changed)
    body = {
        "changes": {"nickname_keywords": ["页面草稿"]},
        "previous": {"nickname_keywords": previous},
    }
    response = api.patch(f"/api/guard/groups/{CHAT}/settings", json=body)
    assert response.status_code == 409
    conflict = response.json()["detail"]
    assert conflict["fields"] == ["nickname_keywords"]
    assert conflict["current"]["nickname_keywords"] == ["其他入口保存"]
    assert persistence.read()["sakura_guard"]["groups"][str(CHAT)]["nickname_keywords"] == [
        "其他入口保存"
    ]
    body["previous"]["nickname_keywords"] = conflict["current"]["nickname_keywords"]
    response = api.patch(f"/api/guard/groups/{CHAT}/settings", json=body)
    assert response.status_code == 200
    assert guard.group(CHAT).nickname_keywords == ["页面草稿"]


def test_autosave_merges_unrelated_concurrent_updates_but_rejects_same_field(
    api, guard, config_store, monkeypatch
):
    persistence, _ = config_store
    assert api.post("/api/guard/groups", json={"chat_id": CHAT}).status_code == 200
    group = guard.group(CHAT).model_dump()

    async def concurrent_edit(chat_id, updated):
        snapshot = persistence.read()
        snapshot["sakura_guard"]["groups"][str(CHAT)]["message_keywords"] = ["并发消息词"]
        persistence.save_changes(snapshot)

    monkeypatch.setattr(guard, "validate_group", concurrent_edit)
    response = api.patch(
        f"/api/guard/groups/{CHAT}/settings",
        json={
            "changes": {"nickname_keywords": ["昵称新词"]},
            "previous": {"nickname_keywords": group["nickname_keywords"]},
        },
    )
    assert response.status_code == 200
    assert guard.group(CHAT).message_keywords == ["并发消息词"]

    async def conflicting_edit(chat_id, updated):
        snapshot = persistence.read()
        snapshot["sakura_guard"]["groups"][str(CHAT)]["nickname_keywords"] = ["同时保存"]
        persistence.save_changes(snapshot)

    monkeypatch.setattr(guard, "validate_group", conflicting_edit)
    response = api.patch(
        f"/api/guard/groups/{CHAT}/settings",
        json={
            "changes": {"nickname_keywords": ["第二次编辑"]},
            "previous": {"nickname_keywords": ["昵称新词"]},
        },
    )
    assert response.status_code == 409
    assert response.json()["detail"]["current"]["nickname_keywords"] == ["同时保存"]


def test_autosave_requires_existing_group_and_current_administrator(api, guard, config_store):
    persistence, original = config_store
    body = {"changes": {"enabled": True}, "previous": {"enabled": False}}
    assert api.patch(f"/api/guard/groups/{CHAT}/settings", json=body).status_code == 404
    assert api.patch("/api/guard/groups/1/settings", json=body).status_code == 422
    for actor in [str(USER), "dev"]:
        assert (
            api.patch(
                f"/api/guard/groups/{CHAT}/settings", json=body, headers={"x-test-user": actor}
            ).status_code
            == 403
        )
    guard.default_admins = lambda: []
    assert api.patch(f"/api/guard/groups/{CHAT}/settings", json=body).status_code == 403
    assert persistence.read() == original


def test_autosave_failure_does_not_reload_uncommitted_draft(api, guard, config_store, monkeypatch):
    persistence, _ = config_store
    assert api.post("/api/guard/groups", json={"chat_id": CHAT}).status_code == 200
    before = persistence.read()
    previous = guard.group(CHAT).nickname_keywords
    monkeypatch.setattr(
        guard, "validate_group", AsyncMock(side_effect=GuardError("guard.permissions_required"))
    )
    body = {"changes": {"nickname_keywords": ["草稿"]}, "previous": {"nickname_keywords": previous}}
    assert api.patch(f"/api/guard/groups/{CHAT}/settings", json=body).status_code == 409
    assert persistence.read() == before
    assert guard.group(CHAT).nickname_keywords == previous
    monkeypatch.setattr(guard, "validate_group", AsyncMock())

    def reject_write(snapshot):
        raise ConfigPersistenceError("保存配置失败")

    monkeypatch.setattr(routes, "save_config_changes", reject_write)
    assert api.patch(f"/api/guard/groups/{CHAT}/settings", json=body).status_code == 503
    assert persistence.read() == before
    assert guard.group(CHAT).nickname_keywords == previous


def test_retention_autosave_preserves_other_settings_and_checks_baseline(api, guard, config_store):
    persistence, original = config_store
    assert (
        api.patch("/api/guard/retention", json={"retention_days": 100, "previous": 90}).status_code
        == 200
    )
    assert guard.config.retention_days == 100
    assert (
        api.patch("/api/guard/retention", json={"retention_days": 110, "previous": 90}).status_code
        == 409
    )
    assert guard.config.retention_days == 100
    response = api.patch("/api/guard/retention", json={"retention_days": 110, "previous": 100})
    assert response.status_code == 200 and response.json()["retention_days"] == 110
    assert (
        api.patch("/api/guard/retention", json={"retention_days": 1, "previous": 110}).status_code
        == 422
    )
    assert (
        api.patch(
            "/api/guard/retention",
            json={"retention_days": 120, "previous": 110},
            headers={"x-test-user": str(USER)},
        ).status_code
        == 403
    )
    saved = persistence.read()
    for key, value in original.items():
        assert saved[key] == value


def test_api_whitelist_conflict_is_visible_and_fails_closed(api, store):
    store.black[USER] = {"user_id": USER}
    response = api.put("/api/guard/whitelist", json={"chat_id": CHAT, "user_id": USER})
    assert response.status_code == 409
    assert (CHAT, USER) not in store.white


def test_verification_api_never_exposes_correct_answers(api, guard):
    guard.repository.query = AsyncMock(
        return_value={"total": 1, "rows": [{"id": "session", "payload": {"correct": 3}}]}
    )
    response = api.get("/api/guard/records/verifications")
    assert response.status_code == 200
    assert "payload" not in response.json()["rows"][0]


async def test_ordinary_member_cannot_execute_management_commands(guard, monkeypatch):
    monkeypatch.setattr(guard_commands, "get_guard", lambda: guard)
    event = SimpleNamespace(
        sender_id=USER, chat_id=CHAT, text="/guard_off", raw_text="/guard_off", reply=AsyncMock()
    )
    await guard_commands.handle_guard(event)
    assert guard.group(CHAT).enabled
    assert "没有权限" in event.reply.await_args.args[0]


def test_main_app_keeps_guard_behind_existing_authentication(monkeypatch):
    from core.web_api.app import create_app

    with TestClient(create_app()) as client:
        assert client.get("/api/guard/config").status_code == 401


async def test_guard_schema_failure_does_not_break_existing_bot_startup(guard, monkeypatch):
    from core.initializers import moderation_initializer as module

    repository = SimpleNamespace(initialize=AsyncMock(side_effect=RuntimeError("CREATE denied")))
    monkeypatch.setattr(module, "GuardRepository", lambda pool: repository)
    initializer = module.ModerationInitializer()
    await initializer.initialize(guard.client, SimpleNamespace(pool=object()))
    assert initializer.service is None
    guard.client.add_event_handler.assert_not_called()


async def test_missing_legacy_config_keeps_guard_disabled(guard, monkeypatch):
    from core.initializers import moderation_initializer as module

    repository = SimpleNamespace(initialize=AsyncMock())
    monkeypatch.setattr(module, "GuardRepository", lambda pool: repository)
    monkeypatch.setattr(
        module, "read_config", lambda: (_ for _ in ()).throw(ConfigPersistenceError("missing"))
    )
    initializer = module.ModerationInitializer()
    await initializer.initialize(guard.client, SimpleNamespace(pool=object()))
    try:
        assert initializer.service is not None
        assert not initializer.service.group(CHAT).enabled
    finally:
        await initializer.shutdown()


@pytest.mark.parametrize("enabled", [True, False])
def test_bot_message_switch_api_roundtrip_preserves_other_settings(
    api, guard, config_store, enabled
):
    persistence, original = config_store
    group = GuardGroup(enabled=True, block_bot_messages=enabled)
    response = api.put(f"/api/guard/groups/{CHAT}", json=group.model_dump())
    assert response.status_code == 200
    assert guard.group(CHAT).block_bot_messages is enabled
    assert (
        api.get("/api/guard/config").json()["config"]["groups"][str(CHAT)]["block_bot_messages"]
        is enabled
    )
    saved = persistence.read()
    for key, value in original.items():
        assert saved[key] == value


@pytest.mark.parametrize("argument,enabled", [("on", True), ("off", False)])
async def test_bot_message_switch_command(guard, config_store, monkeypatch, argument, enabled):
    monkeypatch.setattr(guard_commands, "get_guard", lambda: guard)
    event = SimpleNamespace(
        sender_id=ADMIN,
        chat_id=CHAT,
        text=f"/guard_bots {argument}",
        raw_text=f"/guard_bots {argument}",
        reply=AsyncMock(),
    )
    await guard_commands.handle_guard(event)
    assert guard.group(CHAT).block_bot_messages is enabled
    assert "已保存" in event.reply.await_args.args[0]


async def test_bot_message_switch_command_rejects_ordinary_members(guard, monkeypatch):
    monkeypatch.setattr(guard_commands, "get_guard", lambda: guard)
    event = SimpleNamespace(
        sender_id=USER,
        chat_id=CHAT,
        text="/guard_bots on",
        raw_text="/guard_bots on",
        reply=AsyncMock(),
    )
    await guard_commands.handle_guard(event)
    assert not guard.group(CHAT).block_bot_messages
    assert "没有权限" in event.reply.await_args.args[0]


def test_add_group_is_saved_immediately_and_survives_reload(api, guard, config_store):
    persistence, original = config_store
    chat_id = CHAT - 10
    response = api.post("/api/guard/groups", json={"chat_id": chat_id})
    assert response.status_code == 200 and response.json()["created"]
    saved = persistence.read()
    assert str(chat_id) in saved["sakura_guard"]["groups"]
    assert saved["sakura_guard"]["groups"][str(chat_id)]["enabled"] is False
    guard.reload(saved)
    assert str(chat_id) in api.get("/api/guard/config").json()["config"]["groups"]
    for key, value in original.items():
        assert saved[key] == value


def test_duplicate_add_never_overwrites_existing_group_settings(api, guard, config_store):
    persistence, _ = config_store
    group = GuardGroup(enabled=True, block_bot_messages=True, nickname_keywords=["自定义"])
    assert api.put(f"/api/guard/groups/{CHAT}", json=group.model_dump()).status_code == 200
    response = api.post("/api/guard/groups", json={"chat_id": CHAT})
    assert response.status_code == 200 and not response.json()["created"]
    assert persistence.read()["sakura_guard"]["groups"][str(CHAT)] == group.model_dump()
    assert guard.group(CHAT).enabled and guard.group(CHAT).block_bot_messages


def test_add_group_rejects_invalid_ids_and_unauthorized_users(api, config_store):
    persistence, _ = config_store
    assert api.post("/api/guard/groups", json={"chat_id": 123}).status_code == 422
    assert (
        api.post(
            "/api/guard/groups", json={"chat_id": CHAT}, headers={"x-test-user": str(USER)}
        ).status_code
        == 403
    )
    assert "sakura_guard" not in persistence.read()


def test_joined_groups_endpoint_is_admin_only(api):
    response = api.get("/api/guard/groups")
    assert response.status_code == 200 and "groups" in response.json()
    assert response.json()["complete_history_available"] is False
    assert api.get("/api/guard/groups", headers={"x-test-user": str(USER)}).status_code == 403
