// Copyright 2026 Sakura-Bot. AGPL-3.0.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/handlers"
)

const webScope = engine.WikiWebScope

var webID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var webChannel = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var webSession = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var wikiLinks = regexp.MustCompile(`!?\[([^]\n]*)\]\([^\n)]*\)|\[\[([^]\n]+)\]\]`)
var wikiMarkers = strings.NewReplacer("**", "", "__", "", "`", "", "*", "", "~~", "")

type wikiWeb struct {
	quota        *quotaManager
	pool         *handlers.AIPool
	stream       func(context.Context, string, int64, string) <-chan engine.Chunk
	subscription func(context.Context, int64) string
	log          *slog.Logger
	mu           sync.Mutex
	turns        map[int64]*webTurn
}

type webActor struct {
	user    int64
	session string
}

type webArticle struct {
	Channel  string `json:"channel"`
	Page     string `json:"page"`
	Revision string `json:"revision"`
	Selected string `json:"selected"`
}

type webInput struct {
	ID       string      `json:"id"`
	Question string      `json:"question"`
	Parent   string      `json:"parent,omitempty"`
	Article  *webArticle `json:"article"`
}

type webTurn struct {
	job      *handlers.AIJob
	mu       sync.Mutex
	owner    string
	answer   string
	state    string
	message  string
	changed  chan struct{}
	cancel   context.CancelFunc
	ids      map[string]bool
	finished time.Time
	refunded bool
}

func (t *webTurn) signal() {
	close(t.changed)
	t.changed = make(chan struct{})
}

func startWikiWeb(ctx context.Context, address, token string, service *wikiWeb) (func(), error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return nil, errors.New("Wiki assistant must bind to 127.0.0.1")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	secret := sha256.Sum256([]byte("sakura-wiki-assistant:v1:" + token))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", service.status)
	mux.HandleFunc("POST /checkin", service.checkin)
	mux.HandleFunc("POST /chat", service.chat)
	mux.HandleFunc("GET /chat/{id}", func(w http.ResponseWriter, r *http.Request) { service.resume(w, r, r.PathValue("id")) })
	mux.HandleFunc("DELETE /chat/{id}", service.cancel)
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 8192,
		BaseContext:    func(net.Listener) context.Context { return ctx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			provided, err := hex.DecodeString(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if err != nil || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || !hmac.Equal(provided, secret[:]) {
				webError(w, http.StatusUnauthorized, "内部助手认证失败")
				return
			}
			mux.ServeHTTP(w, r)
		}),
	}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			service.log.Error("Wiki 助手桥接停止", "error", err)
		}
	}()
	return func() { _ = srv.Close() }, nil
}

func webJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func webError(w http.ResponseWriter, status int, message string) {
	webJSON(w, status, map[string]string{"detail": message})
}

func (s *wikiWeb) actor(w http.ResponseWriter, r *http.Request) (webActor, bool) {
	actor := webActor{session: r.Header.Get("X-Wiki-Session")}
	user, err := strconv.ParseInt(r.Header.Get("X-Wiki-User"), 10, 64)
	if err != nil || user <= 0 || !webSession.MatchString(actor.session) {
		webError(w, http.StatusUnauthorized, "Wiki 会话已失效")
		return actor, false
	}
	actor.user = user
	if !s.sessionValid(r.Context(), actor) {
		webError(w, http.StatusUnauthorized, "Wiki 会话已失效")
		return actor, false
	}
	return actor, true
}

func (s *wikiWeb) sessionValid(ctx context.Context, actor webActor) bool {
	digest := sha256.Sum256([]byte(actor.session))
	var valid int
	err := s.quota.db.DB.QueryRowContext(ctx, `SELECT 1 FROM wiki_sessions s JOIN wiki_users u ON u.id=s.user_id
		WHERE s.logout_hash=? AND u.telegram_id=? AND u.blocked=FALSE AND s.refresh_until>UTC_TIMESTAMP(6)`, digest[:], actor.user).Scan(&valid)
	return err == nil && valid == 1
}

func (s *wikiWeb) status(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.actor(w, r)
	if !ok {
		return
	}
	settings, err := s.quota.cfg.AssistantSettings()
	if err != nil {
		webError(w, 503, "助手配置暂不可用")
		return
	}
	limits, err := s.quota.cfg.EffectiveQuota()
	if err != nil {
		webError(w, 503, "额度配置暂不可用")
		return
	}
	day := settings.BillingDay(time.Now())
	status, err := s.quota.db.BillingSnapshot(r.Context(), actor.user, day)
	if err != nil {
		webError(w, 503, "积分与额度暂不可用")
		return
	}
	webJSON(w, 200, struct {
		db.BillingSnapshot
		Day            string `json:"day"`
		Timezone       string `json:"timezone"`
		FreeLimit      int64  `json:"free_limit"`
		SiteLimit      int64  `json:"site_limit"`
		Exempt         bool   `json:"exempt"`
		CheckinEnabled bool   `json:"checkin_enabled"`
	}{status, day, settings.Timezone, limits.UserDaily, limits.TotalDaily,
		s.quota.cfg.IsAdmin(actor.user), settings.CheckinEnabled})
}

func (s *wikiWeb) checkin(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.actor(w, r)
	if !ok {
		return
	}
	settings, err := s.quota.cfg.AssistantSettings()
	if err != nil || !settings.CheckinEnabled {
		webError(w, 403, "签到暂不可用")
		return
	}
	if message := s.subscription(r.Context(), actor.user); message != "" {
		webError(w, 403, message)
		return
	}
	if !s.sessionValid(r.Context(), actor) {
		webError(w, 401, "Wiki 会话已失效")
		return
	}
	units, added, err := s.quota.db.Checkin(r.Context(), actor.user, settings.BillingDay(time.Now()))
	if err != nil {
		webError(w, 503, "签到失败，请重试")
		return
	}
	webJSON(w, 200, map[string]any{"units": units, "added": added})
}

func webReservation(user int64, id string) string { return fmt.Sprintf("web:%d:%s", user, id) }

func (s *wikiWeb) article(ctx context.Context, article *webArticle) (string, error) {
	if article == nil {
		return "", nil
	}
	if !webChannel.MatchString(article.Channel) || !webID.MatchString(article.Page) || !webID.MatchString(article.Revision) || utf8.RuneCountInString(article.Selected) > 500 {
		return "", errors.New("词条参数无效")
	}
	repository := db.WikiRepository{DB: s.quota.db.DB}
	page, err := repository.ReadWiki(ctx, "wiki_get_page", contracts.WikiOptions{Channel: article.Channel, Slug: article.Page})
	if err != nil || page["revision_id"] != article.Revision || page["id"] != article.Page {
		return "", errors.New("词条已更新或不可读取，请刷新页面")
	}
	var body strings.Builder
	claims, _ := page["claims"].([]any)
	selected := strings.TrimSpace(article.Selected)
	found := selected == ""
	for _, claim := range claims {
		if fact, ok := claim.(map[string]any); ok {
			text, _ := fact["text"].(string)
			if !found && strings.Contains(normalizeSelection(text), normalizeSelection(selected)) {
				found = true
			}
			if body.Len() < 5000 {
				body.WriteString(text + "\n\n")
			}
		}
	}
	if !found {
		return "", errors.New("选中文字已变化，请重新选择")
	}
	text := []rune(body.String())
	if len(text) > 3500 {
		text = text[:3500]
	}
	return fmt.Sprintf("频道 %s，词条 %s，修订 %s\n%s\n选中文字：%s", article.Channel, page["title"], article.Revision, string(text), selected), nil
}

func normalizeSelection(text string) string {
	return strings.Join(strings.Fields(wikiMarkers.Replace(html.UnescapeString(wikiLinks.ReplaceAllString(text, "$1$2")))), " ")
}

func (s *wikiWeb) chat(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.actor(w, r)
	if !ok {
		return
	}
	var input webInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24000))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || !webID.MatchString(input.ID) ||
		(input.Parent != "" && !webID.MatchString(input.Parent)) {
		webError(w, 400, "提问参数无效")
		return
	}
	input.Question = strings.TrimSpace(input.Question)
	if input.Question == "" || utf8.RuneCountInString(input.Question) > 4000 {
		webError(w, 400, "问题须为 1 至 4000 字")
		return
	}
	key := webReservation(actor.user, input.ID)
	if turn := s.find(actor, input.ID); turn != nil {
		s.serveTurn(w, r, turn)
		return
	}
	state, err := s.quota.db.ReservationState(r.Context(), key, actor.user)
	if err != nil {
		webError(w, 503, "助手账本暂不可用")
		return
	}
	if state != "" {
		s.resume(w, r, input.ID)
		return
	}
	if message := s.subscription(r.Context(), actor.user); message != "" {
		webError(w, 403, message)
		return
	}
	article, err := s.article(r.Context(), input.Article)
	if err != nil {
		webError(w, 409, err.Error())
		return
	}
	turn, err := s.admit(r.Context(), actor, input, article)
	if err != nil {
		webError(w, 409, err.Error())
		return
	}
	s.serveTurn(w, r, turn)
}

func (s *wikiWeb) admit(ctx context.Context, actor webActor, input webInput, article string) (*webTurn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turns == nil {
		s.turns = make(map[int64]*webTurn)
	}
	for user, turn := range s.turns {
		turn.mu.Lock()
		finished := turn.finished
		turn.mu.Unlock()
		if !finished.IsZero() && (time.Since(finished) > 5*time.Minute || len(s.turns) >= 128) {
			delete(s.turns, user)
		}
	}
	current := s.turns[actor.user]
	if current != nil {
		current.mu.Lock()
		known := current.ids[input.ID] && current.owner == actor.session
		current.mu.Unlock()
		if known {
			return current, nil
		}
	}
	job, existing, err := s.pool.AcquireScoped(actor.user, webScope)
	if err != nil {
		return nil, err
	}
	if existing && (current == nil || current.job != job || current.owner != actor.session) {
		return nil, errors.New("网页助手正在另一会话处理请求")
	}
	if existing {
		current.mu.Lock()
		accepting := current.state == "running" && current.ids[input.Parent]
		current.mu.Unlock()
		if !accepting {
			return nil, errors.New("已有网页回合正在处理，可继续接收该回合")
		}
	} else if input.Parent != "" {
		job.Release()
		return nil, errors.New("上一个回合已结束，请将追问作为新问题发送")
	}
	if existing && article != "" {
		return nil, errors.New("当前回合结束后可发起新的词条提问")
	}
	result, admitted := job.Admit(engine.TurnInput{Query: input.Question}, webReservation(actor.user, input.ID), func() contracts.QuotaCheckResult {
		return s.quota.ReserveTurnMessage(ctx, webReservation(actor.user, input.ID), actor.user, job.ID)
	})
	if !admitted {
		if !existing {
			job.Release()
		}
		return nil, errors.New(result.Message)
	}
	if existing {
		current.mu.Lock()
		current.ids[input.ID] = true
		current.mu.Unlock()
		return current, nil
	}
	turn := &webTurn{job: job, owner: actor.session, state: "running", changed: make(chan struct{}), ids: map[string]bool{input.ID: true}}
	s.turns[actor.user] = turn
	job.Submit(func(work context.Context) { s.generate(work, actor, turn, input, article) })
	return turn, nil
}

func (s *wikiWeb) generate(ctx context.Context, actor webActor, turn *webTurn, input webInput, article string) {
	ctx, cancel := context.WithCancel(ctx)
	turn.mu.Lock()
	turn.cancel = cancel
	requestedCancel := turn.state == "cancelling"
	turn.mu.Unlock()
	if requestedCancel {
		cancel()
	}
	defer cancel()
	success := false
	message := "回答未完成。"
	defer func() {
		ids := turn.job.ReservationIDs()
		if success && ctx.Err() == nil {
			check, stop := context.WithTimeout(ctx, 3*time.Second)
			success = s.sessionValid(check, actor)
			if success && input.Article != nil {
				_, err := s.article(check, input.Article)
				success = err == nil
			}
			stop()
		}
		turn.mu.Lock()
		success = success && ctx.Err() == nil && turn.state != "cancelling"
		turn.state = "settling"
		turn.mu.Unlock()
		cleanup, stop := context.WithTimeout(turn.job.CleanupContext(), 5*time.Second)
		defer stop()
		err := s.quota.db.FinishReservations(cleanup, ids, success)
		if err != nil && cleanup.Err() == nil {
			err = s.quota.db.FinishReservations(cleanup, ids, success)
		}
		turn.mu.Lock()
		switch {
		case err != nil:
			s.log.Error("Wiki 助手计费结算失败", "error", err)
			turn.state, turn.message = "error", "账本暂不可用，助手恢复后会核对本次扣费。"
		case success:
			turn.state = "done"
		default:
			turn.state, turn.message = "error", message+" 本回合扣费已退回。"
			turn.refunded = true
		}
		turn.finished = time.Now()
		turn.signal()
		turn.mu.Unlock()
	}()
	if ctx.Err() != nil || !s.sessionValid(ctx, actor) {
		return
	}
	if reason := s.subscription(ctx, actor.user); reason != "" {
		message = reason
		return
	}
	if input.Article != nil {
		var err error
		article, err = s.article(ctx, input.Article)
		if err != nil {
			message = err.Error()
			return
		}
	}
	monitorDone := make(chan struct{})
	defer close(monitorDone)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-monitorDone:
				return
			case <-ticker.C:
				check, finish := context.WithTimeout(ctx, 3*time.Second)
				valid := s.sessionValid(check, actor)
				if valid && input.Article != nil {
					_, err := s.article(check, input.Article)
					valid = err == nil
				}
				finish()
				if !valid {
					cancel()
					return
				}
			}
		}
	}()
	work := engine.WithTurn(engine.WithScope(ctx, webScope), turn.job)
	if article != "" {
		work = engine.WithWikiContext(work, article)
	}
	for chunk := range s.stream(work, input.Question, actor.user, "") {
		switch chunk.Kind {
		case engine.ChunkText:
			turn.mu.Lock()
			if len(turn.answer)+len(chunk.Text) > 64*1024 {
				turn.mu.Unlock()
				cancel()
				return
			}
			turn.answer += chunk.Text
			turn.signal()
			turn.mu.Unlock()
		case engine.ChunkErr:
			message = chunk.Err
			return
		case engine.ChunkCtrl:
			if chunk.Ctrl == contracts.CtrlDone {
				success = true
			}
		}
	}
}

func (s *wikiWeb) find(actor webActor, id string) *webTurn {
	s.mu.Lock()
	turn := s.turns[actor.user]
	s.mu.Unlock()
	if turn == nil {
		return nil
	}
	turn.mu.Lock()
	defer turn.mu.Unlock()
	if turn.owner != actor.session || !turn.ids[id] || (!turn.finished.IsZero() && time.Since(turn.finished) > 5*time.Minute) {
		return nil
	}
	return turn
}

func (s *wikiWeb) resume(w http.ResponseWriter, r *http.Request, id string) {
	actor, ok := s.actor(w, r)
	if !ok {
		return
	}
	if !webID.MatchString(id) {
		webError(w, 404, "找不到网页回合")
		return
	}
	if turn := s.find(actor, id); turn != nil {
		s.serveTurn(w, r, turn)
		return
	}
	state, err := s.quota.db.ReservationState(r.Context(), webReservation(actor.user, id), actor.user)
	if err != nil {
		webError(w, 503, "助手账本暂不可用")
		return
	}
	if state == "" {
		webError(w, 404, "找不到网页回合，可以重试")
		return
	}
	if state == "refunded" {
		webError(w, 410, "回合已结束并退款，请发起新提问")
		return
	}
	webError(w, 409, "请求已接纳，回答流已不可恢复，请检查额度后发起新提问")
}

func (s *wikiWeb) cancel(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.actor(w, r)
	if !ok {
		return
	}
	turn := s.find(actor, r.PathValue("id"))
	if turn == nil {
		webError(w, 404, "找不到网页回合")
		return
	}
	turn.mu.Lock()
	if turn.state == "running" {
		turn.state = "cancelling"
		if turn.cancel != nil {
			turn.cancel()
		}
	}
	turn.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *wikiWeb) serveTurn(w http.ResponseWriter, r *http.Request, turn *webTurn) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(kind string, payload any) bool {
		data, _ := json.Marshal(payload)
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
		return err == nil && controller.Flush() == nil
	}
	if !write("accepted", true) {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	offset := 0
	for {
		turn.mu.Lock()
		text, state, message, changed, refunded := turn.answer[offset:], turn.state, turn.message, turn.changed, turn.refunded
		offset = len(turn.answer)
		turn.mu.Unlock()
		if text != "" && !write("text", text) {
			return
		}
		if state == "done" || state == "error" {
			write(state, map[string]any{"message": message, "refunded": refunded})
			return
		}
		select {
		case <-changed:
		case <-ticker.C:
			if !write("heartbeat", true) {
				return
			}
		case <-r.Context().Done():
			return // The user may resume or explicitly cancel.
		}
	}
}
