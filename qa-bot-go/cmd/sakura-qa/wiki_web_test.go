package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/handlers"
	"github.com/go-sql-driver/mysql"
)

func TestWikiWebSharedLedgerAndTurnLifecycle(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not configured")
	}
	conf, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conf.ParseTime = true
	conn, err := sql.Open("mysql", conf.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()
	manager := &db.Manager{DB: conn}
	for _, init := range []func(context.Context) error{manager.InitDatabase, manager.InitReservations} {
		if err := init(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// This test database is disposable. The Python integration suite validates the full migration.
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS wiki_users (id CHAR(32) PRIMARY KEY,telegram_id BIGINT NOT NULL,blocked BOOLEAN NOT NULL DEFAULT FALSE)`,
		`CREATE TABLE IF NOT EXISTS wiki_sessions (id CHAR(32) PRIMARY KEY,user_id CHAR(32) NOT NULL,logout_hash BINARY(32) NOT NULL,refresh_until DATETIME NOT NULL)`,
	} {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	user := time.Now().UnixNano()%1000000000 + 10000
	account := fmt.Sprintf("%032x", user)
	credential := strings.Repeat("a", 43)
	digest := sha256.Sum256([]byte(credential))
	if _, err := conn.Exec(`INSERT INTO wiki_users (id,telegram_id) VALUES (?,?)`, account, user); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO wiki_sessions (id,user_id,logout_hash,refresh_until) VALUES (?,?,?,UTC_TIMESTAMP()+INTERVAL 1 HOUR)`, account, account, digest[:]); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(`DELETE FROM wiki_sessions WHERE id=?`, account)
		_, _ = conn.Exec(`DELETE FROM wiki_users WHERE id=?`, account)
		for _, table := range []string{"qa_message_reservations", "qa_points", "qa_points_ledger", "qa_checkins", "usage_quota"} {
			_, _ = conn.Exec(`DELETE FROM `+table+` WHERE user_id=?`, user)
		}
	}()
	cfg := newQuotaTestConfig(t, `{"qa_bot_quota":{"user_daily_limit":1,"total_daily_limit":2000000000}}`)
	q := &quotaManager{db: manager, cfg: cfg, log: testLogger()}
	pool := handlers.NewAIPool(ctx, 1, 1, 15*time.Second)
	defer pool.Close()
	started := make(chan string, 4)
	answer := make(chan bool, 4)
	service := &wikiWeb{quota: q, pool: pool, log: testLogger(), subscription: func(context.Context, int64) string { return "" },
		stream: func(ctx context.Context, query string, _ int64, _ string) <-chan engine.Chunk {
			out := make(chan engine.Chunk)
			started <- engine.Scope(ctx) + ":" + query
			go func() {
				defer close(out)
				select {
				case <-ctx.Done():
				case <-answer:
					out <- engine.TextChunk("**回答**")
					out <- engine.CtrlChunk(contracts.CtrlDone)
				}
			}()
			return out
		}}
	request := func(method, path string, body any) *http.Request {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("X-Wiki-User", fmt.Sprint(user))
		r.Header.Set("X-Wiki-Session", credential)
		return r
	}
	call := func(handler http.HandlerFunc, method, path string, body any) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler(w, request(method, path, body))
		return w
	}
	day := time.Now().UTC().Format("2006-01-02")
	if msg := q.Checkin(ctx, user); !strings.Contains(msg, "签到成功") {
		t.Fatal(msg)
	}
	if web := call(service.checkin, "POST", "/checkin", nil); web.Code != 200 || !strings.Contains(web.Body.String(), `"added":false`) {
		t.Fatal(web.Code, web.Body.String())
	}
	if _, err := conn.Exec(`UPDATE qa_points SET units=50 WHERE user_id=?`, user); err != nil {
		t.Fatal(err)
	}
	telegram := fmt.Sprintf("%d:1", user)
	if !q.ReserveMessage(ctx, telegram, user).Allowed {
		t.Fatal("Telegram reservation failed")
	}
	q.FinishReservation(ctx, telegram, true)
	before, err := manager.BillingSnapshot(ctx, user, day)
	if err != nil || before.FreeUsed != 1 || before.PointUnits != 50 || !before.CheckedIn {
		t.Fatal(before, err)
	}
	actor := webActor{user: user, session: credential}
	first := webInput{ID: strings.Repeat("1", 32), Question: "first"}
	// The same bounded pool includes requests already admitted from Telegram.
	telegramJobs := make([]*handlers.AIJob, 0, 2)
	for i := int64(1); i <= 2; i++ {
		job, _, err := pool.AcquireScoped(user+i, "private")
		if err != nil {
			t.Fatal(err)
		}
		telegramJobs = append(telegramJobs, job)
	}
	if _, err := service.admit(ctx, actor, first, ""); err == nil {
		t.Fatal("full pool accepted a web query")
	}
	for _, job := range telegramJobs {
		job.Release()
	}
	if state, err := manager.ReservationState(ctx, webReservation(user, first.ID), user); err != nil || state != "" {
		t.Fatal("rejected request was billed", state, err)
	}
	service.subscription = func(context.Context, int64) string { return "订阅验证暂不可用" }
	if w := call(service.chat, "POST", "/chat", first); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	service.subscription = func(context.Context, int64) string { return "" }
	turn, err := service.admit(ctx, actor, first, "")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-started:
		if got != "web:wiki:first" {
			t.Fatal(got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("web worker did not start")
	}
	if duplicate, err := service.admit(ctx, actor, first, ""); err != nil || duplicate != turn {
		t.Fatal("duplicate started a new turn", err)
	}
	if _, err := service.admit(ctx, actor, webInput{ID: strings.Repeat("9", 32), Question: "new tab"}, ""); err == nil {
		t.Fatal("new question from another tab became an unrequested follow-up")
	}
	followup := webInput{ID: strings.Repeat("2", 32), Question: "followup", Parent: first.ID}
	if attached, err := service.admit(ctx, actor, followup, ""); err != nil || attached != turn {
		t.Fatal("followup did not attach", err)
	}
	if got, err := manager.BillingSnapshot(ctx, user, day); err != nil || got.PointUnits != 30 || got.SiteUsed != before.SiteUsed+2 {
		t.Fatal(got, err)
	}
	if service.find(webActor{user: user, session: strings.Repeat("b", 43)}, first.ID) != nil {
		t.Fatal("another browser can read the stream")
	}
	stop := request("DELETE", "/chat/"+first.ID, nil)
	stop.SetPathValue("id", first.ID)
	w := httptest.NewRecorder()
	service.cancel(w, stop)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(func(w http.ResponseWriter, r *http.Request) { service.resume(w, r, first.ID) }, "GET", "/chat/"+first.ID, nil)
	if !strings.Contains(w.Body.String(), `"refunded":true`) {
		t.Fatal(w.Body.String())
	}
	if got, err := manager.BillingSnapshot(ctx, user, day); err != nil || got != before {
		t.Fatal("refund changed shared counters", got, err)
	}
	if state, err := manager.ReservationState(ctx, webReservation(user, followup.ID), user); err != nil || state != "refunded" {
		t.Fatal(state, err)
	}
	third := webInput{ID: strings.Repeat("3", 32), Question: "third"}
	answer <- true
	w = call(service.chat, "POST", "/chat", third)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") || !strings.Contains(w.Body.String(), "**回答**") {
		t.Fatal(w.Code, w.Body.String())
	}
	if got, err := manager.BillingSnapshot(ctx, user, day); err != nil || got.PointUnits != 40 || got.SiteUsed != before.SiteUsed+1 {
		t.Fatal(got, err)
	}
	if _, err := conn.Exec(`UPDATE wiki_users SET blocked=TRUE WHERE id=?`, account); err != nil {
		t.Fatal(err)
	}
	if w = call(service.status, "GET", "/status", nil); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestNormalizeWikiSelection(t *testing.T) {
	if got := normalizeSelection("**公开资料**包含 [链接](https://example.test) 和 `代码`\n\n[[另一个条目]]"); got != "公开资料包含 链接 和 代码 另一个条目" {
		t.Fatal(got)
	}
}
