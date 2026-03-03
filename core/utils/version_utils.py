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

import logging
import re
import subprocess
import sys
from pathlib import Path

import aiohttp
from packaging import version

logger = logging.getLogger(__name__)


def get_local_version():
    """从 main.py 读取 __version__"""
    try:
        main_py = Path("main.py")
        if not main_py.exists():
            logger.warning("main.py 不存在")
            return None

        content = main_py.read_text(encoding="utf-8")
        match = re.search(r'__version__\s*=\s*["\']([^"\']+)["\']', content)
        if match:
            return match.group(1)
        return None
    except Exception as e:
        logger.error(f"读取本地版本失败: {e}")
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
    """执行 git pull 更新代码"""
    import asyncio

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
            ["git", "reset", "--hard", branch],
            capture_output=True,
            text=True,
            timeout=30,
        )

        if result.returncode != 0:
            return False, f"Git reset 失败: {result.stderr}"

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
    import asyncio

    try:
        result = await asyncio.to_thread(
            subprocess.run,
            [sys.executable, "-m", "pip", "install", "-r", "requirements.txt"],
            capture_output=True,
            text=True,
            timeout=300,  # 5分钟超时
        )

        if result.returncode == 0:
            return True, "依赖安装完成"
        else:
            return False, f"依赖安装失败: {result.stderr[-500:]}"  # 最后500字符
    except subprocess.TimeoutExpired:
        return False, "依赖安装超时"
    except Exception as e:
        logger.error(f"依赖安装失败: {e}")
        return False, str(e)
