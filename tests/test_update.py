# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

import json
import os
import subprocess
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

import pytest

from core.commands import other_commands as commands
from core.infrastructure.utils import version_utils


@pytest.fixture
def update_event(monkeypatch):
    monkeypatch.setattr(commands, "ADMIN_LIST", [123456])
    monkeypatch.setattr(commands, "get_local_version", lambda: "1.8.9-2")
    monkeypatch.setattr(commands, "get_remote_version", AsyncMock(return_value="1.8.9-3"))
    return SimpleNamespace(sender_id=123456, text="/update", reply=AsyncMock())


async def test_legacy_image_without_git_shows_rebuild_instructions(update_event, monkeypatch):
    monkeypatch.setattr(subprocess, "run", Mock(side_effect=FileNotFoundError("git")))
    pull = AsyncMock()
    monkeypatch.setattr(commands, "git_pull_latest", pull)

    await commands.handle_update(update_event)

    assert "docker compose up -d --build" in update_event.reply.await_args.args[0]
    pull.assert_not_awaited()


async def test_docker_restart_does_not_spawn_child_process(update_event, monkeypatch, tmp_path):
    import core.config as config
    from core.system import shutdown_manager

    flag = tmp_path / ".restart_flag"
    trigger = Mock()
    popen = Mock()
    monkeypatch.setattr(commands, "RESTART_FLAG_FILE", flag)
    monkeypatch.setattr(commands, "is_docker", lambda: True)
    monkeypatch.setattr(config, "trigger_shutdown", trigger)
    monkeypatch.setattr(
        shutdown_manager, "get_shutdown_manager", lambda: SimpleNamespace(detect_pm2=lambda: False)
    )
    monkeypatch.setattr(subprocess, "Popen", popen)

    await commands.handle_restart(update_event)

    assert flag.read_text() == str(update_event.sender_id)
    trigger.assert_called_once()
    popen.assert_not_called()


async def test_telegram_update_installs_builds_then_restarts(update_event, monkeypatch):
    monkeypatch.setattr(subprocess, "run", Mock(return_value=SimpleNamespace(returncode=0)))
    steps = []

    def step(name):
        async def run(*_args):
            steps.append(name)
            return True, "ok"

        return run

    for name in ("git_pull_latest", "install_dependencies", "build_frontend", "handle_restart"):
        monkeypatch.setattr(commands, name, step(name))
    monkeypatch.setattr(commands.asyncio, "sleep", AsyncMock())

    await commands.handle_update(update_event)

    assert steps == ["git_pull_latest", "install_dependencies", "build_frontend", "handle_restart"]


async def test_web_update_build_failure_does_not_restart(monkeypatch):
    import core.config as config
    from core.web_api.routes.commands import _op_update

    monkeypatch.setattr(version_utils, "git_pull_latest", AsyncMock(return_value=(True, "ok")))
    monkeypatch.setattr(version_utils, "install_dependencies", AsyncMock(return_value=(True, "ok")))
    monkeypatch.setattr(
        version_utils, "build_frontend", AsyncMock(return_value=(False, "build failed"))
    )
    trigger = Mock()
    monkeypatch.setattr(config, "trigger_shutdown", trigger)

    result = await _op_update({}, None)

    assert result["success"] is False
    assert result["message"] == "build failed"
    trigger.assert_not_called()


def _git(cwd, *args):
    result = subprocess.run(
        ["git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        check=True,
    )
    return result.stdout.strip()


@pytest.fixture
def git_deployment(tmp_path, monkeypatch):
    remote = tmp_path / "remote.git"
    source = tmp_path / "source"
    deployment = tmp_path / "deployment"
    _git(tmp_path, "init", "--bare", "--initial-branch=main", str(remote))
    _git(tmp_path, "clone", str(remote), str(source))
    (source / "app.py").write_text("old code\n")
    (source / "config.txt").write_text("default\n")
    (source / "web").mkdir()
    (source / "web/package.json").write_text('{"version":"1.0.0"}\n')
    (source / "web/package-lock.json").write_text('{"lockfileVersion":3,"version":"1.0.0"}\n')
    (source / "web/tsconfig.tsbuildinfo").write_text("old tracked cache\n")
    (source / "data").mkdir()
    for name in ("prompt.txt", "poll_prompt.txt", "qa_persona.txt"):
        (source / "data" / name).write_text("default prompt\n")
    (source / "docker-compose.yml").write_text("port: 8080\n")
    (source / ".gitignore").write_text("data/.env\ndata/config.json\n")
    _git(source, "add", ".")
    _git(source, "commit", "-m", "initial")
    _git(source, "push", "origin", "main")
    _git(tmp_path, "clone", str(remote), str(deployment))
    (source / "app.py").write_text("new code\n")
    (source / "web/package.json").write_text('{"version":"2.0.0"}\n')
    (source / "web/package-lock.json").write_text('{"lockfileVersion":3,"version":"2.0.0"}\n')
    (source / "web/tsconfig.tsbuildinfo").unlink()
    with (source / ".gitignore").open("a") as stream:
        stream.write("*.tsbuildinfo\n")
    _git(source, "commit", "-am", "update")
    _git(source, "push", "origin", "main")
    monkeypatch.chdir(deployment)
    return deployment


async def test_git_update_preserves_host_configuration(git_deployment):
    (git_deployment / "config.txt").write_text("custom config\n")
    (git_deployment / "untracked.txt").write_text("runtime data\n")
    custom = {
        "docker-compose.yml": "port: 8081\ndns: 1.1.1.1\n",
        "data/prompt.txt": "custom summary prompt\n",
        "data/poll_prompt.txt": "custom poll prompt\n",
        "data/qa_persona.txt": "custom persona\n",
        "data/.env": "TEST_SECRET=preserve-me\n",
        "data/config.json": '{"channels":["custom"]}\n',
    }
    for name, content in custom.items():
        (git_deployment / name).write_text(content)
    (git_deployment / "web/package-lock.json").write_bytes(b"legacy npm rewrite\r\n")
    (git_deployment / "web/tsconfig.tsbuildinfo").write_text("generated cache\n")

    success, _message = await version_utils.git_pull_latest()

    assert success
    assert (git_deployment / "app.py").read_text() == "new code\n"
    assert (git_deployment / "config.txt").read_text() == "custom config\n"
    assert (git_deployment / "untracked.txt").read_text() == "runtime data\n"
    for name, content in custom.items():
        assert (git_deployment / name).read_text() == content
    assert (git_deployment / "web/package-lock.json").read_text() == (
        '{"lockfileVersion":3,"version":"2.0.0"}\n'
    )
    assert not (git_deployment / "web/tsconfig.tsbuildinfo").exists()
    backup = next((git_deployment / ".git/sakura-update-backups").glob("generated-*"))
    assert (backup / "files/web/package-lock.json").read_bytes() == b"legacy npm rewrite\r\n"
    assert (backup / "files/web/tsconfig.tsbuildinfo").read_text() == "generated cache\n"
    manifest = json.loads((backup / "manifest.json").read_text())
    assert set(manifest["files"]) == {"web/package-lock.json", "web/tsconfig.tsbuildinfo"}


async def test_git_update_keeps_conflicting_local_edits(git_deployment):
    (git_deployment / "app.py").write_text("local changes\n")
    old_head = _git(git_deployment, "rev-parse", "HEAD")

    success, _message = await version_utils.git_pull_latest()

    assert not success
    assert (git_deployment / "app.py").read_text() == "local changes\n"
    assert _git(git_deployment, "rev-parse", "HEAD") == old_head


async def test_git_update_refuses_diverged_branch(git_deployment):
    (git_deployment / "local.txt").write_text("local commit\n")
    _git(git_deployment, "add", "local.txt")
    _git(git_deployment, "commit", "-m", "local commit")
    old_head = _git(git_deployment, "rev-parse", "HEAD")
    lock = git_deployment / "web/package-lock.json"
    lock.write_text("local generated lock\n")

    success, _message = await version_utils.git_pull_latest()

    assert not success
    assert _git(git_deployment, "rev-parse", "HEAD") == old_head
    assert lock.read_text() == "local generated lock\n"
    assert not (git_deployment / ".git/sakura-update-backups").exists()


@pytest.mark.parametrize("conflict", ["app.py", "docker-compose.yml", "data/prompt.txt"])
async def test_failed_update_restores_generated_files_and_keeps_conflicts(git_deployment, conflict):
    source = git_deployment.parent / "source"
    if conflict != "app.py":
        (source / conflict).write_text("new upstream default\n")
        _git(source, "commit", "-am", "update default")
        _git(source, "push", "origin", "main")
    (git_deployment / conflict).write_bytes(b"custom local content\r\n")
    originals = {
        "web/package-lock.json": b"local lock\r\n",
        "web/tsconfig.tsbuildinfo": b"local cache\r\n",
    }
    for name, content in originals.items():
        (git_deployment / name).write_bytes(content)
    old_head = _git(git_deployment, "rev-parse", "HEAD")
    old_index = _git(git_deployment, "write-tree")

    success, message = await version_utils.git_pull_latest()

    assert not success
    assert conflict in message
    assert "sakura-update-backups" in message
    assert _git(git_deployment, "rev-parse", "HEAD") == old_head
    assert _git(git_deployment, "write-tree") == old_index
    assert (git_deployment / conflict).read_bytes() == b"custom local content\r\n"
    for name, content in originals.items():
        assert (git_deployment / name).read_bytes() == content


@pytest.mark.parametrize("name", ["web/package-lock.json", "web/tsconfig.tsbuildinfo"])
async def test_staged_generated_edits_are_never_discarded(git_deployment, name):
    path = git_deployment / name
    path.write_text("staged customization\n")
    _git(git_deployment, "add", name)
    path.write_text("additional unstaged customization\n")
    old_index = _git(git_deployment, "write-tree")
    old_head = _git(git_deployment, "rev-parse", "HEAD")

    success, _message = await version_utils.git_pull_latest()

    assert not success
    assert _git(git_deployment, "rev-parse", "HEAD") == old_head
    assert _git(git_deployment, "write-tree") == old_index
    assert path.read_text() == "additional unstaged customization\n"


@pytest.mark.parametrize("staging", ["unstaged", "staged", "index_only"])
async def test_custom_dependencies_protect_the_lockfile(git_deployment, staging):
    manifest = git_deployment / "web/package.json"
    original = manifest.read_bytes()
    manifest.write_text('{"dependencies":{"custom":"1.0.0"}}\n')
    if staging != "unstaged":
        _git(git_deployment, "add", "web/package.json")
    if staging == "index_only":
        manifest.write_bytes(original)
    lock = git_deployment / "web/package-lock.json"
    lock.write_text("custom dependency lock\n")
    old_index = _git(git_deployment, "write-tree")
    old_head = _git(git_deployment, "rev-parse", "HEAD")

    success, message = await version_utils.git_pull_latest()

    assert not success
    assert "package.json" in message
    assert lock.read_text() == "custom dependency lock\n"
    assert _git(git_deployment, "write-tree") == old_index
    assert _git(git_deployment, "rev-parse", "HEAD") == old_head


async def test_update_handles_deleted_legacy_generated_file(git_deployment):
    cache = git_deployment / "web/tsconfig.tsbuildinfo"
    cache.unlink()

    success, _message = await version_utils.git_pull_latest()

    assert success
    assert not cache.exists()
    backup = next((git_deployment / ".git/sakura-update-backups").glob("generated-*"))
    manifest = json.loads((backup / "manifest.json").read_text())
    assert manifest["files"]["web/tsconfig.tsbuildinfo"]["sha256"] is None


async def test_update_backs_up_untracked_ignored_cache(git_deployment):
    assert (await version_utils.git_pull_latest())[0]
    cache = git_deployment / "web/tsconfig.tsbuildinfo"
    cache.write_text("ignored old cache\n")

    success, _message = await version_utils.git_pull_latest()

    assert success
    assert not cache.exists()
    backup = next((git_deployment / ".git/sakura-update-backups").glob("generated-*"))
    assert (backup / "files/web/tsconfig.tsbuildinfo").read_text() == "ignored old cache\n"


async def test_update_handles_untracked_lock_conflicting_with_incoming_file(git_deployment):
    source = git_deployment.parent / "source"
    (source / "web/package-lock.json").unlink()
    _git(source, "commit", "-am", "legacy source without lock")
    _git(source, "push", "origin", "main")
    assert (await version_utils.git_pull_latest())[0]
    lock = git_deployment / "web/package-lock.json"
    lock.write_bytes(b"untracked npm-generated lock\r\n")
    (source / "web/package-lock.json").write_text("new committed lock\n")
    _git(source, "add", "web/package-lock.json")
    _git(source, "commit", "-m", "restore committed lock")
    _git(source, "push", "origin", "main")

    success, _message = await version_utils.git_pull_latest()

    assert success
    assert lock.read_text() == "new committed lock\n"
    backup = next((git_deployment / ".git/sakura-update-backups").glob("generated-*"))
    assert (
        backup / "files/web/package-lock.json"
    ).read_bytes() == b"untracked npm-generated lock\r\n"


async def test_linked_worktree_uses_its_actual_git_directory(git_deployment, monkeypatch):
    linked = git_deployment.parent / "linked-deployment"
    _git(git_deployment, "worktree", "add", "-b", "linked-deployment", str(linked), "HEAD")
    (linked / "web/package-lock.json").write_text("generated worktree lock\n")
    (linked / "data/prompt.txt").write_text("worktree custom prompt\n")
    monkeypatch.chdir(linked)

    success, _message = await version_utils.git_pull_latest()

    assert success
    assert (linked / ".git").is_file()
    assert (linked / "data/prompt.txt").read_text() == "worktree custom prompt\n"
    backup_root = linked / _git(linked, "rev-parse", "--git-path", "sakura-update-backups")
    backup = next(backup_root.glob("generated-*"))
    assert (backup / "files/web/package-lock.json").read_text() == "generated worktree lock\n"


async def test_backup_failure_leaves_worktree_and_head_untouched(git_deployment, monkeypatch):
    lock = git_deployment / "web/package-lock.json"
    lock.write_text("original local lock\n")
    old_head = _git(git_deployment, "rev-parse", "HEAD")

    def fail_backup(*_args, **_kwargs):
        raise OSError("backup disk unavailable")

    monkeypatch.setattr(version_utils.tempfile, "mkdtemp", fail_backup)

    success, _message = await version_utils.git_pull_latest()

    assert not success
    assert lock.read_text() == "original local lock\n"
    assert _git(git_deployment, "rev-parse", "HEAD") == old_head


async def test_merge_timeout_restores_generated_files(git_deployment, monkeypatch):
    lock = git_deployment / "web/package-lock.json"
    lock.write_bytes(b"original local lock\r\n")
    old_head = _git(git_deployment, "rev-parse", "HEAD")
    run_git = version_utils._run_git

    def timeout_merge(*args, **kwargs):
        if args[0] == "merge":
            raise subprocess.TimeoutExpired("git merge", 30)
        return run_git(*args, **kwargs)

    monkeypatch.setattr(version_utils, "_run_git", timeout_merge)

    success, message = await version_utils.git_pull_latest()

    assert not success
    assert "sakura-update-backups" in message
    assert lock.read_bytes() == b"original local lock\r\n"
    assert _git(git_deployment, "rev-parse", "HEAD") == old_head


async def test_failed_merge_does_not_overwrite_concurrent_edits(git_deployment, monkeypatch):
    lock = git_deployment / "web/package-lock.json"
    lock.write_text("original local lock\n")
    (git_deployment / "app.py").write_text("conflicting local code\n")
    run_git = version_utils._run_git

    def concurrent_edit(*args, **kwargs):
        if args[0] == "merge":
            lock.write_text("concurrent dependency edit\n")
        return run_git(*args, **kwargs)

    monkeypatch.setattr(version_utils, "_run_git", concurrent_edit)

    success, _message = await version_utils.git_pull_latest()

    assert not success
    assert lock.read_text() == "concurrent dependency edit\n"
    backup = next((git_deployment / ".git/sakura-update-backups").glob("generated-*"))
    assert (backup / "files/web/package-lock.json").read_text() == "original local lock\n"


@pytest.mark.skipif(os.name == "nt", reason="Windows symlinks require optional privileges")
async def test_generated_symlink_never_modifies_its_target(git_deployment):
    lock = git_deployment / "web/package-lock.json"
    outside = git_deployment.parent / "outside.json"
    outside.write_text("external configuration\n")
    lock.unlink()
    lock.symlink_to(outside)
    old_head = _git(git_deployment, "rev-parse", "HEAD")

    success, _message = await version_utils.git_pull_latest()

    assert not success
    assert lock.is_symlink()
    assert outside.read_text() == "external configuration\n"
    assert _git(git_deployment, "rev-parse", "HEAD") == old_head
