# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
from types import SimpleNamespace

import pytest
from pydantic import ValidationError
from telethon.tl.types import MessageEntityTextUrl, MessageEntityUrl

from core.moderation.config import GuardConfig, GuardGroup, GuardRule
from core.moderation.matcher import Matcher, extract_links, normalize


@pytest.mark.parametrize(
    "name",
    [
        "主页",
        "主頁",
        "看 我",
        "看\u200b我",
        "頭像",
        "小學生",
        "小\t学\u2060生",
        "ａｂＣ主\ufe0f頁",
        "主\u034f頁",
    ],
)
def test_builtin_variants(name):
    match = Matcher(GuardGroup()).nickname(
        SimpleNamespace(first_name=name, last_name=None, username=None)
    )
    assert match and match.action == "ban" and match.original == name
    assert match.rule_id and match.normalized


def test_combined_display_name_and_username():
    matcher = Matcher(GuardGroup())
    assert (
        matcher.nickname(SimpleNamespace(first_name="主", last_name="頁", username=None)).target
        == "nickname"
    )
    assert (
        matcher.nickname(
            SimpleNamespace(first_name="Alice", last_name=None, username="小學生")
        ).target
        == "username"
    )


def test_traditional_opt_in_and_filter_off():
    user = SimpleNamespace(first_name="臺灣朋友", last_name=None, username=None)
    assert not Matcher(GuardGroup()).nickname(user)
    assert Matcher(GuardGroup(ban_any_traditional=True)).nickname(user).rule_id == "traditional"
    assert not Matcher(GuardGroup(nickname_filter=False, ban_any_traditional=True)).nickname(user)


def test_caption_hidden_link_and_utf16_offsets():
    message = SimpleNamespace(
        message="🙂 harmless text",
        entities=[MessageEntityTextUrl(3, 8, "https://spam.example/%E4%B8%BB%E9%A0%81")],
    )
    config = GuardGroup(message_filter=True, message_keywords=["主页"], message_action="mute")
    match = Matcher(config).message(message)
    assert match.target == "link" and match.action == "mute"
    url = "https://example.com"
    message = SimpleNamespace(message="🙂 " + url, entities=[MessageEntityUrl(3, len(url))])
    assert extract_links(message) == [url]


def test_regex_matches_normalized_text_and_nickname_always_bans():
    rule = GuardRule(id="spam", kind="regex", target="message", value="spam[0-9]+", action="warn")
    match = Matcher(GuardGroup(message_filter=True, rules=[rule])).message(
        SimpleNamespace(message="ＳＰＡＭ\u200b１２３", entities=[])
    )
    assert match.rule_id == "spam" and match.action == "warn"
    rule = GuardRule(id="nick", target="nickname", value="abc", action="delete")
    assert (
        Matcher(GuardGroup(rules=[rule]))
        .nickname(SimpleNamespace(first_name="ABC", last_name=None, username=None))
        .action
        == "ban"
    )


def test_regex_timeout_does_not_block():
    import time

    rules = [
        GuardRule(id=f"expensive_{index}", kind="regex", value="(a+)+$", action="ban")
        for index in range(30)
    ]
    start = time.perf_counter()
    assert (
        Matcher(GuardGroup(message_filter=True, rules=rules)).message(
            SimpleNamespace(message="a" * 16000 + "!", entities=[])
        )
        is None
    )
    assert time.perf_counter() - start < 0.25


def test_slow_regex_cannot_disable_later_keyword_rules():
    rules = [
        GuardRule(id=f"expensive_{index}", kind="regex", value="(a+)+$") for index in range(10)
    ]
    rules.append(GuardRule(id="simple-keyword", value="spam", action="ban"))
    match = Matcher(GuardGroup(message_filter=True, rules=rules)).message(
        SimpleNamespace(message="spam" + "a" * 15000 + "!", entities=[])
    )
    assert match and match.rule_id == "simple-keyword"


@pytest.mark.parametrize(
    "data",
    [
        {"enabled": "false"},
        {"verification_timeout": 0},
        {"reviewer_ids": [-1]},
        {"rules": [{"id": "x", "kind": "regex", "value": "["}]},
        {"unknown": True},
        {"message_keywords": [" "]},
    ],
)
def test_invalid_configuration(data):
    with pytest.raises(ValidationError):
        GuardGroup.model_validate(data)


@pytest.mark.parametrize("key", ["@group", "123", "-0100", "-999999999999999999999"])
def test_group_ids_are_canonical(key):
    with pytest.raises(ValidationError):
        GuardConfig.model_validate({"groups": {key: {}}})


def test_legacy_default_is_disabled():
    assert GuardConfig().groups == {}
    assert not GuardGroup().enabled
    assert not GuardGroup().ban_any_traditional
    assert normalize(" Ａb\u200bC 主頁 ") == "abc主页"


def test_long_caption_audit_evidence_contains_the_actual_match():
    config = GuardGroup(message_filter=True, message_keywords=["主页"])
    matched = Matcher(config).message(
        SimpleNamespace(message="a " * 3000 + "主\u200b頁", entities=[])
    )
    assert "主\u200b頁" in matched.original and "主页" in matched.normalized
    assert len(matched.original) <= 2048 and len(matched.normalized) <= 2048


def test_invisible_only_keyword_does_not_match_every_user():
    rule = GuardRule(id="invisible", value="\u200b", action="ban")
    assert (
        Matcher(GuardGroup(message_filter=True, rules=[rule])).message(
            SimpleNamespace(message="normal", entities=[])
        )
        is None
    )
