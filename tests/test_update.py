# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

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
    _git(source, "add", ".")
    _git(source, "commit", "-m", "initial")
    _git(source, "push", "origin", "main")
    _git(tmp_path, "clone", str(remote), str(deployment))
    (source / "app.py").write_text("new code\n")
    _git(source, "commit", "-am", "update")
    _git(source, "push", "origin", "main")
    monkeypatch.chdir(deployment)
    return deployment


async def test_git_update_preserves_host_configuration(git_deployment):
    (git_deployment / "config.txt").write_text("custom config\n")
    (git_deployment / "untracked.txt").write_text("runtime data\n")

    success, _message = await version_utils.git_pull_latest()

    assert success
    assert (git_deployment / "app.py").read_text() == "new code\n"
    assert (git_deployment / "config.txt").read_text() == "custom config\n"
    assert (git_deployment / "untracked.txt").read_text() == "runtime data\n"


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

    success, _message = await version_utils.git_pull_latest()

    assert not success
    assert _git(git_deployment, "rev-parse", "HEAD") == old_head
