"""
测试转发消息过滤器
"""

from unittest.mock import Mock

from core.forwarding.filters import should_forward_original_only


class TestShouldForwardOriginalOnly:
    """测试 should_forward_original_only 过滤器"""

    def test_forward_all_when_disabled(self):
        """当 forward_original_only=False 时，应该转发所有消息"""
        # 创建模拟消息
        message = Mock()
        message.forward = Mock()
        message.fwd_from = None

        # 测试：forward_original_only=False 应该转发所有消息
        assert should_forward_original_only(message, forward_original_only=False) is True

    def test_skip_forwarded_message_when_enabled(self):
        """当 forward_original_only=True 且消息是转发消息时，应该跳过"""
        # 创建模拟转发消息
        message = Mock()
        message.forward = Mock()  # 有 forward 属性，说明是转发消息
        message.fwd_from = None

        # 测试：forward_original_only=True 且消息是转发消息，应该跳过
        assert should_forward_original_only(message, forward_original_only=True) is False

    def test_forward_original_message_when_enabled(self):
        """当 forward_original_only=True 且消息是原创消息时，应该转发"""
        # 创建模拟原创消息
        message = Mock()
        message.forward = None  # 没有 forward 属性，说明是原创消息
        message.fwd_from = None

        # 测试：forward_original_only=True 且消息是原创消息，应该转发
        assert should_forward_original_only(message, forward_original_only=True) is True

    def test_skip_fwd_from_message(self):
        """当消息有 fwd_from 属性时（旧版 Telethon），应该跳过"""
        # 创建模拟转发消息（使用旧版属性）
        message = Mock()
        message.forward = None
        message.fwd_from = Mock()  # 有 fwd_from 属性，说明是转发消息

        # 测试：有 fwd_from 属性的消息应该被跳过
        assert should_forward_original_only(message, forward_original_only=True) is False

    def test_forward_message_without_forward_attribute(self):
        """当消息对象没有 forward 属性时，应该转发"""
        # 创建没有 forward 属性的消息
        message = Mock(spec=[])  # 空的 spec，没有任何属性
        # 删除可能存在的属性
        if hasattr(message, "forward"):
            delattr(message, "forward")
        if hasattr(message, "fwd_from"):
            delattr(message, "fwd_from")

        # 测试：没有 forward 属性的消息应该被转发
        assert should_forward_original_only(message, forward_original_only=True) is True

    def test_default_value_is_false(self):
        """测试默认值 forward_original_only=False"""
        message = Mock()
        message.forward = Mock()
        message.fwd_from = None

        # 测试：不传参数时，默认为 False，应该转发所有消息
        assert should_forward_original_only(message) is True
