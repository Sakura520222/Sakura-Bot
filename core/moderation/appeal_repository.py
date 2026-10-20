# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""独立申诉记录、输入草稿、追加审计和助手 Bot 投递队列。"""

import hashlib
import uuid
from contextlib import asynccontextmanager
from datetime import timedelta

from core.infrastructure.database.mysql import utc_now_naive

from .repository import GuardStore, encode

TERMINAL = {"approved", "rejected", "withdrawn", "superseded"}
REVIEW_ACTIONS = {"approve", "allow", "global", "reject", "more"}
READ_ACTIONS = {"evidence", "audit"}


def active_key(chat_id, user_id):
    return hashlib.sha256(f"{chat_id}:{user_id}".encode()).hexdigest()


class AppealStore:
    def __init__(self, guard):
        self.guard = guard

    @asynccontextmanager
    async def atomic(self):
        await self.guard.connection.begin()
        try:
            yield
            await self.guard.connection.commit()
        except BaseException:
            await self.guard.connection.rollback()
            raise

    async def get(self, appeal_id):
        return await self.guard.one("SELECT * FROM moderation_appeals WHERE id=%s", (appeal_id,))

    async def active(self, chat_id, user_id):
        return await self.guard.one(
            "SELECT * FROM moderation_appeals WHERE active_key=%s", (active_key(chat_id, user_id),)
        )

    async def input(self, actor_id):
        return await self.guard.one(
            "SELECT * FROM moderation_appeal_inputs WHERE actor_id=%s", (actor_id,)
        )

    async def save_input(self, actor_id, appeal_id, mode, payload, *, token=None):
        token = token or uuid.uuid4().hex
        await self.guard.execute(
            """INSERT INTO moderation_appeal_inputs
            (actor_id,token,appeal_id,mode,payload,updated_at) VALUES (%s,%s,%s,%s,%s,%s)
            ON DUPLICATE KEY UPDATE token=VALUES(token),appeal_id=VALUES(appeal_id),
            mode=VALUES(mode),payload=VALUES(payload),updated_at=VALUES(updated_at)""",
            (actor_id, token, appeal_id, mode, encode(payload), utc_now_naive()),
        )
        return await self.input(actor_id)

    async def clear_input(self, actor_id, token):
        await self.guard.execute(
            "DELETE FROM moderation_appeal_inputs WHERE actor_id=%s AND token=%s", (actor_id, token)
        )

    async def event(self, appeal, actor, action, old_status, payload=None):
        await self.guard.execute(
            """INSERT INTO moderation_appeal_events
            (appeal_id,actor,action,from_status,to_status,payload,created_at)
            VALUES (%s,%s,%s,%s,%s,%s,%s)""",
            (
                appeal["id"],
                str(actor),
                action,
                old_status,
                appeal["status"],
                encode(payload or {}),
                utc_now_naive(),
            ),
        )

    async def create(self, case, snapshot):
        appeal = {
            "id": uuid.uuid4().hex,
            "case_id": case["id"],
            "chat_id": case["chat_id"],
            "user_id": case["user_id"],
            "status": "draft",
            "payload": snapshot,
        }
        async with self.atomic():
            await self.guard.execute(
                """INSERT INTO moderation_appeals
                (id,case_id,chat_id,user_id,active_key,status,published_revision,payload,retry_at,created_at,updated_at)
                VALUES (%s,%s,%s,%s,%s,'draft',1,%s,%s,%s,%s)""",
                (
                    appeal["id"],
                    case["id"],
                    case["chat_id"],
                    case["user_id"],
                    active_key(case["chat_id"], case["user_id"]),
                    encode(snapshot),
                    utc_now_naive(),
                    utc_now_naive(),
                    utc_now_naive(),
                ),
            )
            await self.event(appeal, f"telegram:{case['user_id']}", "created", None)
            await self.save_input(case["user_id"], appeal["id"], "draft", {"text": "", "files": []})
        return await self.get(appeal["id"])

    async def _transition(
        self, appeal, status, actor, action, *, payload=None, error=None, detail=None
    ):
        old_status = appeal["status"]
        await self.guard.execute(
            """UPDATE moderation_appeals SET status=%s,revision=revision+1,active_key=%s,
            payload=%s,error=%s,retry_at=%s,updated_at=%s WHERE id=%s""",
            (
                status,
                None if status in TERMINAL else active_key(appeal["chat_id"], appeal["user_id"]),
                encode(payload if payload is not None else appeal["payload"]),
                error,
                utc_now_naive(),
                utc_now_naive(),
                appeal["id"],
            ),
        )
        result = await self.get(appeal["id"])
        await self.event(result, actor, action, old_status, detail)
        return result

    async def transition(self, appeal, status, actor, action, **kwargs):
        async with self.atomic():
            return await self._transition(appeal, status, actor, action, **kwargs)

    async def rejected_recently(self, case):
        return await self.guard.one(
            """SELECT id FROM moderation_appeals WHERE case_id=%s AND status='rejected'
            AND updated_at>%s LIMIT 1""",
            (case["id"], utc_now_naive() - timedelta(hours=24)),
        )

    async def submission_count(self, user_id):
        row = await self.guard.one(
            """SELECT COUNT(*) AS total FROM moderation_appeal_events e
            JOIN moderation_appeals a ON a.id=e.appeal_id WHERE a.user_id=%s
            AND e.action='submitted' AND e.created_at>%s""",
            (user_id, utc_now_naive() - timedelta(days=1)),
        )
        return row["total"]

    async def notices(self, appeal_id):
        return await self.guard.all(
            "SELECT * FROM moderation_appeal_notifications WHERE appeal_id=%s", (appeal_id,)
        )

    async def save_notice(self, appeal, target_id, message_id, kind):
        await self.guard.execute(
            """INSERT INTO moderation_appeal_notifications
            (appeal_id,target_id,kind,message_id,synced_revision,retry_at)
            VALUES (%s,%s,%s,%s,%s,%s) ON DUPLICATE KEY UPDATE
            message_id=VALUES(message_id),synced_revision=VALUES(synced_revision),
            error=NULL,attempts=0,retry_at=VALUES(retry_at)""",
            (appeal["id"], target_id, kind, message_id, appeal["revision"], utc_now_naive()),
        )

    async def bind_user_card(self, appeal, message_id):
        # 翻页复用同一消息时，旧申诉不再持有该消息，避免异步更新覆盖当前页面。
        await self.guard.execute(
            """UPDATE moderation_appeal_notifications n JOIN moderation_appeals a ON a.id=n.appeal_id
            SET n.message_id=NULL,n.synced_revision=a.revision
            WHERE n.target_id=%s AND n.kind='user' AND n.message_id=%s AND n.appeal_id<>%s""",
            (appeal["user_id"], message_id, appeal["id"]),
        )
        await self.save_notice(appeal, appeal["user_id"], message_id, "user")

    async def input_message(self, actor_id, token, message_id):
        await self.guard.execute(
            """UPDATE moderation_appeal_inputs SET payload=JSON_SET(payload,'$.ui_message_id',%s)
            WHERE actor_id=%s AND token=%s""",
            (message_id, actor_id, token),
        )

    async def publish(self, appeal, reviewers):
        targets = list(dict.fromkeys([appeal["user_id"], *reviewers]))
        async with self.atomic():
            for notice in await self.notices(appeal["id"]):
                if notice["target_id"] not in targets:
                    await self.guard.execute(
                        "UPDATE moderation_appeal_notifications SET kind='retired' WHERE appeal_id=%s AND target_id=%s",
                        (appeal["id"], notice["target_id"]),
                    )
            for target in targets:
                await self.guard.execute(
                    """INSERT INTO moderation_appeal_notifications
                    (appeal_id,target_id,kind,retry_at) VALUES (%s,%s,%s,%s)
                    ON DUPLICATE KEY UPDATE kind=VALUES(kind)""",
                    (
                        appeal["id"],
                        target,
                        "user" if target == appeal["user_id"] else "reviewer",
                        utc_now_naive(),
                    ),
                )
            await self.guard.execute(
                "UPDATE moderation_appeals SET published_revision=revision WHERE id=%s",
                (appeal["id"],),
            )

    async def queue_request(
        self, appeal_id, actor_id, action, revision, origin, reason, request_id
    ):
        await self.guard.execute(
            """INSERT IGNORE INTO moderation_appeal_requests
            (id,appeal_id,actor_id,action,revision,origin_chat_id,origin_message_id,payload,retry_at,created_at)
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)""",
            (
                request_id,
                appeal_id,
                actor_id,
                action,
                revision,
                *origin,
                encode({"reason": reason}),
                utc_now_naive(),
                utc_now_naive(),
            ),
        )

    async def finish_request(self, request, *, error=None, payload=None):
        await self.guard.execute(
            "UPDATE moderation_appeal_requests SET status=%s,error=%s,payload=%s WHERE id=%s",
            ("failed" if error else "done", error, encode(payload or {}), request["id"]),
        )

    async def events(self, appeal_id):
        return await self.guard.all(
            "SELECT * FROM moderation_appeal_events WHERE appeal_id=%s ORDER BY id DESC LIMIT 30",
            (appeal_id,),
        )

    async def execution_error(self, appeal, error):
        # 同一个错误不重复填满审计；首次错误更新卡片，后续只退避。
        async with self.atomic():
            await self.guard.execute(
                "UPDATE moderation_appeals SET error=%s,retry_at=%s WHERE id=%s",
                (error, utc_now_naive() + timedelta(seconds=30), appeal["id"]),
            )
            if error != appeal.get("error"):
                await self.guard.execute(
                    "UPDATE moderation_appeals SET revision=revision+1 WHERE id=%s", (appeal["id"],)
                )
                await self.event(
                    appeal, "system", "execution_failed", appeal["status"], {"error": error}
                )

    async def finish_approval(self, appeal, case):
        decision = appeal["payload"]["decision"]
        actor = f"telegram:{decision['actor_id']}"
        payload = dict(case["payload"])
        payload["review_history"] = [
            *payload.get("review_history", []),
            {
                "action": decision["action"],
                "actor": actor,
                "appeal_id": appeal["id"],
                "reason": decision["reason"],
                "at": utc_now_naive().isoformat(),
            },
        ]
        async with self.atomic():
            if decision["action"] == "global":
                await self.guard.execute(
                    "DELETE FROM moderation_blacklist WHERE user_id=%s AND case_id=%s",
                    (appeal["user_id"], decision.get("blacklist_case_id") or ""),
                )
            if decision["action"] == "allow":
                await self.guard.set_whitelist(
                    appeal["chat_id"], appeal["user_id"], decision["actor_id"], reviewer_actor=actor
                )
            await self.guard.execute(
                """UPDATE moderation_cases SET status=%s,payload=%s,reviewer_id=%s,reviewer_actor=%s,
                reviewed_at=%s,error=NULL,updated_at=%s WHERE id=%s""",
                (
                    "allowed" if decision["action"] == "allow" else "unbanned",
                    encode(payload),
                    decision["actor_id"],
                    actor,
                    utc_now_naive(),
                    utc_now_naive(),
                    case["id"],
                ),
            )
            updated = {**appeal["payload"], "blacklisted": False}
            return await self._transition(
                appeal, "approved", actor, "approved", payload=updated, detail=decision
            )


class AppealRepository:
    def __init__(self, guard_repository):
        self.guard = guard_repository

    @asynccontextmanager
    async def lock(self, user_id):
        async with self.guard.lock(user_id) as store:
            yield AppealStore(store)

    @asynccontextmanager
    async def reader(self):
        async with self.guard.pool.acquire() as connection:
            yield AppealStore(GuardStore(connection))

    async def get(self, appeal_id):
        async with self.reader() as store:
            return await store.get(appeal_id)

    async def input(self, actor_id):
        async with self.reader() as store:
            return await store.input(actor_id)

    async def user_rows(self, user_id, *, cases=False, offset=0):
        async with self.reader() as store:
            if cases:
                return await store.guard.all(
                    """SELECT c.* FROM moderation_cases c WHERE c.user_id=%s
                    AND (c.action='ban' OR c.status='blacklisted'
                    OR JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.review_ban_confirmed'))='true')
                    AND c.status IN ('pending','blacklisted','done')
                    ORDER BY c.created_at DESC,c.id DESC LIMIT 6 OFFSET %s""",
                    (user_id, max(0, offset)),
                )
            return await store.guard.all(
                "SELECT * FROM moderation_appeals WHERE user_id=%s ORDER BY created_at DESC,id DESC LIMIT 6 OFFSET %s",
                (user_id, max(0, offset)),
            )

    async def work(self):
        async with self.reader() as store:
            appeals = await store.guard.all(
                """SELECT a.* FROM moderation_appeals a JOIN moderation_cases c ON c.id=a.case_id WHERE
                (a.status='executing' AND a.retry_at<=%s) OR a.published_revision<a.revision
                OR (a.status='draft' AND a.updated_at<%s)
                OR (a.status IN ('pending','need_info') AND
                (c.status<>JSON_UNQUOTE(JSON_EXTRACT(a.payload,'$.source.status'))
                OR c.action<>JSON_UNQUOTE(JSON_EXTRACT(a.payload,'$.source.action'))
                OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.review_generation')),'0')<>
                COALESCE(JSON_UNQUOTE(JSON_EXTRACT(a.payload,'$.source.payload.review_generation')),'0')))
                ORDER BY a.updated_at ASC LIMIT 50""",
                (utc_now_naive(), utc_now_naive() - timedelta(days=1)),
            )
            requests = await store.guard.all(
                """SELECT r.*,a.user_id FROM moderation_appeal_requests r
                JOIN moderation_appeals a ON a.id=r.appeal_id
                WHERE r.status='pending' ORDER BY r.created_at ASC LIMIT 50"""
            )
        return appeals, requests

    async def invalidate_routing(self):
        async with self.reader() as store:
            await store.guard.execute(
                "UPDATE moderation_appeals SET published_revision=0 WHERE status IN ('submitted','pending','need_info','executing')"
            )

    async def deliveries(self):
        async with self.reader() as store:
            notices = await store.guard.all(
                """SELECT n.*,a.user_id FROM moderation_appeal_notifications n
                JOIN moderation_appeals a ON a.id=n.appeal_id
                WHERE n.kind<>'retired' AND n.synced_revision<a.revision AND n.retry_at<=%s
                ORDER BY n.retry_at ASC LIMIT 50""",
                (utc_now_naive(),),
            )
            results = await store.guard.all(
                """SELECT * FROM moderation_appeal_requests WHERE status<>'pending'
                AND notified=FALSE AND retry_at<=%s ORDER BY created_at ASC LIMIT 50""",
                (utc_now_naive(),),
            )
        return notices, results
