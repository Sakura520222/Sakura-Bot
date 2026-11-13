package contracts

import "context"

// TelegramContext carries Telegram identities verified by the message handler.
type TelegramContext struct {
	ChatID            int64             `json:"chat_id"`
	ChatTitle         string            `json:"chat_title"`
	ThreadID          int               `json:"thread_id"`
	AssistantID       int64             `json:"assistant_id"`
	AssistantUsername string            `json:"assistant_username"`
	Channels          []TelegramChannel `json:"channels"`
}

type TelegramChannel struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Username string `json:"username"`
	URL      string `json:"url"`
}

type telegramContextKey struct{}

func WithTelegramContext(ctx context.Context, telegram TelegramContext) context.Context {
	return context.WithValue(ctx, telegramContextKey{}, telegram)
}

func TelegramContextFrom(ctx context.Context) (TelegramContext, bool) {
	telegram, ok := ctx.Value(telegramContextKey{}).(TelegramContext)
	return telegram, ok
}
