# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
import uuid

import pytest

from core.config.persistence import ConfigPersistenceError
from core.web_api.routes import guard as routes

from .conftest import CHAT, USER
from .test_config_api import api as api
from .test_config_api import config_store as config_store
from .test_deleted_cleanup import deleted as deleted
from .test_deleted_cleanup import drain


def test_manual_api_is_idempotent_and_details_return_only_this_bot(api, deleted):
    body = {"request_id": uuid.uuid4().hex, "chat_ids": [CHAT], "confirmed": True}
    first = api.post("/api/guard/chats/deleted-users", json=body)
    assert first.status_code == 200
    second = api.post("/api/guard/chats/deleted-users", json=body)
    assert second.status_code == 200 and second.json()["id"] == first.json()["id"]
    assert api.get("/api/guard/chats/deleted-users").json()["job"]["id"] == first.json()["id"]
    assert api.get(f"/api/guard/chats/deleted-users/jobs/{first.json()['id']}").status_code == 200
    deleted.guard.bot_id = 998
    assert api.get(f"/api/guard/chats/deleted-users/jobs/{first.json()['id']}").status_code == 404


@pytest.mark.parametrize(
    "patch",
    [
        {"confirmed": False},
        {"confirmed": "true"},
        {"chat_ids": []},
        {"chat_ids": [1]},
        {"chat_ids": [True]},
        {"chat_ids": ["-42"]},
        {"request_id": "bad"},
        {"extra": True},
    ],
)
def test_manual_api_requires_typed_confirmed_scope(api, deleted, patch):
    body = {"request_id": uuid.uuid4().hex, "chat_ids": [CHAT], "confirmed": True, **patch}
    assert api.post("/api/guard/chats/deleted-users", json=body).status_code == 422
    assert not deleted.repository.store.jobs


def test_all_cleanup_routes_recheck_current_system_administrator(api, deleted):
    for method, path, body in [
        ("get", "/api/guard/chats/deleted-users", None),
        ("get", "/api/guard/chats/deleted-users/jobs/unknown", None),
        (
            "post",
            "/api/guard/chats/deleted-users",
            {"request_id": uuid.uuid4().hex, "confirmed": True},
        ),
        (
            "patch",
            "/api/guard/chats/deleted-users/settings",
            {"changes": {"enabled": True}, "previous": {"enabled": False}},
        ),
    ]:
        kwargs = {"headers": {"x-test-user": str(USER)}}
        if body is not None:
            kwargs["json"] = body
        assert getattr(api, method)(path, **kwargs).status_code == 403


def test_settings_autosave_merges_fields_and_preserves_all_existing_configuration(
    api, deleted, config_store
):
    persistence, original = config_store
    result = api.patch(
        "/api/guard/chats/deleted-users/settings",
        json={"changes": {"enabled": True}, "previous": {"enabled": False}},
    )
    assert result.status_code == 200 and result.json() == {"enabled": True, "interval_hours": 24}
    result = api.patch(
        "/api/guard/chats/deleted-users/settings",
        json={"changes": {"interval_hours": 72}, "previous": {"interval_hours": 24}},
    )
    assert result.status_code == 200 and result.json() == {"enabled": True, "interval_hours": 72}
    saved = persistence.read()
    for key, value in original.items():
        assert saved[key] == value
    deleted.guard.reload(saved)
    assert deleted.settings.enabled and deleted.settings.interval_hours == 72
    assert persistence.backup_path.exists()


@pytest.mark.parametrize(
    "changes,previous",
    [
        ({"enabled": "true"}, {"enabled": False}),
        ({"interval_hours": 0}, {"interval_hours": 24}),
        ({"interval_hours": 721}, {"interval_hours": 24}),
        ({"enabled": True}, {}),
        ({}, {}),
        ({"unknown": True}, {"unknown": False}),
    ],
)
def test_invalid_settings_do_not_change_disk(api, deleted, config_store, changes, previous):
    persistence, original = config_store
    assert (
        api.patch(
            "/api/guard/chats/deleted-users/settings",
            json={"changes": changes, "previous": previous},
        ).status_code
        == 422
    )
    assert persistence.read() == original


def test_settings_conflicts_and_write_failure_are_visible_and_leave_runtime_unchanged(
    api, deleted, config_store, monkeypatch
):
    persistence, _ = config_store
    assert (
        api.patch(
            "/api/guard/chats/deleted-users/settings",
            json={"changes": {"interval_hours": 48}, "previous": {"interval_hours": 24}},
        ).status_code
        == 200
    )
    body = {"changes": {"interval_hours": 72}, "previous": {"interval_hours": 24}}
    result = api.patch("/api/guard/chats/deleted-users/settings", json=body)
    assert result.status_code == 409
    assert result.json()["detail"]["current"]["interval_hours"] == 48
    body["previous"]["interval_hours"] = 48

    def reject(_):
        raise ConfigPersistenceError("无法保存")

    monkeypatch.setattr(routes, "save_config_changes", reject)
    before = persistence.read()
    assert api.patch("/api/guard/chats/deleted-users/settings", json=body).status_code == 503
    assert persistence.read() == before
    assert deleted.settings.interval_hours == 48


async def test_api_history_can_show_completed_removal_results_without_hashes(api, deleted):
    job = await deleted.start("admin", uuid.uuid4().hex, [CHAT])
    await drain(deleted)
    result = api.get(f"/api/guard/chats/deleted-users/jobs/{job['id']}")
    assert result.status_code == 200
    assert result.json()["counts"]["removed"] == 1
    assert "access_hash" not in result.text
