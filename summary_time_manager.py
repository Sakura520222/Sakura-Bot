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
                            # 返回包含时间对象和消息ID列表的字典
                            return {
                                "time": datetime.fromisoformat(channel_data["time"]),
                                "report_message_ids": channel_data.get("report_message_ids", [])
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
                            converted_data[ch] = {
                                "time": datetime.fromisoformat(data["time"]),
                                "report_message_ids": data.get("report_message_ids", [])
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
def save_last_summary_time(channel, time_to_save, report_message_ids=None):
    """将指定频道的上次总结时间和报告消息ID保存到文件中
    
    Args:
        channel: 频道标识
        time_to_save: 要保存的时间对象
        report_message_ids: 发送到源频道的报告消息ID列表，可选
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
        
        # 更新指定频道的时间和报告消息ID
        channel_data = {
            "time": time_to_save.isoformat(),
            "report_message_ids": report_message_ids or []
        }
        existing_data[channel] = channel_data
        
        # 写入文件
        with open(LAST_SUMMARY_FILE, "w", encoding="utf-8") as f:
            json.dump(existing_data, f, ensure_ascii=False, indent=2)
        
        logger.info(f"成功保存频道 {channel} 的上次总结时间: {time_to_save}，报告消息ID: {report_message_ids}")
    except Exception as e:
        logger.error(f"保存上次总结时间到文件 {LAST_SUMMARY_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)