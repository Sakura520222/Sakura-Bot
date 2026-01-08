import logging
from openai import OpenAI
from config import LLM_API_KEY, LLM_BASE_URL, LLM_MODEL

logger = logging.getLogger(__name__)

# 初始化 AI 客户端
logger.info("开始初始化AI客户端...")
logger.debug(f"AI客户端配置: Base URL={LLM_BASE_URL}, Model={LLM_MODEL}, API Key={'***' if LLM_API_KEY else '未设置'}")

client_llm = OpenAI(
    api_key=LLM_API_KEY, 
    base_url=LLM_BASE_URL
)

logger.info("AI客户端初始化完成")

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
    
    logger.debug(f"AI请求配置: 模型={LLM_MODEL}, 提示词长度={len(current_prompt)}字符, 上下文长度={len(context_text)}字符")
    logger.debug(f"AI请求总长度: {len(prompt)}字符")
    
    try:
        from datetime import datetime
        start_time = datetime.now()
        response = client_llm.chat.completions.create(
            model=LLM_MODEL,
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
        logger.error(f"AI分析失败: {type(e).__name__}: {e}", exc_info=True)
        return f"AI 分析失败: {e}"