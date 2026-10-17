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
频道消息转发命令处理器

提供转发功能的命令接口：
- /forwarding 查看转发状态
- /forwarding_enable 启用转发
- /forwarding_disable 禁用转发
- /forwarding_stats 查看转发统计
"""

import logging
import re
from typing import TYPE_CHECKING

from core.i18n.i18n import get_text as t
from core.infrastructure.utils.auth import check_admin_permission

if TYPE_CHECKING:
    from telethon import TelegramClient
    from telethon.tl.types import Message

    from core.forwarding.forwarding_handler import ForwardingHandler

logger = logging.getLogger(__name__)


async def cmd_forwarding_status(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    查看转发状态

    用法: /forwarding
    """
    try:
        if not await check_admin_permission(message, "/forwarding"):
            return

        status_text = (
            t("forwarding.status_enabled") if handler.enabled else t("forwarding.status_disabled")
        )
        rule_count = len(handler.config.get("rules", []))

        response = t(
            "forwarding.status_message",
            status=status_text,
            rule_count=rule_count,
        )

        # 如果有规则，列出规则
        if rule_count > 0:
            response += "\n\n" + t("forwarding.rules_header")
            for i, rule in enumerate(handler.config.get("rules", []), 1):
                source = rule.get("source_channel", "Unknown")
                target = rule.get("target_channel", "Unknown")
                response += f"\n{i}. {source} → {target}"

        await message.reply(response)
        logger.info(f"用户 {message.sender_id} 查询转发状态")

    except Exception as e:
        logger.error(f"查询转发状态失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.query_failed", error=str(e)))


async def cmd_forwarding_enable(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    启用转发功能（持久化到配置文件）

    用法: /forwarding_enable
    """
    try:
        if not await check_admin_permission(message, "/forwarding_enable"):
            return

        if handler.enabled:
            await message.reply(t("forwarding.already_enabled"))
            return

        # 更新内存状态
        handler.enabled = True

        # 持久化到配置文件
        from core.config import load_config, save_config_changes

        config = load_config()
        if "forwarding" not in config:
            config["forwarding"] = {}
        config["forwarding"]["enabled"] = True
        save_config_changes(config)

        await message.reply(t("forwarding.enabled"))
        logger.info(f"用户 {message.sender_id} 启用了转发功能（已保存到配置文件）")

    except Exception as e:
        logger.error(f"启用转发功能失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.enable_failed", error=str(e)))


async def cmd_forwarding_disable(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    禁用转发功能（持久化到配置文件）

    用法: /forwarding_disable
    """
    try:
        if not await check_admin_permission(message, "/forwarding_disable"):
            return

        if not handler.enabled:
            await message.reply(t("forwarding.already_disabled"))
            return

        # 更新内存状态
        handler.enabled = False

        # 持久化到配置文件
        from core.config import load_config, save_config_changes

        config = load_config()
        if "forwarding" not in config:
            config["forwarding"] = {}
        config["forwarding"]["enabled"] = False
        save_config_changes(config)

        await message.reply(t("forwarding.disabled"))
        logger.info(f"用户 {message.sender_id} 禁用了转发功能（已保存到配置文件）")

    except Exception as e:
        logger.error(f"禁用转发功能失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.disable_failed", error=str(e)))


async def cmd_forwarding_stats(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    查看转发统计

    用法: /forwarding_stats [频道URL]
    """
    try:
        if not await check_admin_permission(message, "/forwarding_stats"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []
        channel_id = args[0] if args else None

        stats = await handler.get_statistics(channel_id)

        if not stats:
            await message.reply(t("forwarding.stats.no_data"))
            return

        # 格式化统计信息
        if channel_id:
            # 单个频道统计
            response = t(
                "forwarding.stats.single_channel",
                channel=channel_id,
                total=stats.get("total_forwarded", 0),
                last=stats.get("last_forwarded") or t("forwarding.stats.never"),
            )
        else:
            # 所有频道统计
            total_all = stats.get("total_all_channels", 0)
            response = t("forwarding.stats.total", total=total_all)

            # 列出各频道统计
            by_channel = stats.get("by_channel", [])
            if by_channel:
                response += "\n\n" + t("forwarding.stats.by_channel_header")
                for item in by_channel[:10]:  # 最多显示10个
                    channel = item.get("channel_id", "Unknown")
                    count = item.get("total_forwarded", 0)
                    response += f"\n• {channel}: {count}"

        await message.reply(response)
        logger.info(f"用户 {message.sender_id} 查询转发统计")

    except Exception as e:
        logger.error(f"查询转发统计失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.stats_failed", error=str(e)))


async def cmd_forwarding_footer(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    设置或清除自定义底栏

    用法:
    /forwarding_footer <源频道> <目标频道> <底栏内容>
    /forwarding_footer <源频道> <目标频道> clear

    支持的占位符:
    - {source_link}: 源消息链接
    - {source_title}: 源频道名称
    - {target_title}: 目标频道名称
    - {source_channel}: 源频道ID
    - {target_channel}: 目标频道ID
    - {message_id}: 消息ID

    示例:
    /forwarding_footer https://t.me/source https://t.me/target 📢 来源: {source_title}\\n🔗 {source_link}
    /forwarding_footer https://t.me/source https://t.me/target clear
    """
    try:
        if not await check_admin_permission(message, "/forwarding_footer"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            await message.reply(t("forwarding.footer.usage"))
            return

        source_channel = args[0]
        target_channel = args[1]

        # 查找匹配的转发规则
        config = handler.config
        rules = config.get("rules", [])
        matched_rule = None

        for rule in rules:
            if (
                rule.get("source_channel", "").rstrip("/").split("/")[-1]
                == source_channel.rstrip("/").split("/")[-1]
                and rule.get("target_channel", "").rstrip("/").split("/")[-1]
                == target_channel.rstrip("/").split("/")[-1]
            ):
                matched_rule = rule
                break

        if not matched_rule:
            await message.reply(
                t("forwarding.footer.not_found", source=source_channel, target=target_channel)
            )
            return

        # 检查是否要清除底栏
        if len(args) >= 3 and args[2].lower() == "clear":
            matched_rule["custom_footer"] = ""
            _persist_forwarding_config(handler.config, "custom_footer", matched_rule)
            await message.reply(
                t(
                    "forwarding.footer.cleared",
                    source=source_channel,
                    target=target_channel,
                )
            )
            logger.info(f"用户 {message.sender_id} 清除了转发规则底栏")
            return

        # 设置自定义底栏
        if len(args) < 3:
            await message.reply(t("forwarding.footer.invalid_params"))
            return

        # 获取底栏内容（可能包含空格，所以需要特殊处理）
        footer_text = " ".join(args[2:])

        # 存储到配置中
        matched_rule["custom_footer"] = footer_text
        _persist_forwarding_config(handler.config, "custom_footer", matched_rule)

        await message.reply(
            t(
                "forwarding.footer.updated",
                source=source_channel,
                target=target_channel,
                footer=footer_text,
            )
        )
        logger.info(
            f"用户 {message.sender_id} 更新了转发规则底栏: {source_channel} -> {target_channel}"
        )

    except Exception as e:
        logger.error(f"设置底栏失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.query_failed", error=str(e)))


async def cmd_forwarding_default_footer(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    启用或禁用默认底栏

    用法:
    /forwarding_default_footer on - 启用默认底栏
    /forwarding_default_footer off - 禁用默认底栏

    说明:
    - 启用：未设置自定义底栏的转发规则将使用默认格式
    - 禁用：所有转发规则都不会添加底栏（包括自定义底栏）
    """
    try:
        if not await check_admin_permission(message, "/forwarding_default_footer"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if not args:
            await message.reply(t("forwarding.default_footer.usage"))
            return

        action = args[0].lower()

        # 更新全局配置
        config = handler.config

        if action in ["on", "true", "1", "yes"]:
            config["show_default_footer"] = True
            _persist_forwarding_config(handler.config, "show_default_footer")
            await message.reply(t("forwarding.footer.default_enabled"))
            logger.info(f"用户 {message.sender_id} 启用了默认底栏")
        elif action in ["off", "false", "0", "no"]:
            config["show_default_footer"] = False
            _persist_forwarding_config(handler.config, "show_default_footer")
            await message.reply(t("forwarding.footer.default_disabled"))
            logger.info(f"用户 {message.sender_id} 禁用了默认底栏")
        else:
            await message.reply(t("forwarding.default_footer.usage"))

    except Exception as e:
        logger.error(f"设置默认底栏失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.query_failed", error=str(e)))


async def cmd_forwarding_add_rule(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    添加转发规则（持久化到配置文件）

    用法: /forwarding_add_rule <源频道> <目标频道>

    示例: /forwarding_add_rule https://t.me/source https://t.me/target
    """
    try:
        if not await check_admin_permission(message, "/forwarding_add_rule"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            await message.reply(
                "❌ 参数错误\n\n"
                "用法: /forwarding_add_rule <源频道> <目标频道>\n"
                "示例: /forwarding_add_rule https://t.me/source https://t.me/target"
            )
            return

        source_channel = args[0]
        target_channel = args[1]

        # 加载当前配置
        from core.config import load_config, save_config_changes

        config = load_config()

        # 确保 forwarding 配置存在
        if "forwarding" not in config:
            config["forwarding"] = {"enabled": False, "rules": []}

        if "rules" not in config["forwarding"]:
            config["forwarding"]["rules"] = []

        # 检查规则是否已存在
        for rule in config["forwarding"]["rules"]:
            if (
                rule.get("source_channel", "").rstrip("/").split("/")[-1]
                == source_channel.rstrip("/").split("/")[-1]
                and rule.get("target_channel", "").rstrip("/").split("/")[-1]
                == target_channel.rstrip("/").split("/")[-1]
            ):
                await message.reply(f"⚠️ 转发规则已存在:\n{source_channel} → {target_channel}")
                return

        # 添加新规则
        new_rule = {
            "source_channel": source_channel,
            "target_channel": target_channel,
        }
        config["forwarding"]["rules"].append(new_rule)

        # 保存配置
        save_config_changes(config)

        # 更新 handler 的配置
        handler.set_config(config["forwarding"])

        await message.reply(
            f"✅ 转发规则已添加:\n{source_channel} → {target_channel}\n\n"
            f"当前共有 {len(config['forwarding']['rules'])} 条规则\n"
            f"使用 /forwarding_enable 启用转发功能"
        )
        logger.info(f"用户 {message.sender_id} 添加了转发规则: {source_channel} → {target_channel}")

    except Exception as e:
        logger.error(f"添加转发规则失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(f"❌ 添加转发规则失败: {str(e)}")


async def cmd_forwarding_add_and_enable(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    一键添加转发规则并启用（持久化到配置文件）

    用法: /forwarding <源频道> <目标频道>

    示例: /forwarding https://t.me/source https://t.me/target
    """
    try:
        if not await check_admin_permission(message, "/forwarding"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            # 没有参数，显示状态
            return await cmd_forwarding_status(client, message, handler)

        source_channel = args[0]
        target_channel = args[1]

        # 加载当前配置
        from core.config import load_config, save_config_changes

        config = load_config()

        # 确保 forwarding 配置存在
        if "forwarding" not in config:
            config["forwarding"] = {"enabled": True, "rules": []}

        if "rules" not in config["forwarding"]:
            config["forwarding"]["rules"] = []

        # 检查规则是否已存在
        rule_exists = False
        for rule in config["forwarding"]["rules"]:
            if (
                rule.get("source_channel", "").rstrip("/").split("/")[-1]
                == source_channel.rstrip("/").split("/")[-1]
                and rule.get("target_channel", "").rstrip("/").split("/")[-1]
                == target_channel.rstrip("/").split("/")[-1]
            ):
                rule_exists = True
                break

        if not rule_exists:
            # 添加新规则
            new_rule = {
                "source_channel": source_channel,
                "target_channel": target_channel,
            }
            config["forwarding"]["rules"].append(new_rule)

        # 启用转发
        config["forwarding"]["enabled"] = True

        # 保存配置
        save_config_changes(config)

        # 更新 handler 的配置和状态
        handler.set_config(config["forwarding"])
        handler.enabled = True

        if rule_exists:
            response = f"✅ 转发规则已存在，已启用转发:\n{source_channel} → {target_channel}"
        else:
            response = f"✅ 转发规则已添加并启用:\n{source_channel} → {target_channel}"

        response += f"\n\n当前共有 {len(config['forwarding']['rules'])} 条规则"
        await message.reply(response)
        logger.info(
            f"用户 {message.sender_id} 添加并启用了转发规则: {source_channel} → {target_channel}"
        )

    except Exception as e:
        logger.error(f"添加并启用转发规则失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(f"❌ 操作失败: {str(e)}")


async def cmd_forwarding_remove_rule(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    删除转发规则（持久化到配置文件）

    用法: /forwarding_remove_rule <源频道> <目标频道>

    示例: /forwarding_remove_rule https://t.me/source https://t.me/target
    """
    try:
        if not await check_admin_permission(message, "/forwarding_remove_rule"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            await message.reply(
                "❌ 参数错误\n\n"
                "用法: /forwarding_remove_rule <源频道> <目标频道>\n"
                "示例: /forwarding_remove_rule https://t.me/source https://t.me/target"
            )
            return

        source_channel = args[0]
        target_channel = args[1]

        # 加载当前配置
        from core.config import load_config, save_config_changes

        config = load_config()

        if "forwarding" not in config or "rules" not in config["forwarding"]:
            await message.reply("⚠️ 当前没有转发规则")
            return

        # 查找并删除规则
        original_count = len(config["forwarding"]["rules"])
        config["forwarding"]["rules"] = [
            rule
            for rule in config["forwarding"]["rules"]
            if not (
                rule.get("source_channel", "").rstrip("/").split("/")[-1]
                == source_channel.rstrip("/").split("/")[-1]
                and rule.get("target_channel", "").rstrip("/").split("/")[-1]
                == target_channel.rstrip("/").split("/")[-1]
            )
        ]

        if len(config["forwarding"]["rules"]) == original_count:
            await message.reply(f"⚠️ 未找到转发规则:\n{source_channel} → {target_channel}")
            return

        # 保存配置
        save_config_changes(config)

        # 更新 handler 的配置
        handler.set_config(config["forwarding"])

        await message.reply(
            f"✅ 转发规则已删除:\n{source_channel} → {target_channel}\n\n"
            f"当前共有 {len(config['forwarding']['rules'])} 条规则"
        )
        logger.info(f"用户 {message.sender_id} 删除了转发规则: {source_channel} → {target_channel}")

    except Exception as e:
        logger.error(f"删除转发规则失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(f"❌ 删除转发规则失败: {str(e)}")


# ==================== 辅助函数 ====================


def _extract_channel_id(channel_url: str) -> str:
    """
    从频道URL中提取频道标识符

    支持的格式：
    - https://t.me/channelname
    - https://t.me/+invitecode
    - @channelname
    - channelname
    - 数字ID

    Args:
        channel_url: 频道URL或标识符

    Returns:
        str: 提取的频道标识符
    """
    url = channel_url.strip()
    # 移除末尾的斜杠
    url = url.rstrip("/")
    # 提取最后一部分作为标识
    return url.split("/")[-1].lstrip("@")


def _find_rule(config: dict, source_channel: str, target_channel: str) -> dict | None:
    """
    查找匹配的转发规则

    Args:
        config: 转发配置对象
        source_channel: 源频道URL
        target_channel: 目标频道URL

    Returns:
        dict | None: 匹配的规则对象，如果未找到返回 None
    """
    source_id = _extract_channel_id(source_channel)
    target_id = _extract_channel_id(target_channel)

    rules = config.get("rules", [])
    for rule in rules:
        rule_source = _extract_channel_id(rule.get("source_channel", ""))
        rule_target = _extract_channel_id(rule.get("target_channel", ""))

        if rule_source == source_id and rule_target == target_id:
            return rule

    return None


def _persist_forwarding_config(handler_config: dict, field: str, rule: dict | None = None):
    """
    只持久化命令明确修改的字段，保留 WebUI 在其他字段上的最新修改。

    Args:
        handler_config: handler.config 转发配置字典（已被修改的内存引用）
        field: 明确修改的字段名
        rule: 被修改的规则；为 None 时更新转发全局字段
    """
    from copy import deepcopy

    from core.config import read_config, save_config_changes

    config = read_config()
    forwarding = config.setdefault("forwarding", {})
    if rule is None:
        forwarding[field] = deepcopy(handler_config[field])
    else:
        latest_rule = _find_rule(
            forwarding, rule.get("source_channel", ""), rule.get("target_channel", "")
        )
        if latest_rule is None:
            raise ValueError(
                t(
                    "forwarding.error.rule_not_found",
                    source=rule.get("source_channel", ""),
                    target=rule.get("target_channel", ""),
                )
            )
        latest_rule[field] = deepcopy(rule[field])
    save_config_changes(config)
    logger.info("转发配置字段已持久化: %s", field)


def _validate_regex(pattern: str) -> tuple[bool, str]:
    """
    验证正则表达式语法

    Args:
        pattern: 正则表达式字符串

    Returns:
        tuple[bool, str]: (是否有效, 错误信息)
    """
    try:
        re.compile(pattern)
        return True, ""
    except re.error as e:
        return False, str(e)


def _format_list_output(items: list, icon: str = "•") -> str:
    """
    格式化列表输出

    Args:
        items: 列表项
        icon: 列表图标

    Returns:
        str: 格式化的列表字符串
    """
    if not items:
        return t("forwarding.list.empty")
    return "\n".join(f"{icon} {item}" for item in items)


# ==================== 关键词白名单命令 ====================


async def cmd_forwarding_keywords(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    设置/查看/删除转发规则的关键词白名单

    用法:
    /forwarding_keywords <源频道> <目标频道>           - 查看关键词
    /forwarding_keywords <源频道> <目标频道> add <关键词...>    - 添加关键词
    /forwarding_keywords <源频道> <目标频道> remove <关键词...> - 删除关键词
    /forwarding_keywords <源频道> <目标频道> clear    - 清空所有关键词

    示例:
    /forwarding_keywords https://t.me/source https://t.me/target
    /forwarding_keywords https://t.me/source https://t.me/target add 重要 公告
    /forwarding_keywords https://t.me/source https://t.me/target remove 重要
    /forwarding_keywords https://t.me/source https://t.me/target clear
    """
    try:
        if not await check_admin_permission(message, "/forwarding_keywords"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            await message.reply(t("forwarding.keywords.usage"))
            return

        source_channel = args[0]
        target_channel = args[1]

        # 查找规则
        rule = _find_rule(handler.config, source_channel, target_channel)
        if not rule:
            await message.reply(
                t("forwarding.error.rule_not_found", source=source_channel, target=target_channel)
            )
            return

        # 无额外参数，显示当前关键词
        if len(args) == 2:
            keywords = rule.get("keywords", [])
            if keywords:
                response = t("forwarding.keywords.view", count=len(keywords))
                response += "\n\n" + _format_list_output(keywords, "🏷️")
            else:
                response = t("forwarding.keywords.empty")
            await message.reply(response)
            return

        action = args[2].lower()

        # 清空关键词
        if action == "clear":
            rule["keywords"] = []
            _persist_forwarding_config(handler.config, "keywords", rule)
            await message.reply(
                t("forwarding.keywords.cleared", source=source_channel, target=target_channel)
            )
            logger.info(f"用户 {message.sender_id} 清空了关键词白名单")
            return

        # 添加关键词
        if action == "add":
            if len(args) < 4:
                await message.reply(t("forwarding.error.no_keywords_provided"))
                return

            new_keywords = args[3:]
            if "keywords" not in rule:
                rule["keywords"] = []
            rule["keywords"].extend(new_keywords)
            # 去重
            rule["keywords"] = list(set(rule["keywords"]))

            _persist_forwarding_config(handler.config, "keywords", rule)
            keywords_str = "\n".join(f"🏷️ {kw}" for kw in new_keywords)
            await message.reply(
                t(
                    "forwarding.keywords.added",
                    source=source_channel,
                    target=target_channel,
                    keywords=keywords_str,
                )
            )
            logger.info(f"用户 {message.sender_id} 添加了关键词: {new_keywords}")
            return

        # 删除关键词
        if action == "remove":
            if len(args) < 4:
                await message.reply(t("forwarding.error.no_keywords_provided"))
                return

            keywords_to_remove = args[3:]
            if "keywords" not in rule:
                rule["keywords"] = []

            removed = []
            for kw in keywords_to_remove:
                if kw in rule["keywords"]:
                    rule["keywords"].remove(kw)
                    removed.append(kw)

            if removed:
                _persist_forwarding_config(handler.config, "keywords", rule)
                removed_str = "\n".join(f"🏷️ {kw}" for kw in removed)
                await message.reply(
                    t(
                        "forwarding.keywords.removed",
                        source=source_channel,
                        target=target_channel,
                        keywords=removed_str,
                    )
                )
                logger.info(f"用户 {message.sender_id} 删除了关键词: {removed}")
            else:
                await message.reply("⚠️ 指定的关键词不存在")

            return

        # 无效操作
        await message.reply(t("forwarding.keywords.usage"))

    except Exception as e:
        logger.error(f"关键词白名单操作失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.save_failed", error=str(e)))


# ==================== 关键词黑名单命令 ====================


async def cmd_forwarding_blacklist(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    设置/查看/删除转发规则的关键词黑名单

    用法:
    /forwarding_blacklist <源频道> <目标频道>           - 查看黑名单
    /forwarding_blacklist <源频道> <目标频道> add <关键词...>    - 添加关键词
    /forwarding_blacklist <源频道> <目标频道> remove <关键词...> - 删除关键词
    /forwarding_blacklist <源频道> <目标频道> clear    - 清空所有关键词

    示例:
    /forwarding_blacklist https://t.me/source https://t.me/target
    /forwarding_blacklist https://t.me/source https://t.me/target add 广告 垃圾
    /forwarding_blacklist https://t.me/source https://t.me/target remove 广告
    /forwarding_blacklist https://t.me/source https://t.me/target clear
    """
    try:
        if not await check_admin_permission(message, "/forwarding_blacklist"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            await message.reply(t("forwarding.blacklist.usage"))
            return

        source_channel = args[0]
        target_channel = args[1]

        # 查找规则
        rule = _find_rule(handler.config, source_channel, target_channel)
        if not rule:
            await message.reply(
                t("forwarding.error.rule_not_found", source=source_channel, target=target_channel)
            )
            return

        # 无额外参数，显示当前黑名单
        if len(args) == 2:
            blacklist = rule.get("blacklist", [])
            if blacklist:
                response = t("forwarding.blacklist.view", count=len(blacklist))
                response += "\n\n" + _format_list_output(blacklist, "🚫")
            else:
                response = t("forwarding.blacklist.empty")
            await message.reply(response)
            return

        action = args[2].lower()

        # 清空黑名单
        if action == "clear":
            rule["blacklist"] = []
            _persist_forwarding_config(handler.config, "blacklist", rule)
            await message.reply(
                t("forwarding.blacklist.cleared", source=source_channel, target=target_channel)
            )
            logger.info(f"用户 {message.sender_id} 清空了关键词黑名单")
            return

        # 添加关键词
        if action == "add":
            if len(args) < 4:
                await message.reply(t("forwarding.error.no_keywords_provided"))
                return

            new_keywords = args[3:]
            if "blacklist" not in rule:
                rule["blacklist"] = []
            rule["blacklist"].extend(new_keywords)
            # 去重
            rule["blacklist"] = list(set(rule["blacklist"]))

            _persist_forwarding_config(handler.config, "blacklist", rule)
            keywords_str = "\n".join(f"🚫 {kw}" for kw in new_keywords)
            await message.reply(
                t(
                    "forwarding.blacklist.added",
                    source=source_channel,
                    target=target_channel,
                    keywords=keywords_str,
                )
            )
            logger.info(f"用户 {message.sender_id} 添加了黑名单关键词: {new_keywords}")
            return

        # 删除关键词
        if action == "remove":
            if len(args) < 4:
                await message.reply(t("forwarding.error.no_keywords_provided"))
                return

            keywords_to_remove = args[3:]
            if "blacklist" not in rule:
                rule["blacklist"] = []

            removed = []
            for kw in keywords_to_remove:
                if kw in rule["blacklist"]:
                    rule["blacklist"].remove(kw)
                    removed.append(kw)

            if removed:
                _persist_forwarding_config(handler.config, "blacklist", rule)
                removed_str = "\n".join(f"🚫 {kw}" for kw in removed)
                await message.reply(
                    t(
                        "forwarding.blacklist.removed",
                        source=source_channel,
                        target=target_channel,
                        keywords=removed_str,
                    )
                )
                logger.info(f"用户 {message.sender_id} 删除了黑名单关键词: {removed}")
            else:
                await message.reply("⚠️ 指定的关键词不存在")

            return

        # 无效操作
        await message.reply(t("forwarding.blacklist.usage"))

    except Exception as e:
        logger.error(f"关键词黑名单操作失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.save_failed", error=str(e)))


# ==================== 正则白名单命令 ====================


async def cmd_forwarding_patterns(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    设置/查看/删除转发规则的正则表达式白名单

    用法:
    /forwarding_patterns <源频道> <目标频道>           - 查看正则
    /forwarding_patterns <源频道> <目标频道> add <正则1>|<正则2>  - 添加正则
    /forwarding_patterns <源频道> <目标频道> remove <正则1>|<正则2> - 删除正则
    /forwarding_patterns <源频道> <目标频道> clear    - 清空所有正则

    示例:
    /forwarding_patterns https://t.me/source https://t.me/target
    /forwarding_patterns https://t.me/source https://t.me/target add .*紧急.*|.*重要.*
    /forwarding_patterns https://t.me/source https://t.me/target remove .*广告.*
    /forwarding_patterns https://t.me/source https://t.me/target clear
    """
    try:
        if not await check_admin_permission(message, "/forwarding_patterns"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            await message.reply(t("forwarding.patterns.usage"))
            return

        source_channel = args[0]
        target_channel = args[1]

        # 查找规则
        rule = _find_rule(handler.config, source_channel, target_channel)
        if not rule:
            await message.reply(
                t("forwarding.error.rule_not_found", source=source_channel, target=target_channel)
            )
            return

        # 无额外参数，显示当前正则
        if len(args) == 2:
            patterns = rule.get("patterns", [])
            if patterns:
                response = t("forwarding.patterns.view", count=len(patterns))
                response += "\n\n" + _format_list_output(patterns, "🔍")
            else:
                response = t("forwarding.patterns.empty")
            await message.reply(response)
            return

        action = args[2].lower()

        # 清空正则
        if action == "clear":
            rule["patterns"] = []
            _persist_forwarding_config(handler.config, "patterns", rule)
            await message.reply(
                t("forwarding.patterns.cleared", source=source_channel, target=target_channel)
            )
            logger.info(f"用户 {message.sender_id} 清空了正则白名单")
            return

        # 添加正则
        if action == "add":
            if len(args) < 4:
                await message.reply(t("forwarding.error.no_patterns_provided"))
                return

            # 使用 | 分隔多个正则
            pattern_str = " ".join(args[3:])
            new_patterns = [p.strip() for p in pattern_str.split("|") if p.strip()]

            # 验证正则
            invalid_patterns = []
            for pattern in new_patterns:
                valid, error = _validate_regex(pattern)
                if not valid:
                    invalid_patterns.append(f"• {pattern}: {error}")

            if invalid_patterns:
                await message.reply(
                    t("forwarding.patterns.invalid", error="\n".join(invalid_patterns))
                )
                return

            if "patterns" not in rule:
                rule["patterns"] = []
            rule["patterns"].extend(new_patterns)
            # 去重
            rule["patterns"] = list(set(rule["patterns"]))

            _persist_forwarding_config(handler.config, "patterns", rule)
            patterns_str = "\n".join(f"🔍 {p}" for p in new_patterns)
            await message.reply(
                t(
                    "forwarding.patterns.added",
                    source=source_channel,
                    target=target_channel,
                    patterns=patterns_str,
                )
            )
            logger.info(f"用户 {message.sender_id} 添加了正则白名单: {new_patterns}")
            return

        # 删除正则
        if action == "remove":
            if len(args) < 4:
                await message.reply(t("forwarding.error.no_patterns_provided"))
                return

            pattern_str = " ".join(args[3:])
            patterns_to_remove = [p.strip() for p in pattern_str.split("|") if p.strip()]

            if "patterns" not in rule:
                rule["patterns"] = []

            removed = []
            for pattern in patterns_to_remove:
                if pattern in rule["patterns"]:
                    rule["patterns"].remove(pattern)
                    removed.append(pattern)

            if removed:
                _persist_forwarding_config(handler.config, "patterns", rule)
                removed_str = "\n".join(f"🔍 {p}" for p in removed)
                await message.reply(
                    t(
                        "forwarding.patterns.removed",
                        source=source_channel,
                        target=target_channel,
                        patterns=removed_str,
                    )
                )
                logger.info(f"用户 {message.sender_id} 删除了正则白名单: {removed}")
            else:
                await message.reply("⚠️ 指定的正则表达式不存在")

            return

        # 无效操作
        await message.reply(t("forwarding.patterns.usage"))

    except Exception as e:
        logger.error(f"正则白名单操作失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.save_failed", error=str(e)))


# ==================== 正则黑名单命令 ====================


async def cmd_forwarding_blacklist_patterns(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    设置/查看/删除转发规则的正则表达式黑名单

    用法:
    /forwarding_blacklist_patterns <源频道> <目标频道>           - 查看正则
    /forwarding_blacklist_patterns <源频道> <目标频道> add <正则1>|<正则2>  - 添加正则
    /forwarding_blacklist_patterns <源频道> <目标频道> remove <正则1>|<正则2> - 删除正则
    /forwarding_blacklist_patterns <源频道> <目标频道> clear    - 清空所有正则

    示例:
    /forwarding_blacklist_patterns https://t.me/source https://t.me/target
    /forwarding_blacklist_patterns https://t.me/source https://t.me/target add .*广告.*|.*垃圾.*
    /forwarding_blacklist_patterns https://t.me/source https://t.me/target remove .*广告.*
    /forwarding_blacklist_patterns https://t.me/source https://t.me/target clear
    """
    try:
        if not await check_admin_permission(message, "/forwarding_blacklist_patterns"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            await message.reply(t("forwarding.blacklist_patterns.usage"))
            return

        source_channel = args[0]
        target_channel = args[1]

        # 查找规则
        rule = _find_rule(handler.config, source_channel, target_channel)
        if not rule:
            await message.reply(
                t("forwarding.error.rule_not_found", source=source_channel, target=target_channel)
            )
            return

        # 无额外参数，显示当前正则
        if len(args) == 2:
            patterns = rule.get("blacklist_patterns", [])
            if patterns:
                response = t("forwarding.blacklist_patterns.view", count=len(patterns))
                response += "\n\n" + _format_list_output(patterns, "🚫")
            else:
                response = t("forwarding.blacklist_patterns.empty")
            await message.reply(response)
            return

        action = args[2].lower()

        # 清空正则
        if action == "clear":
            rule["blacklist_patterns"] = []
            _persist_forwarding_config(handler.config, "blacklist_patterns", rule)
            await message.reply(
                t(
                    "forwarding.blacklist_patterns.cleared",
                    source=source_channel,
                    target=target_channel,
                )
            )
            logger.info(f"用户 {message.sender_id} 清空了正则黑名单")
            return

        # 添加正则
        if action == "add":
            if len(args) < 4:
                await message.reply(t("forwarding.error.no_patterns_provided"))
                return

            # 使用 | 分隔多个正则
            pattern_str = " ".join(args[3:])
            new_patterns = [p.strip() for p in pattern_str.split("|") if p.strip()]

            # 验证正则
            invalid_patterns = []
            for pattern in new_patterns:
                valid, error = _validate_regex(pattern)
                if not valid:
                    invalid_patterns.append(f"• {pattern}: {error}")

            if invalid_patterns:
                await message.reply(
                    t("forwarding.blacklist_patterns.invalid", error="\n".join(invalid_patterns))
                )
                return

            if "blacklist_patterns" not in rule:
                rule["blacklist_patterns"] = []
            rule["blacklist_patterns"].extend(new_patterns)
            # 去重
            rule["blacklist_patterns"] = list(set(rule["blacklist_patterns"]))

            _persist_forwarding_config(handler.config, "blacklist_patterns", rule)
            patterns_str = "\n".join(f"🚫 {p}" for p in new_patterns)
            await message.reply(
                t(
                    "forwarding.blacklist_patterns.added",
                    source=source_channel,
                    target=target_channel,
                    patterns=patterns_str,
                )
            )
            logger.info(f"用户 {message.sender_id} 添加了正则黑名单: {new_patterns}")
            return

        # 删除正则
        if action == "remove":
            if len(args) < 4:
                await message.reply(t("forwarding.error.no_patterns_provided"))
                return

            pattern_str = " ".join(args[3:])
            patterns_to_remove = [p.strip() for p in pattern_str.split("|") if p.strip()]

            if "blacklist_patterns" not in rule:
                rule["blacklist_patterns"] = []

            removed = []
            for pattern in patterns_to_remove:
                if pattern in rule["blacklist_patterns"]:
                    rule["blacklist_patterns"].remove(pattern)
                    removed.append(pattern)

            if removed:
                _persist_forwarding_config(handler.config, "blacklist_patterns", rule)
                removed_str = "\n".join(f"🚫 {p}" for p in removed)
                await message.reply(
                    t(
                        "forwarding.blacklist_patterns.removed",
                        source=source_channel,
                        target=target_channel,
                        patterns=removed_str,
                    )
                )
                logger.info(f"用户 {message.sender_id} 删除了正则黑名单: {removed}")
            else:
                await message.reply("⚠️ 指定的正则表达式不存在")

            return

        # 无效操作
        await message.reply(t("forwarding.blacklist_patterns.usage"))

    except Exception as e:
        logger.error(f"正则黑名单操作失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.save_failed", error=str(e)))


# ==================== 复制模式命令 ====================


async def cmd_forwarding_copy_mode(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    启用/禁用转发规则的复制模式

    复制模式：转发的消息不显示"转发来源"标识

    用法:
    /forwarding_copy_mode <源频道> <目标频道>         - 查看状态
    /forwarding_copy_mode <源频道> <目标频道> on      - 启用复制模式
    /forwarding_copy_mode <源频道> <目标频道> off     - 禁用复制模式

    示例:
    /forwarding_copy_mode https://t.me/source https://t.me/target
    /forwarding_copy_mode https://t.me/source https://t.me/target on
    /forwarding_copy_mode https://t.me/source https://t.me/target off
    """
    try:
        if not await check_admin_permission(message, "/forwarding_copy_mode"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            await message.reply(t("forwarding.copy_mode.usage"))
            return

        source_channel = args[0]
        target_channel = args[1]

        # 查找规则
        rule = _find_rule(handler.config, source_channel, target_channel)
        if not rule:
            await message.reply(
                t("forwarding.error.rule_not_found", source=source_channel, target=target_channel)
            )
            return

        # 无额外参数，显示当前状态
        if len(args) == 2:
            current_status = rule.get("copy_mode", False)
            status_text = t("status.enabled") if current_status else t("status.disabled")
            await message.reply(
                t(
                    "forwarding.copy_mode.status",
                    source=source_channel,
                    target=target_channel,
                    status=status_text,
                )
            )
            return

        action = args[2].lower()

        # 启用复制模式
        if action in ["on", "true", "1", "yes", "enable"]:
            rule["copy_mode"] = True
            _persist_forwarding_config(handler.config, "copy_mode", rule)
            await message.reply(
                t("forwarding.copy_mode.enabled", source=source_channel, target=target_channel)
            )
            logger.info(f"用户 {message.sender_id} 启用了复制模式")
            return

        # 禁用复制模式
        if action in ["off", "false", "0", "no", "disable"]:
            rule["copy_mode"] = False
            _persist_forwarding_config(handler.config, "copy_mode", rule)
            await message.reply(
                t("forwarding.copy_mode.disabled", source=source_channel, target=target_channel)
            )
            logger.info(f"用户 {message.sender_id} 禁用了复制模式")
            return

        # 无效操作
        await message.reply(t("forwarding.copy_mode.usage"))

    except Exception as e:
        logger.error(f"复制模式操作失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.save_failed", error=str(e)))


# ==================== 只转发原创命令 ====================


async def cmd_forwarding_original_only(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    启用/禁用只转发原创消息

    只转发原创：只转发频道的原创消息，过滤掉其他频道的转发消息

    用法:
    /forwarding_original_only <源频道> <目标频道>         - 查看状态
    /forwarding_original_only <源频道> <目标频道> on      - 启用只转发原创
    /forwarding_original_only <源频道> <目标频道> off     - 禁用只转发原创

    示例:
    /forwarding_original_only https://t.me/source https://t.me/target
    /forwarding_original_only https://t.me/source https://t.me/target on
    /forwarding_original_only https://t.me/source https://t.me/target off
    """
    try:
        if not await check_admin_permission(message, "/forwarding_original_only"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            await message.reply(t("forwarding.original_only.usage"))
            return

        source_channel = args[0]
        target_channel = args[1]

        # 查找规则
        rule = _find_rule(handler.config, source_channel, target_channel)
        if not rule:
            await message.reply(
                t("forwarding.error.rule_not_found", source=source_channel, target=target_channel)
            )
            return

        # 无额外参数，显示当前状态
        if len(args) == 2:
            current_status = rule.get("forward_original_only", False)
            status_text = t("status.enabled") if current_status else t("status.disabled")
            await message.reply(
                t(
                    "forwarding.original_only.status",
                    source=source_channel,
                    target=target_channel,
                    status=status_text,
                )
            )
            return

        action = args[2].lower()

        # 启用只转发原创
        if action in ["on", "true", "1", "yes", "enable"]:
            rule["forward_original_only"] = True
            _persist_forwarding_config(handler.config, "forward_original_only", rule)
            await message.reply(
                t(
                    "forwarding.original_only.enabled",
                    source=source_channel,
                    target=target_channel,
                )
            )
            logger.info(f"用户 {message.sender_id} 启用了只转发原创")
            return

        # 禁用只转发原创
        if action in ["off", "false", "0", "no", "disable"]:
            rule["forward_original_only"] = False
            _persist_forwarding_config(handler.config, "forward_original_only", rule)
            await message.reply(
                t(
                    "forwarding.original_only.disabled",
                    source=source_channel,
                    target=target_channel,
                )
            )
            logger.info(f"用户 {message.sender_id} 禁用了只转发原创")
            return

        # 无效操作
        await message.reply(t("forwarding.original_only.usage"))

    except Exception as e:
        logger.error(f"只转发原创操作失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.save_failed", error=str(e)))


# ==================== 规则详情命令 ====================


async def cmd_forwarding_rule_info(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    查看转发规则的详细配置

    用法: /forwarding_rule_info <源频道> <目标频道>

    示例: /forwarding_rule_info https://t.me/source https://t.me/target
    """
    try:
        if not await check_admin_permission(message, "/forwarding_rule_info"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        if len(args) < 2:
            await message.reply(t("forwarding.rule_info.usage"))
            return

        source_channel = args[0]
        target_channel = args[1]

        # 查找规则
        rule = _find_rule(handler.config, source_channel, target_channel)
        if not rule:
            await message.reply(
                t("forwarding.error.rule_not_found", source=source_channel, target=target_channel)
            )
            return

        # 构建详情输出
        response = t("forwarding.rule_info.title")
        response += "\n\n" + t(
            "forwarding.rule_info.basic", source=source_channel, target=target_channel
        )

        # 白名单关键词
        keywords = rule.get("keywords", [])
        if keywords:
            kw_list = "\n".join(f"🏷️ {kw}" for kw in keywords)
            response += "\n\n" + t(
                "forwarding.rule_info.keywords", count=len(keywords), list=kw_list
            )
        else:
            response += "\n\n" + t("forwarding.rule_info.keywords_empty")

        # 黑名单关键词
        blacklist = rule.get("blacklist", [])
        if blacklist:
            bl_list = "\n".join(f"🚫 {bl}" for bl in blacklist)
            response += "\n\n" + t(
                "forwarding.rule_info.blacklist", count=len(blacklist), list=bl_list
            )
        else:
            response += "\n\n" + t("forwarding.rule_info.blacklist_empty")

        # 正则白名单
        patterns = rule.get("patterns", [])
        if patterns:
            pat_list = "\n".join(f"🔍 {p}" for p in patterns)
            response += "\n\n" + t(
                "forwarding.rule_info.patterns", count=len(patterns), list=pat_list
            )
        else:
            response += "\n\n" + t("forwarding.rule_info.patterns_empty")

        # 正则黑名单
        blacklist_patterns = rule.get("blacklist_patterns", [])
        if blacklist_patterns:
            bp_list = "\n".join(f"🚫 {bp}" for bp in blacklist_patterns)
            response += "\n\n" + t(
                "forwarding.rule_info.blacklist_patterns",
                count=len(blacklist_patterns),
                list=bp_list,
            )
        else:
            response += "\n\n" + t("forwarding.rule_info.blacklist_patterns_empty")

        # 复制模式
        copy_mode = rule.get("copy_mode", False)
        copy_status = t("status.enabled") if copy_mode else t("status.disabled")
        response += "\n\n" + t("forwarding.rule_info.copy_mode", status=copy_status)

        # 只转发原创
        original_only = rule.get("forward_original_only", False)
        original_status = t("status.enabled") if original_only else t("status.disabled")
        response += "\n\n" + t("forwarding.rule_info.original_only", status=original_status)

        # 自定义底栏
        custom_footer = rule.get("custom_footer", "")
        if custom_footer:
            # 限制底栏显示长度
            footer_display = (
                custom_footer if len(custom_footer) <= 50 else custom_footer[:50] + "..."
            )
            response += "\n\n" + t("forwarding.rule_info.footer", footer=footer_display)
        else:
            response += "\n\n" + t("forwarding.rule_info.footer_empty")

        await message.reply(response)
        logger.info(f"用户 {message.sender_id} 查询了规则详情: {source_channel} → {target_channel}")

    except Exception as e:
        logger.error(f"查询规则详情失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.query_failed", error=str(e)))


# ==================== 帮助命令 ====================


async def cmd_forwarding_help(
    client: "TelegramClient", message: "Message", handler: "ForwardingHandler"
):
    """
    显示转发命令的帮助信息

    用法: /forwarding_help [命令名]

    无参数：显示所有转发命令的帮助
    有参数：显示指定命令的详细用法
    """
    try:
        if not await check_admin_permission(message, "/forwarding_help"):
            return

        # 解析参数
        args = message.message.split()[1:] if message.message else []

        # 命令帮助映射
        command_help = {
            "forwarding": {
                "usage": t("forwarding.help.cmd_forwarding_usage"),
                "desc": t("forwarding.help.cmd_forwarding_desc"),
            },
            "forwarding_enable": {
                "usage": "/forwarding_enable",
                "desc": "启用转发功能",
            },
            "forwarding_disable": {
                "usage": "/forwarding_disable",
                "desc": "禁用转发功能",
            },
            "forwarding_stats": {
                "usage": "/forwarding_stats [频道URL]",
                "desc": "查看转发统计。可选参数：指定频道URL查看单个频道统计",
            },
            "forwarding_add_rule": {
                "usage": "/forwarding_add_rule <源频道> <目标频道>",
                "desc": "添加转发规则",
            },
            "forwarding_remove_rule": {
                "usage": "/forwarding_remove_rule <源频道> <目标频道>",
                "desc": "删除转发规则",
            },
            "forwarding_keywords": {
                "usage": t("forwarding.keywords.usage"),
                "desc": "设置关键词白名单。只有包含白名单关键词的消息才会被转发",
            },
            "forwarding_blacklist": {
                "usage": t("forwarding.blacklist.usage"),
                "desc": "设置关键词黑名单。包含黑名单关键词的消息不会被转发",
            },
            "forwarding_patterns": {
                "usage": t("forwarding.patterns.usage"),
                "desc": "设置正则表达式白名单。只有匹配正则的消息才会被转发",
            },
            "forwarding_blacklist_patterns": {
                "usage": t("forwarding.blacklist_patterns.usage"),
                "desc": "设置正则表达式黑名单。匹配正则的消息不会被转发",
            },
            "forwarding_copy_mode": {
                "usage": t("forwarding.copy_mode.usage"),
                "desc": "设置复制模式。启用后转发的消息不显示来源标识",
            },
            "forwarding_original_only": {
                "usage": t("forwarding.original_only.usage"),
                "desc": "设置只转发原创。启用后只转发频道原创消息，过滤转发消息",
            },
            "forwarding_rule_info": {
                "usage": t("forwarding.rule_info.usage"),
                "desc": "查看转发规则的详细配置",
            },
            "forwarding_footer": {
                "usage": t("forwarding.footer.usage"),
                "desc": "设置转发消息的自定义底栏",
            },
            "forwarding_default_footer": {
                "usage": t("forwarding.default_footer.usage"),
                "desc": "启用或禁用默认底栏",
            },
        }

        # 如果有参数，显示特定命令的帮助
        if args:
            cmd_name = args[0].lstrip("/")
            if cmd_name in command_help:
                cmd_info = command_help[cmd_name]
                response = f"📖 **/{cmd_name} 帮助**\n\n"
                response += f"**描述**\n{cmd_info['desc']}\n\n"
                response += f"**用法**\n{cmd_info['usage']}"
            else:
                response = f"⚠️ 未找到命令: /{cmd_name}\n\n"
                response += t("forwarding.help.tip")
        else:
            # 显示所有命令帮助
            response = t("forwarding.help.title")
            response += "\n\n" + t("forwarding.help.basic")
            response += "\n/forwarding - 查看转发状态或快速添加规则"
            response += "\n/forwarding_enable - 启用转发功能"
            response += "\n/forwarding_disable - 禁用转发功能"
            response += "\n/forwarding_stats - 查看转发统计"
            response += "\n/forwarding_help - 查看转发命令帮助"

            response += "\n\n" + t("forwarding.help.rule_management")
            response += "\n/forwarding_add_rule - 添加转发规则"
            response += "\n/forwarding_remove_rule - 删除转发规则"
            response += "\n/forwarding_rule_info - 查看规则详情"

            response += "\n\n" + t("forwarding.help.filter_config")
            response += "\n/forwarding_keywords - 设置关键词白名单"
            response += "\n/forwarding_blacklist - 设置关键词黑名单"
            response += "\n/forwarding_patterns - 设置正则白名单"
            response += "\n/forwarding_blacklist_patterns - 设置正则黑名单"
            response += "\n/forwarding_copy_mode - 设置复制模式"
            response += "\n/forwarding_original_only - 设置只转发原创"

            response += "\n\n" + t("forwarding.help.footer_config")
            response += "\n/forwarding_footer - 设置转发底栏"
            response += "\n/forwarding_default_footer - 启用/禁用默认底栏"

            response += "\n\n" + t("forwarding.help.tip")

        await message.reply(response)
        logger.info(f"用户 {message.sender_id} 查询了转发命令帮助")

    except Exception as e:
        logger.error(f"查询帮助失败: {type(e).__name__}: {e}", exc_info=True)
        await message.reply(t("forwarding.error.query_failed", error=str(e)))
