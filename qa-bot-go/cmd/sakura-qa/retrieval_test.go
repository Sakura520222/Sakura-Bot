package main

import (
	"context"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/embedder"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type embeddingTestEnv map[string]string

func (m embeddingTestEnv) Get(key string) string               { return m[key] }
func (m embeddingTestEnv) GetIntDef(_ string, def int64) int64 { return def }

func TestEmbeddingFailureIsNotAnEmptySearch(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.NotFound(w, r) }))
	defer server.Close()
	for _, available := range []bool{false, true} {
		env := embeddingTestEnv{}
		if available {
			env[embedder.EnvAPIKey] = "test-key"
			env[embedder.EnvAPIBase] = server.URL + "/v1"
		}
		store := toolVectorStore{emb: embedder.NewGenerator(env, embedder.WithLogger(testLogger())), log: testLogger()}
		// A nil vector store also proves no index is touched when embedding fails.
		for _, collection := range []string{"all", "messages", "summaries"} {
			var err error
			switch collection {
			case "all":
				_, err = store.SearchAll(context.Background(), "流萤", 10, nil, "", "")
			case "messages":
				_, err = store.SearchMessages(context.Background(), "流萤", 10, nil, "", "")
			case "summaries":
				_, err = store.SearchSimilar(context.Background(), "流萤", 10, nil, "", "")
			}
			if err == nil {
				t.Fatalf("%s returned a false no-match result", collection)
			}
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("failed all-collection search should stop after first embedding error; calls=%d", calls.Load())
	}
}
