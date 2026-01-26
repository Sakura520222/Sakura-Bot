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

import logging
import os
from datetime import datetime, timezone, timedelta
from telethon.events import NewMessage

from .config import ADMIN_LIST, CHANNELS
from .telegram_client import send_long_message
from .database import get_db_manager

logger = logging.getLogger(__name__)


async def handle_history(event):
    """处理 /history 命令，查看历史总结"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        # 解析命令参数
        parts = command.split()
        channel_id = None
        days = None

        if len(parts) > 1:
            # 有频道参数
            channel_part = parts[1]
            if channel_part.startswith('http'):
                channel_id = channel_part
            else:
                channel_id = f"https://t.me/{channel_part}"

            # 验证频道是否存在
            if channel_id not in CHANNELS:
                await event.reply(f"频道 {channel_id} 不在配置列表中")
                return

        if len(parts) > 2:
            # 有天数参数
            try:
                days = int(parts[2])
            except ValueError:
                await event.reply("天数必须是数字，例如：/history channel1 30")
                return

        # 查询数据库
        db = get_db_manager()

        # 如果指定了天数，计算起始日期
        start_date = None
        if days:
            start_date = datetime.now(timezone.utc) - timedelta(days=days)

        summaries = db.get_summaries(channel_id=channel_id, limit=10, start_date=start_date)

        if not summaries:
            if channel_id:
                await event.reply(f"❌ 频道 {channel_id.split('/')[-1]} 暂无历史总结记录")
            else:
                await event.reply("❌ 暂无历史总结记录")
            return

        # 格式化输出
        channel_name = summaries[0].get('channel_name', '未知频道') if channel_id else "所有频道"
        total_count = len(summaries)

        result = f"📋 **{channel_name} 历史总结**\n\n"
        result += f"共找到 {total_count} 条记录，显示最近 {min(total_count, 10)} 条:\n\n"

        for i, summary in enumerate(summaries[:10], 1):
            created_at = summary.get('created_at', '未知时间')
            summary_type = summary.get('summary_type', 'weekly')
            message_count = summary.get('message_count', 0)
            summary_text = summary.get('summary_text', '')
            summary_message_ids = summary.get('summary_message_ids', [])

            # 类型中文映射
            type_map = {'daily': '日报', 'weekly': '周报', 'manual': '手动总结'}
            type_cn = type_map.get(summary_type, summary_type)

            # 格式化时间
            try:
                dt = datetime.fromisoformat(created_at)
                time_str = dt.strftime('%Y-%m-%d %H:%M')
            except:
                time_str = created_at

            # 提取摘要(前150字符)
            summary_preview = summary_text[:150].replace('\n', ' ') + "..." if len(summary_text) > 150 else summary_text

            # 生成链接(如果有消息ID)
            channel_link = summary.get('channel_id', '')
            msg_link = ""
            if summary_message_ids and channel_link:
                first_msg_id = summary_message_ids[0]
                channel_part = channel_link.split('/')[-1]
                msg_link = f"\n   📝 查看完整: https://t.me/{channel_part}/{first_msg_id}"

            result += f"🔹 **{time_str}** ({type_cn})\n"
            result += f"   📊 处理消息: {message_count} 条\n"
            result += f"   💬 核心要点:\n   {summary_preview}{msg_link}\n\n"

        result += f"💡 提示: 使用 /export 导出完整记录"

        logger.info(f"执行命令 {command} 成功，返回 {total_count} 条记录")
        await send_long_message(event.client, sender_id, result)

    except Exception as e:
        logger.error(f"执行命令 {command} 时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"查询历史记录时出错: {e}")


async def handle_export(event):
    """处理 /export 命令，导出历史记录"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        # 解析命令参数
        parts = command.split()
        channel_id = None
        output_format = "json"  # 默认格式

        if len(parts) > 1:
            # 有频道参数
            channel_part = parts[1]
            if channel_part.startswith('http'):
                channel_id = channel_part
            else:
                # 可能是频道名或格式
                if channel_part.lower() in ['json', 'csv', 'md']:
                    output_format = channel_part.lower()
                else:
                    channel_id = f"https://t.me/{channel_part}"

        if len(parts) > 2:
            # 第二个参数可能是格式或频道
            second_param = parts[2].lower()
            if second_param in ['json', 'csv', 'md']:
                output_format = second_param

        # 如果指定了频道，验证是否存在
        if channel_id and channel_id not in CHANNELS:
            await event.reply(f"频道 {channel_id} 不在配置列表中")
            return

        await event.reply("📦 正在导出历史记录，请稍候...")

        # 导出数据
        db = get_db_manager()
        filename = db.export_summaries(output_format=output_format, channel_id=channel_id)

        if filename:
            # 发送文件
            await event.client.send_file(
                sender_id,
                filename,
                caption=f"✅ 导出成功\n格式: {output_format}\n文件: {filename}"
            )

            logger.info(f"成功导出历史记录: {filename}")

            # 删除临时文件
            try:
                os.remove(filename)
            except:
                pass
        else:
            await event.reply("❌ 导出失败：没有数据可导出或不支持的格式")

    except Exception as e:
        logger.error(f"执行命令 {command} 时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"导出历史记录时出错: {e}")


async def handle_stats(event):
    """处理 /stats 命令，查看统计数据"""
    sender_id = event.sender_id
    command = event.text
    logger.info(f"收到命令: {command}，发送者: {sender_id}")

    # 检查发送者是否为管理员
    if sender_id not in ADMIN_LIST and ADMIN_LIST != ['me']:
        logger.warning(f"发送者 {sender_id} 没有权限执行命令 {command}")
        await event.reply("您没有权限执行此命令")
        return

    try:
        # 解析命令参数
        parts = command.split()
        channel_id = None

        if len(parts) > 1:
            # 有频道参数
            channel_part = parts[1]
            if channel_part.startswith('http'):
                channel_id = channel_part
            else:
                channel_id = f"https://t.me/{channel_part}"

            # 验证频道是否存在
            if channel_id not in CHANNELS:
                await event.reply(f"频道 {channel_id} 不在配置列表中")
                return

        db = get_db_manager()

        if channel_id:
            # 显示指定频道的统计
            stats = db.get_statistics(channel_id=channel_id)
            channel_name = channel_id.split('/')[-1]

            if not stats or stats.get('total_count', 0) == 0:
                await event.reply(f"❌ 频道 {channel_name} 暂无统计数据")
                return

            result = f"📊 **{channel_name} 频道统计**\n\n"

            # 总结统计
            result += "📈 **总结统计**\n"
            result += f"• 总总结次数: {stats['total_count']} 次\n"

            type_stats = stats.get('type_stats', {})
            type_map = {'daily': '日报', 'weekly': '周报', 'manual': '手动'}
            for type_key, type_name in type_map.items():
                count = type_stats.get(type_key, 0)
                if count > 0:
                    result += f"  - {type_name}: {count} 次\n"

            result += f"• 总处理消息: {stats['total_messages']:,} 条\n"
            result += f"• 平均每次: {stats['avg_messages']} 条消息\n\n"

            # 时间分布
            result += "⏰ **时间分布**\n"
            result += f"• 本周: {stats['week_count']} 次\n"
            result += f"• 本月: {stats['month_count']} 次\n"

            last_time = stats.get('last_summary_time')
            if last_time:
                try:
                    dt = datetime.fromisoformat(last_time)
                    if dt.tzinfo is None:
                        dt = dt.replace(tzinfo=timezone.utc)
                    time_diff = datetime.now(timezone.utc) - dt
                    hours = time_diff.total_seconds() / 3600
                    if hours < 1:
                        time_str = f"{int(hours * 60)} 分钟前"
                    elif hours < 24:
                        time_str = f"{int(hours)} 小时前"
                    else:
                        time_str = f"{int(hours / 24)} 天前"
                    result += f"• 最近总结: {time_str}\n\n"
                except:
                    result += f"• 最近总结: {last_time}\n\n"

            # 数据库信息
            result += "💾 **数据库信息**\n"
            result += f"• 记录数: {stats['total_count']} 条\n"

        else:
            # 显示所有频道的统计
            result = "📊 **频道统计概览**\n\n"

            # 获取各频道统计
            channel_ranking = db.get_channel_ranking(limit=10)

            if not channel_ranking:
                await event.reply("❌ 暂无统计数据")
                return

            result += "🏆 **频道排行** (按总结次数)\n\n"
            for i, channel_stats in enumerate(channel_ranking, 1):
                channel_name = channel_stats.get('channel_name', channel_stats.get('channel_id', '未知'))
                summary_count = channel_stats.get('summary_count', 0)
                total_messages = channel_stats.get('total_messages', 0)
                avg_messages = int(total_messages / summary_count) if summary_count > 0 else 0

                result += f"{i}. **{channel_name}**\n"
                result += f"   总结: {summary_count} 次 | 消息: {total_messages:,} 条 | 平均: {avg_messages} 条/次\n\n"

            # 总体统计
            overall_stats = db.get_statistics()
            result += "---\n\n"
            result += "📈 **总体统计**\n"
            result += f"• 总总结次数: {overall_stats['total_count']} 次\n"
            result += f"• 总处理消息: {overall_stats['total_messages']:,} 条\n"
            result += f"• 频道数量: {len(channel_ranking)} 个\n\n"

        logger.info(f"执行命令 {command} 成功")
        await event.reply(result)

    except Exception as e:
        logger.error(f"执行命令 {command} 时出错: {type(e).__name__}: {e}", exc_info=True)
        await event.reply(f"获取统计数据时出错: {e}")
