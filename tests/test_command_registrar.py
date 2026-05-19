# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
测试命令注册器回调过滤逻辑。
"""

from types import SimpleNamespace

from core.initializers.command_registrar import _is_submission_review_callback


def test_submission_review_callback_accepts_signed_approve():
    """测试投稿审核过滤器放行署名通过回调。"""
    event = SimpleNamespace(data=b"submission_signapprove_123")

    assert _is_submission_review_callback(event) is True


def test_submission_review_callback_accepts_existing_actions():
    """测试投稿审核过滤器放行既有审核回调。"""
    accepted_payloads = (
        b"submission_aiopt_123",
        b"submission_approve_123",
        b"submission_reject_123",
        b"submission_restore_123",
    )

    for payload in accepted_payloads:
        event = SimpleNamespace(data=payload)
        assert _is_submission_review_callback(event) is True


def test_submission_review_callback_rejects_unrelated_data():
    """测试投稿审核过滤器拒绝无关回调。"""
    rejected_payloads = (
        b"submission_unknown_123",
        b"mainbot:menu:basic",
        "submission_signapprove_123",
        None,
    )

    for payload in rejected_payloads:
        event = SimpleNamespace(data=payload)
        assert _is_submission_review_callback(event) is False
