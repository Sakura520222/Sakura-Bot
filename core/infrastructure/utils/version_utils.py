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
import logging
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

import aiohttp
from packaging import version

from core.docker_runtime import record_runtime_state

logger = logging.getLogger(__name__)


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


async def git_pull_latest():
    """快进更新代码，保留挂载仓库中的配置和本地修改。"""

    try:
        # 检测远程分支
        detect_branch = await asyncio.to_thread(
            subprocess.run,
            ["git", "branch", "-r"],
            capture_output=True,
            text=True,
            timeout=10,
        )

        if detect_branch.returncode != 0:
            logger.error("无法获取远程分支列表")
            return False, "无法获取远程分支"

        # 确定远程主分支名称
        branch = "origin/main" if "origin/main" in detect_branch.stdout else "origin/master"

        # 执行更新
        result = await asyncio.to_thread(
            subprocess.run,
            ["git", "fetch", "origin"],
            capture_output=True,
            text=True,
            timeout=30,
        )

        if result.returncode != 0:
            return False, f"Git fetch 失败: {result.stderr}"

        result = await asyncio.to_thread(
            subprocess.run,
            ["git", "merge", "--ff-only", branch],
            capture_output=True,
            text=True,
            timeout=30,
        )

        if result.returncode != 0:
            return False, f"Git 快进更新失败，请先处理本地修改或分支分叉: {result.stderr}"

        # 获取最新 commit 信息
        commit_result = await asyncio.to_thread(
            subprocess.run,
            ["git", "rev-parse", "--short", "HEAD"],
            capture_output=True,
            text=True,
            timeout=10,
        )

        new_commit = commit_result.stdout.strip() if commit_result.returncode == 0 else "unknown"

        return True, f"更新成功: {new_commit}"

    except subprocess.TimeoutExpired:
        return False, "Git 操作超时"
    except Exception as e:
        logger.error(f"Git 更新失败: {e}")
        return False, str(e)


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
        npm_install = await asyncio.to_thread(
            subprocess.run,
            [npm, install_command, "--include=dev"],
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
