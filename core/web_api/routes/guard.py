# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""Sakura Guard 管理 API；在现有认证之外实时复核全局管理员权限。"""

import uuid
from typing import Literal

from fastapi import APIRouter, Depends, HTTPException, Query, Request
from pydantic import BaseModel, ConfigDict, Field, ValidationError, model_validator

from core.commands.guard_commands import save_group
from core.config import read_config, save_config_changes
from core.config.persistence import ConfigConflictError, ConfigPersistenceError, configs_equal
from core.i18n.i18n import t
from core.moderation import get_guard
from core.moderation.chat_cleanup import ChatCleanupError
from core.moderation.config import GuardConfig, GuardGroup
from core.moderation.moderator import GuardError
from core.moderation.repository import ACTIVE_VERIFICATIONS
from core.web_api.deps import record_system_audit


def guard_admin(request: Request):
    service = get_guard()
    if service is None:
        raise HTTPException(503, t("guard.unavailable"))
    user = getattr(request.state, "user", None)
    if getattr(user, "user_id", None) == "admin":
        return service, "admin"
    try:
        user_id = int(getattr(user, "user_id", ""))
    except (TypeError, ValueError):
        raise HTTPException(403, t("error.permission_denied")) from None
    if user_id not in service.default_admins():
        raise HTTPException(403, t("error.permission_denied"))
    return service, user_id


GUARD_ADMIN = Depends(guard_admin)
router = APIRouter(dependencies=[GUARD_ADMIN])


class ReviewRequest(BaseModel):
    action: Literal["allow", "blacklist", "unban", "ban"]
    generation: int | None = Field(default=None, ge=0, le=99999999)


class WhitelistRequest(BaseModel):
    chat_id: int = Field(lt=0)
    user_id: int = Field(gt=0, lt=2**63)
    enabled: bool = True


class RetentionRequest(BaseModel):
    retention_days: int = Field(ge=7, le=3650)


class AddGroupRequest(BaseModel):
    chat_id: int = Field(lt=0, gt=-(2**63))


class LeaveChatsRequest(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)

    token: str = Field(min_length=32, max_length=128)
    confirmed: bool

    @model_validator(mode="after")
    def require_confirmation(self):
        if self.confirmed is not True:
            raise ValueError("请先预览并确认操作名单")
        return self


class GroupSwitchRequest(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)

    field: Literal[
        "enabled",
        "verify_join",
        "nickname_filter",
        "message_filter",
        "block_bot_messages",
        "share_global_blacklist",
        "ban_any_traditional",
        "allow_group_admin_review",
        "notification_enabled",
        "verification_notice_cleanup",
    ]
    value: bool


class GroupSettingsRequest(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)

    # GuardGroup 的默认值只用于校验；仅提交显式提供的字段。
    changes: GuardGroup
    previous: GuardGroup

    @model_validator(mode="after")
    def matching_fields(self):
        if not self.changes.model_fields_set or (
            self.changes.model_fields_set != self.previous.model_fields_set
        ):
            raise ValueError("changes and previous must contain the same nonempty fields")
        return self


class RetentionPatchRequest(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)

    retention_days: int = Field(ge=7, le=3650)
    previous: int = Field(ge=7, le=3650)


def settings_conflict(current, fields):
    return HTTPException(
        409,
        {"message": t("guard.config_conflict"), "current": current, "fields": sorted(fields)},
    )


def business_error(error):
    return HTTPException(409, t(str(error)))


@router.get("/config")
async def config(context=GUARD_ADMIN):
    service, _ = context
    return {"config": service.config.model_dump(), "defaults": GuardGroup().model_dump()}


@router.get("/groups")
async def joined_groups(refresh: bool = False, context=GUARD_ADMIN):
    service, _ = context
    service.directory.refresh(force=refresh)
    return service.directory.snapshot()


@router.get("/chats")
async def joined_chats(refresh: bool = False, context=GUARD_ADMIN):
    service, _ = context
    service.directory.refresh(force=refresh)
    return {
        **service.directory.snapshot(),
        "cleanup": service.chat_cleanup.status(),
        "history_cleanup": service.history_cleanup.status(),
    }


@router.post("/chats/leave-preview")
async def preview_leave_chats(context=GUARD_ADMIN):
    service, actor = context
    try:
        return service.chat_cleanup.preview(actor)
    except ChatCleanupError as error:
        raise HTTPException(409, str(error)) from error


@router.post("/chats/leave")
async def leave_chats(body: LeaveChatsRequest, context=GUARD_ADMIN):
    service, actor = context
    try:
        return service.chat_cleanup.start(body.token, actor, audit=record_system_audit)
    except ChatCleanupError as error:
        raise HTTPException(409, str(error)) from error


@router.post("/chats/history-cleanup-preview")
async def preview_clean_chat_history(context=GUARD_ADMIN):
    service, actor = context
    try:
        return service.history_cleanup.preview(actor)
    except ChatCleanupError as error:
        raise HTTPException(409, str(error)) from error


@router.post("/chats/history-cleanup")
async def clean_chat_history(body: LeaveChatsRequest, context=GUARD_ADMIN):
    service, actor = context
    try:
        return service.history_cleanup.start(body.token, actor, audit=record_system_audit)
    except ChatCleanupError as error:
        raise HTTPException(409, str(error)) from error


@router.post("/groups")
async def add_group(body: AddGroupRequest, context=GUARD_ADMIN):
    service, _ = context
    # 立即提交完整配置快照，不等待 Telegram；重复添加不覆盖已有群设置。
    snapshot = read_config()
    groups = snapshot.setdefault("sakura_guard", {}).setdefault("groups", {})
    key = str(body.chat_id)
    created = key not in groups
    if created:
        groups[key] = GuardGroup().model_dump()
        GuardConfig.model_validate(snapshot["sakura_guard"])
        save_config_changes(snapshot)
    service.reload(read_config())
    service.directory.remember(body.chat_id)
    service.directory.refresh()
    return {
        "success": True,
        "created": created,
        "chat_id": key,
        "group": service.group(body.chat_id).model_dump(),
    }


@router.put("/groups/{chat_id}")
async def update_group(chat_id: int, group: GuardGroup, context=GUARD_ADMIN):
    service, _ = context
    if chat_id >= 0:
        raise HTTPException(422, t("guard.invalid_group"))
    try:
        await save_group(service, chat_id, group)
    except GuardError as error:
        raise business_error(error) from error
    return {"success": True}


@router.patch("/groups/{chat_id}")
async def update_group_switch(chat_id: int, body: GroupSwitchRequest, context=GUARD_ADMIN):
    service, _ = context
    if not -(2**63) < chat_id < 0:
        raise HTTPException(422, t("guard.invalid_group"))
    try:
        # 校验前读取基线，只提交指定字段；并发写入由配置存储合并或拒绝。
        snapshot = read_config()
        section = snapshot.get("sakura_guard", {})
        group = section.get("groups", {}).get(str(chat_id))
        if group is None:
            raise HTTPException(404, t("guard.group_not_found"))
        updated = GuardGroup.model_validate({**group, body.field: body.value})
        await service.validate_group(chat_id, updated)
        group[body.field] = body.value
        GuardConfig.model_validate(section)
        save_config_changes(snapshot)
        service.reload(read_config())
    except GuardError as error:
        raise business_error(error) from error
    except ConfigConflictError as error:
        raise HTTPException(409, str(error)) from error
    except ConfigPersistenceError as error:
        raise HTTPException(503, str(error)) from error
    return {"success": True, "group": service.group(chat_id).model_dump()}


@router.patch("/groups/{chat_id}/settings")
async def update_group_settings(chat_id: int, body: GroupSettingsRequest, context=GUARD_ADMIN):
    service, _ = context
    if not -(2**63) < chat_id < 0:
        raise HTTPException(422, t("guard.invalid_group"))
    fields = body.changes.model_fields_set
    changes = body.changes.model_dump(include=fields)
    previous = body.previous.model_dump(include=fields)
    try:
        snapshot = read_config()
        section = snapshot.get("sakura_guard", {})
        group = section.get("groups", {}).get(str(chat_id))
        if group is None:
            raise HTTPException(404, t("guard.group_not_found"))
        current = GuardGroup.model_validate(group).model_dump()
        conflicts = [
            key
            for key in fields
            if not configs_equal(current[key], previous[key])
            and not configs_equal(current[key], changes[key])
        ]
        if conflicts:
            raise settings_conflict(current, conflicts)
        updated = GuardGroup.model_validate({**current, **changes})
        await service.validate_group(chat_id, updated)
        group.update(changes)
        GuardConfig.model_validate(section)
        save_config_changes(snapshot)
        service.reload(read_config())
    except GuardError as error:
        raise business_error(error) from error
    except ConfigConflictError as error:
        latest = read_config().get("sakura_guard", {}).get("groups", {}).get(str(chat_id))
        if latest is None:
            raise HTTPException(404, t("guard.group_not_found")) from error
        raise settings_conflict(GuardGroup.model_validate(latest).model_dump(), fields) from error
    except ConfigPersistenceError as error:
        raise HTTPException(503, str(error)) from error
    except ValidationError as error:
        raise HTTPException(
            422, error.errors(include_context=False, include_input=False, include_url=False)
        ) from error
    return {"success": True, "group": service.group(chat_id).model_dump()}


@router.patch("/retention")
async def update_retention(body: RetentionPatchRequest, context=GUARD_ADMIN):
    service, _ = context
    try:
        snapshot = read_config()
        section = snapshot.setdefault("sakura_guard", {})
        current = section.get("retention_days", GuardConfig().retention_days)
        if current not in {body.previous, body.retention_days}:
            raise settings_conflict({"retention_days": current}, ["retention_days"])
        section["retention_days"] = body.retention_days
        GuardConfig.model_validate(section)
        save_config_changes(snapshot)
        service.reload(read_config())
    except ConfigConflictError as error:
        current = (
            read_config()
            .get("sakura_guard", {})
            .get("retention_days", GuardConfig().retention_days)
        )
        raise settings_conflict({"retention_days": current}, ["retention_days"]) from error
    except ConfigPersistenceError as error:
        raise HTTPException(503, str(error)) from error
    return {"success": True, "retention_days": service.config.retention_days}


@router.put("/retention")
async def retention(body: RetentionRequest, context=GUARD_ADMIN):
    service, _ = context
    snapshot = read_config()
    snapshot.setdefault("sakura_guard", {})["retention_days"] = body.retention_days
    GuardConfig.model_validate(snapshot["sakura_guard"])
    save_config_changes(snapshot)
    service.reload(read_config())
    return {"success": True}


@router.get("/records/{table}")
async def records(
    table: Literal["cases", "whitelist", "blacklist", "verifications", "notifications"],
    chat_id: int | None = None,
    user_id: int | None = None,
    status: str | None = None,
    limit: int = Query(50, ge=1, le=200),
    offset: int = Query(0, ge=0),
    context=GUARD_ADMIN,
):
    service, _ = context
    result = await service.repository.query(
        table, chat_id=chat_id, user_id=user_id, status=status, limit=limit, offset=offset
    )
    if table == "verifications":
        # API 的管理员视图也不需要暴露验证码答案或原始权限快照。
        for row in result["rows"]:
            payload = row.pop("payload", None) or {}
            row["message_cleanup_pending"] = bool(
                row.get("message_id")
                and (row["status"] not in ACTIVE_VERIFICATIONS or row["status"] == "kicking")
                and payload.get("cleanup_pending", True)
            )
    return result


@router.post("/cases/{case_id}/review")
async def review(case_id: str, body: ReviewRequest, context=GUARD_ADMIN):
    service, user_id = context
    try:
        result = await service.reviews.review(
            case_id, body.action, user_id, web=True, expected_generation=body.generation
        )
    except GuardError as error:
        raise business_error(error) from error
    return {"success": True, "case": result}


@router.post("/cases/{case_id}/retry")
async def retry(case_id: str, context=GUARD_ADMIN):
    service, _ = context
    case = await service.repository.get_case(case_id)
    if not case:
        raise HTTPException(404, t("guard.invalid_callback"))
    async with service.repository.lock(case["user_id"]) as store:
        case = await store.case(case_id)
        if case["status"] != "execution_failed":
            raise HTTPException(409, t("guard.already_processing"))
        await store.update_case(case_id, "executing")
        await service.execute_case(store, await store.case(case_id))
        result = await store.case(case_id)
    if result["status"] == "execution_failed":
        raise HTTPException(502, t("guard.operation_failed"))
    return {"success": True, "case": result}


@router.put("/whitelist")
async def whitelist(body: WhitelistRequest, context=GUARD_ADMIN):
    service, reviewer_id = context
    async with service.repository.lock(body.user_id) as store:
        if body.enabled and await store.blacklist(body.user_id):
            raise HTTPException(409, t("guard.blacklist_conflict"))
        await store.set_whitelist(
            body.chat_id,
            body.user_id,
            None if reviewer_id == "admin" else reviewer_id,
            body.enabled,
            reviewer_actor="webui:admin" if reviewer_id == "admin" else f"telegram:{reviewer_id}",
        )
        await service.record_event(
            store,
            body.chat_id,
            body.user_id,
            "whitelist_add" if body.enabled else "whitelist_remove",
            uuid.uuid4().hex,
            "whitelist",
            actor_id=reviewer_id,
        )
    return {"success": True}


@router.delete("/blacklist/{user_id}")
async def remove_blacklist(user_id: int, context=GUARD_ADMIN):
    service, reviewer_id = context
    async with service.repository.lock(user_id) as store:
        previous = await store.blacklist(user_id)
        if previous:
            # 只撤销永久库记录；历史封禁由对应群案件单独解除，不批量解封。
            await store.connection.begin()
            try:
                identifier = uuid.uuid4().hex
                await store.create_case(
                    {
                        "id": identifier,
                        "event_key": identifier.ljust(64, "0"),
                        "chat_id": previous["source_chat_id"],
                        "user_id": user_id,
                        "action": "blacklist_remove",
                        "status": "done",
                        "payload": {
                            "name": previous["payload"].get("name", ""),
                            "evidence": {
                                "target": "global_blacklist",
                                "rule_id": "manual",
                                "reason": f"admin:{reviewer_id}",
                            },
                        },
                    }
                )
                await store.execute("DELETE FROM moderation_blacklist WHERE user_id=%s", (user_id,))
                await store.connection.commit()
            except BaseException:
                await store.connection.rollback()
                raise
    return {"success": True}
