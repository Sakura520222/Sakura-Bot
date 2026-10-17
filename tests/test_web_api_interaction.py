# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""频道投票配置的 HTTP 与持久化回归测试。"""

import json
from urllib.parse import quote

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from core.config.persistence import ConfigStore
from core.infrastructure.config.poll_config import ChannelPollConfigManager
from core.web_api.routes import interaction

CHANNEL = "https://t.me/example"
OTHER_CHANNEL = "https://t.me/other"
POLL_URL = "/api/interaction/poll-settings"
CHANNEL_URL = f"{POLL_URL}/{quote(CHANNEL, safe='')}"


@pytest.fixture
def poll_api(tmp_path, monkeypatch):
    """使用真实配置存储和临时文件，隔离本地频道与服务。"""
    path = tmp_path / "config.json"
    path.write_text(
        json.dumps(
            {
                "channels": [CHANNEL, OTHER_CHANNEL],
                "enable_poll": True,
                "public_voters": True,
                "channel_poll_settings": {OTHER_CHANNEL: {"enabled": False}},
                "channel_auto_poll_settings": {CHANNEL: {"enabled": False}},
                "model": "keep-model",
            }
        ),
        encoding="utf-8",
    )
    store = ConfigStore(path)
    monkeypatch.setattr(interaction, "get_config", store.read)
    monkeypatch.setattr(interaction, "write_config", store.save_changes)
    app = FastAPI()
    app.include_router(interaction.router, prefix="/api/interaction")
    with TestClient(app) as client:
        yield client, store


def test_create_channel_poll_config_survives_reload(poll_api):
    """首次配置可使用完整 URL 保存，刷新后仍存在且不改写其他配置。"""
    client, store = poll_api
    original = dict(store.read())
    settings = {"enabled": False, "send_to_channel": True, "public_voters": False}

    response = client.put(CHANNEL_URL, json=settings)

    assert response.status_code == 200
    assert response.json()["success"] is True
    response = client.get(POLL_URL)
    assert response.json()["data"]["settings"][CHANNEL] == settings
    expected = {
        **original,
        "channel_poll_settings": {**original["channel_poll_settings"], CHANNEL: settings},
    }
    assert store.read() == expected
    assert ChannelPollConfigManager(str(store.path)).get_config(CHANNEL) == settings


@pytest.mark.parametrize("field", ["enabled", "send_to_channel", "public_voters"])
@pytest.mark.parametrize("value", [True, False, None])
def test_channel_poll_patch_preserves_omitted_fields(poll_api, field, value):
    """开关、继承和发送位置更新只影响明确提交的字段。"""
    client, store = poll_api
    original = {"enabled": True, "send_to_channel": True, "public_voters": False}
    assert client.put(CHANNEL_URL, json=original).status_code == 200

    response = client.put(f"{POLL_URL}/@example", json={field: value})

    assert response.status_code == 200
    expected = {**original}
    if value is None:
        expected.pop(field)
    else:
        expected[field] = value
    assert store.read()["channel_poll_settings"][CHANNEL] == expected
    assert client.get(POLL_URL).json()["data"]["settings"][CHANNEL] == expected
    assert store.read()["channel_poll_settings"][OTHER_CHANNEL] == {"enabled": False}


def test_channel_poll_can_restore_global_inheritance(poll_api):
    """取消开关与公开模式覆盖后，运行时读取恢复继承并保留发送位置。"""
    client, store = poll_api
    client.put(
        CHANNEL_URL, json={"enabled": False, "send_to_channel": True, "public_voters": False}
    )

    response = client.put(CHANNEL_URL, json={"enabled": None, "public_voters": None})

    assert response.status_code == 200
    assert store.read()["channel_poll_settings"][CHANNEL] == {"send_to_channel": True}
    config = ChannelPollConfigManager(str(store.path)).get_config(CHANNEL)
    assert config == {"enabled": None, "send_to_channel": True, "public_voters": None}
    assert client.get(POLL_URL).json()["data"]["enable_poll"] is True
    assert client.get(POLL_URL).json()["data"]["public_voters"] is True


def test_create_inherited_channel_config(poll_api):
    """新建时允许两项都继承全局，且能在刷新后继续编辑。"""
    client, store = poll_api

    response = client.put(
        CHANNEL_URL, json={"enabled": None, "send_to_channel": False, "public_voters": None}
    )

    assert response.status_code == 200
    assert client.get(POLL_URL).json()["data"]["settings"][CHANNEL] == {"send_to_channel": False}
    assert store.read()["channel_poll_settings"][CHANNEL] == {"send_to_channel": False}


def test_delete_channel_poll_config_restores_defaults(poll_api):
    """删除只移除目标频道覆盖，其他频道与自动投票设置保持不变。"""
    client, store = poll_api
    original = dict(store.read())
    client.put(
        CHANNEL_URL, json={"enabled": False, "send_to_channel": True, "public_voters": False}
    )

    response = client.delete(CHANNEL_URL)

    assert response.status_code == 200
    assert response.json()["success"] is True
    assert CHANNEL not in client.get(POLL_URL).json()["data"]["settings"]
    assert store.read() == original
    assert ChannelPollConfigManager(str(store.path)).get_config(CHANNEL) == {
        "enabled": None,
        "send_to_channel": False,
        "public_voters": None,
    }


def test_failed_channel_poll_save_does_not_report_success(poll_api, monkeypatch):
    """写入失败时返回失败，磁盘配置保持原样。"""
    client, store = poll_api
    original = store.path.read_bytes()

    def fail_write(_config):
        raise OSError("test write failure")

    monkeypatch.setattr(interaction, "write_config", fail_write)

    response = client.put(CHANNEL_URL, json={"enabled": False})

    assert response.status_code == 500
    assert store.path.read_bytes() == original


@pytest.mark.parametrize("field", ["enabled", "send_to_channel", "public_voters"])
def test_invalid_channel_poll_value_is_rejected(poll_api, field):
    """非法值不得进入持久化配置。"""
    client, store = poll_api
    original = store.path.read_bytes()

    response = client.put(CHANNEL_URL, json={field: "invalid"})

    assert response.status_code == 422
    assert store.path.read_bytes() == original
