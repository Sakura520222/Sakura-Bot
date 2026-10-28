// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// appeal_ui.go：助手 Bot 申诉卡片文本与 InlineKeyboard 生成。
// 逐行移植 core/moderation/appeal_ui.py（154 行）：button/card/buttons/
// input_prompt/input_buttons/user_alert。所有用户与证据文本按 HTML 转义
// （任务规格裁决：使用 Go html.EscapeString；与 Python html.escape 在
// 引号实体上有 &#34;/&#x27; 与 &quot;/&#x27; 之差，行为规格允许）。

package handlers

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

// uiButton 对齐 appeal_ui.py:13-14：文案取 i18n（appeal.<key>），数据为回调串。
func uiButton(key, data string) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: i18n.T("appeal."+key, nil), CallbackData: data}
}

// uiCard 渲染申诉卡片文本（appeal_ui.py:17-56 card 的逐行移植）。
// reviewer=true 为审核人卡片（含 tg://user 提及与更短的意见截断）。
// appeal 为 nil 时返回空串（调用点均已先行判空，此处仅兜底）。
func uiCard(appeal guard.Row, reviewer bool) string {
	if appeal == nil {
		return ""
	}
	payload := asMap(appeal["payload"])
	source := asMap(asMap(payload["source"])["payload"])
	evidence := asList(payload["evidence"])

	files := 0
	for _, item := range evidence {
		files += len(asList(asMap(item)["files"]))
	}

	status := i18n.T("appeal.status_"+rowStr(appeal, "status"), nil)
	if rowStr(appeal, "status") == "pending" && pyTruthy(payload["blacklisted"]) {
		status = i18n.T("appeal.status_blacklisted", nil)
	}

	values := map[string]string{
		"id":      rowStr(appeal, "id"),
		"chat":    html.EscapeString(pyStr(pyOr(source["chat_title"], appeal["chat_id"]))),
		"user_id": strconv.FormatInt(rowInt64(appeal, "user_id"), 10),
		"status":  status,
		"reason":  html.EscapeString(truncRunes(pyStr(asMap(source["evidence"])["reason"]), 300)),
		"name":    html.EscapeString(truncRunes(pyStr(pyOr(source["name"], appeal["user_id"])), 80)),
		"files":   strconv.Itoa(files),
		"rounds":  strconv.Itoa(len(evidence)),
	}
	if len(evidence) > 0 {
		values["text"] = html.EscapeString(truncRunes(pyStr(asMap(evidence[0])["text"]), 1000))
	} else {
		values["text"] = "—"
	}

	cardKey := "appeal.user_card"
	if reviewer {
		cardKey = "appeal.reviewer_card"
	}
	text := i18n.T(cardKey, values)

	if len(evidence) > 1 {
		text += "\n\n" + i18n.T("appeal.latest_evidence",
			map[string]string{"text": html.EscapeString(truncRunes(pyStr(asMap(evidence[len(evidence)-1])["text"]), 700))})
	}
	if files != 0 {
		text += "\n" + i18n.T("appeal.attachment_count", map[string]string{"files": strconv.Itoa(files)})
	}

	if review := asMap(payload["last_review"]); review != nil {
		reasonLimit := 1000
		if reviewer {
			reasonLimit = 500
		}
		text += "\n\n" + i18n.T("appeal.last_review", map[string]string{
			"action": i18n.T("appeal.action_"+pyStr(review["action"]), nil),
			"reason": html.EscapeString(truncRunes(pyStr(review["reason"]), reasonLimit)),
		})
	}
	if pyTruthy(payload["blacklisted"]) {
		text += "\n\n" + i18n.T("appeal.blacklist_hint", nil)
	}
	if errKey := rowStr(appeal, "error"); errKey != "" {
		// 已知命名空间的错误键翻译原文；未知错误显示通用重试提示
		//（appeal_ui.py:48-55）。
		shown := i18n.T("appeal.retrying", nil)
		if hasPrefixAny(errKey, "guard.", "appeal.", "error.") {
			shown = i18n.T(errKey, nil)
		}
		text += "\n\n" + i18n.T("appeal.blocked", map[string]string{"error": html.EscapeString(shown)})
	}
	return text
}

// uiButtons 渲染申诉卡片 InlineKeyboard（appeal_ui.py:59-103 buttons 的逐行
// 移植）。reviewer 卡片：pending 时按 confirm/expanded/默认三态展开操作行，
// 尾行恒为「材料/审计记录」；用户卡片：刷新+历史，need_info 加补充按钮，
// 进行中可撤回、终态给「可申诉的封禁」。
func uiButtons(appeal guard.Row, reviewer bool, expanded bool, confirm string) *models.InlineKeyboardMarkup {
	if appeal == nil {
		return nil
	}
	key := rowStr(appeal, "id")
	revision := rowInt64(appeal, "revision")
	var rows [][]models.InlineKeyboardButton
	if reviewer {
		if rowStr(appeal, "status") == "pending" {
			payload := asMap(appeal["payload"])
			switch {
			case confirm != "":
				rows = [][]models.InlineKeyboardButton{
					{uiButton("confirm_action", fmt.Sprintf("ap:review:%s:%d:%s", key, revision, confirm))},
					{uiButton("back", fmt.Sprintf("ap:admin:%s:%d", key, revision))},
				}
			case expanded:
				actions := []string{"allow"}
				if pyTruthy(payload["blacklisted"]) {
					actions = []string{"global"}
				}
				for _, action := range actions {
					rows = append(rows, []models.InlineKeyboardButton{
						uiButton("action_"+action, fmt.Sprintf("ap:check:%s:%d:%s", key, revision, action)),
					})
				}
				rows = append(rows, []models.InlineKeyboardButton{
					uiButton("back", fmt.Sprintf("ap:admin:%s:%d", key, revision)),
				})
			default:
				first := uiButton("action_approve", fmt.Sprintf("ap:review:%s:%d:approve", key, revision))
				if pyTruthy(payload["blacklisted"]) {
					first = uiButton("action_global_short", fmt.Sprintf("ap:check:%s:%d:global", key, revision))
				}
				rows = [][]models.InlineKeyboardButton{
					{
						first,
						uiButton("action_reject", fmt.Sprintf("ap:review:%s:%d:reject", key, revision)),
					},
					{
						uiButton("action_more", fmt.Sprintf("ap:review:%s:%d:more", key, revision)),
						uiButton("more_actions", fmt.Sprintf("ap:options:%s:%d", key, revision)),
					},
				}
			}
		}
		rows = append(rows, []models.InlineKeyboardButton{
			uiButton("evidence", fmt.Sprintf("ap:read:%s:%d:evidence", key, revision)),
			uiButton("audit", fmt.Sprintf("ap:read:%s:%d:audit", key, revision)),
		})
		return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
	}
	rows = [][]models.InlineKeyboardButton{
		{uiButton("refresh", "ap:view:"+key), uiButton("history", "ap:list:history:0")},
	}
	status := rowStr(appeal, "status")
	if status == "need_info" {
		rows = append(rows, []models.InlineKeyboardButton{
			uiButton("supplement", fmt.Sprintf("ap:supp:%s:%d", key, revision)),
		})
	}
	switch status {
	case "draft", "submitted", "pending", "need_info":
		rows = append(rows, []models.InlineKeyboardButton{
			uiButton("withdraw", fmt.Sprintf("ap:withdraw:%s:%d", key, revision)),
		})
	default:
		rows = append(rows, []models.InlineKeyboardButton{uiButton("new", "ap:list:cases:0")})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// uiInputPrompt 渲染输入界面文本（appeal_ui.py:106-127 input_prompt 的逐行
// 移植）。review 模式仅显示操作确认提示；普通模式带群组上下文、已填文本
// 预览与附件计数。appeal 为 nil 时省略上下文段（对齐 Python 默认参数）。
func uiInputPrompt(entry guard.Row, appeal guard.Row) string {
	payload := asMap(entry["payload"])
	if rowStr(entry, "mode") == "review" {
		return i18n.T("appeal.review_prompt", map[string]string{
			"action": i18n.T("appeal.action_"+pyStr(payload["action"]), nil),
		})
	}
	text := i18n.T("appeal.input_prompt", nil)
	if appeal != nil {
		source := asMap(asMap(appeal["payload"])["source"])
		sourcePayload := asMap(source["payload"])
		chat := html.EscapeString(truncRunes(pyStr(pyOr(sourcePayload["chat_title"], appeal["chat_id"])), 100))
		reason := html.EscapeString(truncRunes(pyStr(asMap(sourcePayload["evidence"])["reason"]), 220))
		text = i18n.T("appeal.input_context", map[string]string{"chat": chat, "reason": reason}) + "\n\n" + text
	}
	if pyTruthy(payload["text"]) {
		text += "\n\n" + html.EscapeString(truncRunes(pyStr(payload["text"]), 1800))
	}
	if files := asList(payload["files"]); len(files) != 0 {
		text += "\n" + i18n.T("appeal.attachment_count", map[string]string{"files": strconv.Itoa(len(files))})
	}
	return text
}

// uiInputButtons 渲染输入界面的提交/取消按钮（appeal_ui.py:130-142
// input_buttons 的逐行移植）：review 模式仅当动作为快速审核三键之一时给
// 「确认提交」；draft/supplement 有非空文本时给提交按钮；取消按钮恒在。
func uiInputButtons(entry guard.Row) *models.InlineKeyboardMarkup {
	if entry == nil {
		return nil
	}
	payload := asMap(entry["payload"])
	token := rowStr(entry, "token")
	mode := rowStr(entry, "mode")
	var row []models.InlineKeyboardButton
	if mode != "review" && strings.TrimSpace(pyStr(payload["text"])) != "" {
		key := "submit"
		if mode == "supplement" {
			key = "submit_supplement"
		}
		row = append(row, uiButton(key, "ap:input:"+token+":submit"))
	} else if mode == "review" {
		switch pyStr(payload["action"]) {
		case "approve", "allow", "global":
			row = append(row, uiButton("confirm", "ap:input:"+token+":submit"))
		}
	}
	row = append(row, uiButton("cancel", "ap:input:"+token+":cancel"))
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row}}
}

// uiUserAlert 渲染通知投递附带的提醒消息（appeal_ui.py:145-154 user_alert
// 的逐行移植）。仅四个终局/补料状态产生提醒，其余返回空串（等价 None）。
func uiUserAlert(appeal guard.Row) string {
	if appeal == nil {
		return ""
	}
	status := rowStr(appeal, "status")
	switch status {
	case "approved", "rejected", "need_info", "superseded":
	default:
		return ""
	}
	payload := asMap(appeal["payload"])
	sourcePayload := asMap(asMap(payload["source"])["payload"])
	chat := html.EscapeString(truncRunes(pyStr(pyOr(sourcePayload["chat_title"], appeal["chat_id"])), 100))
	reason := html.EscapeString(truncRunes(pyStr(asMap(payload["last_review"])["reason"]), 1000))
	return i18n.T("appeal.alert_"+status, map[string]string{"chat": chat, "reason": reason})
}

// truncRunes 等价 Python text[:n]（按 Unicode 码点截断，README 硬规则 3）。
func truncRunes(text string, n int) string {
	return tg.TruncateRunes(text, n)
}

// hasPrefixAny 报告 s 是否以任一前缀开头（对齐 Python str.startswith(tuple)）。
func hasPrefixAny(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
