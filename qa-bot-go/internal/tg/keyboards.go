// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package tg

import (
	"strings"

	"github.com/go-telegram/bot/models"
)

// QA Bot 底部键盘按钮文案，与 core/telegram/keyboards.py 逐字对齐（含 emoji）。
const (
	// MenuAsk 对齐 QA_MENU_ASK
	MenuAsk = "🔎 指定频道查询"
	// MenuChannels 对齐 QA_MENU_CHANNELS
	MenuChannels = "📚 可订阅频道"
	// MenuSubscriptions 对齐 QA_MENU_SUBSCRIPTIONS
	MenuSubscriptions = "⭐ 我的订阅"
	// MenuSubmit 对齐 QA_MENU_SUBMIT
	MenuSubmit = "📝 投稿"
	// MenuStatus 对齐 QA_MENU_STATUS
	MenuStatus = "📊 状态"
	// MenuClear 对齐 QA_MENU_CLEAR
	MenuClear = "🧹 清除记忆"
	// MenuHelp 对齐 QA_MENU_HELP
	MenuHelp = "❓ 帮助"
)

// 投稿键盘按钮文案，与 core/telegram/keyboards.py 逐字对齐（含 emoji）。
const (
	// SubmitMenuCancel 对齐 SUBMIT_MENU_CANCEL
	SubmitMenuCancel = "❌ 取消投稿"
	// SubmitMenuSkipContent 对齐 SUBMIT_MENU_SKIP_CONTENT
	SubmitMenuSkipContent = "⏭ 跳过正文"
	// SubmitMenuSkipMedia 对齐 SUBMIT_MENU_SKIP_MEDIA
	SubmitMenuSkipMedia = "⏭ 跳过媒体"
	// SubmitMenuDoneMedia 对齐 SUBMIT_MENU_DONE_MEDIA
	SubmitMenuDoneMedia = "✅ 完成投稿"
	// SubmitMenuConfirm 对齐 SUBMIT_MENU_CONFIRM
	SubmitMenuConfirm = "✅ 确认提交"
	// SubmitMenuAnonymousYes 对齐 SUBMIT_MENU_ANONYMOUS_YES
	SubmitMenuAnonymousYes = "🕶 匿名投稿"
	// SubmitMenuAnonymousNo 对齐 SUBMIT_MENU_ANONYMOUS_NO
	SubmitMenuAnonymousNo = "👤 署名投稿"
)

// MenuAppeal 申诉主菜单按钮文案。
//
// Python 版在模块 import 期求值一次：QA_MENU_APPEAL = t("appeal.menu")，
// zh-CN 词典值为 "🚫 封禁申诉"。Go 侧不 import 并行开发的 i18n 实现包，
// 由 main 在启动阶段按当前语言赋值一次（对齐 Python 每进程只求值一次的语义）：
//
//	tg.MenuAppeal = i18n.T("appeal.menu")
var MenuAppeal = "🚫 封禁申诉"

// replyKeyboard 由文本行构建常驻 ReplyKeyboardMarkup（resize + persistent）。
func replyKeyboard(rows [][]string, placeholder string) *models.ReplyKeyboardMarkup {
	keyboard := make([][]models.KeyboardButton, 0, len(rows))
	for _, row := range rows {
		kbRow := make([]models.KeyboardButton, 0, len(row))
		for _, text := range row {
			kbRow = append(kbRow, models.KeyboardButton{Text: text})
		}
		keyboard = append(keyboard, kbRow)
	}
	return &models.ReplyKeyboardMarkup{
		Keyboard:              keyboard,
		ResizeKeyboard:        true,
		IsPersistent:          true,
		InputFieldPlaceholder: placeholder,
	}
}

// BuildQAMainMenuKeyboard 构建 QA Bot 常驻主菜单键盘
// （core/telegram/keyboards.py 的 build_qa_main_menu_keyboard 逐行移植）。
func BuildQAMainMenuKeyboard() *models.ReplyKeyboardMarkup {
	return replyKeyboard([][]string{
		{MenuSubmit, MenuAppeal},
		{MenuChannels, MenuSubscriptions},
		{MenuStatus, MenuClear, MenuHelp},
	}, "直接输入问题，或点击下方按钮")
}

// BuildSubmissionTitleKeyboard 构建投稿标题阶段键盘。
func BuildSubmissionTitleKeyboard() *models.ReplyKeyboardMarkup {
	return replyKeyboard([][]string{
		{SubmitMenuCancel},
	}, "请输入投稿标题")
}

// BuildSubmissionContentKeyboard 构建投稿正文阶段键盘。
func BuildSubmissionContentKeyboard() *models.ReplyKeyboardMarkup {
	return replyKeyboard([][]string{
		{SubmitMenuSkipContent, SubmitMenuCancel},
	}, "请输入正文，或点击跳过")
}

// BuildSubmissionAnonymousKeyboard 构建投稿匿名选择阶段键盘。
func BuildSubmissionAnonymousKeyboard() *models.ReplyKeyboardMarkup {
	return replyKeyboard([][]string{
		{SubmitMenuAnonymousYes, SubmitMenuAnonymousNo},
		{SubmitMenuCancel},
	}, "请选择是否匿名投稿")
}

// BuildSubmissionMediaKeyboard 构建投稿媒体阶段键盘。
func BuildSubmissionMediaKeyboard() *models.ReplyKeyboardMarkup {
	return replyKeyboard([][]string{
		{SubmitMenuDoneMedia, SubmitMenuSkipMedia},
		{SubmitMenuCancel},
	}, "发送媒体文件，或点击完成/跳过")
}

// BuildSubmissionConfirmKeyboard 构建投稿确认阶段键盘。
func BuildSubmissionConfirmKeyboard() *models.ReplyKeyboardMarkup {
	return replyKeyboard([][]string{
		{SubmitMenuConfirm, SubmitMenuCancel},
	}, "请确认是否提交")
}

// MenuButtonPattern 返回 6 个主菜单按钮的 Regex 过滤器源串
// （qa_bot.py:1023-1027 的 menu_button_filter 逐字等价）。
func MenuButtonPattern() string {
	alt := strings.Join([]string{
		MenuAsk, MenuChannels, MenuSubscriptions, MenuStatus, MenuClear, MenuHelp,
	}, "|")
	return "^(" + alt + ")$"
}

// SubmissionButtonPattern 返回 7 个游离投稿按钮的 Regex 过滤器源串
// （qa_bot.py:1029-1036 的 submission_menu_filter 逐字等价）。
func SubmissionButtonPattern() string {
	alt := strings.Join([]string{
		SubmitMenuCancel, SubmitMenuSkipContent, SubmitMenuSkipMedia,
		SubmitMenuDoneMedia, SubmitMenuConfirm, SubmitMenuAnonymousYes,
		SubmitMenuAnonymousNo,
	}, "|")
	return "^(" + alt + ")$"
}
