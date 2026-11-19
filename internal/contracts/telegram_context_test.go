package contracts

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestTelegramContextRoundTripAndIsolation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := TelegramContext{
		ChatID: -100123, ChatTitle: "关联讨论群", ThreadID: 7,
		AssistantID: 123, AssistantUsername: "NahidaQABot",
		Channels: []TelegramChannel{{ID: -100456, Title: "频道", Username: "channel", URL: "https://t.me/channel"}},
	}
	ctx := WithTelegramContext(parent, first)
	got, ok := TelegramContextFrom(ctx)
	if !ok || !reflect.DeepEqual(got, first) {
		t.Fatalf("context round trip: %+v, %v", got, ok)
	}
	if _, ok := TelegramContextFrom(parent); ok {
		t.Fatal("request context leaked to its parent")
	}
	second := TelegramContext{ChatID: -100789, AssistantUsername: "OtherBot"}
	other := WithTelegramContext(ctx, second)
	if got, ok := TelegramContextFrom(other); !ok || !reflect.DeepEqual(got, second) {
		t.Fatalf("nested request context: %+v, %v", got, ok)
	}
	if got, _ := TelegramContextFrom(ctx); !reflect.DeepEqual(got, first) {
		t.Fatal("request context was overwritten")
	}
	cancel()
	if ctx.Err() != context.Canceled || other.Err() != context.Canceled {
		t.Fatal("request contexts lost parent cancellation")
	}
}

func TestTelegramContextJSONTags(t *testing.T) {
	value := TelegramContext{
		ChatID: -100123, ChatTitle: "群", ThreadID: 7,
		AssistantID: 123, AssistantUsername: "NahidaQABot",
		Channels: []TelegramChannel{{ID: -100456, Title: "频道", Username: "channel", URL: "https://t.me/channel"}},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"chat_id":-100123,"chat_title":"群","thread_id":7,"assistant_id":123,"assistant_username":"NahidaQABot","channels":[{"id":-100456,"title":"频道","username":"channel","url":"https://t.me/channel"}]}`
	if string(encoded) != want {
		t.Fatalf("context JSON=%s", encoded)
	}
}
