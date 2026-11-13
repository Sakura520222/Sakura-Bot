# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
"""按钮断言覆盖 Telethon 原始字段与 type 包装字段。"""

from types import SimpleNamespace

import pytest


@pytest.mark.parametrize("wrapped", [False, True])
def test_inline_button_helpers_read_supported_shapes(
    wrapped, inline_button_payload, inline_button_url
):
    callback = SimpleNamespace(data=b"sg:v:session:b")
    profile = SimpleNamespace(url="tg://user?id=200")
    if wrapped:
        callback = SimpleNamespace(type=callback)
        profile = SimpleNamespace(type=profile)
    assert inline_button_payload(callback) == b"sg:v:session:b"
    assert inline_button_url(profile) == "tg://user?id=200"


@pytest.mark.parametrize("value", [None, 123, b"tg://user?id=200"])
def test_inline_button_url_rejects_invalid_values(value, inline_button_url):
    with pytest.raises(AssertionError):
        inline_button_url(SimpleNamespace(url=value))
