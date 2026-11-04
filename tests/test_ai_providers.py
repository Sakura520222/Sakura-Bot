"""Provider/model persistence, migration, revision and secret regressions (#144)."""

import json
from copy import deepcopy
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from core.config.ai_runtime import effective_ai_config
from core.config.persistence import ConfigStore
from core.config.runtime import RuntimeConfigReader
from core.web_api.routes import ai_profiles as routes


@pytest.fixture
def api(tmp_path, monkeypatch):
    path = tmp_path / "config.json"
    path.write_text(
        json.dumps(
            {
                "api_key": "default-secret",
                "base_url": "https://default.example/v1",
                "model": "default",
                "channels": ["keep"],
                "ai_profiles": {
                    "p": {
                        "name": "Provider",
                        "base_url": "https://p.example/v1",
                        "api_key": "provider-secret",
                        "model": "A",
                        "supports_vision": True,
                    }
                },
                "ai_routing": {
                    "summary": {"profile_id": "p"},
                    "assistant": {"profile_id": "p", "model": "B", "supports_vision": False},
                },
            }
        ),
        encoding="utf-8",
    )
    store = ConfigStore(path)
    monkeypatch.setattr(routes, "get_config", store.read)
    monkeypatch.setattr(routes, "write_config", store.save_changes)
    app = FastAPI()
    app.include_router(routes.router, prefix="/api/ai")
    with TestClient(app) as client:
        yield client, store


def payload(client):
    data = client.get("/api/ai/providers").json()["data"]
    data.pop("effective")
    for p in data["providers"].values():
        p.pop("api_key_set")
        p.pop("api_key_preview")
    return data


def test_migration_preserves_credentials_models_and_legacy_routes_without_writing(api):
    client, store = api
    before = store.path.read_bytes()
    public = client.get("/api/ai/providers")
    assert "secret" not in public.text
    body = payload(client)
    assert set(body["providers"]["p"]["models"]) == {"A", "B"}
    assert store.path.read_bytes() == before
    assert client.put("/api/ai/providers", json=body).status_code == 200
    cfg = store.read()
    assert cfg["ai_profiles"]["p"]["api_key"] == "provider-secret"
    assert cfg["ai_providers"]["p"]["api_key"] == "provider-secret"
    assert cfg["channels"] == ["keep"]
    assert effective_ai_config(cfg, "summary").model == "A"
    assert effective_ai_config(cfg, "summary").supports_vision
    assert effective_ai_config(cfg, "assistant").model == "B"
    assert not effective_ai_config(cfg, "assistant").supports_vision
    assert effective_ai_config(cfg, "poll").api_key == "default-secret"


def test_incremental_models_roundtrip_restart_independent_capabilities_and_revisions(
    api, monkeypatch
):
    client, store = api
    body = payload(client)
    body["providers"]["p"]["models"]["B"]["supports_vision"] = False
    body["routing"]["assistant"]["supports_vision"] = None
    assert client.put("/api/ai/providers", json=body).status_code == 200
    stale = deepcopy(body)
    body = payload(client)
    body["providers"]["p"]["models"]["C"] = {"name": "Third", "supports_vision": True}
    body["routing"]["vision"] = {"provider_id": "p", "model_id": "C"}
    assert client.put("/api/ai/providers", json=body).status_code == 200
    assert client.put("/api/ai/providers", json=stale).status_code == 409
    reloaded = ConfigStore(store.path).read()
    assert set(reloaded["ai_providers"]["p"]["models"]) == {"A", "B", "C"}
    for purpose, model, capability in [
        ("summary", "A", True),
        ("assistant", "B", False),
        ("vision", "C", True),
    ]:
        conf = effective_ai_config(reloaded, purpose)
        assert (conf.model, conf.supports_vision, conf.api_key) == (
            model,
            capability,
            "provider-secret",
        )
    import core.config as config_module

    monkeypatch.setattr(config_module._old_config, "CONFIG_FILE", str(store.path))
    assert effective_ai_config(RuntimeConfigReader().read(), "vision").model == "C"
    cfg = store.read()
    cfg["ai_providers"]["p"]["models"]["B"]["supports_vision"] = True
    store.save_changes(cfg)
    assert effective_ai_config(RuntimeConfigReader().read(), "assistant").supports_vision


def test_dangling_references_invalid_types_and_old_clients_cannot_delete_models(api):
    client, store = api
    old = client.get("/api/ai/profiles").json()["data"]
    old.pop("effective")
    for p in old["profiles"].values():
        p.pop("api_key_set")
        p.pop("api_key_preview")
    body = payload(client)
    assert client.put("/api/ai/providers", json=body).status_code == 200
    assert client.put("/api/ai/profiles", json=old).status_code == 409
    before = store.path.read_bytes()
    body = payload(client)
    del body["providers"]["p"]["models"]["A"]
    assert client.put("/api/ai/providers", json=body).status_code == 400
    assert store.path.read_bytes() == before
    body = payload(client)
    body["providers"]["p"]["models"]["A"]["supports_vision"] = "false"
    assert client.put("/api/ai/providers", json=body).status_code == 400
    assert store.path.read_bytes() == before


def test_save_failure_or_noop_writer_never_reports_success(api, monkeypatch):
    client, store = api
    body = payload(client)
    body["providers"]["p"]["models"]["C"] = {"supports_vision": False}
    monkeypatch.setattr(routes, "write_config", lambda _: None)
    response = client.put("/api/ai/providers", json=body)
    assert response.status_code == 409
    assert "ai_providers" not in store.read()


def test_model_discovery_failure_and_manual_call_preserve_saved_models(api, monkeypatch):
    client, store = api
    body = payload(client)
    assert client.put("/api/ai/providers", json=body).status_code == 200
    before = store.path.read_bytes()
    sdk = AsyncMock()
    sdk.__aenter__.return_value = sdk
    sdk.models.list.return_value = SimpleNamespace(
        data=[SimpleNamespace(id="C"), SimpleNamespace(id="A")]
    )
    connections = []

    def factory(**kw):
        connections.append(kw)
        return sdk

    monkeypatch.setattr(routes, "AsyncOpenAI", factory)
    assert client.post("/api/ai/providers/p/models").json()["data"] == ["A", "C"]
    sdk.models.list.side_effect = ValueError("provider-secret data:image/png;base64,never")
    result = client.post("/api/ai/providers/p/models")
    assert result.status_code == 502 and "secret" not in result.text
    assert (
        client.post(
            "/api/ai/providers/p/test", json={"kind": "call", "model": "manual"}
        ).status_code
        == 200
    )
    assert sdk.chat.completions.create.call_args.kwargs["model"] == "manual"
    assert all(c["api_key"] == "provider-secret" for c in connections)
    assert store.path.read_bytes() == before


def test_key_preserve_clear_and_unrelated_changes(api):
    client, store = api
    body = payload(client)
    cfg = store.read()
    cfg["unrelated"] = "new"
    store.save_changes(cfg)
    assert client.put("/api/ai/providers", json=body).status_code == 200
    assert store.read()["unrelated"] == "new"
    body = payload(client)
    body["providers"]["p"]["api_key"] = ""
    assert client.put("/api/ai/providers", json=body).status_code == 200
    assert store.read()["ai_providers"]["p"]["api_key"] == ""
    assert "secret" not in client.get("/api/ai/providers").text
