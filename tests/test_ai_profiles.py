# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

import json
from copy import deepcopy
from unittest.mock import AsyncMock

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

import core.config as config_module
from core.config.ai_profiles import parse_ai_profiles
from core.config.ai_runtime import effective_ai_config
from core.config.persistence import ConfigStore
from core.config.runtime import RuntimeConfigReader
from core.web_api.routes import ai_profiles as routes


def sample():
    return {
        "api_key": "legacy-secret",
        "base_url": "https://legacy.example/v1",
        "model": "legacy",
        "ai_profiles": {
            name: {
                "name": name,
                "base_url": f"https://{name}.example/v1",
                "api_key": f"{name}-secret",
                "model": f"{name}-model",
                "supports_vision": name == "vision",
            }
            for name in ("summary", "assistant", "vision")
        },
        "ai_routing": {name: {"profile_id": name} for name in ("summary", "assistant", "vision")},
        "ai_vision": {"enabled": True, "ocr": True, "description": False},
    }


def test_routes_are_isolated_and_legacy_fallback():
    config = sample()
    for purpose in ("summary", "assistant", "vision"):
        resolved = effective_ai_config(config, purpose)
        assert resolved.api_key == f"{purpose}-secret"
        assert resolved.model == f"{purpose}-model"
        assert resolved.base_url == f"https://{purpose}.example/v1"
        assert resolved.vision_enabled and resolved.vision_ocr and not resolved.vision_description
        assert "secret" not in repr(resolved)
    assert effective_ai_config(config, "poll").model == "legacy"
    config["ai_routing"]["summary"].update(model="override", supports_vision=True)
    assert effective_ai_config(config).model == "override"
    assert effective_ai_config(config).supports_vision
    assert effective_ai_config(config, "assistant").model == "assistant-model"
    config["ai_profiles"]["summary"]["api_key"] = ""
    assert effective_ai_config(config).api_key == ""  # never inherit another provider's key


@pytest.mark.parametrize(
    "change",
    [
        lambda c: c["ai_routing"]["summary"].update(profile_id="missing"),
        lambda c: c["ai_profiles"]["summary"].update(base_url="https://u:p@bad.example"),
        lambda c: c["ai_profiles"]["summary"].update(provider="unsupported"),
        lambda c: c["ai_profiles"]["summary"].update(model="  "),
        lambda c: c["ai_profiles"]["summary"].update(api_key="secret\n"),
        lambda c: c["ai_vision"].update(enabled="false"),
        lambda c: c.update(ai_profiles=None),
    ],
)
def test_schema_rejects_invalid_config(change):
    config = sample()
    change(config)
    with pytest.raises(ValueError):
        parse_ai_profiles(config)


@pytest.fixture
def api(tmp_path, monkeypatch):
    path = tmp_path / "config.json"
    path.write_text(json.dumps(sample()), encoding="utf-8")
    store = ConfigStore(path)
    monkeypatch.setattr(routes, "get_config", store.read)
    monkeypatch.setattr(routes, "write_config", store.save_changes)
    app = FastAPI()
    app.include_router(routes.router, prefix="/api/ai")
    with TestClient(app) as client:
        yield client, store


def payload(client):
    public = client.get("/api/ai/profiles").json()["data"]
    public.pop("effective")
    for profile in public["profiles"].values():
        profile.pop("api_key_preview")
        profile.pop("api_key_set")
    return public


def test_crud_masks_preserves_secrets_and_checks_revision(api):
    client, store = api
    assert "secret" not in client.get("/api/ai/profiles").text
    body = payload(client)
    stale = deepcopy(body)
    body["profiles"]["summary"]["name"] = "renamed"
    assert client.put("/api/ai/profiles", json=body).status_code == 200
    assert store.read()["ai_profiles"]["summary"]["api_key"] == "summary-secret"
    assert client.put("/api/ai/profiles", json=stale).status_code == 409
    body = payload(client)
    del body["profiles"]["summary"]
    assert client.put("/api/ai/profiles", json=body).status_code == 400
    del body["routing"]["summary"]
    assert client.put("/api/ai/profiles", json=body).status_code == 200
    assert effective_ai_config(store.read()).model == "legacy"


def test_invalid_input_and_upstream_errors_do_not_leak_secrets(api, monkeypatch):
    client, store = api
    before = store.path.read_bytes()
    body = payload(client)
    body["profiles"]["summary"]["api_key"] = "extremely-secret\n"
    response = client.put("/api/ai/profiles", json=body)
    assert response.status_code == 400 and "extremely-secret" not in response.text
    assert store.path.read_bytes() == before
    monkeypatch.setattr(
        routes, "AsyncOpenAI", lambda **kw: (_ for _ in ()).throw(ValueError("summary-secret"))
    )
    response = client.post("/api/ai/profiles/summary/models")
    assert response.status_code == 502 and "summary-secret" not in response.text
    assert store.path.read_bytes() == before


def test_manual_model_call_does_not_require_model_listing(api, monkeypatch):
    client, _ = api
    sdk = AsyncMock()
    sdk.__aenter__.return_value = sdk
    sdk.models.list.side_effect = ValueError("not supported")
    monkeypatch.setattr(routes, "AsyncOpenAI", lambda **kw: sdk)
    assert client.post("/api/ai/profiles/summary/models").status_code == 502
    assert (
        client.post(
            "/api/ai/profiles/summary/test", json={"kind": "call", "model": "manual"}
        ).status_code
        == 200
    )
    assert sdk.chat.completions.create.call_args.kwargs["model"] == "manual"


def test_hot_reload_and_restart_use_same_routes(tmp_path, monkeypatch):
    path = tmp_path / "config.json"
    path.write_text(json.dumps(sample()), encoding="utf-8")
    store = ConfigStore(path)
    monkeypatch.setattr(config_module._old_config, "CONFIG_FILE", str(path))
    reader = RuntimeConfigReader()
    assert effective_ai_config(reader.read(), "assistant").model == "assistant-model"
    config = store.read()
    config["ai_routing"]["assistant"]["model"] = "fast-new"
    store.save_changes(config)
    assert effective_ai_config(reader.read(), "assistant").model == "fast-new"
    assert effective_ai_config(RuntimeConfigReader().read(), "assistant").model == "fast-new"
    path.write_text('{"ai_profiles": null}')
    assert effective_ai_config(reader.read(), "assistant").model == "fast-new"


def test_purpose_clients_pin_credentials_and_model_during_reload(monkeypatch):
    import httpx
    from openai import AsyncOpenAI, OpenAI

    from core.ai import ai_client

    config = sample()
    sent = []

    def respond(request):
        sent.append(
            (
                request.url.host,
                request.headers["authorization"],
                json.loads(request.content)["model"],
            )
        )
        return httpx.Response(
            200,
            json={
                "id": "test",
                "object": "chat.completion",
                "created": 1,
                "model": "test",
                "choices": [
                    {
                        "index": 0,
                        "message": {"role": "assistant", "content": "ok"},
                        "finish_reason": "stop",
                    }
                ],
            },
        )

    monkeypatch.setattr(ai_client, "_purpose_clients", {})
    monkeypatch.setattr(
        ai_client,
        "effective_ai_config",
        lambda purpose="summary": effective_ai_config(config, purpose),
    )
    monkeypatch.setattr(
        ai_client,
        "OpenAI",
        lambda **kw: OpenAI(**kw, http_client=httpx.Client(transport=httpx.MockTransport(respond))),
    )
    monkeypatch.setattr(
        ai_client,
        "AsyncOpenAI",
        lambda **kw: AsyncOpenAI(
            **kw, http_client=httpx.AsyncClient(transport=httpx.MockTransport(respond))
        ),
    )
    for name in ("client_llm", "async_client_llm", "_client_connection"):
        monkeypatch.setattr(ai_client, name, getattr(ai_client, name))
    pinned = {
        purpose: ai_client.get_ai_runtime(purpose) for purpose in ("summary", "assistant", "vision")
    }
    config["ai_profiles"]["assistant"].update(
        api_key="replacement-secret", base_url="https://replacement.example/v1", model="replacement"
    )
    replacement = ai_client.get_ai_runtime("assistant")
    for client, model in [*pinned.values(), replacement]:
        client.chat.completions.create(model=model, messages=[{"role": "user", "content": "test"}])
    assert sent == [
        ("summary.example", "Bearer summary-secret", "summary-model"),
        ("assistant.example", "Bearer assistant-secret", "assistant-model"),
        ("vision.example", "Bearer vision-secret", "vision-model"),
        ("replacement.example", "Bearer replacement-secret", "replacement"),
    ]


def test_invalid_profile_secrets_never_escape_runtime_or_legacy_endpoint(monkeypatch, caplog):
    from core.web_api.routes import ai_config

    config = sample()
    config["ai_profiles"]["summary"]["api_key"] = "never-log-this-secret\n"
    with pytest.raises(ValueError) as error:
        effective_ai_config(config)
    assert "never-log-this-secret" not in str(error.value)
    monkeypatch.setattr(ai_config, "get_config", lambda: config)
    app = FastAPI()
    app.include_router(ai_config.router, prefix="/api/ai")
    with TestClient(app) as client:
        response = client.get("/api/ai/config")
    assert response.status_code == 500
    assert "never-log-this-secret" not in response.text + caplog.text


def parity_cases():
    from pathlib import Path

    path = Path(__file__).parents[1] / "qa-bot-go/internal/config/testdata/ai_profiles_parity.json"
    return json.loads(path.read_text(encoding="utf-8"))


@pytest.mark.parametrize("case", parity_cases(), ids=lambda case: case["name"])
def test_python_go_share_identical_resolution_fixture(case, monkeypatch):
    from dataclasses import asdict
    from types import SimpleNamespace

    from core import settings

    for key in ("LLM_API_KEY", "LLM_BASE_URL", "LLM_MODEL", "DEEPSEEK_API_KEY"):
        monkeypatch.delenv(key, raising=False)
    defaults = settings.AISettings(**case["env"])
    monkeypatch.setattr(settings, "get_settings", lambda: SimpleNamespace(ai=defaults))
    assert asdict(effective_ai_config(case["config"], case["purpose"])) == case["expected"]


def test_startup_accepts_saved_profile_without_legacy_environment_key(monkeypatch):
    from types import SimpleNamespace

    from core import settings

    defaults = SimpleNamespace(
        telegram=SimpleNamespace(api_id=1, api_hash="hash", bot_token="token"),
        ai=SimpleNamespace(effective_api_key=None),
        userbot=SimpleNamespace(userbot_enabled=False),
    )
    monkeypatch.setattr(settings, "get_settings", lambda: defaults)
    monkeypatch.setattr("core.config.ai_runtime.runtime_config.read", sample)
    assert settings.validate_required_settings() == (True, [])


def test_models_and_connection_test_use_saved_profile(api, monkeypatch):
    from types import SimpleNamespace

    client, store = api
    sdk = AsyncMock()
    sdk.__aenter__.return_value = sdk
    sdk.models.list.return_value = SimpleNamespace(
        data=[SimpleNamespace(id="z-model"), SimpleNamespace(id="a-model")]
    )
    connections = []

    def factory(**kwargs):
        connections.append(kwargs)
        return sdk

    monkeypatch.setattr(routes, "AsyncOpenAI", factory)
    before = store.path.read_bytes()
    response = client.post("/api/ai/profiles/summary/models")
    assert response.json()["data"] == ["a-model", "z-model"]
    assert (
        client.post("/api/ai/profiles/summary/test", json={"kind": "connection"}).status_code == 200
    )
    assert all(
        c["api_key"] == "summary-secret" and c["base_url"] == "https://summary.example/v1"
        for c in connections
    )
    assert store.path.read_bytes() == before


def test_explicit_key_clear_and_unrelated_config_change(api):
    client, store = api
    body = payload(client)
    config = store.read()
    config["unrelated_option"] = {"keep": True}
    store.save_changes(config)
    body["profiles"]["summary"]["api_key"] = ""
    assert client.put("/api/ai/profiles", json=body).status_code == 200
    assert store.read()["ai_profiles"]["summary"]["api_key"] == ""
    assert store.read()["ai_profiles"]["assistant"]["api_key"] == "assistant-secret"
    assert store.read()["unrelated_option"] == {"keep": True}
