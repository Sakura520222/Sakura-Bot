// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mainbot assembles and supervises the main Bot process.
package mainbot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/filelock"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/logging"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/webapi"
)

type Options struct {
	WebOnly      bool
	LoginUserBot bool
}

func Run(ctx context.Context, opts Options) error {
	cfg := config.Default()
	if err := ensureConfig(ctx, cfg.ConfigPath()); err != nil {
		return err
	}
	if err := cfg.Init(); err != nil {
		return err
	}
	logger, _, err := logging.Setup(logging.SetupMainOptionsFromEnv(cfg.Env))
	if err != nil {
		return err
	}
	i18n.SetLanguage(cfg.Language())
	if level, ok := cfg.RawSnapshot()["log_level"].(string); ok {
		logging.SetDefaultLevel(level)
	}
	if opts.WebOnly {
		return serveWeb(ctx, cfg, logger, webapi.New(webapi.Deps{
			Config: cfg, Logger: logger, StaticDir: webStaticDir(),
		}))
	}

	apiID, err := cfg.TelegramAPIID()
	if err != nil {
		return err
	}
	hash := cfg.TelegramAPIHash()
	if hash == "" {
		return errors.New("缺少 API_HASH")
	}
	userOpts := mtproto.UserBotOptions{
		APIID: apiID, APIHash: hash, PhoneNumber: cfg.UserBotPhoneNumber(),
		SessionPath: cfg.UserBotSessionPath(), Enabled: cfg.UserBotEnabled(),
		FallbackToBot: cfg.UserBotFallbackToBot(), Logger: logger,
	}
	if opts.LoginUserBot {
		userOpts.Enabled = true
		user := mtproto.NewUserBotManager(userOpts)
		defer user.Stop()
		return user.Start(ctx)
	}

	token := cfg.TelegramBotToken()
	if token == "" {
		return errors.New("缺少 BOT_TOKEN")
	}
	for ctx.Err() == nil {
		runCtx, stop := context.WithCancel(ctx)
		var restart atomic.Bool
		var pendingRelease atomic.Pointer[stagedRelease]
		requestRestart := func(reason string) {
			logger.Info("正在重启主 Bot", "reason", reason)
			restart.Store(true)
			stop()
		}
		requestReleaseRestart := func(reason, executable, staticDir string) {
			pendingRelease.Store(&stagedRelease{executable: executable, staticDir: staticDir})
			requestRestart(reason)
		}
		err := runBot(runCtx, stop, requestRestart, requestReleaseRestart, cfg, logger, mtproto.BotOptions{
			APIID: apiID, APIHash: hash, BotToken: token,
			SessionPath: filepath.Join(cfg.DataDirectory(), "sessions", "bot_session"),
			Logger:      logger,
		}, userOpts)
		stop()
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if staged := pendingRelease.Load(); staged != nil && ctx.Err() == nil {
			return restartRelease(*staged)
		}
		if !restart.Load() {
			break
		}
	}
	return nil
}

type stagedRelease struct {
	executable string
	staticDir  string
}

const staticDirEnv = "SAKURA_WEB_DIST"

func webStaticDir() string {
	if dir := os.Getenv(staticDirEnv); dir != "" {
		return dir
	}
	return filepath.Join("web", "dist")
}

func ensureConfig(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lock, err := filelock.Acquire(wait, path+".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.WriteString("{}\n")
	if err := errors.Join(err, f.Close()); err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}

func serveWeb(ctx context.Context, cfg *config.Config, logger *slog.Logger, handler http.Handler) error {
	addr := net.JoinHostPort(cfg.WebUIHost(), strconv.Itoa(cfg.WebUIPort()))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("启动 WebUI: %w", err)
	}
	srv := &http.Server{
		Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
		case <-stopped:
			return
		}
		grace, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(grace); err != nil {
			_ = srv.Close()
		}
	}()
	logger.Info("WebUI 已启动", "address", listener.Addr().String())
	err = srv.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		<-done
		return nil
	}
	return err
}
