package tools

import (
	"context"
	"errors"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"strings"
	"sync"
	"testing"
)

type wikiFake struct{ fail bool }

func (w wikiFake) ReadWiki(_ context.Context, name string, options contracts.WikiOptions) (map[string]any, error) {
	if w.fail {
		return nil, errors.New("private provider error must not escape")
	}
	return map[string]any{"query": options.Query, "channel_id": options.Channel, "operation": name, "publication_id": "published"}, nil
}
func TestWikiToolsFallbackAndRequestIsolation(t *testing.T) {
	disabled := NewToolExecutor(nil, nil, nil)
	if result := disabled.Execute(context.Background(), "wiki_search", nil); !strings.Contains(result, "wiki_not_initialized") {
		t.Fatal(result)
	}
	failed := NewToolExecutor(nil, nil, nil, wikiFake{fail: true})
	result := failed.Execute(context.Background(), "wiki_search", nil)
	if strings.Contains(result, "private provider") || !strings.Contains(result, "error") {
		t.Fatal(result)
	}
	var group sync.WaitGroup
	for _, query := range []string{"request_A", "request_B"} {
		group.Add(1)
		go func(query string) {
			defer group.Done()
			executor := NewToolExecutor(nil, nil, nil, wikiFake{})
			response := executor.Execute(context.Background(), "wiki_search", map[string]any{"query": query, "channel_id": "public"})
			if !strings.Contains(response, query) {
				t.Error(response)
			}
			if len(executor.GetAllResults()) != 0 {
				t.Error("Wiki results leaked into raw-source context")
			}
		}(query)
	}
	group.Wait()
}
