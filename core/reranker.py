# -*- coding: utf-8 -*-
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

"""
重排序器 - 对检索结果进行精排
提升RAG系统的准确性
"""

import logging
import os
from typing import List, Dict, Any, Optional
from openai import OpenAI

logger = logging.getLogger(__name__)


class Reranker:
    """重排序器"""

    def __init__(self):
        """初始化Reranker"""
        self.api_key = os.getenv("RERANKER_API_KEY")
        self.api_base = os.getenv("RERANKER_API_BASE", "https://api.siliconflow.cn/v1/rerank")
        self.model = os.getenv("RERANKER_MODEL", "BAAI/bge-reranker-v2-m3")
        self.top_k = int(os.getenv("RERANKER_TOP_K", "20"))
        self.final_k = int(os.getenv("RERANKER_FINAL", "5"))

        if not self.api_key:
            logger.warning("未设置RERANKER_API_KEY，重排序功能将不可用")
            self.client = None
        else:
            try:
                self.client = OpenAI(
                    api_key=self.api_key,
                    base_url=self.api_base
                )
                logger.info(f"Reranker初始化成功: {self.model}")
            except Exception as e:
                logger.error(f"Reranker初始化失败: {type(e).__name__}: {e}")
                self.client = None

    def is_available(self) -> bool:
        """检查Reranker服务是否可用"""
        return self.client is not None

    def rerank(self, query: str, candidates: List[Dict[str, Any]],
               top_k: Optional[int] = None) -> List[Dict[str, Any]]:
        """
        对检索结果重排序

        Args:
            query: 用户查询
            candidates: 候选文档列表，每个文档包含summary_id, summary_text等
            top_k: 返回前K个结果，默认使用配置的final_k

        Returns:
            重排序后的文档列表
        """
        if not self.client:
            logger.warning("Reranker服务不可用，返回原始结果")
            return candidates[:top_k or self.final_k]

        if not candidates:
            return []

        if top_k is None:
            top_k = self.final_k

        try:
            # 准备文档列表
            documents = [doc.get('summary_text', '') for doc in candidates]

            # 调用Reranker API
            response = self.client.requests.post(
                f"{self.api_base}",
                headers={
                    "Authorization": f"Bearer {self.api_key}",
                    "Content-Type": "application/json"
                },
                json={
                    "model": self.model,
                    "query": query,
                    "documents": documents,
                    "top_n": min(len(documents), top_k)
                }
            )

            result = response.json()

            # 提取排序结果
            if 'results' in result:
                reranked_results = []
                for item in result['results']:
                    index = item['index']
                    score = item.get('relevance_score', 0)
                    doc = candidates[index].copy()
                    doc['rerank_score'] = score
                    reranked_results.append(doc)

                logger.info(f"重排序完成: {len(reranked_results)} 个结果")
                return reranked_results
            else:
                logger.warning(f"Reranker API返回格式异常: {result}")
                return candidates[:top_k]

        except Exception as e:
            logger.error(f"重排序失败: {type(e).__name__}: {e}")
            return candidates[:top_k]


# 创建全局Reranker实例
reranker = None

def get_reranker():
    """获取全局Reranker实例"""
    global reranker
    if reranker is None:
        reranker = Reranker()
    return reranker