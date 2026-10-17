# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""配置事务、失败恢复、并发修改及不可变历史的回归测试。"""

import asyncio
import json
import os
import secrets
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor

import pytest

from core.config import persistence
from core.config.event_bus import AsyncIOEventBus
from core.config.events import ConfigChangedEvent, ConfigValidationErrorEvent
from core.config.manager import ConfigManager
from core.config.persistence import ConfigConflictError, ConfigPersistenceError, ConfigStore


@pytest.fixture
def store(tmp_path):
    path = tmp_path / "config.json"
    config = {
        "api_key": secrets.token_urlsafe(24),
        "base_url": "https://example.invalid/v1",
        "model": "example-model",
        "channels": ["one", "two"],
        "summary_schedules": {"one": {"frequency": "daily", "hour": 9, "minute": 0}},
        "channel_poll_settings": {"one": {"enabled": True}, "two": {"enabled": False}},
        "forwarding": {
            "enabled": True,
            "rules": [{"source_channel": "one", "target_channel": "two"}],
        },
        "future": {"nested": [1, {"keep": True}]},
    }
    path.write_text(json.dumps(config), encoding="utf-8")
    return ConfigStore(path)


def test_patch_preserves_unknown_fields_and_replacement_deletes(store):
    original = store.read()
    saved = store.update_fields({"model": "new-model"})
    assert saved == {**original, "model": "new-model"}
    del saved["future"]
    result = store.replace(saved)
    assert "future" not in result
    assert result["channels"] == original["channels"]


@pytest.mark.parametrize("invalid", ["{", "[]", "null", '{"channels": "wrong"}', '{"model": []}'])
def test_invalid_existing_config_is_never_overwritten(store, invalid):
    store.initialize()
    backup = store.backup_path.read_bytes()
    histories = {path.name: path.read_bytes() for path in store._history_files()}
    store.path.write_text(invalid, encoding="utf-8")
    with pytest.raises(ConfigPersistenceError):
        store.update_fields({"model": "new-model"})
    assert store.path.read_text(encoding="utf-8") == invalid
    assert store.backup_path.read_bytes() == backup
    assert histories == {path.name: path.read_bytes() for path in store._history_files()}


def test_missing_config_and_missing_read_baseline_are_rejected(store):
    snapshot = store.read()
    store.path.unlink()
    with pytest.raises(ConfigPersistenceError):
        store.update_fields({"model": "new-model"})
    with pytest.raises(ConfigPersistenceError):
        store.save_changes(snapshot)
    assert not store.path.exists()
    with pytest.raises(ConfigPersistenceError, match="基线"):
        store.save_changes({"model": "new-model"})


@pytest.mark.parametrize(
    "fields", [{"summary_schedules": []}, {"channels": None}, {"log_level": "invalid"}]
)
def test_invalid_candidate_is_rejected_before_backup_or_write(store, fields):
    original = store.path.read_bytes()
    with pytest.raises(ConfigPersistenceError):
        store.update_fields(fields)
    assert store.path.read_bytes() == original
    assert not store.backup_path.exists()
    assert not store.history_dir.exists()


def test_three_field_draft_and_bulk_deletion_are_rejected(store):
    original = store.path.read_bytes()
    with pytest.raises(ConfigPersistenceError, match="删除"):
        store.replace(
            {
                key: value
                for key, value in store.read().items()
                if key in {"api_key", "base_url", "model"}
            }
        )
    assert store.path.read_bytes() == original


def test_stale_snapshots_merge_different_nested_paths_and_preserve_deletions(store):
    first, second = store.read(), store.read()
    del first["channel_poll_settings"]["one"]
    second["channel_poll_settings"]["two"]["enabled"] = True
    store.save_changes(second)
    result = store.save_changes(first)
    assert result["channel_poll_settings"] == {"two": {"enabled": True}}
    assert result["future"] == first["future"]


def test_stale_snapshots_add_distinct_paths_to_new_mapping(store):
    first, second = store.read(), store.read()
    first["new_option"] = {"one": 1}
    second["new_option"] = {"two": 2}
    store.save_changes(second)
    assert store.save_changes(first)["new_option"] == {"one": 1, "two": 2}


def test_conflicting_field_or_array_changes_are_rejected(store):
    for key, left_value, right_value in [
        ("model", "left", "right"),
        ("channels", ["one"], ["two"]),
    ]:
        left, right = store.read(), store.read()
        left[key], right[key] = left_value, right_value
        store.save_changes(right)
        committed = store.path.read_bytes()
        with pytest.raises(ConfigConflictError):
            store.save_changes(left)
        assert store.path.read_bytes() == committed


def test_threads_do_not_lose_independent_updates(store):
    original = store.read()

    def update(index):
        return ConfigStore(store.path).update_fields({f"worker_{index}": index})

    with ThreadPoolExecutor(max_workers=6) as executor:
        list(executor.map(update, range(12)))
    saved = store.read()
    assert saved == {**original, **{f"worker_{index}": index for index in range(12)}}


def test_processes_share_the_same_transaction_lock(store):
    script = "from core.config.persistence import ConfigStore; import sys; ConfigStore(sys.argv[1]).update_fields({sys.argv[2]: True})"
    workers = [
        subprocess.Popen(
            [sys.executable, "-c", script, str(store.path), f"process_{index}"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        for index in range(4)
    ]
    for worker in workers:
        _, errors = worker.communicate(timeout=30)
        assert worker.returncode == 0, errors.decode("utf-8", errors="replace")
    saved = store.read()
    assert all(saved[f"process_{index}"] is True for index in range(4))


@pytest.mark.parametrize("failure", ["backup", "replace", "fsync"])
def test_failed_io_preserves_original_and_recovery_versions(store, monkeypatch, failure):
    store.initialize()
    original = store.path.read_bytes()
    backup = store.backup_path.read_bytes()
    real_replace = persistence.os.replace

    def replace(source, target):
        if (failure == "backup" and target == store.backup_path) or (
            failure == "replace" and target == store.path
        ):
            raise OSError("模拟磁盘写入失败")
        return real_replace(source, target)

    monkeypatch.setattr(persistence.os, "replace", replace)
    if failure == "fsync":
        monkeypatch.setattr(
            persistence.os, "fsync", lambda _fd: (_ for _ in ()).throw(OSError("模拟同步失败"))
        )
    with pytest.raises(ConfigPersistenceError):
        store.update_fields({"model": "new-model"})
    assert store.path.read_bytes() == original
    assert store.backup_path.read_bytes() == backup
    assert not list(store.path.parent.glob("*.tmp"))


def test_history_is_immutable_bounded_and_restart_does_not_overwrite_backup(store, monkeypatch):
    monkeypatch.setattr(persistence, "BACKUP_LIMIT", 3)
    store.initialize()
    first = store._history_files()[0]
    initial = first.read_bytes()
    store.update_fields({"model": "version-1"})
    last_valid = store.backup_path.read_bytes()
    for _ in range(3):
        ConfigStore(store.path).initialize()
    assert store.backup_path.read_bytes() == last_valid
    assert first.read_bytes() == initial
    for index in range(6):
        store.update_fields({"model": f"version-{index + 2}"})
    assert len(store._history_files()) == 3
    assert all(
        isinstance(json.loads(path.read_text(encoding="utf-8")), dict)
        for path in store._history_files()
    )


@pytest.mark.asyncio
async def test_restart_refuses_silent_field_loss_without_poisoning_backups(store):
    assert ConfigManager(asyncio.get_running_loop()).initialize_sync(store.path)[0]
    histories = {path.name: path.read_bytes() for path in store._history_files()}
    backup = store.backup_path.read_bytes()
    store.path.write_text(json.dumps({"model": "new-model"}), encoding="utf-8")
    for _ in range(3):
        success, _ = ConfigManager(asyncio.get_running_loop()).initialize_sync(store.path)
        assert not success
    assert store.backup_path.read_bytes() == backup
    assert histories == {path.name: path.read_bytes() for path in store._history_files()}


@pytest.mark.asyncio
async def test_invalid_reload_uses_history_when_last_valid_is_corrupt(store):
    original = store.initialize()
    store.backup_path.write_text("{", encoding="utf-8")
    store.path.write_text("[", encoding="utf-8")
    manager = ConfigManager(asyncio.get_running_loop())
    manager._config_file = store.path
    manager.event_bus = AsyncIOEventBus()
    errors = []

    async def receive(error):
        errors.append(error)

    await manager.event_bus.subscribe(ConfigValidationErrorEvent, receive)
    success, _ = await manager.reload_config()
    assert not success
    assert json.loads(store.path.read_text(encoding="utf-8")) == original
    assert errors[0].rolled_back
    assert store.backup_path.read_text(encoding="utf-8") == "{"


@pytest.mark.asyncio
async def test_reload_and_restart_preserve_config_and_only_publish_once(store):
    manager = ConfigManager(asyncio.get_running_loop())
    assert manager.initialize_sync(store.path)[0]
    manager.event_bus = AsyncIOEventBus()
    events = []

    async def receive(event):
        events.append(event)

    await manager.event_bus.subscribe(ConfigChangedEvent, receive)
    saved = store.update_fields({"model": "new-model"})
    assert all(
        success
        for success, _ in await asyncio.gather(manager.reload_config(), manager.reload_config())
    )
    assert len(events) == 1
    assert events[0].changed_fields == {"model"}
    for _ in range(3):
        restarted = ConfigManager(asyncio.get_running_loop())
        assert restarted.initialize_sync(store.path)[0]
        assert await restarted.get_config() == saved
    snapshot = await manager.get_config()
    snapshot.clear()
    assert await manager.get_config() == saved
    assert saved["api_key"] not in repr(events[0])


def test_full_json_serialization_failure_never_truncates_file(store):
    original = store.path.read_bytes()
    with pytest.raises(ConfigPersistenceError):
        store.update_fields({"future": {"invalid": object()}})
    assert store.path.read_bytes() == original


def test_validation_errors_do_not_echo_pasted_secrets(store):
    secret = secrets.token_urlsafe(32)
    with pytest.raises(ConfigPersistenceError) as caught:
        store.update_fields({"log_level": secret})
    assert secret not in str(caught.value)


def test_rollback_prefers_newest_recoverable_version_to_stale_memory(store):
    old_snapshot = store.initialize()
    store.update_fields({"model": "first-commit"})
    store.update_fields({"model": "second-commit"})
    store.path.write_text("{", encoding="utf-8")
    restored, error, rolled_back = store.reload(old_snapshot)
    assert error and rolled_back
    assert restored["model"] == "first-commit"
    assert json.loads(store.path.read_text(encoding="utf-8"))["model"] == "first-commit"


def test_reload_keeps_multiple_successful_deletions_since_last_notification(store):
    old_snapshot = store.initialize()
    first = store.read()
    for key in ("api_key", "base_url"):
        del first[key]
    store.save_changes(first)
    second = store.read()
    for key in ("model", "future"):
        del second[key]
    committed = store.save_changes(second)
    restored, error, rolled_back = store.reload(old_snapshot)
    assert error == ""
    assert not rolled_back
    assert restored == committed
    assert json.loads(store.path.read_text(encoding="utf-8")) == committed


@pytest.mark.parametrize("method", ["update_fields", "save_changes"])
def test_json_value_type_change_is_a_real_change(store, method):
    store.update_fields({"future": {"value": True}})
    if method == "update_fields":
        store.update_fields({"future": {"value": 1}})
    else:
        snapshot = store.read()
        snapshot["future"]["value"] = 1
        store.save_changes(snapshot)
    assert type(json.loads(store.path.read_text(encoding="utf-8"))["future"]["value"]) is int


@pytest.mark.skipif(os.name != "nt", reason="Windows DACL 回归验证")
@pytest.mark.parametrize("protected", [True, False])
def test_windows_atomic_update_preserves_acl_and_creates_private_backup(store, protected):
    environment = {
        **os.environ,
        "SAKURA_TEST_ACL_ROOT": str(store.path.parent),
        "SAKURA_TEST_ACL_PROTECTED": str(protected),
    }
    setup = r"""
$ErrorActionPreference = 'Stop'
$root = $env:SAKURA_TEST_ACL_ROOT
$file = Join-Path $root 'config.json'
$users = [System.Security.Principal.SecurityIdentifier]::new('S-1-5-32-545')
$owner = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
$parent = [System.IO.Directory]::GetAccessControl($root, 'Access')
$parent.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new($users, 'ReadAndExecute', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
[System.IO.Directory]::SetAccessControl($root, $parent)
# 预先存在且继承宽松权限的备份/历史目录也不能令新凭据变为公开可读。
[System.IO.Directory]::CreateDirectory((Join-Path $root 'config.json.backups')) | Out-Null
[System.IO.File]::Copy($file, (Join-Path $root 'config.json.last_valid'))
if ($env:SAKURA_TEST_ACL_PROTECTED -eq 'True') {
    $acl = [System.IO.File]::GetAccessControl($file, 'Access')
    $acl.SetAccessRuleProtection($true, $false)
    $acl.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new($owner, 'FullControl', 'Allow'))
    [System.IO.File]::SetAccessControl($file, $acl)
}
(Get-Acl -LiteralPath $file).GetSecurityDescriptorSddlForm('Access')
"""

    def powershell(script):
        return subprocess.check_output(
            ["powershell", "-NoProfile", "-NonInteractive", "-Command", script],
            env=environment,
            text=True,
        ).strip()

    original = powershell(setup)
    store.update_fields({"model": "new-model"})
    updated = powershell(
        "(Get-Acl -LiteralPath (Join-Path $env:SAKURA_TEST_ACL_ROOT 'config.json')).GetSecurityDescriptorSddlForm('Access')"
    )

    # AI 是描述符的自动继承处理标记；比较实际 ACE 和 P 保护位，并在下面
    # 改变父目录权限，实测未受保护的配置仍会跟随继承策略更新。
    def access_policy(sddl):
        control, rules = sddl.split("(", 1)
        return control.replace("AI", ""), rules

    assert access_policy(updated) == access_policy(original)
    backup = powershell(
        "(Get-Acl -LiteralPath (Join-Path $env:SAKURA_TEST_ACL_ROOT 'config.json.last_valid')).GetSecurityDescriptorSddlForm('Access')"
    )
    assert "BU" not in backup
    assert "D:P" in backup
    history = store._history_files()[0]
    environment["SAKURA_TEST_HISTORY_FILE"] = str(history)
    history_acl = powershell(
        "(Get-Acl -LiteralPath $env:SAKURA_TEST_HISTORY_FILE).GetSecurityDescriptorSddlForm('Access')"
    )
    assert "BU" not in history_acl
    assert not list(store.path.parent.glob(".config-write-*"))
    after_parent_change = powershell(r"""
$ErrorActionPreference = 'Stop'
$root = $env:SAKURA_TEST_ACL_ROOT
$users = [System.Security.Principal.SecurityIdentifier]::new('S-1-5-32-545')
$parent = [System.IO.Directory]::GetAccessControl($root, 'Access')
$parent.SetAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new($users, 'Read', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
[System.IO.Directory]::SetAccessControl($root, $parent)
(Get-Acl -LiteralPath (Join-Path $root 'config.json')).GetSecurityDescriptorSddlForm('Access')
""")
    if protected:
        assert access_policy(after_parent_change) == access_policy(original)
    else:
        assert "(A;ID;FR;;;BU)" in after_parent_change


@pytest.mark.asyncio
async def test_atomic_replacement_triggers_real_file_watcher(store):
    from core.config.file_watcher import FileWatcher

    loop = asyncio.get_running_loop()
    manager = ConfigManager(loop)
    assert manager.initialize_sync(store.path)[0]
    manager.event_bus = AsyncIOEventBus()
    received = asyncio.Event()

    async def changed(_event):
        received.set()

    await manager.event_bus.subscribe(ConfigChangedEvent, changed)
    watcher = FileWatcher(manager, loop)
    watcher.start(str(store.path.parent))
    try:
        await asyncio.sleep(0.1)
        await asyncio.to_thread(store.update_fields, {"model": "atomic-replacement"})
        await asyncio.wait_for(received.wait(), timeout=5)
        assert (await manager.get_config())["model"] == "atomic-replacement"
    finally:
        watcher.stop()


@pytest.mark.skipif(os.name != "nt", reason="Windows DACL 失败保护")
def test_acl_copy_failure_refuses_commit(store, monkeypatch):
    original = store.path.read_bytes()

    def fail(*_args, **_kwargs):
        raise OSError("模拟 DACL 复制失败")

    monkeypatch.setattr(persistence, "_copy_windows_dacl", fail)
    with pytest.raises(ConfigPersistenceError):
        store.update_fields({"model": "new-model"})
    assert store.path.read_bytes() == original
    assert not list(store.path.parent.glob(".config-write-*"))


def test_nullable_legacy_ai_fields_can_still_be_updated(store):
    original = store.read()
    nullable = store.update_fields({"api_key": None, "base_url": None, "model": None})
    assert nullable["api_key"] is None
    updated = store.update_fields({"model": "new-model"})
    assert updated["api_key"] is None
    assert updated["base_url"] is None
    assert updated["future"] == original["future"]


@pytest.mark.skipif(os.name != "nt", reason="Windows 私有目录依赖 Python 3.13+")
def test_unsupported_windows_runtime_refuses_insecure_temporary_files(store, monkeypatch):
    original = store.path.read_bytes()
    monkeypatch.setattr(persistence, "version_info", (3, 12))
    with pytest.raises(ConfigPersistenceError, match="3.13"):
        store._atomic_write(store.path, b"{}")
    assert store.path.read_bytes() == original
    assert not list(store.path.parent.glob(".config-write-*"))
