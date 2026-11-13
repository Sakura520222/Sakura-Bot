package db

import (
	"context"
	"fmt"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMessageReservationsConcurrentAndIdempotent(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if err := m.InitReservations(ctx); err != nil {
		t.Fatal(err)
	}
	user := uniqueUserID()
	cleanupQuota(t, m, user)
	t.Cleanup(func() { m.DB.Exec("DELETE FROM qa_message_reservations WHERE user_id=?", user) })
	limits := contracts.QuotaLimits{UserDaily: 3, TotalDaily: 2147483647}
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, e := m.ReserveMessage(ctx, fmt.Sprintf("test:%d:%d", user, i), user, limits, false)
			if e != nil {
				t.Error(e)
			}
			if r.Allowed {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 3 {
		t.Fatalf("accepted=%d", accepted.Load())
	}
	var id string
	if e := m.DB.QueryRow("SELECT id FROM qa_message_reservations WHERE user_id=? LIMIT 1", user).Scan(&id); e != nil {
		t.Fatal(e)
	}
	r, e := m.ReserveMessage(ctx, id, user, limits, false)
	if e != nil || r.Allowed {
		t.Fatalf("duplicate admitted: %+v %v", r, e)
	}
	for i := 0; i < 5; i++ {
		if e := m.FinishReservation(ctx, id, false); e != nil {
			t.Fatal(e)
		}
	}
	used, _, e := m.GetQuotaUsage(ctx, user, "")
	if e != nil || used != 2 {
		t.Fatalf("refund changed wrong amount: %d %v", used, e)
	}
}

func TestAnonymousGroupReservationsHaveHardCapAndRefund(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if err := m.InitReservations(ctx); err != nil {
		t.Fatal(err)
	}
	group := -uniqueUserID()
	day := "2099-12-20"
	t.Cleanup(func() {
		for _, table := range []string{"qa_message_reservations", "usage_quota", "qa_points", "qa_points_ledger"} {
			m.DB.Exec("DELETE FROM "+table+" WHERE user_id=?", group)
		}
		m.DB.Exec("DELETE FROM qa_daily_total WHERE billing_day=?", day)
	})
	if _, err := m.DB.Exec("INSERT INTO qa_points(user_id,units) VALUES(?,100)", group); err != nil {
		t.Fatal(err)
	}
	limits := contracts.QuotaLimits{UserDaily: 2, TotalDaily: 3}
	for i := 0; i < 2; i++ {
		result, err := m.ReserveMessageOnDay(ctx, fmt.Sprintf("group:%d:%d", group, i), group, limits, true, day)
		if err != nil || !result.Allowed || result.IsAdmin || result.Remaining != int64(1-i) {
			t.Fatalf("group reservation %d: %+v %v", i, result, err)
		}
	}
	result, err := m.ReserveMessageOnDay(ctx, fmt.Sprintf("group:%d:cap", group), group, limits, true, day)
	if err != nil || result.Allowed || !strings.Contains(result.Message, "匿名") {
		t.Fatalf("group cap bypassed: %+v %v", result, err)
	}
	points, err := m.Points(ctx, group)
	if err != nil || points != 100 {
		t.Fatalf("personal points changed: %d %v", points, err)
	}
	id := fmt.Sprintf("group:%d:0", group)
	for i := 0; i < 2; i++ {
		if err := m.FinishReservation(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	used, total, err := m.BillingUsage(ctx, group, day)
	if err != nil || used != 1 || total != 1 {
		t.Fatalf("group refund: used=%d total=%d err=%v", used, total, err)
	}
	result, err = m.ReserveMessageOnDay(ctx, id, group, limits, false, day)
	if err != nil || result.Allowed {
		t.Fatalf("duplicate refunded message re-admitted: %+v %v", result, err)
	}
	limits.TotalDaily = 1
	result, err = m.ReserveMessageOnDay(ctx, fmt.Sprintf("group:%d:global", group), group, limits, true, day)
	if err != nil || result.Allowed || !strings.Contains(result.Message, "全站") {
		t.Fatalf("global cap bypassed: %+v %v", result, err)
	}
}

func TestAnonymousGroupConcurrentReservationsShareLimit(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if err := m.InitReservations(ctx); err != nil {
		t.Fatal(err)
	}
	group := -uniqueUserID()
	day := "2099-12-19"
	t.Cleanup(func() {
		m.DB.Exec("DELETE FROM qa_message_reservations WHERE user_id=?", group)
		m.DB.Exec("DELETE FROM usage_quota WHERE user_id=?", group)
		m.DB.Exec("DELETE FROM qa_daily_total WHERE billing_day=?", day)
	})
	limits := contracts.QuotaLimits{UserDaily: 3, TotalDaily: 10}
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			result, err := m.ReserveMessageOnDay(ctx, fmt.Sprintf("group:%d:message:%d", group, i), group, limits, false, day)
			if err != nil {
				t.Error(err)
			}
			if result.Allowed {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 3 {
		t.Fatalf("concurrent shared group cap admitted %d messages", accepted.Load())
	}
}
func TestPointsCheckinGlobalCapAndRecovery(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if e := m.InitReservations(ctx); e != nil {
		t.Fatal(e)
	}
	user := uniqueUserID()
	day := "2099-12-21"
	cleanupQuota(t, m, user)
	t.Cleanup(func() {
		for _, table := range []string{"qa_message_reservations", "qa_points", "qa_checkins", "qa_points_ledger"} {
			m.DB.Exec("DELETE FROM "+table+" WHERE user_id=?", user)
		}
		m.DB.Exec("DELETE FROM qa_daily_total WHERE billing_day=?", day)
	})
	var wg sync.WaitGroup
	var awards atomic.Int32
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			units, added, e := m.Checkin(ctx, user, day)
			if e != nil {
				t.Error(e)
			}
			if units < 1 || units > 50 {
				t.Errorf("invalid award %d", units)
			}
			if added {
				awards.Add(1)
			}
		}()
	}
	wg.Wait()
	if awards.Load() != 1 {
		t.Fatal("duplicate checkin", awards.Load())
	}
	m.DB.Exec("UPDATE qa_points SET units=20 WHERE user_id=?", user)
	limits := contracts.QuotaLimits{UserDaily: 1, TotalDaily: 3}
	for i := 0; i < 3; i++ {
		r, e := m.ReserveMessageOnDay(ctx, fmt.Sprintf("points:%d:%d", user, i), user, limits, false, day)
		if e != nil || !r.Allowed {
			t.Fatalf("reserve %d: %+v %v", i, r, e)
		}
	}
	n, _ := m.Points(ctx, user)
	if n != 0 {
		t.Fatal("wrong points debit", n)
	}
	m.DB.Exec("UPDATE qa_points SET units=100 WHERE user_id=?", user)
	r, e := m.ReserveMessageOnDay(ctx, fmt.Sprintf("points:%d:cap", user), user, limits, false, day)
	if e != nil || r.Allowed {
		t.Fatal("points bypassed global cap", r, e)
	}
	for i := 0; i < 3; i++ {
		if e = m.FinishReservation(ctx, fmt.Sprintf("points:%d:1", user), false); e != nil {
			t.Fatal(e)
		}
	}
	n, _ = m.Points(ctx, user)
	if n != 110 {
		t.Fatal("refund not idempotent", n)
	}
	// Recovery handles remaining reservations, including the free message on its original day.
	if e = m.RecoverReservations(ctx); e != nil {
		t.Fatal(e)
	}
	free, _, _ := m.GetQuotaUsage(ctx, user, day)
	if free != 0 {
		t.Fatal("cross-day free refund failed", free)
	}
	n, _ = m.Points(ctx, user)
	if n != 120 {
		t.Fatal("restart refund failed", n)
	}
}
func TestCommentSnapshotEditAndPostIsolation(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if e := m.InitComments(ctx); e != nil {
		t.Fatal(e)
	}
	chat := -uniqueUserID()
	t.Cleanup(func() { m.DB.Exec("DELETE FROM qa_comments WHERE chat_id=?", chat) })
	for _, r := range []CommentRecord{{ChatID: chat, MessageID: 1, RootID: 1, Text: "original", Date: 1}, {ChatID: chat, MessageID: 2, RootID: 1, Text: "comment", Date: 2}, {ChatID: chat, MessageID: 3, RootID: 3, Text: "other post", Date: 3}, {ChatID: chat, MessageID: 2, RootID: 1, Text: "edited comment", Date: 2, Edited: true}} {
		if e := m.SaveComment(ctx, r); e != nil {
			t.Fatal(e)
		}
	}
	s, e := m.CommentContext(ctx, chat, 1)
	if e != nil || !strings.Contains(s, "edited comment") || strings.Contains(s, "other post") {
		t.Fatal(s, e)
	}
	if m.CommentRoot(ctx, chat, 2) != 1 {
		t.Fatal("root mapping lost")
	}
}
func TestAssistantRuntimeLease(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	release, e := m.HoldAssistantLease(ctx, func() {})
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if second, e := m.HoldAssistantLease(ctx, func() {}); e == nil {
		second()
		t.Fatal("two runtimes acquired lease")
	}
}
func TestScopedHistoryClearPreservesOtherPosts(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	user := uniqueUserID()
	t.Cleanup(func() { m.ClearUserConversations(ctx, user, "") })
	for _, scope := range []string{"post:a", "post:b", "private"} {
		var metadata map[string]any
		if scope != "private" {
			metadata = map[string]any{"scope_key": scope}
		}
		if e := m.SaveConversation(ctx, user, scope, "user", "text", metadata); e != nil {
			t.Fatal(e)
		}
	}
	if n, e := m.ClearScopeConversations(ctx, user, "post:a"); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if n, e := m.ClearScopeConversations(ctx, user, "private"); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	history, e := m.GetConversationHistory(ctx, user, "post:b", 20)
	if e != nil || len(history) != 1 {
		t.Fatal("other post history lost", e)
	}
}

func TestRecentHistoryAndScopeRecovery(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	user := uniqueUserID()
	t.Cleanup(func() { m.ClearUserConversations(ctx, user, "") })
	for i := 0; i < 25; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := m.SaveConversation(ctx, user, "same-session", role, fmt.Sprint(i), map[string]any{"scope_key": "post:10"}); err != nil {
			t.Fatal(err)
		}
	}
	history, err := m.GetConversationHistory(ctx, user, "same-session", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 20 || history[0].Content != "5" || history[19].Content != "24" {
		t.Fatalf("not the latest history in stable order: %+v", history)
	}
	since := time.Now().UTC().Add(-30 * time.Minute)
	id, _, err := m.GetRecentConversationSession(ctx, user, "post:10", since)
	if err != nil || id != "same-session" {
		t.Fatal(id, err)
	}
	for _, scope := range []string{"private", "post:11"} {
		id, _, err = m.GetRecentConversationSession(ctx, user, scope, since)
		if err != nil || id != "" {
			t.Fatal("scope leak", id, err)
		}
	}
	if _, err = m.ClearScopeConversations(ctx, user, "post:10"); err != nil {
		t.Fatal(err)
	}
	id, _, err = m.GetRecentConversationSession(ctx, user, "post:10", since)
	if err != nil || id != "" {
		t.Fatal("cleared history restored", id, err)
	}
}
func TestAssistantCommentIdentityPreservesLegacyText(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if err := m.InitComments(ctx); err != nil {
		t.Fatal(err)
	}
	chat := -uniqueUserID()
	t.Cleanup(func() { m.DB.Exec("DELETE FROM qa_comments WHERE chat_id=?", chat) })
	for _, r := range []CommentRecord{{ChatID: chat, MessageID: 1, RootID: 1, UserID: 0, Text: "原来的助手回答"}, {ChatID: chat, MessageID: 2, RootID: 1, UserID: 123, AuthorRole: "assistant", Text: "新的助手回答"}} {
		if err := m.SaveComment(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.MarkAssistantComment(ctx, chat, 1, 123); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.CommentContext(ctx, chat, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"原来的助手回答", "新的助手回答", `"author_role":"assistant"`, `"user_id":123`} {
		if !strings.Contains(strings.ReplaceAll(snapshot, " ", ""), fragment) {
			t.Fatal("missing identity or text", fragment, snapshot)
		}
	}
}
