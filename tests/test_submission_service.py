# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
测试投稿服务审核参数传递。
"""

from unittest.mock import AsyncMock, MagicMock

import pytest

from core.services.submission_service import SubmissionService


@pytest.fixture
def service():
    """创建投稿服务实例。"""
    submission_service = SubmissionService()
    submission_service.repo = MagicMock()
    return submission_service


@pytest.mark.asyncio
async def test_approve_submission_forced_signature_updates_repo(service):
    """测试署名通过会写入强制署名标记。"""
    service.repo.update_submission_status = AsyncMock(return_value=True)
    service.repo.get_submission = AsyncMock(
        return_value={"id": 123, "title": "测试标题", "signature_forced": False}
    )

    result = await service.approve_submission(123, reviewed_by=456, signature_forced=True)

    service.repo.update_submission_status.assert_awaited_once_with(
        submission_id=123,
        status="approved",
        reviewed_by=456,
        signature_forced=True,
    )
    assert result["success"] is True
    assert result["submission"]["signature_forced"] is True


@pytest.mark.asyncio
async def test_approve_submission_normal_does_not_reset_signature(service):
    """测试普通通过不改写强制署名标记。"""
    service.repo.update_submission_status = AsyncMock(return_value=True)
    service.repo.get_submission = AsyncMock(return_value={"id": 123, "title": "测试标题"})

    result = await service.approve_submission(123, reviewed_by=456)

    service.repo.update_submission_status.assert_awaited_once_with(
        submission_id=123,
        status="approved",
        reviewed_by=456,
        signature_forced=None,
    )
    assert result["success"] is True
