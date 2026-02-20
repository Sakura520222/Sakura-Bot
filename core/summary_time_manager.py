# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

import json
import logging
import os
import shutil
import tempfile
import time
from datetime import datetime, timezone

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
    from core.config import LAST_SUMMARY_FILE

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
                            # 兼容旧格式：如果读取的时间没有时区信息，强制视为UTC
                            if time_obj.tzinfo is None:
                                time_obj = time_obj.replace(tzinfo=timezone.utc)
                                logger.debug("检测到旧格式时间戳，已自动转换为UTC")
                            logger.info(f"成功读取频道 {channel} 的上次总结时间: {time_obj.astimezone().strftime('%Y-%m-%d %H:%M:%S %Z')}")
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
                                time_obj = datetime.fromisoformat(data["time"])
                                if time_obj.tzinfo is None:
                                    time_obj = time_obj.replace(tzinfo=timezone.utc)
                                converted_data[ch] = {
                                    "time": time_obj,
                                    "summary_message_ids": data.get("report_message_ids", []),
                                    "poll_message_ids": data.get("poll_message_ids", []),
                                    "button_message_ids": data.get("button_message_ids", [])
                                }
                            else:
                                time_obj = datetime.fromisoformat(data["time"])
                                if time_obj.tzinfo is None:
                                    time_obj = time_obj.replace(tzinfo=timezone.utc)
                                converted_data[ch] = {
                                    "time": time_obj,
                                    "summary_message_ids": data.get("summary_message_ids", []),
                                    "poll_message_ids": data.get("poll_message_ids", []),
                                    "button_message_ids": data.get("button_message_ids", [])
                                }
                        else:
                            time_obj = datetime.fromisoformat(data["time"])
                            if time_obj.tzinfo is None:
                                time_obj = time_obj.replace(tzinfo=timezone.utc)
                            converted_data[ch] = time_obj
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
    from core.config import LAST_SUMMARY_FILE

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
        # 使用UTC时间的isoformat()，会自动带上+00:00后缀
        channel_data = {
            "time": time_to_save.isoformat(),
            "summary_message_ids": summary_message_ids or [],
            "poll_message_ids": poll_message_ids or [],
            "button_message_ids": button_message_ids or []
        }
        existing_data[channel] = channel_data

        # 使用临时文件+原子重命名的方式写入（避免文件被锁定的问题）
        max_retries = 5
        base_delay = 0.3  # 秒

        for attempt in range(max_retries):
            try:
                # 创建临时文件
                temp_dir = os.path.dirname(LAST_SUMMARY_FILE)
                with tempfile.NamedTemporaryFile(
                    mode='w',
                    encoding='utf-8',
                    dir=temp_dir,
                    suffix='.tmp',
                    delete=False
                ) as temp_file:
                    # 写入数据到临时文件
                    json.dump(existing_data, temp_file, ensure_ascii=False, indent=2)
                    temp_path = temp_file.name

                # 刷新并关闭临时文件后，再进行原子替换
                # 使用 shutil.move 实现跨文件系统的兼容性
                try:
                    # 先删除目标文件（如果存在）
                    if os.path.exists(LAST_SUMMARY_FILE):
                        os.remove(LAST_SUMMARY_FILE)
                    # 重命名临时文件为目标文件
                    shutil.move(temp_path, LAST_SUMMARY_FILE)
                except OSError:
                    # 如果删除或移动失败，清理临时文件
                    try:
                        os.unlink(temp_path)
                    except OSError:
                        pass
                    raise

                logger.info(f"成功保存频道 {channel} 的上次总结时间: {time_to_save.astimezone().strftime('%Y-%m-%d %H:%M:%S %Z')} (UTC: {time_to_save.strftime('%Y-%m-%d %H:%M:%S')})")
                logger.debug(f"总结消息ID: {summary_message_ids}, 投票消息ID: {poll_message_ids}, 按钮消息ID: {button_message_ids}")
                return  # 成功则退出函数

            except PermissionError as e:
                if attempt < max_retries - 1:
                    # 指数退避：0.3, 0.6, 1.2, 2.4 秒
                    delay = base_delay * (2 ** attempt)
                    logger.warning(f"文件被占用，第 {attempt + 1} 次重试... (等待 {delay:.1f}秒，错误: {e})")
                    time.sleep(delay)
                else:
                    logger.error("保存上次总结时间失败: 文件被其他程序占用或权限不足。请关闭可能打开该文件的程序后重试。")
                    logger.error(f"受影响的频道: {channel}, 时间: {time_to_save}")
                    # 尝试回退到原始文件名（可能需要从备份恢复）
                    raise PermissionError(f"无法保存文件 {LAST_SUMMARY_FILE}，已被其他程序锁定。请关闭 VSCode 或其他可能打开该文件的程序。") from e

            except Exception as e:
                logger.error(f"保存上次总结时间到文件 {LAST_SUMMARY_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)
                raise
    except Exception as e:
        logger.error(f"保存上次总结时间失败: {type(e).__name__}: {e}", exc_info=True)
        raise
