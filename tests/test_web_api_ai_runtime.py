# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""WebUI 配置保存、真实 SDK 请求及助手独立进程的热更新回归。"""

import json
import os
import subprocess
import sys
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from openai import AsyncOpenAI, OpenAI

import core.config as config_module
from core.ai import ai_client
from core.ai.quota_manager import QuotaManager
from core.config import runtime
from core.config.persistence import ConfigPersistenceError, ConfigStore
from core.config.runtime import RuntimeConfigReader
from core.web_api.routes import ai_config as routes


@pytest.fixture
def runtime_api(tmp_path, monkeypatch):
    path = tmp_path / "config.json"
    original = {
        "channels": ["@keep"],
        "forwarding": {"enabled": True, "rules": []},
        "sakura_guard": {"groups": {}},
        "future_option": {"nested": [1, True]},
        "api_key": "old-private-key",
        "base_url": "https://old.example/v1",
        "model": "old-model",
    }
    path.write_text(json.dumps(original), encoding="utf-8")
    store = ConfigStore(path)
    reader = RuntimeConfigReader()
    monkeypatch.setattr(config_module._old_config, "CONFIG_FILE", str(path))
    monkeypatch.setattr(runtime, "runtime_config", reader)
    monkeypatch.setattr("core.config.ai_runtime.runtime_config", reader)
    monkeypatch.setattr("core.ai.quota_manager.runtime_config", reader)
    monkeypatch.setattr(routes, "get_config", store.read)
    monkeypatch.setattr(routes, "write_config", store.save_changes)
    for name in ("client_llm", "async_client_llm", "_client_connection"):
        monkeypatch.setattr(ai_client, name, getattr(ai_client, name))
    requests = []

    def respond(request):
        requests.append(request)
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
    app = FastAPI()
    app.include_router(routes.router, prefix="/api/ai")
    with TestClient(app) as client:
        yield client, store, original, requests


def test_ai_save_switches_real_sdk_requests_and_keeps_existing_request(runtime_api):
    client, store, original, requests = runtime_api
    previous_client, previous_model = ai_client.get_ai_runtime()
    response = client.put(
        "/api/ai/config",
        json={
            "api_key": "new-private-key",
            "base_url": "https://new.example/v1",
            "model": "new-model",
        },
    )
    assert response.status_code == 200
    next_client, model = ai_client.get_ai_runtime()
    assert next_client is not previous_client
    assert previous_model == "old-model" and model == "new-model"
    for sdk_client, sdk_model in ((previous_client, previous_model), (next_client, model)):
        sdk_client.chat.completions.create(
            model=sdk_model, messages=[{"role": "user", "content": "test"}]
        )
    assert [request.url.host for request in requests] == ["old.example", "new.example"]
    assert [json.loads(request.content)["model"] for request in requests] == [
        "old-model",
        "new-model",
    ]
    assert requests[-1].headers["authorization"] == "Bearer new-private-key"
    visible = client.get("/api/ai/config").json()["data"]
    assert visible["model"] == "new-model" and visible["api_key_set"]
    assert "new-private-key" not in json.dumps(visible)
    for key in ("channels", "forwarding", "sakura_guard", "future_option"):
        assert store.read()[key] == original[key]
    assert store.backup_path.exists()


async def test_model_only_save_and_async_summary_use_new_model(runtime_api):
    client, _, _, requests = runtime_api
    previous, _ = ai_client.get_async_ai_runtime()
    assert (
        client.put("/api/ai/config", json={"model": "replacement", "api_key": " "}).status_code
        == 200
    )
    current, model = ai_client.get_async_ai_runtime()
    assert current is previous and model == "replacement"
    assert await ai_client.analyze_with_ai(["message"], "prompt") == "ok"
    assert json.loads(requests[-1].content)["model"] == "replacement"
    assert requests[-1].headers["authorization"] == "Bearer old-private-key"


@pytest.mark.parametrize(
    "body",
    [
        {"base_url": "ftp://bad.example"},
        {"base_url": "https://user:pass@host.example"},
        {"base_url": "https://host.example:bad"},
        {"model": "bad\nvalue"},
        {"api_key": "private-secret\nvalue"},
    ],
)
def test_invalid_ai_config_does_not_write_or_disclose_credentials(runtime_api, body, caplog):
    client, store, _, _ = runtime_api
    before = store.path.read_bytes()
    response = client.put("/api/ai/config", json=body)
    assert response.status_code == 400
    assert store.path.read_bytes() == before
    assert "private-secret" not in response.text + caplog.text


async def test_quota_save_applies_to_existing_manager_without_reset(runtime_api, monkeypatch):
    client, _, _, _ = runtime_api
    monkeypatch.setenv("QA_BOT_USER_LIMIT", "3")
    monkeypatch.setenv("QA_BOT_DAILY_LIMIT", "200")
    used = 3
    db = MagicMock()
    db.get_total_daily_usage = AsyncMock(return_value=used)
    db.get_quota_usage = AsyncMock(return_value={"usage_count": used})

    async def reserve(user_id, daily_limit, is_admin):
        nonlocal used
        if is_admin:
            return {"allowed": True, "remaining": -1, "used": 0}
        allowed = used < daily_limit
        used += int(allowed)
        return {
            "allowed": allowed,
            "remaining": max(0, daily_limit - used),
            "used": used,
            "daily_limit": daily_limit,
        }

    db.check_and_increment_quota = AsyncMock(side_effect=reserve)
    monkeypatch.setattr("core.ai.quota_manager.get_db_manager", lambda: db)
    manager = QuotaManager()
    monkeypatch.setattr(manager, "is_admin", lambda user: user == 1)
    assert not (await manager.check_quota(42))["allowed"]
    assert (
        client.put(
            "/api/ai/qa-quota", json={"user_daily_limit": 5, "total_daily_limit": 500}
        ).status_code
        == 200
    )
    assert (await manager.get_usage_status(42))["remaining"] == 2
    assert (await manager.check_quota(42))["remaining"] == 1
    assert used == 4
    assert client.put("/api/ai/qa-quota", json={"user_daily_limit": 1}).status_code == 200
    assert not (await manager.check_quota(42))["allowed"]
    assert (await manager.check_quota(1))["allowed"]
    assert used == 4
    assert client.get("/api/ai/qa-quota").json()["data"] == {
        "user_daily_limit": 1,
        "total_daily_limit": 500,
    }


@pytest.mark.parametrize(
    "body",
    [
        {"user_daily_limit": 0},
        {"user_daily_limit": True},
        {"user_daily_limit": 1.5},
        {"user_daily_limit": "5"},
        {"total_daily_limit": 9},
        {"total_daily_limit": 2147483648},
        {"extra": 1},
        {},
    ],
)
def test_quota_invalid_updates_preserve_full_config(runtime_api, body):
    client, store, _, _ = runtime_api
    before = store.path.read_bytes()
    assert client.put("/api/ai/qa-quota", json=body).status_code in {400, 422}
    assert store.path.read_bytes() == before


def test_corrupt_file_keeps_last_valid_runtime_settings(runtime_api):
    client, store, _, _ = runtime_api
    assert ai_client.get_ai_runtime()[1] == "old-model"
    store.path.write_text("{", encoding="utf-8")
    assert ai_client.get_ai_runtime()[1] == "old-model"
    assert client.put("/api/ai/config", json={"model": "rejected"}).status_code == 400
    assert store.path.read_text(encoding="utf-8") == "{"


def test_store_rejects_invalid_quota_even_outside_webui(runtime_api):
    _, store, _, _ = runtime_api
    with pytest.raises(ConfigPersistenceError):
        store.update_fields({"qa_bot_quota": {"user_daily_limit": False}})


def test_running_independent_process_reads_updated_model_and_quota(runtime_api):
    client, store, _, _ = runtime_api
    script = """
import json, logging, sys
logging.disable(logging.CRITICAL)
import core.config as config_module
config_module._old_config.CONFIG_FILE = sys.argv[1]
from core.ai.ai_client import get_ai_runtime
from core.config.qa_quota import effective_qa_quota
from core.config.runtime import runtime_config
for _ in range(2):
    sdk, model = get_ai_runtime()
    print(json.dumps({"model": model, "host": sdk.base_url.host, "new_key": sdk.api_key == "live-private-key", "quota": effective_qa_quota(runtime_config.read()).model_dump()}), flush=True)
    if not sys.stdin.readline(): break
"""
    env = {
        **os.environ,
        "LLM_API_KEY": "test-only",
        "QA_BOT_USER_LIMIT": "3",
        "QA_BOT_DAILY_LIMIT": "200",
        "PYTHONIOENCODING": "utf-8",
    }
    process = subprocess.Popen(
        [sys.executable, "-u", "-c", script, str(store.path)],
        cwd=Path(__file__).resolve().parents[1],
        env=env,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        encoding="utf-8",
    )
    try:
        initial = json.loads(process.stdout.readline())
        assert initial["model"] == "old-model" and initial["quota"]["user_daily_limit"] == 3
        assert (
            client.put(
                "/api/ai/config",
                json={
                    "model": "live-new-model",
                    "base_url": "https://live.example/v1",
                    "api_key": "live-private-key",
                },
            ).status_code
            == 200
        )
        assert client.put("/api/ai/qa-quota", json={"user_daily_limit": 8}).status_code == 200
        process.stdin.write("continue\n")
        process.stdin.flush()
        latest = json.loads(process.stdout.readline())
        assert latest["model"] == "live-new-model" and latest["quota"]["user_daily_limit"] == 8
        assert latest["host"] == "live.example" and latest["new_key"]
    finally:
        process.communicate("\n", timeout=15)
    assert process.returncode == 0
