# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""依赖镜像更新请求

/update 检测到镜像层变更（Go 源码/Dockerfile/依赖/前端/构建工作流）时写入
data/.image_update_request 标记；宿主机的 sakura-image-deploy systemd 单元
（deploy/ 目录提供，install-deployer.sh 一键安装）监听该标记并执行
scripts/deploy.sh：拉取 GHCR 新镜像 + 重建容器。用户全程无需登录服务器。

未安装 deployer 时优雅降级：标记照写（下次手动 deploy.sh 会消费），
/update 走既有的容器内 pip/npm 自愈路径，功能不受影响。
"""

import logging
from pathlib import Path

logger = logging.getLogger(__name__)

# 这些路径的变更意味着依赖镜像需要重建（GitHub Actions 构建、GHCR 分发）
IMAGE_LAYER_PREFIXES = (
    "qa-bot-go/",
    "web/",
    ".github/workflows/docker-build.yml",
    "Dockerfile",
    "docker-compose.yml",
    "requirements.txt",
)

REQUEST_FLAG = Path("data") / ".image_update_request"
# deployer 安装时写入；容器内据此判断是否跳过容器内 pip/npm 重建
DEPLOYER_MARKER = Path("data") / ".image_deployer_installed"


def needs_image_update(changed_paths: list[str]) -> bool:
    """判断本次变更是否触及依赖镜像层"""
    return any(
        path == prefix or path.startswith(prefix)
        for path in changed_paths
        for prefix in IMAGE_LAYER_PREFIXES
    )


def deployer_installed() -> bool:
    """宿主机是否已安装自动部署单元"""
    return DEPLOYER_MARKER.is_file()


def request_image_update() -> bool:
    """写入镜像更新请求标记（宿主机 deployer 将自动执行 deploy.sh）

    Returns:
        bool: 标记是否写入成功
    """
    try:
        REQUEST_FLAG.parent.mkdir(parents=True, exist_ok=True)
        REQUEST_FLAG.write_text("requested\n", encoding="utf-8")
        logger.info(f"已写入镜像更新请求: {REQUEST_FLAG}")
        return True
    except Exception as e:
        logger.error(f"写入镜像更新请求失败: {type(e).__name__}: {e}")
        return False
