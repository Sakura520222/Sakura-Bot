# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Issue #106：AI 会话不能破坏完整配置或泄露凭据。"""

import json
import logging
import secrets
from copy import deepcopy
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

import core.config as config_module
from core.commands import ai_config_commands as commands
from core.infrastructure.utils.states import UserContext


@pytest.fixture
def ai_session(tmp_path, monkeypatch):
    original = {
        "channels": ["https://t.me/example"],
        "summary_schedules": {"https://t.me/example": {"hour": 9, "minute": 0}},
        "channel_poll_settings": {"https://t.me/example": {"enabled": True}},
        "channel_auto_poll_settings": {"https://t.me/example": {"enabled": False}},
        "forwarding": {"enabled": True, "rules": []},
        "comment_welcome": {"default": {"enabled": True}},
        "future_option": {"nested": [1, {"keep": True}]},
        "api_key": secrets.token_urlsafe(24),
        "base_url": "https://old.example/v1",
        "model": "old-model",
    }
    config_file = tmp_path / "config.json"
    config_file.write_text(json.dumps(original), encoding="utf-8")
    monkeypatch.setattr(config_module._old_config, "CONFIG_FILE", str(config_file))
    # 不让测试写入的配置残留在模块全局变量中。
    for name in config_module._MUTABLE_CONFIG_NAMES:
        monkeypatch.setattr(config_module._old_config, name, deepcopy(getattr(config_module, name)))
    config_module.update_module_variables(original)
    context = UserContext()
    monkeypatch.setattr(commands, "get_user_context", lambda: context)
    monkeypatch.setattr(commands, "ADMIN_LIST", [123456, 789012])
    return SimpleNamespace(path=config_file, original=original, context=context)


def event(text, sender_id=123456):
    return SimpleNamespace(text=text, sender_id=sender_id, reply=AsyncMock())


@pytest.mark.asyncio
async def test_setaicfg_preserves_every_other_field(ai_session):
    await commands.handle_set_ai_config(event("/setaicfg"))
    for value in [secrets.token_urlsafe(24), "https://new.example/v1", "new-model"]:
        await commands.handle_ai_config_input(event(value))
    saved = json.loads(ai_session.path.read_text(encoding="utf-8"))
    for key, value in ai_session.original.items():
        if key not in {"api_key", "base_url", "model"}:
            assert key in saved, f"业务配置字段丢失：{key}"
            assert saved[key] == value
    assert saved["model"] == "new-model"


@pytest.mark.asyncio
async def test_final_step_does_not_replace_full_file(ai_session):
    ai_session.context.start_setting_ai_config(123456)
    ai_session.context.update_ai_config(123456, "api_key", secrets.token_urlsafe(24))
    ai_session.context.update_ai_config(123456, "base_url", "https://new.example/v1")
    await commands.handle_ai_config_input(event("new-model"))
    saved = json.loads(ai_session.path.read_text(encoding="utf-8"))
    assert "channels" in saved, "AI 三字段覆盖了完整配置"
    assert saved["channels"] == ai_session.original["channels"]


@pytest.mark.asyncio
@pytest.mark.parametrize("skipped", ["api_key", "base_url", "model", "all"])
async def test_skip_does_not_write_or_overwrite_latest_values(ai_session, skipped):
    await commands.handle_set_ai_config(event("/setaicfg"))
    updates = {
        "api_key": secrets.token_urlsafe(24),
        "base_url": "https://new.example/v1",
        "model": "new-model",
    }
    expected = deepcopy(ai_session.original)
    for field, value in updates.items():
        if skipped in {field, "all"}:
            await commands.handle_ai_config_input(event("/skip"))
        else:
            expected[field] = value
            await commands.handle_ai_config_input(event(value))
        if field == "api_key" and skipped in {field, "all"}:
            # 管理员跳过后，另一入口更新该字段；最终提交不能恢复旧值。
            expected[field] = secrets.token_urlsafe(24)
            config_module.update_config_fields({field: expected[field]})
    assert json.loads(ai_session.path.read_text(encoding="utf-8")) == expected


@pytest.mark.asyncio
@pytest.mark.parametrize("completed_steps", [0, 1, 2])
async def test_cancel_does_not_write_any_file(ai_session, completed_steps):
    original = ai_session.path.read_bytes()
    await commands.handle_set_ai_config(event("/setaicfg"))
    for _ in range(completed_steps):
        await commands.handle_ai_config_input(event("/skip"))
    await commands.handle_ai_config_input(event("/cancel"))
    assert ai_session.path.read_bytes() == original
    assert not ai_session.context.is_setting_ai_config(123456)
    assert not ai_session.path.with_name("config.json.backups").exists()


@pytest.mark.asyncio
async def test_two_users_keep_independent_drafts(ai_session):
    for sender in (123456, 789012):
        await commands.handle_set_ai_config(event("/setaicfg", sender))
    first_key, second_key = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
    await commands.handle_ai_config_input(event(first_key))
    await commands.handle_ai_config_input(event(second_key, 789012))
    await commands.handle_ai_config_input(event("/cancel"))
    for value in ("/skip", "second-model"):
        await commands.handle_ai_config_input(event(value, 789012))
    saved = json.loads(ai_session.path.read_text(encoding="utf-8"))
    assert saved["api_key"] == second_key
    assert saved["model"] == "second-model"
    assert saved["channels"] == ai_session.original["channels"]


@pytest.mark.asyncio
@pytest.mark.parametrize("length", [1, 8, 10, 20, 21, 40])
async def test_secret_is_not_logged_or_echoed_for_any_length(ai_session, caplog, length):
    # 使用运行时生成的私用字符，避免单字符测试误匹配普通 UI 文本。
    secret = chr(0xE000 + secrets.randbelow(1000)) * length
    caplog.set_level(logging.DEBUG)
    replies = []
    for handler, value in [
        (commands.handle_set_ai_config, "/setaicfg " + secret),
        (commands.handle_ai_config_input, secret),
        (commands.handle_ai_config_input, "https://new.example/v1"),
        (commands.handle_ai_config_input, "new-model"),
        (commands.handle_show_ai_config, "/showaicfg " + secret),
    ]:
        message = event(value)
        await handler(message)
        replies.extend(str(call.args[0]) for call in message.reply.call_args_list)
    assert secret not in caplog.text
    assert all(secret not in reply for reply in replies)
    assert any("https://new.example/v1" in reply for reply in replies)
    assert all("{url}" not in reply for reply in replies)


@pytest.mark.asyncio
async def test_failure_is_safe_and_can_be_retried(ai_session, monkeypatch, caplog):
    original = ai_session.path.read_bytes()
    secret = secrets.token_urlsafe(24)
    await commands.handle_set_ai_config(event("/setaicfg"))
    await commands.handle_ai_config_input(event(secret))
    await commands.handle_ai_config_input(event("/skip"))
    real_update = commands.update_config_fields

    def fail(_fields):
        raise OSError(secret)

    monkeypatch.setattr(commands, "update_config_fields", fail)
    message = event("new-model")
    await commands.handle_ai_config_input(message)
    assert ai_session.path.read_bytes() == original
    assert ai_session.context.is_setting_ai_config(123456)
    assert ai_session.context.get_ai_config(123456)["model"] is None
    assert secret not in caplog.text
    assert secret not in message.reply.call_args.args[0]
    monkeypatch.setattr(commands, "update_config_fields", real_update)
    await commands.handle_ai_config_input(event("retry-model"))
    assert json.loads(ai_session.path.read_text(encoding="utf-8"))["model"] == "retry-model"


@pytest.mark.asyncio
async def test_missing_or_invalid_file_reports_failure_without_false_success(ai_session):
    await commands.handle_set_ai_config(event("/setaicfg"))
    await commands.handle_ai_config_input(event("/skip"))
    await commands.handle_ai_config_input(event("/skip"))
    ai_session.path.write_text("{", encoding="utf-8")
    message = event("new-model")
    await commands.handle_ai_config_input(message)
    assert ai_session.path.read_text(encoding="utf-8") == "{"
    assert ai_session.context.is_setting_ai_config(123456)
    assert message.reply.call_args.args[0] == commands.get_text("aicfg.save_failed")


@pytest.mark.asyncio
async def test_ai_input_cannot_be_consumed_as_either_prompt(ai_session, monkeypatch, caplog):
    from core.commands import prompt_commands

    context = ai_session.context
    monkeypatch.setattr(prompt_commands, "get_user_context", lambda: context)
    save_prompt, save_poll = AsyncMock(), AsyncMock()
    monkeypatch.setattr(prompt_commands, "save_prompt", save_prompt)
    monkeypatch.setattr(prompt_commands, "save_poll_prompt", save_poll)
    context.start_setting_prompt(789012)
    context.start_setting_prompt(123456)
    context.start_setting_poll_prompt(123456)
    await commands.handle_set_ai_config(event("/setaicfg"))
    assert not context.is_waiting_for_prompt(123456)
    assert not context.is_waiting_for_poll_prompt(123456)
    assert context.is_waiting_for_prompt(789012)
    # 即使旧状态再次重叠，两个通用输入处理器也必须让出敏感 AI 会话。
    context.start_setting_prompt(123456)
    context.start_setting_poll_prompt(123456)
    secret = secrets.token_urlsafe(24)
    message = event(secret)
    await prompt_commands.handle_prompt_input(message)
    await prompt_commands.handle_poll_prompt_input(message)
    await commands.handle_ai_config_input(message)
    save_prompt.assert_not_called()
    save_poll.assert_not_called()
    assert secret not in caplog.text
    assert all(secret not in call.args[0] for call in message.reply.call_args_list)


@pytest.mark.asyncio
async def test_webui_snapshot_and_ai_patch_keep_both_updates(ai_session):
    from core.web_api import deps

    web_config = deps.get_config()
    web_config["channels"].append("https://t.me/another")
    await commands.handle_set_ai_config(event("/setaicfg"))
    for value in ("/skip", "/skip", "new-model"):
        await commands.handle_ai_config_input(event(value))
    deps.write_config(web_config)
    saved = json.loads(ai_session.path.read_text(encoding="utf-8"))
    assert saved["model"] == "new-model"
    assert saved["channels"] == ["https://t.me/example", "https://t.me/another"]


def test_read_failure_fallback_cannot_be_written(ai_session):
    from core.config.persistence import ConfigPersistenceError

    ai_session.path.write_text("{", encoding="utf-8")
    fallback = config_module.load_config()
    assert fallback == {}
    fallback["model"] = "new-model"
    with pytest.raises(ConfigPersistenceError):
        config_module.save_config_changes(fallback)
    assert ai_session.path.read_text(encoding="utf-8") == "{"


def test_legacy_schedule_poll_and_forwarding_write_entries(ai_session):
    channel = "https://t.me/example"
    assert config_module.set_channel_schedule_v2(channel, "daily", hour=12, minute=30)
    assert config_module.set_channel_poll_config(channel, enabled=False)
    assert config_module.set_channel_auto_poll_config(channel, enabled=True)
    assert config_module.add_forwarding_rule("source", "target")
    assert config_module.remove_forwarding_rule("source", "target")
    assert config_module.delete_channel_schedule(channel)
    assert config_module.delete_channel_poll_config(channel)
    assert config_module.delete_channel_auto_poll_config(channel)
    saved = json.loads(ai_session.path.read_text(encoding="utf-8"))
    assert saved["summary_schedules"] == {}
    assert saved["channel_poll_settings"] == {}
    assert saved["channel_auto_poll_settings"] == {}
    assert saved["forwarding_rules"] == []
    for key in ("api_key", "channels", "forwarding", "future_option", "comment_welcome"):
        assert saved[key] == ai_session.original[key]


def test_schedule_and_poll_managers_preserve_other_channels_and_fields(ai_session):
    from core.infrastructure.config.channel_config import ChannelScheduleManager
    from core.infrastructure.config.poll_config import ChannelPollConfigManager

    channel = "https://t.me/example"
    schedules = ChannelScheduleManager(config_file=str(ai_session.path))
    first = ChannelPollConfigManager(str(ai_session.path))
    stale = ChannelPollConfigManager(str(ai_session.path))
    schedules.set_schedule("another", "daily", hour=10)
    first.set_config(channel, send_to_channel=True)
    stale.set_config(channel, public_voters=False)
    assert first.delete_config(channel)
    assert schedules.delete_schedule(channel)
    saved = json.loads(ai_session.path.read_text(encoding="utf-8"))
    assert channel not in saved["channel_poll_settings"]
    assert set(saved["summary_schedules"]) == {"another"}
    for key in ("api_key", "channels", "forwarding", "future_option", "comment_welcome"):
        assert saved[key] == ai_session.original[key]


@pytest.mark.asyncio
async def test_webui_channel_poll_forwarding_and_welcome_write_entries(ai_session):
    from core.web_api.routes import channels, forwarding, interaction

    channel = "https://t.me/example"
    await interaction.update_poll_settings(channel, interaction.PollSettingsUpdate(enabled=False))
    await interaction.delete_channel_poll_settings(channel)
    await forwarding.add_rule(
        forwarding.ForwardingRuleCreate(source_channel="source", target_channel="target")
    )
    await forwarding.delete_rule(0)
    await interaction.update_channel_comment_welcome(
        channel, interaction.CommentWelcomeUpdate(enabled=False)
    )
    added = await channels.add_channel(channels.ChannelAddRequest(channel_url="another"))
    deleted = await channels.delete_channel(channels.ChannelDeleteRequest(channel_url="another"))
    assert added.success and deleted.success
    saved = json.loads(ai_session.path.read_text(encoding="utf-8"))
    assert saved["channel_poll_settings"] == {}
    assert saved["forwarding"]["rules"] == []
    assert saved["channel_comment_welcome"][channel]["enabled"] is False
    for key in ("api_key", "channels", "summary_schedules", "future_option", "comment_welcome"):
        assert saved[key] == ai_session.original[key]


@pytest.mark.asyncio
async def test_failed_webui_save_does_not_schedule_reload(ai_session, monkeypatch):
    from core.config.persistence import ConfigPersistenceError
    from core.web_api import deps

    manager = SimpleNamespace(reload_config=AsyncMock())
    monkeypatch.setattr(deps, "_config_manager", manager)
    config = deps.get_config()
    config["channels"] = "invalid"
    with pytest.raises(ConfigPersistenceError):
        deps.write_config(config)
    manager.reload_config.assert_not_called()


def test_saved_poll_switch_updates_runtime_only_after_commit(ai_session):
    config_module.update_config_fields({"enable_poll": True})
    config_module.update_config_fields({"enable_poll": False})
    assert config_module.ENABLE_POLL is False


@pytest.mark.asyncio
async def test_forwarding_command_preserves_newer_webui_rule_fields(ai_session, monkeypatch):
    from core.commands import forwarding_commands

    forwarding = {
        "enabled": True,
        "rules": [
            {
                "source_channel": "https://t.me/source",
                "target_channel": "https://t.me/target",
                "keywords": [],
                "copy_mode": False,
            }
        ],
    }
    config_module.update_config_fields({"forwarding": forwarding})
    handler = SimpleNamespace(config=deepcopy(forwarding))
    web_config = config_module.read_config()
    web_config["forwarding"]["rules"][0]["copy_mode"] = True
    config_module.save_config_changes(web_config)
    monkeypatch.setattr(forwarding_commands, "check_admin_permission", AsyncMock(return_value=True))
    message = SimpleNamespace(
        message="/forwarding_keywords source target add new-filter",
        sender_id=123456,
        reply=AsyncMock(),
    )
    await forwarding_commands.cmd_forwarding_keywords(None, message, handler)
    saved = json.loads(ai_session.path.read_text(encoding="utf-8"))
    assert saved["forwarding"]["rules"][0]["keywords"] == ["new-filter"]
    assert saved["forwarding"]["rules"][0]["copy_mode"] is True
    assert saved["future_option"] == ai_session.original["future_option"]
