# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
投稿服务模块

提供投稿业务逻辑：提交、AI 优化等。
"""

import logging
from typing import Any

from core.ai.ai_client import async_client_llm, get_llm_model
from core.infrastructure.database.submission_repo import get_submission_repo

logger = logging.getLogger(__name__)


class SubmissionService:
    """投稿业务逻辑服务"""

    def __init__(self):
        self.repo = get_submission_repo()

    async def submit(
        self,
        submitter_id: int,
        submitter_name: str,
        title: str,
        content: str | None = None,
        media_files: list[dict[str, Any]] | None = None,
        target_channel: str | None = None,
        is_anonymous: bool = False,
    ) -> dict[str, Any]:
        """提交投稿

        Args:
            submitter_id: 投稿者ID
            submitter_name: 投稿者用户名
            title: 投稿标题
            content: 投稿正文
            media_files: 媒体文件信息列表
            target_channel: 目标频道URL
            is_anonymous: 是否匿名投稿

        Returns:
            {"success": bool, "submission_id": int|None, "message": str}
        """
        try:
            submission_id = await self.repo.create_submission(
                submitter_id=submitter_id,
                submitter_name=submitter_name,
                title=title,
                content=content,
                media_files=media_files,
                target_channel=target_channel,
                is_anonymous=is_anonymous,
            )
            if submission_id:
                logger.info(f"投稿提交成功: ID={submission_id}, 投稿者={submitter_name}")
                return {
                    "success": True,
                    "submission_id": submission_id,
                    "message": "投稿已成功提交，请等待管理员审核。",
                }
            else:
                return {
                    "success": False,
                    "submission_id": None,
                    "message": "投稿提交失败，请稍后重试。",
                }
        except Exception as e:
            logger.error(f"投稿提交失败: {type(e).__name__}: {e}", exc_info=True)
            return {"success": False, "submission_id": None, "message": f"投稿提交时发生错误: {e}"}

    async def get_submission(self, submission_id: int) -> dict[str, Any] | None:
        """获取投稿详情"""
        return await self.repo.get_submission(submission_id)

    async def ai_optimize(self, submission_id: int) -> dict[str, Any]:
        """对投稿内容进行 AI 优化

        Returns:
            {"success": bool, "optimized_content": str|None, "message": str}
        """
        try:
            submission = await self.repo.get_submission(submission_id)
            if not submission:
                return {"success": False, "optimized_content": None, "message": "投稿不存在"}

            # 构建优化提示词
            original_text = submission["content"] or ""
            title = submission["title"]

            # 调用 AI 服务
            model = get_llm_model()
            response = await async_client_llm.chat.completions.create(
                model=model,
                messages=[
                    {
                        "role": "system",
                        "content": (
                            "你是一个专业的内容编辑助手。用户会给你一段投稿的标题和正文，"
                            "请你优化标题和正文的可读性和表达质量，保持原意不变。"
                            "必须严格使用以下JSON格式返回，不要添加任何其他内容：\n"
                            '{"title": "优化后的标题", "content": "优化后的正文"}'
                        ),
                    },
                    {
                        "role": "user",
                        "content": f"标题：{title}\n\n正文：\n{original_text}"
                        if original_text.strip()
                        else f"标题：{title}\n\n正文：(无)",
                    },
                ],
            )

            # 空值保护：校验 API 响应结构
            if not response.choices or not response.choices[0].message:
                logger.error(
                    f"AI 优化服务返回了无效的响应结构: "
                    f"choices={len(response.choices) if response.choices else 0}"
                )
                return {
                    "success": False,
                    "optimized_content": None,
                    "message": "AI 优化服务返回了无效的响应",
                }

            raw_result = response.choices[0].message.content
            if not raw_result:
                logger.error("AI 优化服务返回了空内容")
                return {
                    "success": False,
                    "optimized_content": None,
                    "message": "AI 优化服务返回了空内容",
                }

            raw_result = raw_result.strip()

            # 解析 JSON 结果
            import json

            optimized_title = None
            optimized_content = None
            try:
                # 尝试直接解析
                parsed = json.loads(raw_result)
                optimized_title = parsed.get("title")
                optimized_content = parsed.get("content")
            except json.JSONDecodeError:
                # 尝试提取 JSON 块
                import re

                json_match = re.search(r"\{[^{}]+\}", raw_result, re.DOTALL)
                if json_match:
                    parsed = json.loads(json_match.group())
                    optimized_title = parsed.get("title")
                    optimized_content = parsed.get("content")
                else:
                    # 回退：整个结果作为正文优化
                    optimized_content = raw_result

            # 保存优化结果
            await self.repo.update_submission_status(
                submission_id=submission_id,
                status="pending",
                ai_optimized_content=optimized_content or original_text,
                ai_optimized_title=optimized_title or title,
            )
            return {
                "success": True,
                "optimized_content": optimized_content or original_text,
                "optimized_title": optimized_title or title,
                "message": "AI 优化完成",
            }

        except Exception as e:
            logger.error(f"AI 优化投稿失败: {type(e).__name__}: {e}", exc_info=True)
            return {
                "success": False,
                "optimized_content": None,
                "message": f"AI 优化时发生错误: {e}",
            }

    async def approve_submission(
        self, submission_id: int, reviewed_by: int, signature_forced: bool = False
    ) -> dict[str, Any]:
        """通过投稿

        Args:
            submission_id: 投稿ID
            reviewed_by: 审核人ID
            signature_forced: 是否由管理员强制署名

        Returns:
            {"success": bool, "submission": dict|None, "message": str}
        """
        try:
            success = await self.repo.update_submission_status(
                submission_id=submission_id,
                status="approved",
                reviewed_by=reviewed_by,
                signature_forced=signature_forced if signature_forced else None,
            )
            submission = await self.repo.get_submission(submission_id)
            if success and submission is not None and signature_forced:
                submission["signature_forced"] = True
            return {
                "success": success,
                "submission": submission,
                "message": "投稿已通过" if success else "更新投稿状态失败",
            }
        except Exception as e:
            logger.error(f"通过投稿失败: {type(e).__name__}: {e}", exc_info=True)
            return {"success": False, "submission": None, "message": f"通过投稿时发生错误: {e}"}

    async def reject_submission(self, submission_id: int, reviewed_by: int) -> dict[str, Any]:
        """拒绝投稿"""
        try:
            success = await self.repo.update_submission_status(
                submission_id=submission_id,
                status="rejected",
                reviewed_by=reviewed_by,
            )
            return {
                "success": success,
                "message": "投稿已拒绝" if success else "更新投稿状态失败",
            }
        except Exception as e:
            logger.error(f"拒绝投稿失败: {type(e).__name__}: {e}", exc_info=True)
            return {"success": False, "message": f"拒绝投稿时发生错误: {e}"}


# 全局实例
_submission_service: SubmissionService | None = None


def get_submission_service() -> SubmissionService:
    """获取全局投稿服务实例"""
    global _submission_service
    if _submission_service is None:
        _submission_service = SubmissionService()
    return _submission_service
