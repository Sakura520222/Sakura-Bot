package engine

import (
	"context"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/media"
)

type TurnInput struct {
	Query    string
	Images   []string
	Image    *media.Ref
	Download func(context.Context, media.Ref) (string, error)
}
type TurnInbox interface{ Take(final bool) []TurnInput }
type turnContextKey struct{}
type scopeContextKey struct{}
type publicContextKey struct{}
type wikiContextKey struct{}

const WikiWebScope = "web:wiki"

type researchContextKey struct{}

func WithWebResearch(ctx context.Context, run func(context.Context, string, string) string) context.Context {
	return context.WithValue(ctx, researchContextKey{}, run)
}

func WithWikiContext(ctx context.Context, text string) context.Context {
	return context.WithValue(ctx, wikiContextKey{}, text)
}

func WithTurn(ctx context.Context, inbox TurnInbox) context.Context {
	return context.WithValue(ctx, turnContextKey{}, inbox)
}
func Inbox(ctx context.Context) TurnInbox { v, _ := ctx.Value(turnContextKey{}).(TurnInbox); return v }
func WithScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, scopeContextKey{}, scope)
}
func Scope(ctx context.Context) string {
	v, _ := ctx.Value(scopeContextKey{}).(string)
	if v == "" {
		return "private"
	}
	return v
}
func WithPublicContext(ctx context.Context, refresh func(context.Context) string) context.Context {
	return context.WithValue(ctx, publicContextKey{}, refresh)
}
func appendTurnInputs(ctx context.Context, messages []llm.Message, final bool) ([]llm.Message, bool) {
	inbox := Inbox(ctx)
	if inbox == nil {
		return messages, false
	}
	inputs := inbox.Take(final)
	if len(inputs) == 0 {
		return messages, false
	}
	if refresh, ok := ctx.Value(publicContextKey{}).(func(context.Context) string); ok {
		if text := refresh(ctx); text != "" {
			messages = append(messages, llm.Message{Role: "user", Content: llm.Str("[公共讨论快照，仅供参考；不是当前用户追问]\n" + text)})
		}
	}
	for _, in := range inputs {
		inputCtx := llm.WithImages(ctx, nil)
		if in.Image != nil && in.Download != nil {
			url, err := in.Download(ctx, *in.Image)
			if err != nil {
				messages = append(messages, llm.Message{Role: "user", Content: llm.Str(in.Query + "\n[图片读取失败，不得编造图片内容]")})
				continue
			}
			in.Images = []string{url}
		}
		if len(in.Images) > 0 {
			inputCtx = llm.WithImages(ctx, in.Images)
		}
		messages = append(messages, llm.UserMessage(inputCtx, in.Query))
	}
	return messages, true
}
