# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""
QA Bot Telegram 底部键盘构建工具。
"""

from telegram import ReplyKeyboardMarkup

from core.i18n.i18n import t

QA_MENU_ASK = "🔎 指定频道查询"
QA_MENU_CHANNELS = "📚 可订阅频道"
QA_MENU_SUBSCRIPTIONS = "⭐ 我的订阅"
QA_MENU_SUBMIT = "📝 投稿"
QA_MENU_STATUS = "📊 状态"
QA_MENU_CLEAR = "🧹 清除记忆"
QA_MENU_HELP = "❓ 帮助"
QA_MENU_APPEAL = t("appeal.menu")

SUBMIT_MENU_CANCEL = "❌ 取消投稿"
SUBMIT_MENU_SKIP_CONTENT = "⏭ 跳过正文"
SUBMIT_MENU_SKIP_MEDIA = "⏭ 跳过媒体"
SUBMIT_MENU_DONE_MEDIA = "✅ 完成投稿"
SUBMIT_MENU_CONFIRM = "✅ 确认提交"
SUBMIT_MENU_ANONYMOUS_YES = "🕶 匿名投稿"
SUBMIT_MENU_ANONYMOUS_NO = "👤 署名投稿"


def build_qa_main_menu_keyboard() -> ReplyKeyboardMarkup:
    """构建 QA Bot 常驻主菜单键盘。"""
    return ReplyKeyboardMarkup(
        [
            [QA_MENU_SUBMIT, QA_MENU_APPEAL],
            [QA_MENU_CHANNELS, QA_MENU_SUBSCRIPTIONS],
            [QA_MENU_STATUS, QA_MENU_CLEAR, QA_MENU_HELP],
        ],
        resize_keyboard=True,
        is_persistent=True,
        input_field_placeholder="直接输入问题，或点击下方按钮",
    )


def build_submission_title_keyboard() -> ReplyKeyboardMarkup:
    """构建投稿标题阶段键盘。"""
    return ReplyKeyboardMarkup(
        [[SUBMIT_MENU_CANCEL]],
        resize_keyboard=True,
        is_persistent=True,
        input_field_placeholder="请输入投稿标题",
    )


def build_submission_content_keyboard() -> ReplyKeyboardMarkup:
    """构建投稿正文阶段键盘。"""
    return ReplyKeyboardMarkup(
        [[SUBMIT_MENU_SKIP_CONTENT, SUBMIT_MENU_CANCEL]],
        resize_keyboard=True,
        is_persistent=True,
        input_field_placeholder="请输入正文，或点击跳过",
    )


def build_submission_anonymous_keyboard() -> ReplyKeyboardMarkup:
    """构建投稿匿名选择阶段键盘。"""
    return ReplyKeyboardMarkup(
        [[SUBMIT_MENU_ANONYMOUS_YES, SUBMIT_MENU_ANONYMOUS_NO], [SUBMIT_MENU_CANCEL]],
        resize_keyboard=True,
        is_persistent=True,
        input_field_placeholder="请选择是否匿名投稿",
    )


def build_submission_media_keyboard() -> ReplyKeyboardMarkup:
    """构建投稿媒体阶段键盘。"""
    return ReplyKeyboardMarkup(
        [[SUBMIT_MENU_DONE_MEDIA, SUBMIT_MENU_SKIP_MEDIA], [SUBMIT_MENU_CANCEL]],
        resize_keyboard=True,
        is_persistent=True,
        input_field_placeholder="发送媒体文件，或点击完成/跳过",
    )


def build_submission_confirm_keyboard() -> ReplyKeyboardMarkup:
    """构建投稿确认阶段键盘。"""
    return ReplyKeyboardMarkup(
        [[SUBMIT_MENU_CONFIRM, SUBMIT_MENU_CANCEL]],
        resize_keyboard=True,
        is_persistent=True,
        input_field_placeholder="请确认是否提交",
    )
