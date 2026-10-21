# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""MySQL 持久化、跨实例用户锁与可恢复的操作意图。错误向调用者传播。"""

import asyncio
import json
from contextlib import asynccontextmanager
from datetime import timedelta
from pathlib import Path

import aiomysql

from core.infrastructure.database.mysql import utc_now_naive

ACTIVE_VERIFICATIONS = (
    "preparing",
    "waiting",
    "passing",
    "kicking",
    "cancelling",
    "admin_pending",
    "held",
)


def encode(value):
    return json.dumps(value, ensure_ascii=False)


def decode(row):
    if row and isinstance(row.get("payload"), str):
        row["payload"] = json.loads(row["payload"])
    return row


class GuardStore:
    """同一连接上的工作单元；外部副作用前的意图单独提交。"""

    def __init__(self, connection):
        self.connection = connection

    async def execute(self, sql, params=()):
        async with self.connection.cursor(aiomysql.DictCursor) as cursor:
            await cursor.execute(sql, params)
            return cursor.rowcount

    async def all(self, sql, params=()):
        async with self.connection.cursor(aiomysql.DictCursor) as cursor:
            await cursor.execute(sql, params)
            return [decode(row) for row in await cursor.fetchall()]

    async def one(self, sql, params=()):
        rows = await self.all(sql, params)
        return rows[0] if rows else None

    async def case(self, case_id):
        return await self.one("SELECT * FROM moderation_cases WHERE id=%s", (case_id,))

    async def case_for_event(self, event_key):
        return await self.one("SELECT * FROM moderation_cases WHERE event_key=%s", (event_key,))

    async def later_mute(self, case):
        rows = await self.all(
            """SELECT * FROM moderation_cases WHERE chat_id=%s AND user_id=%s AND id<>%s AND action='mute'
            AND status IN ('executing','done','reviewing_allow','reviewing_unban','reviewing_ban','reviewing_blacklist')
            ORDER BY created_at DESC LIMIT 50""",
            (case["chat_id"], case["user_id"], case["id"]),
        )
        start = case["payload"].get("restriction_started_at") or case["created_at"].isoformat()
        for row in rows:
            other = row["payload"].get("restriction_started_at") or row["created_at"].isoformat()
            if other > start or (
                not case["payload"].get("restriction_started_at") and other == start
            ):
                return row
        return None

    async def active_ban(self, chat_id, user_id, *, exclude_case_id=None):
        return await self.one(
            """SELECT * FROM moderation_cases WHERE chat_id=%s AND user_id=%s AND id<>%s
            AND (action='ban' OR JSON_UNQUOTE(JSON_EXTRACT(payload,'$.review_ban_confirmed'))='true' OR status IN ('reviewing_ban','reviewing_blacklist'))
            AND status IN ('executing','pending','reviewing_allow','reviewing_blacklist','reviewing_unban','reviewing_ban')
            ORDER BY created_at DESC LIMIT 1""",
            (chat_id, user_id, exclude_case_id or ""),
        )

    async def create_case(self, case):
        await self.execute(
            """INSERT INTO moderation_cases
            (id,event_key,chat_id,user_id,action,status,payload,reviewer_id,reviewer_actor,reviewed_at,created_at,updated_at)
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)""",
            (
                case["id"],
                case["event_key"],
                case["chat_id"],
                case["user_id"],
                case["action"],
                case["status"],
                encode(case["payload"]),
                case.get("reviewer_id"),
                case.get("reviewer_actor"),
                case.get("reviewed_at"),
                utc_now_naive(),
                utc_now_naive(),
            ),
        )

    async def update_case(self, case_id, status, *, error=None, payload=None):
        if payload is None:
            await self.execute(
                "UPDATE moderation_cases SET status=%s,error=%s,updated_at=%s WHERE id=%s",
                (status, error, utc_now_naive(), case_id),
            )
        else:
            await self.execute(
                "UPDATE moderation_cases SET status=%s,error=%s,payload=%s,updated_at=%s WHERE id=%s",
                (status, error, encode(payload), utc_now_naive(), case_id),
            )

    async def blacklist(self, user_id):
        return await self.one("SELECT * FROM moderation_blacklist WHERE user_id=%s", (user_id,))

    async def touch_blacklist(self, user_id):
        await self.execute(
            "UPDATE moderation_blacklist SET last_seen_at=%s WHERE user_id=%s",
            (utc_now_naive(), user_id),
        )

    async def whitelisted(self, chat_id, user_id):
        return bool(
            await self.one(
                "SELECT user_id FROM moderation_whitelist WHERE chat_id=%s AND user_id=%s",
                (chat_id, user_id),
            )
        )

    async def set_whitelist(
        self, chat_id, user_id, reviewer_id, enabled=True, *, reviewer_actor=None
    ):
        if enabled:
            await self.execute(
                "INSERT INTO moderation_whitelist (chat_id,user_id,reviewer_id,reviewer_actor,created_at) VALUES (%s,%s,%s,%s,%s) ON DUPLICATE KEY UPDATE reviewer_id=VALUES(reviewer_id),reviewer_actor=VALUES(reviewer_actor)",
                (
                    chat_id,
                    user_id,
                    reviewer_id,
                    reviewer_actor or f"telegram:{reviewer_id}",
                    utc_now_naive(),
                ),
            )
        else:
            await self.execute(
                "DELETE FROM moderation_whitelist WHERE chat_id=%s AND user_id=%s",
                (chat_id, user_id),
            )

    async def claim_review(self, case_id, action, reviewer_id, *, reviewer_actor=None):
        return bool(
            await self.execute(
                """UPDATE moderation_cases SET status=%s,reviewer_id=%s,reviewer_actor=%s,reviewed_at=%s,error=NULL,updated_at=%s
            WHERE id=%s AND ((status='pending' AND action IN ('ban','mute','warn','report')) OR (status='done' AND action IN ('mute','warn')) OR (%s='kick' AND action='report' AND status IN ('allowed','blacklisted','unbanned','unmuted','ignored')))""",
                (
                    f"reviewing_{action}",
                    reviewer_id,
                    reviewer_actor or f"telegram:{reviewer_id}",
                    utc_now_naive(),
                    utc_now_naive(),
                    case_id,
                    action,
                ),
            )
        )

    async def finish_review(self, case, action):
        # Telegram 成功之后，名单和终态在同一个数据库事务提交。
        payload = dict(case["payload"])
        payload["review_history"] = [
            *payload.get("review_history", []),
            {
                "action": action,
                "actor": case.get("reviewer_actor"),
                "at": utc_now_naive().isoformat(),
            },
        ]
        await self.connection.begin()
        try:
            if action == "allow":
                if await self.blacklist(case["user_id"]):
                    raise ValueError("global_blacklist_conflict")
                await self.set_whitelist(
                    case["chat_id"],
                    case["user_id"],
                    case["reviewer_id"],
                    reviewer_actor=case.get("reviewer_actor"),
                )
            elif action == "blacklist":
                await self.execute(
                    """INSERT INTO moderation_blacklist
                    (user_id,source_chat_id,case_id,payload,reviewer_id,reviewer_actor,first_seen_at,last_seen_at)
                    VALUES (%s,%s,%s,%s,%s,%s,%s,%s) ON DUPLICATE KEY UPDATE
                    last_seen_at=VALUES(last_seen_at),reviewer_id=VALUES(reviewer_id),reviewer_actor=VALUES(reviewer_actor),
                    source_chat_id=VALUES(source_chat_id),case_id=VALUES(case_id),payload=VALUES(payload)""",
                    (
                        case["user_id"],
                        case["chat_id"],
                        case["id"],
                        encode(payload),
                        case["reviewer_id"],
                        case.get("reviewer_actor") or f"telegram:{case['reviewer_id']}",
                        case["created_at"],
                        utc_now_naive(),
                    ),
                )
                await self.set_whitelist(
                    case["chat_id"], case["user_id"], case["reviewer_id"], False
                )
            elif action not in {"unban", "ban", "kick"}:
                raise ValueError("invalid review action")
            unban_status = (
                "unbanned"
                if case["action"] == "ban" or payload.get("review_ban_confirmed")
                else "unmuted"
                if case["action"] == "mute"
                else "ignored"
            )
            if action == "ban":
                payload["review_ban_confirmed"] = True
                payload["review_generation"] = payload.get("review_generation", 0) + 1
            await self.update_case(
                case["id"],
                {
                    "allow": "allowed",
                    "blacklist": "blacklisted",
                    "unban": unban_status,
                    "ban": "pending",
                    "kick": "kicked",
                }[action],
                payload=payload,
            )
            await self.connection.commit()
        except BaseException:
            await self.connection.rollback()
            raise

    async def verification(self, chat_id, user_id):
        return await self.one(
            "SELECT * FROM moderation_verification_sessions WHERE chat_id=%s AND user_id=%s",
            (chat_id, user_id),
        )

    async def verification_by_id(self, session_id):
        return await self.one(
            "SELECT * FROM moderation_verification_sessions WHERE id=%s", (session_id,)
        )

    async def save_verification(self, session):
        await self.execute(
            """INSERT INTO moderation_verification_sessions
            (id,chat_id,user_id,status,payload,message_id,attempts,expires_at,error,updated_at)
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s) ON DUPLICATE KEY UPDATE
            id=VALUES(id),status=VALUES(status),payload=VALUES(payload),message_id=VALUES(message_id),
            attempts=VALUES(attempts),expires_at=VALUES(expires_at),error=VALUES(error),updated_at=VALUES(updated_at)""",
            (
                session["id"],
                session["chat_id"],
                session["user_id"],
                session["status"],
                encode(session["payload"]),
                session.get("message_id"),
                session.get("attempts", 0),
                session["expires_at"],
                session.get("error"),
                utc_now_naive(),
            ),
        )

    async def notices(self, case_id):
        return await self.all("SELECT * FROM moderation_notifications WHERE case_id=%s", (case_id,))

    async def notice(self, case_id, target_id, message_id, status, error=None, attempts=0):
        protected = "(synced_status='removed' OR (synced_status='deleting' AND COALESCE(VALUES(synced_status),'') NOT IN ('deleting','removed')))"
        await self.execute(
            f"""INSERT INTO moderation_notifications
            (case_id,target_id,message_id,synced_status,error,attempts,retry_at) VALUES (%s,%s,%s,%s,%s,%s,%s)
            ON DUPLICATE KEY UPDATE message_id=IF({protected},message_id,VALUES(message_id)),
            error=IF({protected},error,VALUES(error)),attempts=IF({protected},attempts,VALUES(attempts)),
            retry_at=IF({protected},retry_at,VALUES(retry_at)),
            synced_status=IF({protected},synced_status,VALUES(synced_status))""",
            (
                case_id,
                target_id,
                message_id,
                status,
                error,
                attempts,
                utc_now_naive() + timedelta(seconds=min(3600, 30 * 2 ** min(attempts, 7))),
            ),
        )


class GuardRepository:
    def __init__(self, pool):
        self.pool = pool

    async def initialize(self):
        schema = Path(__file__).with_name("schema.sql").read_text(encoding="utf-8")
        async with self.pool.acquire() as connection:
            store = GuardStore(connection)
            for statement in schema.split(";"):
                if statement.strip():
                    await store.execute(statement)

    async def get_case(self, case_id):
        async with self.pool.acquire() as connection:
            return await GuardStore(connection).case(case_id)

    async def get_verification(self, session_id):
        async with self.pool.acquire() as connection:
            return await GuardStore(connection).verification_by_id(session_id)

    async def cleaned_chat_history(self, bot_id):
        async with self.pool.acquire() as connection:
            rows = await GuardStore(connection).all(
                "SELECT chat_id FROM moderation_cleaned_chat_history WHERE bot_id=%s", (bot_id,)
            )
            return {row["chat_id"] for row in rows}

    async def mark_chat_history_cleaned(self, bot_id, chat_id, actor):
        async with self.pool.acquire() as connection:
            await GuardStore(connection).execute(
                """INSERT INTO moderation_cleaned_chat_history (bot_id,chat_id,actor,cleaned_at)
                VALUES (%s,%s,%s,%s) ON DUPLICATE KEY UPDATE actor=VALUES(actor),cleaned_at=VALUES(cleaned_at)""",
                (bot_id, chat_id, str(actor), utc_now_naive()),
            )

    async def restore_chat_history(self, bot_id, chat_ids):
        if not chat_ids:
            return
        async with self.pool.acquire() as connection:
            store = GuardStore(connection)
            for chat_id in chat_ids:
                await store.execute(
                    "DELETE FROM moderation_cleaned_chat_history WHERE bot_id=%s AND chat_id=%s",
                    (bot_id, chat_id),
                )

    @asynccontextmanager
    async def lock(self, user_id):
        # 连接级锁跨事务提交仍有效；进程/连接断开即释放，无过期租约双写。
        # 全局按用户锁定，串行化跨群拉黑与本群解封冲突。
        async with self.pool.acquire() as connection:
            store = GuardStore(connection)
            name = f"sakura_guard:user:{int(user_id)}"
            try:
                acquired = await asyncio.wait_for(
                    store.one("SELECT GET_LOCK(%s, 5) AS acquired", (name,)), timeout=6
                )
            except BaseException:
                # 取消时服务端可能刚拿到锁，关闭连接保证不会把锁带回连接池。
                connection.close()
                raise
            if not acquired or acquired["acquired"] != 1:
                raise TimeoutError("Sakura Guard user lock unavailable")
            try:
                yield store
            finally:
                try:
                    await connection.rollback()
                    released = await asyncio.wait_for(
                        store.one("SELECT RELEASE_LOCK(%s) AS released", (name,)), timeout=5
                    )
                    if not released or released["released"] != 1:
                        connection.close()
                except BaseException:
                    connection.close()
                    raise

    async def query(self, table, *, chat_id=None, user_id=None, status=None, limit=50, offset=0):
        tables = {
            "cases": "moderation_cases",
            "whitelist": "moderation_whitelist",
            "blacklist": "moderation_blacklist",
            "verifications": "moderation_verification_sessions",
            "notifications": "moderation_notifications",
        }
        name = tables[table]
        notification_query = table == "notifications"
        source = (
            f"{name} n JOIN moderation_cases c ON c.id=n.case_id" if notification_query else name
        )
        clauses, values = [], []
        if chat_id is not None:
            clauses.append(
                (
                    "c.chat_id"
                    if notification_query
                    else "source_chat_id"
                    if table == "blacklist"
                    else "chat_id"
                )
                + "=%s"
            )
            values.append(chat_id)
        if user_id is not None:
            clauses.append(("c.user_id" if notification_query else "user_id") + "=%s")
            values.append(user_id)
        if status and table in {"cases", "verifications", "blacklist", "notifications"}:
            column = (
                "n.synced_status"
                if notification_query and status in {"deleting", "removed"}
                else "c.status"
                if notification_query
                else "status"
            )
            clauses.append(column + "=%s")
            values.append(status)
        where = " WHERE " + " AND ".join(clauses) if clauses else ""
        order = (
            "n.retry_at"
            if table == "notifications"
            else "last_seen_at"
            if table == "blacklist"
            else "created_at"
            if table in {"cases", "whitelist"}
            else "updated_at"
        )
        async with self.pool.acquire() as connection:
            store = GuardStore(connection)
            total = await store.one(f"SELECT COUNT(*) AS total FROM {source}{where}", values)
            columns = "n.*, c.chat_id, c.user_id" if notification_query else "*"
            rows = await store.all(
                f"SELECT {columns} FROM {source}{where} ORDER BY {order} DESC LIMIT %s OFFSET %s",
                (*values, min(max(limit, 1), 200), max(offset, 0)),
            )
        return {"rows": rows, "total": total["total"]}

    async def work(self, *, control_policy=None):
        async with self.pool.acquire() as connection:
            store = GuardStore(connection)
            cases = await store.all(
                """SELECT DISTINCT c.* FROM moderation_cases c
                LEFT JOIN moderation_notifications n ON n.case_id=c.id
                WHERE NOT (c.action='verify_start' AND JSON_EXTRACT(c.payload,'$.notification_replaced_by') IS NOT NULL)
                AND (c.status IN ('executing','reviewing_allow','reviewing_blacklist','reviewing_unban','reviewing_ban','reviewing_kick')
                OR n.case_id IS NULL OR ((n.synced_status IS NULL OR n.synced_status NOT IN ('deleting','removed')) AND (n.synced_status IS NULL OR n.synced_status<>c.status OR n.error IS NOT NULL) AND n.retry_at<=%s)
                OR (JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.notification_cleanup_pending'))='true' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.notification_cleanup_retry_at')),'1970-01-01T00:00:00')<=%s)
                OR (c.action='report' AND c.status='pending' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.report_controls_version')),'0')<>'2')
                OR (((c.action IN ('ban','mute','warn','report') AND (c.status='pending' OR (c.status='done' AND c.action IN ('mute','warn')) OR (c.action='report' AND c.status IN ('allowed','blacklisted','unbanned','unmuted','ignored')))) OR (c.action='verify_skip' AND c.status='done') OR (JSON_EXTRACT(c.payload,'$.verification_failure') IS NOT NULL AND JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.verification_result'))='held') OR (JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.verification_state')) IN ('waiting','passing','cancelling','held','passed','admin_skipped'))) AND (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.controls_version')),'0')<>'2' OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.kick_button_enabled')),'true')<>COALESCE(JSON_UNQUOTE(JSON_EXTRACT(%s,CONCAT('$."',c.chat_id,'"'))),'true')))
                OR JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.report_notice_dirty'))='true'
                OR JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.ban_message_cleanup_dirty'))='true'
                OR (JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.ban_message_cleanup.status'))='pending' AND COALESCE(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.ban_message_cleanup.retry_at')),'null'),'1970-01-01T00:00:00')<=%s)
                OR (c.action='report' AND JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.report_message_deletion.status'))='pending' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.report_message_deletion.retry_at')),'1970-01-01T00:00:00')<=%s))
                ORDER BY c.updated_at ASC LIMIT 50""",
                (
                    utc_now_naive(),
                    utc_now_naive().isoformat(timespec="seconds"),
                    encode(control_policy or {}),
                    utc_now_naive().isoformat(),
                    utc_now_naive().isoformat(),
                ),
            )
            sessions = await store.all("""SELECT * FROM moderation_verification_sessions
                WHERE status IN ('preparing','waiting','passing','kicking','cancelling','admin_pending','held')
                OR (message_id IS NOT NULL AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.cleanup_pending')),'true')='true')
                OR (status IN ('passed','cancelled','left') AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.lifecycle'))='true' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.lifecycle_recorded')),'false')='false')
                ORDER BY CASE WHEN status IN ('preparing','waiting','passing','kicking','cancelling','admin_pending','held') THEN 0 ELSE 1 END, expires_at ASC,updated_at ASC LIMIT 100""")
        return cases, sessions

    async def cleanup(self, days):
        cutoff = utc_now_naive() - timedelta(days=days)
        async with self.pool.acquire() as connection:
            store = GuardStore(connection)
            # 永久名单、待审核和待恢复操作不随审计留存期限删除。
            await store.execute(
                "DELETE FROM moderation_cases WHERE created_at<%s AND status IN ('done','allowed','unbanned','unmuted','ignored','blacklisted','execution_failed','cancelled','manual','kicked') AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.notification_cleanup_pending')),'false')<>'true' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.report_message_deletion.status')),'')<>'pending' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.report_notice_dirty')),'false')<>'true' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.ban_message_cleanup.status')),'')<>'pending' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.ban_message_cleanup_dirty')),'false')<>'true' AND NOT EXISTS (SELECT 1 FROM moderation_appeals a WHERE a.case_id=moderation_cases.id) LIMIT 500",
                (cutoff,),
            )
            await store.execute(
                "DELETE FROM moderation_verification_sessions WHERE updated_at<%s AND status NOT IN ('preparing','waiting','passing','kicking','cancelling','admin_pending','admin_kick_pending','held') AND (message_id IS NULL OR JSON_UNQUOTE(JSON_EXTRACT(payload,'$.cleanup_pending'))='false') LIMIT 500",
                (cutoff,),
            )
            await store.execute(
                """DELETE FROM moderation_deleted_cleanup_jobs WHERE updated_at<%s
                AND active_key IS NULL AND status IN ('completed','partial','cancelled') LIMIT 100""",
                (cutoff,),
            )
