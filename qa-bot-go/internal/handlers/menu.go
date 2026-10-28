// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// menu.go：主菜单按钮与游离投稿键盘按钮的处理
// （qa_bot.py:508-575 handle_menu_button / handle_stray_submission_button /
// cancel_submission_outside_conversation 的移植）。
package handlers

import (
	"context"
	"strings"

	"github.com/go-telegram/bot/models"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

// HandleMenuButton 处理底部主菜单按钮（qa_bot.py:508-536 的移植）。
// 6 个主菜单文本精确匹配后转调对应命令；QA_MENU_ASK 单独回复用法文案。
func (c *Commands) HandleMenuButton(ctx context.Context, upd *models.Update) {
	_, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}

	// Python：text = update.message.text.strip() if update.message and update.message.text else ""
	text := ""
	if msg.Text != "" {
		text = strings.TrimSpace(msg.Text)
	}

	switch text {
	case tg.MenuHelp:
		c.Help(ctx, upd)
	case tg.MenuStatus:
		c.Status(ctx, upd)
	case tg.MenuClear:
		c.Clear(ctx, upd)
	case tg.MenuChannels:
		c.ListChannels(ctx, upd)
	case tg.MenuSubscriptions:
		c.MySubscriptions(ctx, upd)
	case tg.MenuAsk:
		// qa_bot.py:523-531：菜单"指定频道查询"回复用法（HTML + 主键盘）。
		message := "🔎 <b>指定频道查询</b>\n\n" +
			"请按以下格式发送命令：\n" +
			"<code>/ask &lt;频道链接或@用户名&gt; &lt;问题&gt;</code>\n\n" +
			"示例：\n" +
			"<code>/ask @channel_name 最近有什么更新？</code>\n\n" +
			"💡 也可以直接输入自然语言问题，我会自动检索相关记录。"
		c.replyHTML(ctx, msg, message)
	}
}

// HandleStraySubmissionButton 处理会话外残留的投稿键盘按钮
// （qa_bot.py:538-554 的移植）：回 submission.no_active_submission_restart。
func (c *Commands) HandleStraySubmissionButton(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}

	// Python：text = update.message.text.strip() if update.message.text else ""
	text := ""
	if msg.Text != "" {
		text = strings.TrimSpace(msg.Text)
	}
	c.log.Infof("拦截会话外投稿按钮: user_id=%d, text=%s", user.ID, text)
	c.reply(ctx, msg, i18n.T("submission.no_active_submission_restart", nil), "", tg.BuildQAMainMenuKeyboard())
}

// CancelSubmissionOutsideConversation 处理会话外的 /cancel_submit 命令
// （qa_bot.py:556-575 的移植）：清内存投稿态，回 submission.no_active_submission。
func (c *Commands) CancelSubmissionOutsideConversation(ctx context.Context, upd *models.Update) {
	user, msg, ok := effectiveUserMessage(upd)
	if !ok {
		return
	}

	c.clearSubmissionState(user.ID)

	c.log.Infof("处理会话外取消投稿命令: user_id=%d", user.ID)
	c.reply(ctx, msg, i18n.T("submission.no_active_submission", nil), "", tg.BuildQAMainMenuKeyboard())
}

// clearSubmissionState 复刻 Python 的延迟导入 + try/except：
// 投稿状态仅存内存 dict，清理失败仅记日志（清理本身不会失败，异常路径
// 以 recover 兜底）。
func (c *Commands) clearSubmissionState(userID int64) {
	if c.submissions == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			c.log.Errorf("清理投稿状态失败: %T: %v", r, r)
		}
	}()
	c.submissions.ClearUserState(userID)
}
