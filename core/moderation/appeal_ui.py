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
        "text": escape(evidence[0]["text"][:1200]) if evidence else "—",
        "files": sum(len(item.get("files", [])) for item in evidence),
        "rounds": len(evidence),
    }
    text = t("appeal.reviewer_card" if reviewer else "appeal.user_card", **values)
    review = payload.get("last_review")
    if review:
        text += "\n\n" + t(
            "appeal.last_review",
            action=t(f"appeal.action_{review['action']}"),
            reason=escape(review["reason"][:1000]),
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


def buttons(appeal, *, reviewer=False):
    key, revision = appeal["id"], appeal["revision"]
    if reviewer:
        rows = []
        if appeal["status"] == "pending":
            actions = [
                "approve",
                "allow",
                *(["global"] if appeal["payload"].get("blacklisted") else []),
                "reject",
                "more",
            ]
            rows.extend(
                [
                    [button(f"action_{action}", f"ap:review:{key}:{revision}:{action}")]
                    for action in actions
                ]
            )
        rows.append(
            [
                button(action, f"ap:read:{key}:{revision}:{action}")
                for action in ("evidence", "audit")
            ]
        )
        return InlineKeyboardMarkup(rows)
    rows = [[button("refresh", f"ap:view:{key}")]]
    if appeal["status"] == "need_info":
        rows.append([button("supplement", f"ap:supp:{key}:{revision}")])
    if appeal["status"] in {"draft", "submitted", "pending", "need_info"}:
        rows.append([button("withdraw", f"ap:withdraw:{key}:{revision}")])
    rows.append([button("history", "ap:list:history:0")])
    return InlineKeyboardMarkup(rows)


def input_prompt(entry):
    payload = entry["payload"]
    review = entry["mode"] == "review"
    text = (
        t("appeal.review_prompt", action=t(f"appeal.action_{payload['action']}"))
        if review
        else t("appeal.input_prompt")
    )
    text += "\n\n" + t(
        "appeal.input_preview",
        text=escape(payload.get("text", "")[:1800]) or "—",
        files=len(payload.get("files", [])),
    )
    return text


def input_buttons(entry):
    return InlineKeyboardMarkup(
        [
            [button("confirm", f"ap:input:{entry['token']}:submit")],
            [button("cancel", f"ap:input:{entry['token']}:cancel")],
        ]
    )
