import logging
from config import PROMPT_FILE, DEFAULT_PROMPT

logger = logging.getLogger(__name__)

# 读取提示词函数
def load_prompt():
    """从文件中读取提示词，如果文件不存在则使用默认提示词"""
    logger.info(f"开始读取提示词文件: {PROMPT_FILE}")
    try:
        with open(PROMPT_FILE, "r", encoding="utf-8") as f:
            content = f.read().strip()
            logger.info(f"成功读取提示词文件，长度: {len(content)}字符")
            return content
    except FileNotFoundError:
        logger.warning(f"提示词文件 {PROMPT_FILE} 不存在，将使用默认提示词并创建文件")
        # 如果文件不存在，使用默认提示词并创建文件
        save_prompt(DEFAULT_PROMPT)
        return DEFAULT_PROMPT
    except Exception as e:
        logger.error(f"读取提示词文件 {PROMPT_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)
        # 如果读取失败，使用默认提示词
        return DEFAULT_PROMPT

# 保存提示词函数
def save_prompt(prompt):
    """将提示词保存到文件中"""
    logger.info(f"开始保存提示词到文件: {PROMPT_FILE}")
    try:
        with open(PROMPT_FILE, "w", encoding="utf-8") as f:
            f.write(prompt)
        logger.info(f"成功保存提示词到文件，长度: {len(prompt)}字符")
    except Exception as e:
        logger.error(f"保存提示词到文件 {PROMPT_FILE} 时出错: {type(e).__name__}: {e}", exc_info=True)