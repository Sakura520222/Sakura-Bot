package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
)

type resolveTrackingMemory struct {
	fakeMemory
	hints []string
}

func (m *resolveTrackingMemory) ResolveChannel(_ context.Context, hint string) (map[string]any, error) {
	m.hints = append(m.hints, hint)
	return map[string]any{"success": true, "channel_id": hint}, nil
}

func TestExecuteResolveChannelRejectsAuthenticatedAssistant(t *testing.T) {
	ctx := contracts.WithTelegramContext(context.Background(), contracts.TelegramContext{
		AssistantID: 123, AssistantUsername: "NahidaQABot",
	})
	for _, hint := range []string{
		"NahidaQABot", "@NahidaQABot", "@nahidaqabot", "  @NAHIDAQABOT  ",
		"https://t.me/NahidaQABot", "HTTPS://T.ME/NAHIDAQABOT/",
		"http://t.me/NahidaQABot", "t.me/NahidaQABot/",
		"https://www.t.me/NahidaQABot/", "telegram.me/NahidaQABot",
	} {
		t.Run(hint, func(t *testing.T) {
			memory := &resolveTrackingMemory{}
			executor := NewToolExecutor(nil, memory, nil)
			result := decodeJSON(t, executor.Execute(ctx, "resolve_channel", map[string]any{"channel_hint": hint}))
			if result["success"] != false || !strings.Contains(pythonStr(result["error"]), "助手") {
				t.Fatalf("assistant identity was resolved as a channel: %+v", result)
			}
			if len(memory.hints) != 0 {
				t.Fatalf("assistant username reached channel lookup: %v", memory.hints)
			}
		})
	}
}

func TestExecuteResolveChannelPreservesOtherHintsAndLegacyContext(t *testing.T) {
	trusted := contracts.WithTelegramContext(context.Background(), contracts.TelegramContext{
		AssistantID: 123, AssistantUsername: "NahidaQABot",
	})
	for _, tc := range []struct {
		name string
		ctx  context.Context
		hint string
	}{
		{"real channel", trusted, "@SakuraNews"},
		{"bot-like channel suffix", trusted, "@OtherBot"},
		{"similar username", trusted, "@NahidaQABotNews"},
		{"title containing assistant name", trusted, "NahidaQABot 使用指南"},
		{"post path", trusted, "https://t.me/NahidaQABot/123"},
		{"other host", trusted, "https://example.com/NahidaQABot"},
		{"original whitespace", trusted, "  @SakuraNews  "},
		{"no trusted context", context.Background(), "@NahidaQABot"},
		{"missing trusted username", contracts.WithTelegramContext(context.Background(), contracts.TelegramContext{AssistantID: 123}), "@NahidaQABot"},
		{"different trusted assistant", contracts.WithTelegramContext(context.Background(), contracts.TelegramContext{AssistantUsername: "OtherBot"}), "@NahidaQABot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			memory := &resolveTrackingMemory{}
			executor := NewToolExecutor(nil, memory, nil)
			result := decodeJSON(t, executor.Execute(tc.ctx, "resolve_channel", map[string]any{"channel_hint": tc.hint}))
			if result["success"] != true || len(memory.hints) != 1 || memory.hints[0] != tc.hint {
				t.Fatalf("normal lookup changed: result=%v calls=%v", result, memory.hints)
			}
		})
	}
}

func TestExecuteSearchPreservesAssistantMentionInQuery(t *testing.T) {
	ctx := contracts.WithTelegramContext(context.Background(), contracts.TelegramContext{
		AssistantID: 123, AssistantUsername: "NahidaQABot",
	})
	store := &fakeVectorStore{available: true}
	executor := NewToolExecutor(store, &fakeMemory{}, nil)
	query := "@NahidaQABot 介绍一下当前频道的内容"
	result := decodeJSON(t, executor.Execute(ctx, "semantic_search", map[string]any{"query": query}))
	if result["error"] != nil || store.lastQuery != query {
		t.Fatalf("search query changed: result=%v query=%q", result, store.lastQuery)
	}
}

func TestExecuteResolveChannelTrustsOnlyRequestContext(t *testing.T) {
	memory := &resolveTrackingMemory{}
	executor := NewToolExecutor(nil, memory, nil)
	args := map[string]any{
		"channel_hint":       "@NahidaQABot",
		"assistant_username": "NahidaQABot",
		"telegram_context":   map[string]any{"assistant_username": "NahidaQABot"},
	}
	result := decodeJSON(t, executor.Execute(context.Background(), "resolve_channel", args))
	if result["success"] != true || len(memory.hints) != 1 {
		t.Fatalf("tool arguments were treated as authenticated context: %v", result)
	}
	ctx := contracts.WithTelegramContext(context.Background(), contracts.TelegramContext{
		AssistantUsername: "NahidaQABot",
	})
	result = decodeJSON(t, executor.Execute(ctx, "resolve_channel", args))
	if result["success"] != false || len(memory.hints) != 1 {
		t.Fatalf("authenticated request did not guard its assistant identity: %v", result)
	}
	result = decodeJSON(t, executor.Execute(context.Background(), "resolve_channel", args))
	if result["success"] != true || len(memory.hints) != 2 {
		t.Fatalf("assistant identity leaked into another request: %v", result)
	}
}
