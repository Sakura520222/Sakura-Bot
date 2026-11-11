package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
)

type restoringConversationStore struct {
	fakeStore
	sessions map[sessionKey]sessionEntry
}

func (s *restoringConversationStore) GetRecentConversationSession(_ context.Context, user int64, scope string, _ time.Time) (string, time.Time, error) {
	entry := s.sessions[sessionKey{user, scope}]
	return entry.sessionID, entry.lastActive, nil
}

func TestRestartRestoresScopedAssistantHistory(t *testing.T) {
	now := time.Now().UTC()
	store := &restoringConversationStore{sessions: map[sessionKey]sessionEntry{{7, "post:10"}: {sessionID: "existing-session", lastActive: now.Add(-4 * time.Minute)}}}
	store.history = []db.HistoryItem{{Role: "user", Content: "小妲这什么意思"}, {Role: "assistant", Content: "我之前解释了原帖"}}
	h := newHarness(t, []scriptEntry{{lines: []string{contentLine("继续解释")}}})
	h.engine.conversations = NewConversations(store)
	h.engine.conversations.now = func() time.Time { return now }
	ctx := WithScope(context.Background(), "post:10")
	chunks := collectChunks(t, h.engine.ProcessQueryStream(ctx, "你刚才说了什么？", 7, ""))
	for _, chunk := range chunks {
		if chunk.Ctrl == contracts.CtrlNewSession {
			t.Fatal("restart discarded the active session")
		}
	}
	request := h.llm.requestBody(t, 0)
	if len(request.Messages) != 4 || request.Messages[2].Role != "assistant" || request.Messages[2].text() != "我之前解释了原帖" {
		t.Fatalf("previous assistant role lost: %+v", request.Messages)
	}
	id, fresh := h.engine.conversations.GetOrCreateScopedSession(7, "post:10")
	if fresh || id != "existing-session" {
		t.Fatal(id, fresh)
	}
}
func TestSessionRestoreRespectsUserScopeAndTimeout(t *testing.T) {
	now := time.Now().UTC()
	store := &restoringConversationStore{sessions: map[sessionKey]sessionEntry{
		{7, "post:10"}: {sessionID: "expired", lastActive: now.Add(-30 * time.Minute)},
		{7, "post:11"}: {sessionID: "fresh", lastActive: now.Add(-time.Minute)},
	}}
	c := NewConversations(store)
	c.now = func() time.Time { return now }
	for _, key := range []sessionKey{{7, "post:10"}, {7, "private"}, {8, "post:11"}} {
		id, fresh := c.GetOrCreateScopedSession(key.User, key.Scope)
		if !fresh || id == "fresh" || id == "expired" {
			t.Fatalf("invalid restore for %+v: %s", key, id)
		}
	}
	if id, fresh := c.GetOrCreateScopedSession(7, "post:11"); fresh || id != "fresh" {
		t.Fatal(id, fresh)
	}
}
