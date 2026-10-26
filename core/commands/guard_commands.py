# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""严格授权的群管命令，与 WebUI 共用配置持久化和审核服务。"""

import logging
import uuid

from core.config import read_config, save_config_changes
from core.i18n.i18n import t
from core.moderation import get_guard
from core.moderation.config import GuardConfig, GuardGroup, migrate_nickname_keywords
from core.moderation.moderator import GuardError

logger = logging.getLogger(__name__)


async def save_group(service, chat_id, group):
    await service.validate_group(chat_id, group)
    snapshot = read_config()
    migrate_nickname_keywords(snapshot)
    section = snapshot.setdefault("sakura_guard", {})
    section.setdefault("groups", {})[str(chat_id)] = group.model_dump()
    GuardConfig.model_validate(section)
    save_config_changes(snapshot)
    service.reload(read_config())


async def handle_guard(event):
    service = get_guard()
    if service is None:
        await event.reply(t("guard.unavailable"))
        return
    try:
        parts = (getattr(event, "raw_text", None) or event.text).split()
        command = parts.pop(0).split("@")[0]
        chat_id = getattr(event, "chat_id", None)
        if parts and parts[0].startswith("-"):
            chat_id = int(parts.pop(0))
        if command in {"/guard_addword", "/guard_delword"}:
            if event.sender_id not in service.default_admins():
                raise GuardError("error.permission_denied")
            word = " ".join(parts)
            if not word:
                raise GuardError("guard.help")
            snapshot = read_config()
            migrate_nickname_keywords(snapshot)
            section = snapshot["sakura_guard"]
            words = section["nickname_keywords"]
            section["nickname_keywords"] = (
                list(dict.fromkeys([*words, word]))
                if command == "/guard_addword"
                else [value for value in words if value != word]
            )
            GuardConfig.model_validate(section)
            save_config_changes(snapshot)
            service.reload(read_config())
            await event.reply(
                t("guard.global_keywords_saved", count=len(service.config.nickname_keywords))
            )
            return
        if not chat_id or chat_id >= 0:
            if event.sender_id not in service.default_admins():
                raise GuardError("error.permission_denied")
            await event.reply(
                t("guard.help")
                + "\n"
                + "\n".join(
                    f"{key}: {group.enabled}" for key, group in service.config.groups.items()
                ),
                parse_mode=None,
            )
            return
        if event.sender_id not in service.default_admins() and not await service.authorized(
            event.sender_id, chat_id
        ):
            raise GuardError("error.permission_denied")
        group = service.group(chat_id).model_copy(deep=True)
        if command in {"/guard_on", "/guard_off"}:
            group.enabled = command == "/guard_on"
        elif command == "/guard_verify":
            if not parts or parts[0] not in {"on", "off"}:
                raise GuardError("guard.help")
            group.verify_join = parts[0] == "on"
        elif command == "/guard_bots":
            if len(parts) != 1 or parts[0] not in {"on", "off"}:
                raise GuardError("guard.help")
            group.block_bot_messages = parts[0] == "on"
        elif command == "/guard_whitelist":
            if len(parts) != 2 or parts[0] not in {"add", "remove"}:
                raise GuardError("guard.help")
            user_id = int(parts[1])
            if user_id <= 0:
                raise GuardError("guard.help")
            async with service.repository.lock(user_id) as store:
                if parts[0] == "add" and await store.blacklist(user_id):
                    raise GuardError("guard.blacklist_conflict")
                await store.set_whitelist(chat_id, user_id, event.sender_id, parts[0] == "add")
                await service.record_event(
                    store,
                    chat_id,
                    user_id,
                    "whitelist_add" if parts[0] == "add" else "whitelist_remove",
                    uuid.uuid4().hex,
                    "whitelist",
                    actor_id=event.sender_id,
                )
            await event.reply(t("guard.saved"))
            return
        elif command == "/guard_unban":
            if len(parts) != 1:
                raise GuardError("guard.help")
            result = await service.reviews.review(
                parts[0], "allow", event.sender_id, expected_chat_id=chat_id
            )
            await event.reply(
                t("guard.review_result", status=t(f"guard.status_{result['status']}"))
            )
            return
        elif command == "/guard_logs":
            result = await service.repository.query("cases", chat_id=chat_id, limit=10)
            await event.reply(
                "\n".join(
                    f"{row['id']} | {row['user_id']} | {row['action']} | {row['status']}"
                    for row in result["rows"]
                )
                or t("guard.empty"),
                parse_mode=None,
            )
            return
        else:
            await event.reply(
                t(
                    "guard.group_status",
                    chat_id=chat_id,
                    enabled=group.enabled,
                    verify=group.verify_join,
                    nickname=group.nickname_filter,
                    message=group.message_filter,
                    block_bots=group.block_bot_messages,
                    sharing=group.share_global_blacklist,
                )
                + "\n\n"
                + t("guard.help"),
                parse_mode=None,
            )
            return
        # assignment 不执行 Pydantic 校验，统一重新解析后才保存。
        await save_group(service, chat_id, GuardGroup.model_validate(group.model_dump()))
        await event.reply(t("guard.saved"))
    except GuardError as error:
        await event.reply(t(str(error)), parse_mode=None)
    except (ValueError, TypeError):
        await event.reply(t("guard.help"), parse_mode=None)
    except Exception:
        logger.exception("Guard 命令执行失败")
        await event.reply(t("guard.operation_failed"))
