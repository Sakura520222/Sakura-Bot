// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/botcmd"
	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/embedder"
	"github.com/Sakura520222/Sakura-Bot/internal/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/poll"
	"github.com/Sakura520222/Sakura-Bot/internal/scheduler"
	"github.com/Sakura520222/Sakura-Bot/internal/summary"
	"github.com/Sakura520222/Sakura-Bot/internal/tg"
	"github.com/Sakura520222/Sakura-Bot/internal/vectors"
	"github.com/Sakura520222/Sakura-Bot/internal/wiki"
)

// Deps contains the services owned by the main Bot process.
type Deps struct {
	Context                  context.Context
	Config                   *config.Config
	DB                       *db.Manager
	BotClient                *mtproto.Client
	UserBot                  *mtproto.UserBotManager
	CommandRouter            *botcmd.Router
	Scheduler                *scheduler.Scheduler
	SummaryService           *summary.Service
	TimeManager              *summary.TimeManager
	PollRegenerations        *poll.RegenStore
	Vector                   *vectors.Store
	Images                   *media.ImagePipeline
	Embedding                *embedder.Generator
	HistoryClient            func() mtproto.Messenger
	Guard                    *guard.Service
	QAManager                QAProcess
	TriggerRestart           func(reason string)
	StartedAt                time.Time
	Wiki                     *wiki.Runtime
	WikiReader               *wiki.PublicReader
	WikiStore                *wiki.Store
	WikiAccounts             *wiki.AccountService
	WikiAvatars              *wiki.AvatarService
	DirectBotAPI             *tg.DirectBotAPI
	UpstreamHTTP             *http.Client
	ConsumeLegacyLoginReplay func(context.Context, string, time.Time) (bool, error)
	Logger                   *slog.Logger
	StaticDir                string
}

type Server struct {
	deps         Deps
	logger       *slog.Logger
	wikiLoginRL  *IPRateLimiter
	wikiPublicRL *IPRateLimiter
	handler      http.Handler
	startedAt    time.Time
	summaryJobs  *summaryJobs
}

// New builds the handler for both the API and the Vue application.
func New(deps Deps) *Server {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.StartedAt.IsZero() {
		deps.StartedAt = time.Now()
	}
	if deps.Wiki != nil {
		deps.WikiReader = deps.Wiki.Reader
		deps.WikiStore = deps.Wiki.Store
		deps.WikiAccounts = deps.Wiki.Accounts
		deps.WikiAvatars = deps.Wiki.Avatar
	}
	if deps.WikiAccounts != nil && deps.ConsumeLegacyLoginReplay == nil {
		deps.ConsumeLegacyLoginReplay = deps.WikiAccounts.ConsumeLegacyReplay
	}
	s := &Server{
		deps:         deps,
		logger:       deps.Logger,
		startedAt:    deps.StartedAt,
		wikiLoginRL:  NewIPRateLimiter(60, time.Minute, 4096),
		wikiPublicRL: NewIPRateLimiter(120, time.Minute, 4096),
	}
	if deps.Context != nil && deps.SummaryService != nil {
		s.summaryJobs = newSummaryJobs(deps.Context, deps.Logger, deps.SummaryService.GenerateChannelSummary)
	}
	s.handler = s.wrapMiddlewares(s.routes())
	return s
}

// StopSummaryJobs cancels outstanding WebUI summaries and waits for the workers to exit.
func (s *Server) StopSummaryJobs() {
	if s.summaryJobs != nil {
		s.summaryJobs.stop()
	}
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	s.registerCoreRoutes(mux)
	s.registerDocsRoutes(mux)
	s.registerAIRoutes(mux)
	s.registerDatabaseRoutes(mux)
	s.registerForwardingRoutes(mux)
	s.registerPublicWikiRoutes(mux)
	s.registerWikiAccountRoutes(mux)
	s.registerWikiAssistantRoutes(mux)
	s.registerWikiCommunityRoutes(mux)
	s.registerWikiResearchRoutes(mux)
	s.registerWikiAdminRoutes(mux)
	s.registerSystemRoutes(mux)
	s.registerStatsRoutes(mux)
	s.registerScheduleRoutes(mux)
	s.registerInteractionRoutes(mux)
	s.registerVectorRoutes(mux)
	s.registerImageRoutes(mux)
	s.registerGuardRoutes(mux)
	mux.HandleFunc("/", s.serveStatic)
	return mux
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) botToken() string {
	if s.deps.Config == nil || s.deps.Config.Env == nil {
		return ""
	}
	return s.deps.Config.TelegramBotToken()
}

func (s *Server) devMode() bool {
	return s.deps.Config != nil && s.deps.Config.Env != nil && s.deps.Config.WebUIDevMode()
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		writeHTTPError(w, http.StatusNotFound, "Not Found")
		return
	}
	if s.deps.StaticDir == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(s.deps.StaticDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name != "" && filepath.Ext(name) != "" {
		if file, err := root.Open(name); err == nil {
			defer file.Close()
			if info, err := file.Stat(); err == nil && info.Mode().IsRegular() {
				http.ServeContent(w, r, info.Name(), info.ModTime(), file)
				return
			}
		}
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
	}
	index, err := root.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer index.Close()
	info, err := index.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", info.ModTime(), index)
}
