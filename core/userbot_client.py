# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
UserBot 客户端管理模块

提供长期运行的 UserBot 客户端实例，用于：
- 抓取频道历史消息（总结功能）
- 实时监听频道新消息（转发功能）
"""

import os

from telethon import TelegramClient
from telethon.errors import SessionPasswordNeededError

from .config import logger
from .settings import get_settings


class UserBotClient:
    """
    UserBot 客户端管理器

    管理长期运行的 UserBot 客户端实例，提供：
    - 客户端初始化和启动
    - 首次登录（交互式验证码输入）
    - 自动重连机制
    - 状态检查
    """

    def __init__(
        self,
        api_id: int,
        api_hash: str,
        session_path: str = "data/sessions/user_session",
        phone_number: str | None = None,
    ):
        """
        初始化 UserBot 客户端管理器

        Args:
            api_id: Telegram API ID
            api_hash: Telegram API Hash
            session_path: Session 文件路径
            phone_number: 用户手机号（国际格式，如 +8613800138000）
        """
        self.api_id = api_id
        self.api_hash = api_hash
        self.session_path = session_path
        self.phone_number = phone_number

        self.client: TelegramClient | None = None
        self._is_connected = False
        self._is_initialized = False

        logger.info(
            f"UserBot 客户端管理器已初始化: session_path={session_path}, "
            f"phone_number={phone_number}"
        )

    def is_available(self) -> bool:
        """
        检查 UserBot 客户端是否可用

        Returns:
            是否可用（已连接且已初始化）
        """
        return self._is_connected and self._is_initialized and self.client is not None

    async def start(self) -> bool:
        """
        启动 UserBot 客户端

        首次运行时需要交互式输入验证码，后续自动加载 session。

        Returns:
            是否启动成功
        """
        try:
            # 检查 session 文件是否存在
            session_exists = os.path.exists(self.session_path + ".session")  # noqa: ASYNC240

            logger.info(f"开始启动 UserBot 客户端，session 存在: {session_exists}")

            # 创建客户端实例
            self.client = TelegramClient(
                self.session_path,
                self.api_id,
                self.api_hash,
            )

            # 连接到 Telegram 服务器
            await self.client.connect()

            if not await self.client.is_user_authorized():
                # 用户未授权，需要登录
                if not self.phone_number:
                    logger.error("未配置手机号，无法进行 UserBot 登录")
                    logger.error("请在 .env 文件中设置 USERBOT_PHONE_NUMBER")
                    return False

                logger.info(f"UserBot 未授权，开始登录流程: {self.phone_number}")

                # 发送验证码
                await self.client.send_code_request(self.phone_number)

                # 交互式输入验证码
                print("\n" + "=" * 60)
                print("UserBot 首次登录")
                print("=" * 60)
                print(f"已向手机号 {self.phone_number} 发送验证码")
                print("请在下方输入验证码（输入 'cancel' 取消登录）：")
                print("-" * 60)

                try:
                    code = input("验证码: ").strip()  # noqa: ASYNC250

                    if code.lower() == "cancel":
                        logger.info("用户取消 UserBot 登录")
                        await self.client.disconnect()
                        return False

                    # 尝试使用验证码登录
                    try:
                        await self.client.sign_in(self.phone_number, code)
                    except SessionPasswordNeededError:
                        # 需要两步验证密码
                        print("检测到两步验证，请输入密码：")
                        password = input("密码: ").strip()  # noqa: ASYNC250

                        if not password:
                            logger.error("未输入密码，登录失败")
                            await self.client.disconnect()
                            return False

                        await self.client.sign_in(password=password)

                    logger.info("UserBot 登录成功")

                except Exception as e:
                    logger.error(f"UserBot 登录失败: {type(e).__name__}: {e}")
                    await self.client.disconnect()
                    return False

            # 验证连接状态
            if not self.client.is_connected():
                logger.error("UserBot 客户端未连接")
                return False

            self._is_connected = True
            self._is_initialized = True

            # 获取当前用户信息
            me = await self.client.get_me()
            logger.info(
                f"UserBot 客户端启动成功: 用户={me.first_name} (@{me.username}), ID={me.id}"
            )

            return True

        except Exception as e:
            logger.error(f"启动 UserBot 客户端失败: {type(e).__name__}: {e}", exc_info=True)
            self._is_connected = False
            self._is_initialized = False

            # 清理客户端
            if self.client:
                try:
                    await self.client.disconnect()
                except Exception:
                    pass
                self.client = None

            return False

    async def stop(self):
        """停止 UserBot 客户端"""
        try:
            if self.client and self.client.is_connected():
                await self.client.disconnect()
                logger.info("UserBot 客户端已停止")
        except Exception as e:
            logger.error(f"停止 UserBot 客户端失败: {type(e).__name__}: {e}")
        finally:
            self._is_connected = False
            self._is_initialized = False
            self.client = None

    def get_client(self) -> TelegramClient | None:
        """
        获取 UserBot 客户端实例

        Returns:
            TelegramClient 实例，如果未初始化则返回 None
        """
        return self.client

    async def check_connection(self) -> bool:
        """
        检查并恢复连接

        Returns:
            连接是否正常
        """
        try:
            if not self.client:
                return False

            if not self.client.is_connected():
                logger.warning("UserBot 连接已断开，尝试重新连接...")
                await self.client.connect()
                self._is_connected = True
                logger.info("UserBot 重新连接成功")

            return True

        except Exception as e:
            logger.error(f"检查 UserBot 连接失败: {type(e).__name__}: {e}")
            self._is_connected = False
            return False

    async def get_status(self) -> dict:
        """
        获取 UserBot 状态信息

        Returns:
            状态信息字典
        """
        try:
            if not self.client:
                return {
                    "available": False,
                    "connected": False,
                    "error": "客户端未初始化",
                }

            # 检查连接状态
            is_connected = self.client.is_connected()

            # 获取用户信息（如果已连接）
            user_info = None
            if is_connected and await self.client.is_user_authorized():
                try:
                    me = await self.client.get_me()
                    user_info = {
                        "id": me.id,
                        "first_name": me.first_name,
                        "last_name": me.last_name,
                        "username": me.username,
                        "phone": me.phone,
                    }
                except Exception as e:
                    logger.warning(f"获取用户信息失败: {e}")

            return {
                "available": self.is_available(),
                "connected": is_connected,
                "initialized": self._is_initialized,
                "session_path": self.session_path,
                "phone_number": self.phone_number,
                "user_info": user_info,
            }

        except Exception as e:
            logger.error(f"获取 UserBot 状态失败: {type(e).__name__}: {e}")
            return {
                "available": False,
                "connected": False,
                "error": str(e),
            }

    async def join_channel(self, channel_link: str) -> dict:
        """
        加入频道

        Args:
            channel_link: 频道链接（支持多种格式）
                - https://t.me/channelname
                - @channelname
                - https://t.me/+invitecode (私有频道邀请链接)

        Returns:
            操作结果字典，包含 success, message, channel_info
        """
        try:
            if not self.is_available():
                return {
                    "success": False,
                    "message": "UserBot 不可用",
                    "error": "client_not_available",
                }

            from telethon.errors import (
                ChannelInvalidError,
                ChannelPrivateError,
                UserAlreadyParticipantError,
            )
            from telethon.tl.functions.channels import JoinChannelRequest
            from telethon.tl.types import Channel

            # 解析频道链接
            channel_entity = None
            try:
                # 尝试获取频道实体
                channel_entity = await self.client.get_entity(channel_link)
            except ValueError:
                # 尝试作为邀请链接处理
                if "t.me/+" in channel_link or "joinchat" in channel_link:
                    try:
                        from telethon.tl.functions.channels import ImportChatInviteRequest

                        # 提取邀请码
                        invite_hash = channel_link.split("/")[-1]
                        if "joinchat" in invite_hash:
                            invite_hash = invite_hash.split("=")[-1]

                        await self.client(ImportChatInviteRequest(invite_hash))
                        logger.info(f"UserBot 通过邀请链接加入频道成功: {channel_link}")
                        return {
                            "success": True,
                            "message": "通过邀请链接加入频道成功",
                            "channel": channel_link,
                        }
                    except Exception as invite_error:
                        logger.error(f"通过邀请链接加入频道失败: {invite_error}")
                        return {
                            "success": False,
                            "message": "无法加入频道（可能是无效的邀请链接或需要权限）",
                            "error": "invite_link_failed",
                            "channel": channel_link,
                        }
                else:
                    return {
                        "success": False,
                        "message": "无效的频道链接格式",
                        "error": "invalid_channel_format",
                        "channel": channel_link,
                    }
            except ChannelPrivateError:
                return {
                    "success": False,
                    "message": "无法加入频道（私有频道，需要手动邀请）",
                    "error": "channel_private",
                    "channel": channel_link,
                }
            except ChannelInvalidError:
                return {
                    "success": False,
                    "message": "无法加入频道（频道不存在或无效）",
                    "error": "channel_invalid",
                    "channel": channel_link,
                }
            except Exception as e:
                logger.error(f"获取频道实体失败: {type(e).__name__}: {e}")
                return {
                    "success": False,
                    "message": f"无法加入频道: {str(e)}",
                    "error": "get_entity_failed",
                    "channel": channel_link,
                }

            # 尝试加入频道
            try:
                await self.client(JoinChannelRequest(channel_entity))
                logger.info(f"UserBot 加入频道成功: {channel_link}")

                # 获取频道信息
                channel_info = None
                try:
                    if isinstance(channel_entity, Channel):
                        channel_info = {
                            "id": channel_entity.id,
                            "title": channel_entity.title,
                            "username": channel_entity.username,
                        }
                except Exception:
                    pass

                return {
                    "success": True,
                    "message": "加入频道成功",
                    "channel": channel_link,
                    "channel_info": channel_info,
                }

            except UserAlreadyParticipantError:
                logger.info(f"UserBot 已经是频道成员: {channel_link}")
                return {
                    "success": True,
                    "message": "已经是频道成员",
                    "channel": channel_link,
                    "already_joined": True,
                }

        except Exception as e:
            logger.error(f"加入频道失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "success": False,
                "message": f"加入频道失败: {str(e)}",
                "error": "join_failed",
                "channel": channel_link,
            }

    async def leave_channel(self, channel_link: str) -> dict:
        """
        离开频道

        Args:
            channel_link: 频道链接或用户名

        Returns:
            操作结果字典，包含 success, message
        """
        try:
            if not self.is_available():
                return {
                    "success": False,
                    "message": "UserBot 不可用",
                    "error": "client_not_available",
                }

            from telethon.errors import (
                ChannelInvalidError,
                ChannelPrivateError,
                UserNotParticipantError,
            )
            from telethon.tl.functions.channels import LeaveChannelRequest

            # 获取频道实体
            try:
                channel_entity = await self.client.get_entity(channel_link)
            except ChannelPrivateError:
                return {
                    "success": False,
                    "message": "无法离开频道（私有频道或无权限）",
                    "error": "channel_private",
                    "channel": channel_link,
                }
            except ChannelInvalidError:
                return {
                    "success": False,
                    "message": "无法离开频道（频道不存在或无效）",
                    "error": "channel_invalid",
                    "channel": channel_link,
                }
            except Exception as e:
                logger.error(f"获取频道实体失败: {type(e).__name__}: {e}")
                return {
                    "success": False,
                    "message": f"无法离开频道: {str(e)}",
                    "error": "get_entity_failed",
                    "channel": channel_link,
                }

            # 尝试离开频道
            try:
                await self.client(LeaveChannelRequest(channel_entity))
                logger.info(f"UserBot 离开频道成功: {channel_link}")
                return {
                    "success": True,
                    "message": "离开频道成功",
                    "channel": channel_link,
                }

            except UserNotParticipantError:
                logger.info(f"UserBot 未加入频道: {channel_link}")
                return {
                    "success": True,
                    "message": "未加入该频道",
                    "channel": channel_link,
                    "not_joined": True,
                }

        except Exception as e:
            logger.error(f"离开频道失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "success": False,
                "message": f"离开频道失败: {str(e)}",
                "error": "leave_failed",
                "channel": channel_link,
            }

    async def list_joined_channels(self) -> dict:
        """
        列出 UserBot 已加入的所有频道

        Returns:
            操作结果字典，包含 success, channels (列表)
        """
        try:
            if not self.is_available():
                return {
                    "success": False,
                    "message": "UserBot 不可用",
                    "error": "client_not_available",
                }

            from telethon.tl.types import Channel

            # 获取所有对话
            channels = []
            async for dialog in self.client.iter_dialogs():
                if isinstance(dialog.entity, Channel):
                    channels.append(
                        {
                            "id": dialog.entity.id,
                            "title": dialog.entity.title,
                            "username": dialog.entity.username,
                        }
                    )

            logger.info(f"获取到 UserBot 已加入的频道: {len(channels)} 个")
            return {
                "success": True,
                "channels": channels,
                "count": len(channels),
            }

        except Exception as e:
            logger.error(f"列出频道失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "success": False,
                "message": f"列出频道失败: {str(e)}",
                "error": "list_failed",
            }

    async def join_all_forwarding_channels(self, forwarding_config: dict) -> dict:
        """
        自动加入所有转发配置的源频道

        Args:
            forwarding_config: 转发配置字典（来自 config.json）

        Returns:
            操作结果字典，包含 success_count, failed_count, failed_list
        """
        try:
            if not self.is_available():
                return {
                    "success": False,
                    "message": "UserBot 不可用",
                    "error": "client_not_available",
                }

            # 检查转发配置
            if not forwarding_config or not forwarding_config.get("enabled"):
                return {
                    "success": False,
                    "message": "转发功能未启用",
                    "error": "forwarding_disabled",
                }

            rules = forwarding_config.get("rules", [])
            if not rules:
                return {
                    "success": False,
                    "message": "没有配置转发规则",
                    "error": "no_forwarding_rules",
                }

            # 提取所有源频道（去重）
            source_channels = set()
            for rule in rules:
                source = rule.get("source_channel")
                if source:
                    source_channels.add(source)

            if not source_channels:
                return {
                    "success": False,
                    "message": "转发规则中没有源频道",
                    "error": "no_source_channels",
                }

            logger.info(f"开始自动加入 {len(source_channels)} 个源频道...")

            # 逐个加入频道
            success_count = 0
            failed_list = []

            for channel in source_channels:
                result = await self.join_channel(channel)
                if result.get("success"):
                    success_count += 1
                    logger.info(f"✅ 成功加入源频道: {channel}")
                else:
                    failed_list.append(
                        {
                            "channel": channel,
                            "reason": result.get("message", "未知错误"),
                        }
                    )
                    logger.warning(f"❌ 加入源频道失败: {channel} - {result.get('message')}")

            logger.info(
                f"自动加入源频道完成: 成功 {success_count}/{len(source_channels)}, "
                f"失败 {len(failed_list)}"
            )

            return {
                "success": True,
                "total": len(source_channels),
                "success_count": success_count,
                "failed_count": len(failed_list),
                "failed_list": failed_list,
            }

        except Exception as e:
            logger.error(f"自动加入源频道失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "success": False,
                "message": f"自动加入源频道失败: {str(e)}",
                "error": "join_all_failed",
            }


# 全局 UserBot 客户端实例
_userbot_client: UserBotClient | None = None


def get_userbot_client() -> UserBotClient | None:
    """
    获取全局 UserBot 客户端实例

    Returns:
        UserBotClient 实例，如果未初始化则返回 None
    """
    return _userbot_client


async def init_userbot_client() -> UserBotClient | None:
    """
    初始化全局 UserBot 客户端实例

    从配置中读取参数并创建客户端实例。

    Returns:
        UserBotClient 实例，如果配置缺失或创建失败则返回 None
    """
    global _userbot_client

    try:
        settings = get_settings()

        # 检查是否启用 UserBot（正确访问 userbot 子配置）
        userbot_enabled = settings.userbot.userbot_enabled
        if not userbot_enabled:
            logger.info("UserBot 功能未启用（USERBOT_ENABLED=false）")
            return None

        # 获取配置参数
        api_id = settings.telegram.api_id
        api_hash = settings.telegram.api_hash

        if not api_id or not api_hash:
            logger.error("UserBot 配置缺失：API_ID 或 API_HASH 未设置")
            return None

        phone_number = settings.userbot.userbot_phone_number
        session_path = settings.userbot.userbot_session_path

        # 确保 sessions 目录存在
        session_dir = os.path.dirname(session_path)
        if session_dir:
            os.makedirs(session_dir, exist_ok=True)

        # 创建 UserBot 客户端实例
        _userbot_client = UserBotClient(
            api_id=api_id,
            api_hash=api_hash,
            session_path=session_path,
            phone_number=phone_number,
        )

        logger.info("全局 UserBot 客户端实例已创建")
        return _userbot_client

    except Exception as e:
        logger.error(f"初始化 UserBot 客户端失败: {type(e).__name__}: {e}", exc_info=True)
        _userbot_client = None
        return None


def set_userbot_client(client: UserBotClient | None):
    """
    设置全局 UserBot 客户端实例

    Args:
        client: UserBotClient 实例
    """
    global _userbot_client
    _userbot_client = client
    logger.info("全局 UserBot 客户端实例已更新")
