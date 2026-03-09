# core/config/manager.py
import asyncio
import json
import logging
from pathlib import Path

from core.config.event_bus import AsyncIOEventBus
from core.config.events import ConfigChangedEvent
from core.config.validator import ConfigValidator

logger = logging.getLogger(__name__)

# 配置文件路径
CONFIG_FILE = Path("data/config.json")
LAST_VALID_CONFIG = Path("data/config.json.last_valid")


class ConfigManager:
    """配置管理器（原子性更新 + 热重载）"""

    def __init__(self, loop: asyncio.AbstractEventLoop):
        self._loop = loop
        self._write_lock = asyncio.Lock()
        self._config_snapshot: dict | None = None
        self._config_version = 0
        self._event_bus: AsyncIOEventBus | None = None
        self._config_file = CONFIG_FILE

    @property
    def event_bus(self) -> AsyncIOEventBus | None:
        return self._event_bus

    @event_bus.setter
    def event_bus(self, value: AsyncIOEventBus):
        self._event_bus = value

    def initialize_sync(self, config_file: Path = None) -> tuple[bool, str]:
        """
        同步初始化配置（纯同步方法，解决启动顺序问题）

        Returns:
            (成功, 错误信息)
        """
        if config_file:
            self._config_file = config_file

        try:
            # 同步读取并解析配置文件
            if not self._config_file.exists():
                return False, f"配置文件不存在: {self._config_file}"

            with open(self._config_file, encoding="utf-8") as f:
                config_dict = json.load(f)

            self._config_snapshot = config_dict

            # 创建初始备份
            try:
                with open(LAST_VALID_CONFIG, "w", encoding="utf-8") as f:
                    json.dump(config_dict, f, ensure_ascii=False, indent=2)
                logger.info(f"✅ 初始配置已备份到: {LAST_VALID_CONFIG}")
            except Exception as e:
                logger.warning(f"创建初始备份失败: {e}")

            logger.info(f"✅ 配置已同步加载: {self._config_file}")
            return True, ""

        except json.JSONDecodeError as e:
            return False, f"JSON格式错误: {e}"
        except Exception as e:
            return False, f"初始化失败: {e}"

    async def get_config(self) -> dict:
        """获取当前配置（异步方法）"""
        return self._config_snapshot

    async def _backup_current_config(self) -> tuple[bool, str]:
        """
        备份当前有效配置到 config.json.last_valid

        Returns:
            (成功, 错误信息)
        """
        try:
            if self._config_snapshot is None:
                return False, "当前配置为空，无法备份"

            # 使用 asyncio.to_thread 避免阻塞事件循环
            def _write_backup():
                with open(LAST_VALID_CONFIG, "w", encoding="utf-8") as f:
                    json.dump(self._config_snapshot, f, ensure_ascii=False, indent=2)

            await asyncio.to_thread(_write_backup)

            logger.info(f"✅ 配置已备份到: {LAST_VALID_CONFIG}")
            return True, ""

        except Exception as e:
            error_msg = f"备份配置失败: {e}"
            logger.error(error_msg, exc_info=True)
            return False, error_msg

    async def _restore_from_backup(self) -> tuple[bool, str]:
        """
        从备份恢复配置文件

        Returns:
            (成功, 错误信息)
        """
        try:
            # 使用 asyncio.to_thread 检查文件存在
            def _check_exists():
                return LAST_VALID_CONFIG.exists()

            backup_exists = await asyncio.to_thread(_check_exists)
            if not backup_exists:
                return False, "备份文件不存在"

            # 使用 asyncio.to_thread 读取备份配置
            def _read_backup():
                with open(LAST_VALID_CONFIG, encoding="utf-8") as f:
                    return json.load(f)

            backup_config = await asyncio.to_thread(_read_backup)

            # 使用 asyncio.to_thread 写入配置文件
            def _write_config():
                with open(self._config_file, "w", encoding="utf-8") as f:
                    json.dump(backup_config, f, ensure_ascii=False, indent=2)

            await asyncio.to_thread(_write_config)

            logger.info(f"✅ 配置已从备份恢复: {LAST_VALID_CONFIG}")
            return True, ""

        except Exception as e:
            error_msg = f"从备份恢复配置失败: {e}"
            logger.error(error_msg, exc_info=True)
            return False, error_msg

    async def reload_config(self) -> tuple[bool, str]:
        """原子性配置重载（带自动回滚）"""
        try:
            # 1. 备份当前有效配置（在验证新配置之前）
            backup_success, backup_error = await self._backup_current_config()
            if not backup_success:
                logger.warning(f"备份配置失败，继续重载: {backup_error}")

            # 2. 验证新配置
            success, error_event, config_dict = ConfigValidator.validate_config_file(
                self._config_file
            )

            if not success:
                # 3. 验证失败：自动回滚配置文件
                restore_success, restore_error = await self._restore_from_backup()

                if restore_success:
                    # 回滚成功，发布增强的错误事件
                    rollback_event = error_event.model_copy(
                        update={
                            "error": f"{error_event.error}\n\n🔄 配置已自动回滚到最后有效版本",
                            "rolled_back": True,
                        }
                    )
                else:
                    # 回滚失败，发布严重错误事件
                    rollback_event = error_event.model_copy(
                        update={
                            "error": f"{error_event.error}\n\n⚠️ 配置回滚失败: {restore_error}",
                            "rolled_back": False,
                        }
                    )

                # 发布错误事件
                if self._event_bus:
                    await self._event_bus.publish(rollback_event)

                logger.error(f"配置验证失败: {error_event.error}")
                return False, rollback_event.error

            # 4. 验证成功：原子更新配置快照
            old_config = self._config_snapshot
            async with self._write_lock:
                self._config_snapshot = config_dict
                self._config_version += 1

            # 5. 发布成功事件
            if self._event_bus:
                await self._event_bus.publish(
                    ConfigChangedEvent(
                        config=config_dict, version=self._config_version, old_config=old_config
                    )
                )

            logger.info(f"✅ 配置已更新到版本 {self._config_version}")
            return True, ""

        except Exception as e:
            error_msg = f"配置重载异常: {str(e)}"
            logger.error(error_msg, exc_info=True)
            return False, error_msg
