# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Wiki-only sessions and Telegram identity bindings in MySQL."""

import base64
import hashlib
import re
import secrets
import uuid
from datetime import timedelta

from .models import utc

FLOW_SECONDS = 600
ACCESS_SECONDS = 900
REFRESH_SECONDS = 7 * 24 * 3600
TOKEN = re.compile(r"[A-Za-z0-9_-]{43}")


class AccountDenied(ValueError):
    pass


def token_hash(token):
    if not token or not TOKEN.fullmatch(token):
        return None
    return hashlib.sha256(token.encode("ascii")).hexdigest()


class WikiAccounts:
    def __init__(self, store):
        self.store = store

    async def begin(self, browser, path, config, refresh_session=None):
        state, nonce, verifier = (secrets.token_urlsafe(32) for _ in range(3))
        await self.store.query(
            "DELETE FROM wiki_login_flows WHERE expires_at<=UTC_TIMESTAMP(6) LIMIT 100"
        )
        await self.store.query(
            """INSERT INTO wiki_login_flows
            (state_hash,browser_hash,nonce,verifier,redirect_path,refresh_session,client_id,origin,expires_at)
            VALUES (UNHEX(%s),UNHEX(%s),%s,%s,%s,%s,%s,%s,DATE_ADD(UTC_TIMESTAMP(6),INTERVAL %s SECOND))""",
            (
                token_hash(state),
                token_hash(browser),
                nonce,
                verifier,
                path,
                refresh_session,
                config.client_id,
                config.origin,
                FLOW_SECONDS,
            ),
        )
        return state, nonce, verifier

    async def consume(self, state, browser, config):
        state_hash, browser_hash = token_hash(state), token_hash(browser)
        if not state_hash or not browser_hash:
            return None
        async with self.store.transaction() as cur:
            await cur.execute(
                """SELECT nonce,verifier,redirect_path,refresh_session FROM wiki_login_flows
                WHERE state_hash=UNHEX(%s) AND browser_hash=UNHEX(%s) AND client_id=%s
                AND origin=%s AND expires_at>UTC_TIMESTAMP(6) FOR UPDATE""",
                (state_hash, browser_hash, config.client_id, config.origin),
            )
            flow = await cur.fetchone()
            if flow:
                await cur.execute(
                    "DELETE FROM wiki_login_flows WHERE state_hash=UNHEX(%s)", (state_hash,)
                )
            return flow

    async def session(self, token, *, refresh=False):
        key = token_hash(token)
        if not key:
            return None
        column = "refresh" if refresh else "access"
        return await self.store.one(
            f"""SELECT s.id session_id,s.refresh_until,u.id,u.telegram_id,u.display_name,u.username,
            u.avatar IS NOT NULL AS has_avatar,u.show_avatar,u.show_username,u.anonymous_contributions
            FROM wiki_sessions s JOIN wiki_users u ON u.id=s.user_id WHERE s.{column}_hash=UNHEX(%s)
            AND s.{column}_until>UTC_TIMESTAMP(6) AND s.refresh_until>UTC_TIMESTAMP(6) AND u.blocked=FALSE""",
            (key,),
        )

    @staticmethod
    async def lock_user(cur, user_id):
        await cur.execute("SELECT blocked FROM wiki_users WHERE id=%s FOR UPDATE", (user_id,))
        row = await cur.fetchone()
        if not row or row["blocked"]:
            raise AccountDenied("Wiki account is unavailable")

    async def login(self, identity, avatar, refresh_session, logout_token):
        telegram_id, subject, name, username, _ = identity
        async with self.store.transaction() as cur:
            await cur.execute(
                """INSERT INTO wiki_users (id,telegram_id,telegram_subject,display_name,username,profile_at)
                VALUES (%s,%s,UNHEX(%s),%s,%s,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE id=wiki_users.id""",
                (uuid.uuid4().hex, telegram_id, subject.hex(), name, username),
            )
            await cur.execute(
                """SELECT id,telegram_subject,blocked FROM wiki_users
                WHERE telegram_id=%s FOR UPDATE""",
                (telegram_id,),
            )
            user = await cur.fetchone()
            if not user or user["telegram_subject"] != subject or user["blocked"]:
                raise AccountDenied("Wiki account is unavailable")
            logout_hash = token_hash(logout_token)
            if refresh_session:
                await cur.execute(
                    """SELECT id FROM wiki_sessions WHERE id=%s AND user_id=%s AND logout_hash=UNHEX(%s)
                    AND refresh_until>UTC_TIMESTAMP(6) FOR UPDATE""",
                    (refresh_session, user["id"], logout_hash),
                )
                if not await cur.fetchone():
                    raise AccountDenied("Wiki session expired during profile refresh")
            avatar_data, avatar_type = avatar
            await cur.execute(
                """UPDATE wiki_users SET display_name=%s,username=%s,avatar=FROM_BASE64(%s),avatar_type=%s,
                profile_at=UTC_TIMESTAMP(6) WHERE id=%s""",
                (
                    name,
                    username,
                    base64.b64encode(avatar_data).decode("ascii") if avatar_data else None,
                    avatar_type,
                    user["id"],
                ),
            )
            if logout_hash:
                await cur.execute(
                    "DELETE FROM wiki_sessions WHERE logout_hash=UNHEX(%s)", (logout_hash,)
                )
            access, refresh, logout = (secrets.token_urlsafe(32) for _ in range(3))
            await cur.execute(
                """INSERT INTO wiki_sessions (id,user_id,access_hash,refresh_hash,logout_hash,access_until,refresh_until)
                VALUES (%s,%s,UNHEX(%s),UNHEX(%s),UNHEX(%s),DATE_ADD(UTC_TIMESTAMP(6),INTERVAL %s SECOND),
                DATE_ADD(UTC_TIMESTAMP(6),INTERVAL %s SECOND))""",
                (
                    uuid.uuid4().hex,
                    user["id"],
                    token_hash(access),
                    token_hash(refresh),
                    token_hash(logout),
                    ACCESS_SECONDS,
                    REFRESH_SECONDS,
                ),
            )
        return access, refresh, REFRESH_SECONDS, logout

    async def rotate(self, refresh_token):
        session = await self.session(refresh_token, refresh=True)
        if not session:
            return None
        async with self.store.transaction() as cur:
            await self.lock_user(cur, session["id"])
            access, refresh = (secrets.token_urlsafe(32) for _ in range(2))
            await cur.execute(
                """UPDATE wiki_sessions SET access_hash=UNHEX(%s),refresh_hash=UNHEX(%s),
                access_until=LEAST(refresh_until,DATE_ADD(UTC_TIMESTAMP(6),INTERVAL %s SECOND))
                WHERE id=%s AND refresh_hash=UNHEX(%s) AND refresh_until>UTC_TIMESTAMP(6)""",
                (
                    token_hash(access),
                    token_hash(refresh),
                    ACCESS_SECONDS,
                    session["session_id"],
                    token_hash(refresh_token),
                ),
            )
            if not cur.rowcount:
                return None
        seconds = max(1, int((session["refresh_until"] - utc()).total_seconds()))
        return access, refresh, seconds

    async def logout(self, logout_token, browser):
        async with self.store.transaction() as cur:
            await cur.execute(
                "DELETE FROM wiki_sessions WHERE logout_hash=UNHEX(%s)", (token_hash(logout_token),)
            )
            await cur.execute(
                "DELETE FROM wiki_login_flows WHERE browser_hash=UNHEX(%s)", (token_hash(browser),)
            )

    async def privacy(self, session, access, preferences):
        async with self.store.transaction() as cur:
            await self.lock_user(cur, session["id"])
            await cur.execute(
                """SELECT id FROM wiki_sessions WHERE id=%s AND access_hash=UNHEX(%s)
                AND access_until>UTC_TIMESTAMP(6) FOR UPDATE""",
                (session["session_id"], token_hash(access)),
            )
            if not await cur.fetchone():
                raise AccountDenied("Wiki session expired")
            await cur.execute(
                """UPDATE wiki_users SET show_avatar=%s,show_username=%s,anonymous_contributions=%s
                WHERE id=%s""",
                (
                    preferences.show_avatar,
                    preferences.show_username,
                    preferences.anonymous_contributions,
                    session["id"],
                ),
            )

    async def set_blocked(self, user_id, blocked):
        async with self.store.transaction() as cur:
            await cur.execute("SELECT id FROM wiki_users WHERE id=%s FOR UPDATE", (user_id,))
            if not await cur.fetchone():
                return False
            await cur.execute("UPDATE wiki_users SET blocked=%s WHERE id=%s", (blocked, user_id))
            if blocked:
                await cur.execute("DELETE FROM wiki_sessions WHERE user_id=%s", (user_id,))
        return True

    async def cleanup(self):
        await self.store.query(
            "DELETE FROM wiki_sessions WHERE refresh_until<=UTC_TIMESTAMP(6) LIMIT 1000"
        )
        await self.store.query(
            "DELETE FROM wiki_login_flows WHERE expires_at<=UTC_TIMESTAMP(6) LIMIT 1000"
        )
        await self.store.query(
            "DELETE FROM wiki_legacy_login_replays WHERE expires_at<=UTC_TIMESTAMP(6) LIMIT 1000"
        )

    async def consume_legacy(self, signature, now):
        return await self.store.query(
            "INSERT IGNORE INTO wiki_legacy_login_replays VALUES (UNHEX(%s),%s)",
            (signature, utc(now) + timedelta(minutes=10)),
        )
