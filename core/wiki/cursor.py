# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Bounded, query-specific continuation tokens for public reads."""

from base64 import urlsafe_b64decode, urlsafe_b64encode
from datetime import datetime, timedelta
from hashlib import sha256
from json import dumps, loads
from secrets import token_hex

from .models import utc


class InvalidCursor(ValueError):
    pass


def scope_key(*values):
    return sha256(dumps(values, default=str, ensure_ascii=False).encode()).hexdigest()[:20]


def read_cursor(token, scope, *, shuffle=False):
    if token is None:
        return utc(), token_hex(16) if shuffle else "", None
    try:
        key, start, seed, position = loads(urlsafe_b64decode(token + "=" * (-len(token) % 4)))
        start = datetime.fromisoformat(start)
        if key != scope or not utc() - timedelta(hours=2) <= start <= utc():
            raise InvalidCursor
        if not isinstance(seed, str) or len(seed) != (32 if shuffle else 0):
            raise InvalidCursor
        if seed and any(ch not in "0123456789abcdef" for ch in seed):
            raise InvalidCursor
        if not isinstance(position, list) or len(position) != 3:
            raise InvalidCursor
        order, date, identity = position
        if type(order) is not int or not 0 <= order <= 1000:
            raise InvalidCursor
        if not isinstance(identity, str) or len(identity) != 32:
            raise InvalidCursor
        date = datetime.fromisoformat(date)
        if date.tzinfo is not None:
            raise InvalidCursor
        return start, seed, (order, date, identity)
    except (TypeError, ValueError, UnicodeError, OverflowError) as error:
        raise InvalidCursor("游标已失效，请刷新列表。") from error


def write_cursor(scope, start, seed, position):
    value = dumps([scope, start, seed, position], default=str, separators=(",", ":"))
    return urlsafe_b64encode(value.encode()).decode().rstrip("=")
