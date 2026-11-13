package contracts

import "context"

type anonymousQuotaKey struct{}

type anonymousQuota struct {
	chatID     int64
	dailyLimit int
	channel    string
}

// WithAnonymousQuota carries the allowance of an authorized linked discussion.
// The negative chat ID identifies its shared group actor, never a Telegram user.
func WithAnonymousQuota(ctx context.Context, chatID int64, dailyLimit int, channel string) context.Context {
	return context.WithValue(ctx, anonymousQuotaKey{}, anonymousQuota{chatID, dailyLimit, channel})
}

func AnonymousChannel(ctx context.Context) string {
	allowance, _ := ctx.Value(anonymousQuotaKey{}).(anonymousQuota)
	return allowance.channel
}

func AnonymousQuota(ctx context.Context) (chatID int64, dailyLimit int, ok bool) {
	allowance, found := ctx.Value(anonymousQuotaKey{}).(anonymousQuota)
	return allowance.chatID, allowance.dailyLimit, found && allowance.chatID < 0 && allowance.dailyLimit > 0 && allowance.dailyLimit <= 1000
}
