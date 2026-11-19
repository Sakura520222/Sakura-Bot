// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package mtproto 封装基于 gotd/td 的 Telegram MTProto 客户端（主 Bot 与 UserBot）、
// Telethon SQLite Session 兼容迁移、实时事件分发与群管底层 RPC。
package mtproto

import (
	_ "github.com/gotd/td/session"
	_ "github.com/gotd/td/telegram"
	_ "github.com/gotd/td/telegram/auth"
	_ "github.com/gotd/td/telegram/downloader"
	_ "github.com/gotd/td/telegram/message"
	_ "github.com/gotd/td/telegram/peers"
	_ "github.com/gotd/td/telegram/updates"
	_ "github.com/gotd/td/telegram/uploader"
	_ "github.com/gotd/td/tg"
	_ "github.com/makiuchi-d/gozxing"
	_ "github.com/makiuchi-d/gozxing/qrcode"
	_ "golang.org/x/net/html"
	_ "golang.org/x/sync/errgroup"
	_ "golang.org/x/text/unicode/norm"
)
