# Copyright 2026 Sakura-频道总结助手
# 
# 本项目采用 CC BY-NC-SA 4.0 许可证
# 您可以自由地共享、修改本作品，但必须遵守以下条件：
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
# 
# 本项目源代码：https://github.com/Sakura520222/Sakura-Channel-Summary-Assistant
# 许可证全文：https://creativecommons.org/licenses/by-nc-sa/4.0/deed.zh

import os
import json
import logging
from datetime import datetime

logger = logging.getLogger(__name__)

# 读取上次总结时间函数
def load_last_summary_time(channel=None, include_report_ids=False):
    """从文件中读取上次总结的时间和报告消息ID

    Args:
        channel: 可选，指定频道。如果提供，只返回该频道的信息；
                如果不提供，返回所有频道的信息字典
        include_report_ids: 可选，是否包含报告消息ID。默认False只返回时间，True返回包含时间和消息ID的字典
                                新版: 返回summary_message_ids, poll_message_ids, button_message_ids三类ID
    """
    from config import LAST_SUMMARY_FILE

    logger.info(f"开始读取上次总结时间文件: {LAST_SUMMARY_FILE}")
    try:
        with open(LAST_SUMMARY_FILE, "r", encoding="utf-8") as f:
            content = f.read().strip()
            if content:
                last_data = json.loads(content)
                logger.info(f"成功读取所有频道的上次总结数据: {last_data}")

                if channel:
                    # 返回指定频道的信息
                    channel_data = last_data.get(channel)
                    if channel_data:
                        if include_report_ids:
                            # 兼容旧格式: report_message_ids
                            if "report_message_ids" in channel_data:
                                # 旧格式
                                return {
                                    "time": datetime.fromisoformat(channel_data["time"]),
                                    "summary_message_ids": channel_data.get("report_message_ids", []),
                                    "poll_message_ids": channel_data.get("poll_message_ids", []),
                                    "button_message_ids": channel_data.get("button_message_ids", [])
                                }
                            else:
                                # 新格式
                                return {
                                    "time": datetime.fromisoformat(channel_data["time"]),
                                    "summary_message_ids": channel_data.get("summary_message_ids", []),
                                    "poll_message_ids": channel_data.get("poll_message_ids", []),
                                    "button_message_ids": channel_data.get("button_message_ids", [])
                                }
                        else:
                            # 只返回时间对象
                            time_obj = datetime.fromisoformat(channel_data["time"])
                            logger.info(f"成功读取频道 {channel} 的上次总结时间: {time_obj}")
                            return time_obj
                    else:
                        logger.warning(f"频道 {channel} 的上次总结时间不存在")
                        return None
                else:
                    # 返回所有频道的信息
                    converted_data = {}
                    for ch, data in last_data.items():
                        if include_report_ids:
                            # 兼容旧格式
                            if "report_message_ids" in data:
                                converted_data[ch] = {
                                    "time": datetime.fromisoformat(data["time"]),
                                    "summary_message_ids": data.get("report_message_ids", []),
                                    "poll_message_ids": data.get("poll_message_ids", []),
                                    "button_message_ids": data.get("button_message_ids", [])
                                }
                            else:
                                converted_data[ch] = {
                                    "time": datetime.fromisoformat(data["time"]),
                                    "summary_message_ids": data.get("summary_message_ids", []),
                                    "poll_message_ids": data.get("poll_message_ids", []),
                                    "button_message_ids": data.get("button_message_ids", [])
                                }
                        else:
                            converted_data[ch] = datetime.fromisoformat(data["time"])
                    return converted_data
            else:
                logger.warning(f"上次总结时间文件 {LAST_SUMMARY_FILE} 内容为空")
                return None if channel else {}
    except FileNotFoundError:
        logger.warning(f"上次总结时间文件 {LAST_SUMMARY_FILE} 不存在")
        return None if channel else {}
    except Exception as e:
        logger.error(f"读取上次总结时间文件 {LAST_SUMMARY_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)
        return None if channel else {}

# 保存上次总结时间函数
def save_last_summary_time(channel, time_to_save, summary_message_ids=None, poll_message_ids=None, button_message_ids=None, report_message_ids=None):
    """将指定频道的上次总结时间和报告消息ID保存到文件中

    Args:
        channel: 频道标识
        time_to_save: 要保存的时间对象
        summary_message_ids: 总结消息ID列表(新格式)
        poll_message_ids: 投票消息ID列表(新格式)
        button_message_ids: 按钮消息ID列表(新格式)
        report_message_ids: 发送到源频道的报告消息ID列表(旧格式,兼容参数)
    """
    from config import LAST_SUMMARY_FILE

    logger.info(f"开始保存频道 {channel} 的上次总结时间到文件: {LAST_SUMMARY_FILE}")
    try:
        # 先读取现有数据
        existing_data = {}
        if os.path.exists(LAST_SUMMARY_FILE):
            with open(LAST_SUMMARY_FILE, "r", encoding="utf-8") as f:
                content = f.read().strip()
                if content:
                    existing_data = json.loads(content)

        # 支持新格式和旧格式
        # 如果提供report_message_ids(旧格式),将其作为summary_message_ids
        if report_message_ids is not None and summary_message_ids is None:
            summary_message_ids = report_message_ids

        # 类型检查和清理: 确保所有参数都是列表
        # 防止传入字典或其他类型导致数据结构错误
        if summary_message_ids is not None and not isinstance(summary_message_ids, list):
            # 如果summary_message_ids是字典,提取summary_message_ids键
            if isinstance(summary_message_ids, dict):
                logger.warning(f"检测到summary_message_ids是字典格式,自动提取: {summary_message_ids}")
                summary_message_ids = summary_message_ids.get("summary_message_ids", [])
            else:
                logger.error(f"summary_message_ids类型错误: {type(summary_message_ids)}, 使用空列表")
                summary_message_ids = []

        if poll_message_ids is not None and not isinstance(poll_message_ids, list):
            logger.error(f"poll_message_ids类型错误: {type(poll_message_ids)}, 使用空列表")
            poll_message_ids = []

        if button_message_ids is not None and not isinstance(button_message_ids, list):
            logger.error(f"button_message_ids类型错误: {type(button_message_ids)}, 使用空列表")
            button_message_ids = []

        # 更新指定频道的时间和消息ID
        channel_data = {
            "time": time_to_save.isoformat(),
            "summary_message_ids": summary_message_ids or [],
            "poll_message_ids": poll_message_ids or [],
            "button_message_ids": button_message_ids or []
        }
        existing_data[channel] = channel_data

        # 写入文件
        with open(LAST_SUMMARY_FILE, "w", encoding="utf-8") as f:
            json.dump(existing_data, f, ensure_ascii=False, indent=2)

        logger.info(f"成功保存频道 {channel} 的上次总结时间: {time_to_save}")
        logger.debug(f"总结消息ID: {summary_message_ids}, 投票消息ID: {poll_message_ids}, 按钮消息ID: {button_message_ids}")
    except Exception as e:
        logger.error(f"保存上次总结时间到文件 {LAST_SUMMARY_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)
