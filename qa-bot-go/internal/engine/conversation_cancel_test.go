package engine

import (
	"context"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"sync"
	"testing"
)

type clearRaceStore struct {
	mu     sync.Mutex
	writes int
}

func (s *clearRaceStore) SaveConversation(context.Context, int64, string, string, string, map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	return nil
}
func (s *clearRaceStore) ClearUserConversations(context.Context, int64, string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.writes
	s.writes = 0
	return int64(n), nil
}
func (s *clearRaceStore) GetConversationHistory(context.Context, int64, string, int) ([]db.HistoryItem, error) {
	return nil, nil
}
func TestClearRejectsLateWrites(t *testing.T) {
	store := &clearRaceStore{}
	c := NewConversations(store)
	ctx := context.Background()
	old, _ := c.GetOrCreateSession(42)
	c.SaveMessage(ctx, 42, old, "user", "old", nil)
	c.ClearUserHistory(ctx, 42, "")
	if c.SaveMessage(ctx, 42, old, "assistant", "late", nil) {
		t.Fatal("resurrected cleared session")
	}
	current, _ := c.GetOrCreateSession(42)
	if c.SaveMessage(ctx, 42, old, "assistant", "late", nil) {
		t.Fatal("wrote stale session over new session")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if c.SaveMessage(canceled, 42, current, "assistant", "canceled", nil) {
		t.Fatal("persisted canceled reply")
	}
	if !c.SaveMessage(ctx, 42, current, "user", "new", nil) {
		t.Fatal("new session rejected")
	}
}
