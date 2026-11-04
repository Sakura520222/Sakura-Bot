package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/config"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/embedder"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/vectors"
)

// Live probes use real read-only retrieval and model calls, without writing a
// Telegram user's conversation history or sending any Telegram messages.
type liveProbeConversationStore struct {
	mu     sync.Mutex
	answer string
}

func (s *liveProbeConversationStore) SaveConversation(_ context.Context, _ int64, _ string, role, content string, _ map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if role == "assistant" {
		s.answer = content
	}
	return nil
}
func (s *liveProbeConversationStore) GetConversationHistory(context.Context, int64, string, int) ([]db.HistoryItem, error) {
	return nil, nil
}
func (s *liveProbeConversationStore) ClearUserConversations(context.Context, int64, string) (int64, error) {
	return 0, nil
}

func TestLiveChannelHistoryRetrieval(t *testing.T) {
	if os.Getenv("SAKURA_LIVE_RETRIEVAL_PROBE") != "1" {
		t.Skip("requires explicit live deployment probe opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	cfg := config.New("data/.env", "data/config.json")
	if err := cfg.Init(); err != nil {
		t.Fatal("configuration unavailable")
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	previous := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(previous)
	vec := vectors.New(cfg.Env.Get(config.KeyVectorDBPath))
	defer vec.Close()
	query := os.Getenv("SAKURA_LIVE_RETRIEVAL_QUERY")
	if query == "" {
		query = "流萤"
	}
	store := toolVectorStore{vec: vec, emb: embedder.NewGenerator(cfg.Env), log: logger}
	hits, err := store.SearchMessages(ctx, query, 8, nil, "", "")
	if err != nil {
		t.Fatal("live message retrieval failed; inspect redacted service logs")
	}
	if len(hits) == 0 {
		t.Fatal("no historical message results for probe query")
	}
	mgr := db.New(cfg)
	if err := mgr.Open(ctx); err != nil {
		t.Fatal("database unavailable")
	}
	defer mgr.Close()
	conversationStore := &liveProbeConversationStore{}
	conversations := engine.NewConversations(conversationStore)
	qa := assembleEngine(cfg, mgr, conversations, vec, logger)
	for chunk := range qa.ProcessQueryStream(ctx, query, 1, "") {
		if chunk.Kind == engine.ChunkErr {
			t.Fatal("live agent request failed")
		}
	}
	conversationStore.mu.Lock()
	answer := conversationStore.answer
	conversationStore.mu.Unlock()
	if !strings.Contains(answer, "t.me/") {
		t.Fatal("answer did not cite any historical message links")
	}
	if !strings.Contains(logs.String(), "semantic_search") {
		t.Fatal("agent did not execute semantic search")
	}
	report := map[string]any{"message_results": len(hits), "agent_searched_history": true, "answer_has_post_links": true, "answer_characters": len([]rune(answer)), "conversation_writes_mocked": true, "telegram_delivery_mocked": true}
	data, _ := json.Marshal(report)
	t.Log(string(data))
}
