# Copyright 2026 Sakura-频道总结助手
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant
# 许可证全文：参见 LICENSE 文件

"""投票配置管理模块

此模块管理频道级的投票配置，包括启用状态和发送位置。
"""

import logging
import json
from pathlib import Path
from typing import Literal

from .constants import CONFIG_FILE
from .exceptions import ConfigurationError

logger = logging.getLogger(__name__)


# 投票发送位置类型
PollSendLocation = Literal['channel', 'discussion']


# 投票配置类型定义
ChannelPollConfig = dict[
    str,  # 'enabled', 'send_to_channel'
    bool | None
]


class ChannelPollConfigManager:
    """频道投票配置管理器
    
    管理频道级的投票配置，包括启用状态和发送位置。
    """
    
    def __init__(self, config_file: str | None = None):
        """初始化配置管理器
        
        Args:
            config_file: 配置文件路径，默认为 config.json
        """
        self._config_file = Path(config_file or CONFIG_FILE)
        self._poll_settings: dict[str, ChannelPollConfig] = {}
        self._load_settings()
    
    def _load_settings(self) -> None:
        """从配置文件加载投票配置"""
        if not self._config_file.exists():
            logger.info("配置文件不存在，使用空配置")
            self._poll_settings = {}
            return
        
        try:
            with open(self._config_file, 'r', encoding='utf-8') as f:
                config = json.load(f)
            
            settings = config.get('channel_poll_settings', {})
            if isinstance(settings, dict):
                self._poll_settings = settings
                logger.info(f"已加载 {len(self._poll_settings)} 个频道的投票配置")
            else:
                logger.warning("配置文件中的 channel_poll_settings 格式不正确")
                self._poll_settings = {}
        except json.JSONDecodeError as e:
            raise ConfigurationError(f"配置文件格式错误: {e}")
        except Exception as e:
            raise ConfigurationError(f"加载配置文件失败: {e}")
    
    def _save_settings(self) -> None:
        """保存投票配置到文件"""
        try:
            # 读取完整配置
            full_config = {}
            if self._config_file.exists():
                with open(self._config_file, 'r', encoding='utf-8') as f:
                    full_config = json.load(f)
            
            # 更新投票配置部分
            full_config['channel_poll_settings'] = self._poll_settings
            
            # 确保目录存在
            self._config_file.parent.mkdir(parents=True, exist_ok=True)
            
            # 保存配置
            with open(self._config_file, 'w', encoding='utf-8') as f:
                json.dump(full_config, f, ensure_ascii=False, indent=2)
            
            logger.info(f"已保存 {len(self._poll_settings)} 个频道的投票配置")
        except Exception as e:
            raise ConfigurationError(f"保存配置文件失败: {e}")
    
    def get_config(self, channel: str) -> ChannelPollConfig:
        """获取指定频道的投票配置
        
        Args:
            channel: 频道 URL
        
        Returns:
            包含以下键的字典：
            - enabled (bool | None): 是否启用投票（None 表示使用全局配置）
            - send_to_channel (bool): true=频道模式, false=讨论组模式
        """
        if channel in self._poll_settings:
            config = self._poll_settings[channel]
            return {
                'enabled': config.get('enabled', None),
                'send_to_channel': config.get('send_to_channel', False)
            }
        
        # 没有独立配置，返回默认配置
        return {
            'enabled': None,  # 使用全局配置
            'send_to_channel': False  # 默认讨论组模式
        }
    
    def set_config(
        self,
        channel: str,
        enabled: bool | None = None,
        send_to_channel: bool | None = None
    ) -> None:
        """设置指定频道的投票配置
        
        Args:
            channel: 频道 URL
            enabled: 是否启用投票（None 表示不修改）
            send_to_channel: 投票发送位置（None 表示不修改）
                True - 频道模式（直接发送到频道）
                False - 讨论组模式（发送到讨论组）
        
        Raises:
            ConfigurationError: 保存配置失败时
        """
        # 获取频道当前配置
        if channel not in self._poll_settings:
            self._poll_settings[channel] = {}
        
        channel_config = self._poll_settings[channel]
        
        # 更新配置（只更新提供的参数）
        if enabled is not None:
            channel_config['enabled'] = enabled
            logger.info(f"设置频道 {channel} 的投票启用状态: {enabled}")
        
        if send_to_channel is not None:
            channel_config['send_to_channel'] = send_to_channel
            location = '频道' if send_to_channel else '讨论组'
            logger.info(f"设置频道 {channel} 的投票发送位置: {location}")
        
        # 保存配置
        self._save_settings()
        logger.info(f"已更新频道 {channel} 的投票配置")
    
    def delete_config(self, channel: str) -> bool:
        """删除指定频道的投票配置
        
        删除后，该频道将使用全局投票配置。
        
        Args:
            channel: 频道 URL
        
        Returns:
            是否成功删除配置
        """
        if channel in self._poll_settings:
            del self._poll_settings[channel]
            self._save_settings()
            logger.info(f"已删除频道 {channel} 的投票配置，将使用全局配置")
            return True
        
        logger.info(f"频道 {channel} 没有独立的投票配置，无需删除")
        return False
    
    def get_all_configs(self) -> dict[str, ChannelPollConfig]:
        """获取所有频道的投票配置
        
        Returns:
            所有频道配置的字典
        """
        return self._poll_settings.copy()
    
    def format_config_text(
        self,
        channel: str,
        global_enabled: bool = True
    ) -> str:
        """格式化频道投票配置为可读文本
        
        Args:
            channel: 频道 URL
            global_enabled: 全局投票启用状态
        
        Returns:
            格式化后的文本
        """
        config = self.get_config(channel)
        
        # 确定实际启用状态
        enabled = config['enabled']
        if enabled is None:
            enabled = global_enabled
            enabled_text = f"全局配置 ({'启用' if enabled else '禁用'})"
        else:
            enabled_text = '启用' if enabled else '禁用'
        
        # 确定发送位置
        send_to_channel = config['send_to_channel']
        location_text = '频道模式' if send_to_channel else '讨论组模式'
        
        return f"{enabled_text} | {location_text}"


# 全局配置管理器实例
_poll_config_manager: ChannelPollConfigManager | None = None


def get_poll_config_manager() -> ChannelPollConfigManager:
    """获取全局频道投票配置管理器（单例模式）
    
    Returns:
        ChannelPollConfigManager 实例
    """
    global _poll_config_manager
    if _poll_config_manager is None:
        _poll_config_manager = ChannelPollConfigManager()
        logger.info("频道投票配置管理器已初始化")
    return _poll_config_manager


# 便捷函数（保持向后兼容）
def get_channel_poll_config(channel: str) -> ChannelPollConfig:
    """获取指定频道的投票配置"""
    return get_poll_config_manager().get_config(channel)


def set_channel_poll_config(
    channel: str,
    enabled: bool | None = None,
    send_to_channel: bool | None = None
) -> None:
    """设置指定频道的投票配置"""
    get_poll_config_manager().set_config(channel, enabled, send_to_channel)


def delete_channel_poll_config(channel: str) -> bool:
    """删除指定频道的投票配置"""
    return get_poll_config_manager().delete_config(channel)