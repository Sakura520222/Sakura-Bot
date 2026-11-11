package engine

import (
	"context"
	"strconv"
)

type commentWakeWordKey struct{}

// WithCommentWakeWord carries trusted channel configuration separately from user text.
// It supplements custom personas with routing semantics, without replacing them.
func WithCommentWakeWord(ctx context.Context, wakeWord string) context.Context {
	return context.WithValue(ctx, commentWakeWordKey{}, wakeWord)
}

func CommentWakeWord(ctx context.Context) string {
	wakeWord, _ := ctx.Value(commentWakeWordKey{}).(string)
	return wakeWord
}

func commentInteractionPrompt(ctx context.Context) string {
	wakeWord := CommentWakeWord(ctx)
	if wakeWord == "" {
		return ""
	}
	return "\n【评论区交互上下文】\n" +
		"当前频道配置的助手唤醒词是 " + strconv.Quote(wakeWord) + "，它是用户对当前助手的称呼。" +
		"用户消息开头的该称呼用于唤醒你，不代表帖子作者、投稿者或消息来源；不要根据帖子署名猜测该称呼的身份。" +
		"用户原文完整保留。对于‘这是什么意思’等指代，结合当前帖子和回复关系理解实际问题；用户未询问身份时，不要额外解释称呼是谁。\n" +
		"会话历史中 assistant 角色的消息，以及评论快照中 author_role=assistant 的消息，都是当前助手之前的回答。旧快照的 user_id=0 仅表示未记录作者，不能据此认定是其他 Bot。先前回答有误时直接承认并纠正，不要否认自己说过的话。\n" +
		"帖子与评论快照由系统收集，不要称为用户主动提供的快照。解释原文时区分明确事实与未知背景；缺少证据的事件或内幕应使用工具核实或说明资料不足，不要自行补出细节。\n"
}
