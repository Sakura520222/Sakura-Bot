# Copyright 2026 Sakura-频道总结助手
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

import logging
from openai import OpenAI
from .settings import get_llm_api_key, get_llm_base_url, get_llm_model
from .error_handler import retry_with_backoff, record_error
from .poll_prompt_manager import load_poll_prompt

logger = logging.getLogger(__name__)

# 初始化 AI 客户端
logger.info("开始初始化AI客户端...")

# 获取 AI 配置
api_key = get_llm_api_key()
base_url = get_llm_base_url()
model = get_llm_model()

logger.debug(f"AI客户端配置: Base URL={base_url}, Model={model}, API Key={'***' if api_key else '未设置'}")

client_llm = OpenAI(
    api_key=api_key, 
    base_url=base_url
)

logger.info("AI客户端初始化完成")

@retry_with_backoff(
    max_retries=3,
    base_delay=1.0,
    max_delay=30.0,
    exponential_backoff=True,
    retry_on_exceptions=(ConnectionError, TimeoutError, Exception)
)
def analyze_with_ai(messages, current_prompt):
    """调用 AI 进行汇总
    
    Args:
        messages: 要分析的消息列表
        current_prompt: 当前使用的提示词
    """
    logger.info("开始调用AI进行消息汇总")
    
    if not messages:
        logger.info("没有需要分析的消息，返回空结果")
        return "本周无新动态。"

    context_text = "\n\n---\n\n".join(messages)
    prompt = f"{current_prompt}{context_text}"
    
    model = get_llm_model()
    logger.debug(f"AI请求配置: 模型={model}, 提示词长度={len(current_prompt)}字符, 上下文长度={len(context_text)}字符")
    logger.debug(f"AI请求总长度: {len(prompt)}字符")
    
    try:
        from datetime import datetime
        start_time = datetime.now()
        response = client_llm.chat.completions.create(
            model=model,
            messages=[
                {"role": "system", "content": "你是一个专业的资讯摘要助手，擅长提取重点并保持客观。"},
                {"role": "user", "content": prompt},
            ]
        )
        end_time = datetime.now()
        
        processing_time = (end_time - start_time).total_seconds()
        logger.info(f"AI分析完成，处理时间: {processing_time:.2f}秒")
        logger.debug(f"AI响应状态: 成功，选择索引={response.choices[0].index}, 完成原因={response.choices[0].finish_reason}")
        logger.debug(f"AI响应长度: {len(response.choices[0].message.content)}字符")
        
        return response.choices[0].message.content
    except Exception as e:
        record_error(e, "analyze_with_ai")
        logger.error(f"AI分析失败: {type(e).__name__}: {e}", exc_info=True)
        return f"AI 分析失败: {e}"


@retry_with_backoff(
    max_retries=3,
    base_delay=1.0,
    max_delay=30.0,
    exponential_backoff=True,
    retry_on_exceptions=(ConnectionError, TimeoutError, Exception)
)
def generate_poll_from_summary(summary_text):
    """根据总结内容生成投票
    
    Args:
        summary_text: 总结文本
    
    Returns:
        dict: 包含question和options的字典，格式为:
            {"question": "投票问题", "options": ["选项1", "选项2", "选项3", "选项4"]}
    """
    logger.info("开始调用AI生成投票")
    
    if not summary_text or len(summary_text.strip()) < 10:
        logger.warning("总结文本太短，无法生成有意义的投票")
        return {
            "question": "你对本周总结有什么看法？",
            "options": ["非常满意", "比较满意", "一般", "有待改进"]
        }
    
    # 使用可配置的投票提示词
    poll_prompt_template = load_poll_prompt()
    prompt = poll_prompt_template.format(summary_text=summary_text)

    model = get_llm_model()
    logger.debug(f"投票生成请求配置: 模型={model}, 提示词长度={len(prompt)}字符")
    
    try:
        from datetime import datetime
        import json
        import re
        
        start_time = datetime.now()
        response = client_llm.chat.completions.create(
            model=model,
            messages=[
                {"role": "system", "content": "你是一个幽默风趣的互动策划专家，擅长从枯燥的文字中挖掘槽点或亮点，创作让人忍不住想投票的双语投票。"},
                {"role": "user", "content": prompt},
            ]
        )
        end_time = datetime.now()
        
        processing_time = (end_time - start_time).total_seconds()
        logger.info(f"AI投票生成完成，处理时间: {processing_time:.2f}秒")
        
        response_text = response.choices[0].message.content
        logger.debug(f"AI投票生成响应: {response_text}")
        
        # 尝试从响应中提取JSON
        try:
            # 查找JSON部分
            json_match = re.search(r'\{.*\}', response_text, re.DOTALL)
            if json_match:
                json_str = json_match.group()
                poll_data = json.loads(json_str)
                
                # 验证数据结构
                if 'question' in poll_data and 'options' in poll_data:
                    if isinstance(poll_data['options'], list) and len(poll_data['options']) >= 2:
                        # 验证投票问题长度（Telegram限制255字符）
                        question = poll_data['question'].strip()
                        if len(question) > 255:
                            logger.warning(f"投票问题超长: {len(question)}字符，将截断")
                            # 截断到255字符，确保不截断中文字符
                            truncated = question[:255]
                            while truncated and (ord(truncated[-1]) & 0xC0) == 0x80:
                                truncated = truncated[:-1]
                            poll_data['question'] = truncated + '...'
                            logger.info(f"投票问题已截断为: {poll_data['question']}")
                        
                        # 验证选项长度（Telegram限制100字符）
                        valid_options = []
                        for i, option in enumerate(poll_data['options']):
                            option_text = str(option).strip()
                            if len(option_text) > 100:
                                logger.warning(f"投票选项{i+1}超长: {len(option_text)}字符，将截断")
                                # 截断到100字符
                                truncated = option_text[:100]
                                while truncated and (ord(truncated[-1]) & 0xC0) == 0x80:
                                    truncated = truncated[:-1]
                                valid_options.append(truncated + '...')
                            else:
                                valid_options.append(option_text)
                        
                        poll_data['options'] = valid_options
                        logger.info(f"成功生成投票: {poll_data['question']}，选项数: {len(poll_data['options'])}")
                        return poll_data
                    else:
                        logger.warning(f"投票选项格式不正确: {poll_data['options']}")
                else:
                    logger.warning(f"投票数据结构不完整: {poll_data}")
        except json.JSONDecodeError as e:
            logger.error(f"解析AI返回的JSON失败: {e}，原始响应: {response_text}")
        
        # 如果JSON解析失败，返回默认投票
        logger.warning("使用默认投票作为备选")
        return {
            "question": "你对本周总结有什么看法？",
            "options": ["非常满意", "比较满意", "一般", "有待改进"]
        }
        
    except Exception as e:
        record_error(e, "generate_poll_from_summary")
        logger.error(f"AI投票生成失败: {type(e).__name__}: {e}", exc_info=True)
        # 返回默认投票
        return {
            "question": "你对本周总结有什么看法？",
            "options": ["非常满意", "比较满意", "一般", "有待改进"]
        }
