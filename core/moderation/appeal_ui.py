# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
"""助手 Bot 申诉卡片；所有用户和证据文本按 HTML 转义。"""

from html import escape

from telegram import InlineKeyboardButton, InlineKeyboardMarkup

from core.i18n.i18n import t


def button(key, data):
    return InlineKeyboardButton(t(f"appeal.{key}"), callback_data=data)


def card(appeal, *, reviewer=False):
    payload = appeal["payload"]
    source = payload["source"]["payload"]
    evidence = payload.get("evidence", [])
    values = {
        "id": appeal["id"],
        "chat": escape(str(source.get("chat_title") or appeal["chat_id"])),
        "user_id": appeal["user_id"],
        "status": t(f"appeal.status_{appeal['status']}"),
        "reason": escape(str(source.get("evidence", {}).get("reason", "—"))[:300]),
        "name": escape(str(source.get("name") or appeal["user_id"])[:80]),
        "text": escape(evidence[0]["text"][:1000]) if evidence else "—",
        "files": sum(len(item.get("files", [])) for item in evidence),
        "rounds": len(evidence),
    }
    text = t("appeal.reviewer_card" if reviewer else "appeal.user_card", **values)
    if len(evidence) > 1:
        text += "\n\n" + t("appeal.latest_evidence", text=escape(evidence[-1]["text"][:700]))
    if values["files"]:
        text += "\n" + t("appeal.attachment_count", files=values["files"])
    review = payload.get("last_review")
    if review:
        text += "\n\n" + t(
            "appeal.last_review",
            action=t(f"appeal.action_{review['action']}"),
            reason=escape(review["reason"][: 500 if reviewer else 1000]),
        )
    if payload.get("blacklisted"):
        text += "\n\n" + t("appeal.blacklist_hint")
    if appeal.get("error"):
        key = appeal["error"]
        text += "\n\n" + t(
            "appeal.blocked",
            error=escape(
                t(key) if key.startswith(("guard.", "appeal.", "error.")) else t("appeal.retrying")
            ),
        )
    return text


def buttons(appeal, *, reviewer=False, expanded=False, confirm=None):
    key, revision = appeal["id"], appeal["revision"]
    if reviewer:
        rows = []
        if appeal["status"] == "pending":
            if confirm:
                rows = [
                    [button("confirm_action", f"ap:review:{key}:{revision}:{confirm}")],
                    [button("back", f"ap:admin:{key}:{revision}")],
                ]
            elif expanded:
                actions = ["allow", *(["global"] if appeal["payload"].get("blacklisted") else [])]
                rows = [
                    [button(f"action_{action}", f"ap:check:{key}:{revision}:{action}")]
                    for action in actions
                ]
                rows.append([button("back", f"ap:admin:{key}:{revision}")])
            else:
                rows = [
                    [
                        button("action_approve", f"ap:review:{key}:{revision}:approve"),
                        button("action_reject", f"ap:review:{key}:{revision}:reject"),
                    ],
                    [
                        button("action_more", f"ap:review:{key}:{revision}:more"),
                        button("more_actions", f"ap:options:{key}:{revision}"),
                    ],
                ]
        rows.append(
            [
                button(action, f"ap:read:{key}:{revision}:{action}")
                for action in ("evidence", "audit")
            ]
        )
        return InlineKeyboardMarkup(rows)
    rows = [[button("refresh", f"ap:view:{key}"), button("history", "ap:list:history:0")]]
    if appeal["status"] == "need_info":
        rows.append([button("supplement", f"ap:supp:{key}:{revision}")])
    if appeal["status"] in {"draft", "submitted", "pending", "need_info"}:
        rows.append([button("withdraw", f"ap:withdraw:{key}:{revision}")])
    else:
        rows.append([button("new", "ap:list:cases:0")])
    return InlineKeyboardMarkup(rows)


def input_prompt(entry, appeal=None):
    payload = entry["payload"]
    review = entry["mode"] == "review"
    if review:
        return t("appeal.review_prompt", action=t(f"appeal.action_{payload['action']}"))
    text = t("appeal.input_prompt")
    if appeal:
        source = appeal["payload"]["source"]["payload"]
        text = (
            t(
                "appeal.input_context",
                chat=escape(str(source.get("chat_title") or appeal["chat_id"])[:100]),
                reason=escape(str(source.get("evidence", {}).get("reason", "—"))[:220]),
            )
            + "\n\n"
            + text
        )
    if payload.get("text"):
        text += "\n\n" + escape(payload["text"][:1800])
    if payload.get("files"):
        text += "\n" + t("appeal.attachment_count", files=len(payload["files"]))
    return text


def input_buttons(entry):
    row = []
    if entry["mode"] != "review" and entry["payload"].get("text", "").strip():
        row.append(
            button(
                "submit_supplement" if entry["mode"] == "supplement" else "submit",
                f"ap:input:{entry['token']}:submit",
            )
        )
    elif entry["mode"] == "review" and entry["payload"]["action"] in {"approve", "allow", "global"}:
        row.append(button("confirm", f"ap:input:{entry['token']}:submit"))
    row.append(button("cancel", f"ap:input:{entry['token']}:cancel"))
    return InlineKeyboardMarkup([row])


def user_alert(appeal):
    status = appeal["status"]
    if status not in {"approved", "rejected", "need_info", "superseded"}:
        return None
    source = appeal["payload"]["source"]["payload"]
    return t(
        f"appeal.alert_{status}",
        chat=escape(str(source.get("chat_title") or appeal["chat_id"])[:100]),
        reason=escape(appeal["payload"].get("last_review", {}).get("reason", "")[:1000]),
    )
