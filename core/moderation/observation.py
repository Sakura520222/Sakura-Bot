# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
"""新成员风险画像及观察期的本地结构检测。"""

import hashlib
import math
import re
import unicodedata
from collections import Counter
from datetime import UTC, timedelta

from telethon.tl.types import MessageMediaContact, User, UserProfilePhoto, UserProfilePhotoEmpty

from core.i18n.i18n import t
from core.infrastructure.database.mysql import utc_now_naive

from .matcher import Match, extract_links, normalize

RANDOM_NAME = re.compile(r"[A-Za-z]{6,20} [A-Za-z]{6,20}")
AD_TEXT = re.compile(
    r"免费领取|扫码(?:咨询|进群|领取)|兼职赚钱|高额返利|稳赚|进群领|clickhere|earnmoney|guaranteedprofit"
)
CONTACT_TEXT = re.compile(r"(?:微信|wechat|whatsapp|电报|telegram|商务合作)[:：@][a-z0-9_+\-]{4,}")
PROMOTION = re.compile(r"私聊|加我|进群|推广|优惠|合作|joinus|dmme")
PAID_RECRUITMENT = re.compile(r"代签|招(?:人|募)|兼职|接(?:单|活)")
INCOME_CLAIM = re.compile(
    r"(?:一天|每天)[^。！？!?]{0,12}(?:赚|收益|利润|收入)|日(?:赚|入)[0-9一二三四五六七八九十百千万几]+|(?:保底|至少)[0-9]+(?:w|万|k|千|元)"
)
RECRUITMENT_CONTACT = re.compile(
    r"有?兴趣(?:的)?(?:来|私聊|联系)|有意(?:者|的)?(?:私聊|联系)|私聊我|联系我|加我"
)
MESSAGING_LINK = re.compile(r"(?<![a-z0-9.])(?:t\.me|wa\.me|telegram\.me)/[a-z0-9_+\-/]+", re.I)


def name_cluster(name):
    if not RANDOM_NAME.fullmatch(name):
        return None
    words = name.casefold().split()
    letters = "".join(words)
    entropy = -sum(
        (count / len(letters)) * math.log2(count / len(letters))
        for count in Counter(letters).values()
    )
    if entropy < 3.2 and not any(re.search(r"[^aeiouy]{5,}", word) for word in words):
        return None
    return " ".join("".join("v" if char in "aeiouy" else "c" for char in word) for word in words)


def message_fingerprint(message):
    text = normalize((getattr(message, "message", "") or "")[:4096])
    return hashlib.sha256(text.encode()).hexdigest() if len(text) >= 24 else None


def profile_snapshot(user, config, previous_enforcement):
    first, last = getattr(user, "first_name", "") or "", getattr(user, "last_name", "") or ""
    name = " ".join(f"{first} {last}".split())[:256]
    username = getattr(user, "username", None) or None
    if not username:
        username = next(
            (alias.username for alias in getattr(user, "usernames", None) or [] if alias.active),
            None,
        )
    photo = getattr(user, "photo", None)
    has_photo = (
        True
        if isinstance(photo, UserProfilePhoto)
        else False
        if (isinstance(photo, UserProfilePhotoEmpty) or (isinstance(user, User) and not user.min))
        else None
    )
    cluster = name_cluster(name)
    signals = [
        key
        for key, fired in {
            "no_username": not username,
            "no_photo": has_photo is False,
            "random_name": cluster is not None,
            "prior_enforcement": previous_enforcement,
        }.items()
        if fired
    ]
    weights = config.verification_risk_weights
    return {
        "source": "telegram_join",
        "user_id": user.id,
        "name": name,
        "username": username,
        "has_visible_photo": has_photo,
        "premium": getattr(user, "premium", None),
        "observed_at": utc_now_naive().isoformat(),
        "name_cluster": cluster,
        "signals": signals,
        "score": sum(weights.get(key, 0) for key in signals),
    }


def observation_state(config):
    if not config.observation_minutes:
        return None
    now = utc_now_naive()
    return {
        "expires_at": (now + timedelta(minutes=config.observation_minutes))
        .replace(tzinfo=UTC)
        .timestamp(),
        "messages": {},
        "hits": 0,
    }


def observation_active(config, state, message_id):
    now = utc_now_naive().replace(tzinfo=UTC).timestamp()
    return bool(
        state
        and config.observation_minutes
        and now < state["expires_at"]
        and (
            str(message_id) in state["messages"]
            or len(state["messages"]) < config.observation_messages
        )
    )


def inspect_message(config, state, message, *, shared_pattern=False):
    now = utc_now_naive().replace(tzinfo=UTC).timestamp()
    if not observation_active(config, state, message.id):
        return None, False
    message_id = str(message.id)
    messages = state["messages"]
    text = getattr(message, "message", "") or ""
    normalized = normalize(text[:4096])
    links = extract_links(message)
    links.extend(
        link
        for link in MESSAGING_LINK.findall(unicodedata.normalize("NFKC", text[:4096]))
        if not any(link in existing for existing in links)
    )
    contact = isinstance(
        getattr(message, "media", None), MessageMediaContact
    ) or CONTACT_TEXT.search(normalized)
    buttons = any(
        getattr(row, "buttons", None)
        for row in getattr(getattr(message, "reply_markup", None), "rows", None) or []
    )
    signature = hashlib.sha256(normalized.encode()).hexdigest() if len(normalized) >= 24 else None
    prior = messages.get(message_id)
    # Count genuine new messages once. Edit updates are still checked by content.
    repeat = signature is not None and any(
        other != message_id and value["signature"] == signature for other, value in messages.items()
    )
    recent = sum(
        other != message_id and now - value["at"] <= 15 for other, value in messages.items()
    )
    if (
        links
        or shared_pattern
        or contact
        or buttons
        or AD_TEXT.search(normalized)
        or (
            PAID_RECRUITMENT.search(normalized)
            and INCOME_CLAIM.search(normalized)
            and RECRUITMENT_CONTACT.search(normalized)
        )
        or repeat
        or (recent >= 3 and PROMOTION.search(normalized))
    ):
        state["hits"] += 1
        if config.observation_action == "extend":
            state["expires_at"] = now + config.observation_minutes * 60
        evidence = "\n".join([text, *links])[:2048]
        return Match(
            "observation",
            "new-member-observation",
            t("guard.observation_match"),
            evidence,
            normalize(evidence),
            "delete",
        ), True
    if not normalized and not getattr(message, "media", None):
        return None, False
    if prior is None or prior["signature"] != signature:
        messages[message_id] = {"at": prior["at"] if prior else now, "signature": signature}
        return None, True
    return None, False
