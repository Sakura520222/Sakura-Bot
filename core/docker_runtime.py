# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""源码挂载部署的启动准备；仅使用标准库，以便安装应用依赖前执行。"""

import hashlib
import os
import shutil
import subprocess
import sys
from pathlib import Path

RUNTIME_DIR = Path("/opt/sakura")


def is_docker() -> bool:
    """识别官方镜像和旧版 Docker 部署。"""
    return os.environ.get("SAKURA_DOCKER") == "1" or Path("/.dockerenv").is_file()


def dependency_fingerprint(kind: str, root: Path = Path(".")) -> str:
    """计算依赖/前端源码指纹，排除安装、构建生成的文件。"""
    digest = hashlib.sha256()
    if kind == "requirements":
        digest.update((root / "requirements.txt").read_bytes())
    elif kind == "frontend":
        web = root / "web"
        for directory, dirs, files in os.walk(web):
            dirs[:] = sorted(
                name
                for name in dirs
                if name not in {"node_modules", "dist", ".vite", ".git"}
                and not (Path(directory) / name).is_symlink()
            )
            for name in sorted(files):
                path = Path(directory) / name
                if path.is_symlink() or name.endswith(".tsbuildinfo"):
                    continue
                digest.update(path.relative_to(web).as_posix().encode())
                digest.update(b"\0")
                digest.update(path.read_bytes())
                digest.update(b"\0")
    else:
        raise ValueError(f"未知依赖类型: {kind}")
    return digest.hexdigest()


def record_runtime_state(kind: str, root: Path = Path(".")) -> None:
    """更新成功后记录容器内状态，避免重启时重复安装和构建。"""
    if os.environ.get("SAKURA_DOCKER") == "1":
        RUNTIME_DIR.mkdir(parents=True, exist_ok=True)
        (RUNTIME_DIR / f"{kind}.sha256").write_text(
            dependency_fingerprint(kind, root), encoding="ascii"
        )


def _read_marker(name: str) -> str:
    path = RUNTIME_DIR / f"{name}.sha256"
    return path.read_text(encoding="ascii").strip() if path.is_file() else ""


def prepare_runtime(root: Path = Path(".")) -> None:
    """同步挂载源码所需的依赖；失败时停止启动，保留旧状态供重试。"""
    if dependency_fingerprint("requirements", root) != _read_marker("requirements"):
        print("同步 Python 依赖...", flush=True)
        subprocess.run(
            [sys.executable, "-m", "pip", "install", "-r", "requirements.txt"],
            cwd=root,
            check=True,
        )
        record_runtime_state("requirements", root)

    if not (root / "web/package.json").is_file():
        return

    fingerprint = dependency_fingerprint("frontend", root)
    if (root / "web/dist/index.html").is_file() and fingerprint == _read_marker("frontend"):
        return

    cache = RUNTIME_DIR / "web-dist"
    if fingerprint == _read_marker("image-frontend") and (cache / "index.html").is_file():
        print("恢复镜像中的 WebUI 构建产物...", flush=True)
        shutil.copytree(cache, root / "web/dist", dirs_exist_ok=True)
    else:
        print("构建挂载源码的 WebUI...", flush=True)
        npm = shutil.which("npm") or "npm"
        install_command = "ci" if (root / "web/package-lock.json").is_file() else "install"
        subprocess.run([npm, install_command, "--include=dev"], cwd=root / "web", check=True)
        subprocess.run([npm, "run", "build"], cwd=root / "web", check=True)
    record_runtime_state("frontend", root)


if __name__ == "__main__":
    if sys.argv[1:] == ["record-image"]:
        record_runtime_state("requirements")
        record_runtime_state("frontend")
        (RUNTIME_DIR / "image-frontend.sha256").write_text(
            dependency_fingerprint("frontend"), encoding="ascii"
        )
    elif sys.argv[1:] == ["prepare"]:
        prepare_runtime()
    else:
        raise SystemExit("用法: sakura-docker-runtime.py {record-image|prepare}")
