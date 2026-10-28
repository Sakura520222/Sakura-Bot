# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""版本检查和更新工具模块"""

import asyncio
import hashlib
import json
import logging
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
from contextlib import contextmanager
from pathlib import Path

from packaging import version

from core.docker_runtime import record_runtime_state

logger = logging.getLogger(__name__)

# 仅兼容旧部署遗留的生成文件，绝不清理配置、提示词或其他源码。
_GENERATED_UPDATE_FILES = ("web/package-lock.json", "web/tsconfig.tsbuildinfo")


def get_local_version():
    """读取本地版本号

    依次尝试从 core/__init__.py 和 main.py 中读取 __version__。
    """
    # 优先从 core/__init__.py 读取（版本号的实际定义位置）
    version_files = [
        Path("core/__init__.py"),
        Path("main.py"),
    ]
    for version_file in version_files:
        try:
            if not version_file.exists():
                continue

            content = version_file.read_text(encoding="utf-8")
            match = re.search(r'__version__\s*=\s*["\']([^"\']+)["\']', content)
            if match:
                return match.group(1)
        except Exception as e:
            logger.debug(f"从 {version_file} 读取版本号失败: {e}")
            continue

    logger.error("无法从任何源文件读取版本号")
    return None


async def get_remote_version():
    """从 GitHub Release API 获取最新版本"""
    # 惰性导入：版本检查很少运行，避免主进程常驻 aiohttp（约 3-5MB）
    import aiohttp

    try:
        async with aiohttp.ClientSession() as session:
            async with session.get(
                "https://api.github.com/repos/Sakura520222/Sakura-Bot/releases/latest",
                timeout=aiohttp.ClientTimeout(total=10),
            ) as response:
                if response.status == 200:
                    data = await response.json()
                    tag_name = data.get("tag_name", "")
                    # 移除 'v' 前缀
                    return tag_name.lstrip("v")
                else:
                    logger.warning(f"GitHub API 返回状态码: {response.status}")
                    return None
    except Exception as e:
        logger.error(f"获取远程版本失败: {e}")
        return None


def compare_versions(local, remote):
    """比较两个版本，返回 remote 是否更新"""
    try:
        return version.parse(remote) > version.parse(local)
    except Exception as e:
        logger.error(f"版本比较失败: {e}")
        return False


def _run_git(*args, cwd=None, check=True):
    return subprocess.run(
        ["git", *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        encoding="utf-8",
        check=check,
        timeout=30,
    )


def _generated_file_state(path):
    """保留原始字节和权限；拒绝沿符号链接清理文件。"""
    if path.is_symlink() or path.parent.is_symlink():
        raise ValueError(f"生成文件路径是符号链接，拒绝自动处理: {path}")
    try:
        info = path.stat()
    except FileNotFoundError:
        return None
    if not stat.S_ISREG(info.st_mode):
        raise ValueError(f"生成文件路径不是普通文件，拒绝自动处理: {path}")
    return path.read_bytes(), stat.S_IMODE(info.st_mode)


def _restore_generated_file(path, state):
    if state is None:
        path.unlink(missing_ok=True)
        return
    content, mode = state
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as stream:
            temporary = Path(stream.name)
            stream.write(content)
        temporary.chmod(mode)
        os.replace(temporary, path)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


@contextmanager
def _clean_generated_files(root, head):
    """先持久备份再清理；合并失败时恢复，不覆盖期间发生的并发修改。"""
    paths = _GENERATED_UPDATE_FILES
    if _run_git("diff", "--cached", "--name-only", "--", *paths, cwd=root).stdout:
        raise ValueError("生成文件存在已暂存修改，请先提交或取消暂存后再更新")
    changed = _run_git("diff", "--name-only", "-z", "--", *paths, cwd=root).stdout
    untracked = _run_git("ls-files", "--others", "-z", "--", *paths, cwd=root).stdout
    names = sorted(set(filter(None, (changed + untracked).split("\0"))))
    if any(name not in paths for name in names):
        raise ValueError("生成文件路径包含目录或异常路径，拒绝自动处理")
    if not names:
        yield None
        return

    if "web/package-lock.json" in names:
        manifest_tracked = _run_git(
            "ls-files", "--error-unmatch", "--", "web/package.json", cwd=root, check=False
        )
        manifest_diff = _run_git(
            "diff", "--quiet", "HEAD", "--", "web/package.json", cwd=root, check=False
        )
        manifest_staged = _run_git(
            "diff", "--cached", "--quiet", "--", "web/package.json", cwd=root, check=False
        )
        if manifest_tracked.returncode or manifest_diff.returncode or manifest_staged.returncode:
            raise ValueError(
                "package.json 存在本地修改或未跟踪，保留自定义依赖和锁文件，请先手动处理"
            )
        manifest_state = _generated_file_state(root / "web/package.json")

    original = {name: _generated_file_state(root / name) for name in names}
    tracked = set(_run_git("ls-files", "-z", "--", *names, cwd=root).stdout.split("\0"))
    git_path = _run_git("rev-parse", "--git-path", "sakura-update-backups", cwd=root).stdout.strip()
    backup_root = (root / git_path).resolve()
    backup_root.mkdir(mode=0o700, parents=True, exist_ok=True)
    backup = Path(tempfile.mkdtemp(prefix="generated-", dir=backup_root))
    metadata = {"head": head, "files": {}}
    for name, state in original.items():
        if state is not None:
            saved = backup / "files" / name
            saved.parent.mkdir(parents=True, exist_ok=True)
            saved.write_bytes(state[0])
            saved.chmod(state[1])
        metadata["files"][name] = {
            "sha256": hashlib.sha256(state[0]).hexdigest() if state is not None else None,
            "mode": state[1] if state is not None else None,
            "tracked": name in tracked,
        }
    (backup / "manifest.json").write_text(json.dumps(metadata, indent=2), encoding="utf-8")
    logger.info("更新前生成文件已备份: %s", backup)

    cleaned = {}
    try:
        if _run_git("rev-parse", "HEAD", cwd=root).stdout.strip() != head:
            raise RuntimeError("备份期间 HEAD 已变化，停止更新")
        if _run_git("diff", "--cached", "--name-only", "--", *paths, cwd=root).stdout:
            raise RuntimeError("备份期间生成文件已被暂存，停止更新")
        if "web/package-lock.json" in names and (
            _generated_file_state(root / "web/package.json") != manifest_state
            or _run_git(
                "diff", "--cached", "--quiet", "--", "web/package.json", cwd=root, check=False
            ).returncode
        ):
            raise RuntimeError("备份期间 package.json 已变化，停止更新")
        for name, state in original.items():
            if _generated_file_state(root / name) != state:
                raise RuntimeError(f"备份期间生成文件已变化，停止更新: {name}")
        for name, state in original.items():
            if _generated_file_state(root / name) != state:
                raise RuntimeError(f"清理前生成文件已变化，停止更新: {name}")
            if name in tracked:
                _run_git("restore", f"--source={head}", "--worktree", "--", name, cwd=root)
            else:
                (root / name).unlink()
            cleaned[name] = _generated_file_state(root / name)
        yield backup
    except Exception as error:
        for name, state in cleaned.items():
            try:
                current = _generated_file_state(root / name)
                if current == state:
                    _restore_generated_file(root / name, original[name])
                elif current != original[name]:
                    logger.error("生成文件发生并发修改，保留当前内容；原内容见备份: %s", name)
            except Exception:
                logger.exception("恢复生成文件失败，原内容见备份: %s", name)
        detail = error.stderr if isinstance(error, subprocess.CalledProcessError) else str(error)
        raise RuntimeError(f"{detail}；生成文件原内容已备份至 {backup}") from error


def _fast_forward_update():
    root = Path(_run_git("rev-parse", "--show-toplevel").stdout.strip()).resolve()
    _run_git("fetch", "origin", cwd=root)
    target = None
    for branch in ("main", "master"):
        result = _run_git(
            "rev-parse", "--verify", f"refs/remotes/origin/{branch}", cwd=root, check=False
        )
        if result.returncode == 0:
            target = result.stdout.strip()
            break
    if target is None:
        return False, "无法获取远程 main/master 分支"
    head = _run_git("rev-parse", "HEAD", cwd=root).stdout.strip()
    ancestor = _run_git("merge-base", "--is-ancestor", head, target, cwd=root, check=False)
    if ancestor.returncode != 0:
        return False, "Git 分支分叉或无法确认快进关系，请先手动处理；本地文件未清理"
    with _clean_generated_files(root, head) as backup:
        _run_git("merge", "--ff-only", target, cwd=root)
    message = f"更新成功: {target[:7]}"
    if backup is not None:
        message += f"；旧生成文件备份: {backup}"
    return True, message


async def git_pull_latest():
    """仅清理已备份的生成文件，快进更新并保留配置及其他本地修改。"""

    try:
        return await asyncio.to_thread(_fast_forward_update)
    except subprocess.TimeoutExpired:
        return False, "Git 操作超时"
    except subprocess.CalledProcessError as error:
        return False, f"Git 更新失败，请先处理本地修改: {error.stderr}"
    except Exception as e:
        logger.error(f"Git 更新失败: {e}")
        return False, str(e)


async def pull_with_changed_paths() -> tuple[bool, str, list[str]]:
    """快进更新并返回本次变更的文件相对路径列表（供镜像层变更检测）。

    Returns:
        (success, message, changed)：changed 为空表示无可检测变更
        （更新失败、仓库异常或本就是最新）。
    """
    try:
        before = await asyncio.to_thread(lambda: _run_git("rev-parse", "HEAD").stdout.strip())
    except Exception:
        before = ""

    success, message = await git_pull_latest()
    if not success or not before:
        return success, message, []

    def _changed() -> list[str]:
        result = _run_git("diff", "--name-only", before, "HEAD", check=False)
        return [line.strip() for line in result.stdout.splitlines() if line.strip()]

    try:
        return success, message, await asyncio.to_thread(_changed)
    except Exception:
        return success, message, []


async def install_dependencies():
    """在当前虚拟环境中安装依赖"""

    try:
        result = await asyncio.to_thread(
            subprocess.run,
            [sys.executable, "-m", "pip", "install", "-r", "requirements.txt"],
            capture_output=True,
            text=True,
            timeout=300,  # 5分钟超时
        )

        if result.returncode == 0:
            await asyncio.to_thread(record_runtime_state, "requirements")
            return True, "依赖安装完成"
        else:
            return False, f"依赖安装失败: {result.stderr[-500:]}"  # 最后500字符
    except subprocess.TimeoutExpired:
        return False, "依赖安装超时"
    except Exception as e:
        logger.error(f"依赖安装失败: {e}")
        return False, str(e)


async def build_frontend():
    """按锁文件安装前端依赖并构建应用。"""

    try:
        # 检查web目录
        if not await asyncio.to_thread(os.path.exists, "web"):
            logger.warning("web 目录不存在，跳过前端构建")
            return True, "前端目录不存在，已跳过"

        # 检查 package.json
        if not await asyncio.to_thread(os.path.exists, "web/package.json"):
            logger.warning("web/package.json 不存在，跳过前端构建")
            return True, "package.json 不存在，已跳过"

        # 检查 Node.js
        node_check = await asyncio.to_thread(
            subprocess.run,
            ["node", "--version"],
            capture_output=True,
            text=True,
            timeout=5,
        )

        if node_check.returncode != 0:
            logger.warning("Node.js 未安装或不在 PATH 中")
            return False, "Node.js 未安装，请先安装 Node.js"

        logger.info(f"检测到 Node.js: {node_check.stdout.strip()}")

        # 安装前端依赖
        logger.info("安装前端依赖...")
        npm = shutil.which("npm") or "npm"
        has_lockfile = await asyncio.to_thread(Path("web/package-lock.json").is_file)
        install_command = "ci" if has_lockfile else "install"
        install_args = [npm, install_command, "--include=dev"]
        if not has_lockfile:
            install_args.append("--package-lock=false")
        npm_install = await asyncio.to_thread(
            subprocess.run,
            install_args,
            cwd="web",
            capture_output=True,
            text=True,
            timeout=600,  # 10分钟超时
        )

        if npm_install.returncode != 0:
            error_msg = (
                npm_install.stderr[-500:]
                if npm_install.stderr
                else npm_install.stdout[-500:]
                if npm_install.stdout
                else "无错误输出"
            )
            logger.error(f"npm install 失败: {error_msg}")
            return False, f"前端依赖安装失败: {error_msg}"

        logger.info("前端依赖安装完成")

        # 构建前端
        logger.info("构建前端应用...")
        npm_build = await asyncio.to_thread(
            subprocess.run,
            [npm, "run", "build"],
            cwd="web",
            capture_output=True,
            text=True,
            timeout=600,  # 10分钟超时
        )

        if npm_build.returncode != 0:
            error_msg = (
                npm_build.stderr[-500:]
                if npm_build.stderr
                else npm_build.stdout[-500:]
                if npm_build.stdout
                else "无错误输出"
            )
            logger.error(f"npm run build 失败: {error_msg}")
            return False, f"前端构建失败: {error_msg}"

        logger.info("前端构建完成")
        await asyncio.to_thread(record_runtime_state, "frontend")
        return True, "前端构建完成"

    except subprocess.TimeoutExpired:
        logger.error("前端构建超时")
        return False, "前端构建超时（超过10分钟）"
    except FileNotFoundError as e:
        logger.error(f"执行文件不存在: {e}")
        return False, f"执行文件不存在: {e}"
    except Exception as e:
        logger.error(f"前端构建失败: {e}")
        return False, str(e)
