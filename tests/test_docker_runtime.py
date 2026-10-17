# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

import subprocess
from unittest.mock import Mock

import pytest

from core import docker_runtime as runtime


@pytest.fixture
def project(tmp_path, monkeypatch):
    root = tmp_path / "project"
    root.mkdir()
    (root / "requirements.txt").write_text("example==1.0\n")
    (root / "web/src").mkdir(parents=True)
    (root / "web/package.json").write_text("{}")
    (root / "web/package-lock.json").write_text("{}")
    (root / "web/src/App.vue").write_text("<template>old</template>")
    monkeypatch.setenv("SAKURA_DOCKER", "1")
    monkeypatch.setattr(runtime, "RUNTIME_DIR", tmp_path / "runtime")
    runtime.record_runtime_state("requirements", root)
    runtime.record_runtime_state("frontend", root)
    return root


def test_fingerprint_tracks_source_but_ignores_generated_files(project):
    before = runtime.dependency_fingerprint("frontend", project)
    for directory in ("node_modules", "dist", ".vite"):
        path = project / "web" / directory
        path.mkdir()
        (path / "generated.js").write_text("generated")
    (project / "web/tsconfig.tsbuildinfo").write_text("generated")
    assert runtime.dependency_fingerprint("frontend", project) == before
    (project / "web/src/App.vue").write_text("<template>new</template>")
    assert runtime.dependency_fingerprint("frontend", project) != before


def test_mounted_source_restores_matching_image_frontend(project, monkeypatch):
    cache = runtime.RUNTIME_DIR / "web-dist"
    cache.mkdir()
    (cache / "index.html").write_text("image frontend")
    (runtime.RUNTIME_DIR / "image-frontend.sha256").write_text(
        runtime.dependency_fingerprint("frontend", project)
    )
    run = Mock()
    monkeypatch.setattr(runtime.subprocess, "run", run)

    runtime.prepare_runtime(project)

    assert (project / "web/dist/index.html").read_text() == "image frontend"
    run.assert_not_called()


def test_restart_reuses_synced_dependencies_and_frontend(project, monkeypatch):
    (project / "web/dist").mkdir()
    (project / "web/dist/index.html").write_text("current frontend")
    run = Mock()
    monkeypatch.setattr(runtime.subprocess, "run", run)
    runtime.prepare_runtime(project)
    run.assert_not_called()


@pytest.mark.parametrize("has_lockfile", [True, False])
def test_recreated_container_syncs_updated_mounted_source(project, monkeypatch, has_lockfile):
    (project / "requirements.txt").write_text("example==2.0\n")
    (project / "web/src/App.vue").write_text("<template>new</template>")
    if not has_lockfile:
        (project / "web/package-lock.json").unlink()
    run = Mock()
    monkeypatch.setattr(runtime.subprocess, "run", run)
    monkeypatch.setattr(runtime.shutil, "which", lambda _name: "npm")

    runtime.prepare_runtime(project)

    args = [call.args[0] for call in run.call_args_list]
    assert args[0][1:] == ["-m", "pip", "install", "-r", "requirements.txt"]
    install = (
        ["npm", "ci", "--include=dev"]
        if has_lockfile
        else ["npm", "install", "--include=dev", "--package-lock=false"]
    )
    assert args[1:] == [install, ["npm", "run", "build"]]
    assert runtime._read_marker("requirements") == runtime.dependency_fingerprint(
        "requirements", project
    )
    assert runtime._read_marker("frontend") == runtime.dependency_fingerprint("frontend", project)


@pytest.mark.parametrize("failed_step", ["requirements", "frontend"])
def test_failed_sync_keeps_old_marker_for_retry(project, monkeypatch, failed_step):
    old_marker = runtime._read_marker(failed_step)
    path = project / ("requirements.txt" if failed_step == "requirements" else "web/src/App.vue")
    path.write_text("changed")
    monkeypatch.setattr(
        runtime.subprocess,
        "run",
        Mock(side_effect=subprocess.CalledProcessError(1, ["install"])),
    )
    with pytest.raises(subprocess.CalledProcessError):
        runtime.prepare_runtime(project)
    assert runtime._read_marker(failed_step) == old_marker


def test_source_install_does_not_write_docker_state(project, monkeypatch):
    before = runtime._read_marker("requirements")
    monkeypatch.delenv("SAKURA_DOCKER")
    (project / "requirements.txt").write_text("changed")
    runtime.record_runtime_state("requirements", project)
    assert runtime._read_marker("requirements") == before
