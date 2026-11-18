// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Sakura520222/Sakura-Bot/internal/mainbot"
	"github.com/Sakura520222/Sakura-Bot/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	showVersion := flag.Bool("version", false, "显示版本")
	webOnly := flag.Bool("web-only", false, "仅启动 WebUI，不连接 Telegram 或 MySQL")
	loginUserBot := flag.Bool("login-userbot", false, "交互登录 UserBot 并保存会话")
	migrateVectors := flag.Bool("migrate-vectors", false, "停机时从旧 ChromaDB 创建 SQLite 向量库")
	verifyVectors := flag.Bool("verify-vectors", false, "校验旧 ChromaDB 与 SQLite 向量库的一致性")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return 0
	}
	modes := 0
	for _, enabled := range []bool{*webOnly, *loginUserBot, *migrateVectors, *verifyVectors} {
		if enabled {
			modes++
		}
	}
	if flag.NArg() != 0 || modes > 1 {
		flag.Usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *migrateVectors || *verifyVectors {
		if err := mainbot.MigrateVectors(ctx, *verifyVectors, os.Stdout); err != nil {
			slog.Error("向量迁移或校验失败", "error", err)
			return 1
		}
		return 0
	}
	if err := mainbot.Run(ctx, mainbot.Options{WebOnly: *webOnly, LoginUserBot: *loginUserBot}); err != nil {
		slog.Error("主 Bot 已退出", "error", err)
		return 1
	}
	return 0
}
