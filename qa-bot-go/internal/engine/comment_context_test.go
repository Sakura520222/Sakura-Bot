package engine

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
)

type emptyCommentInbox struct{}

func (emptyCommentInbox) Take(bool) []TurnInput { return nil }

func TestCommentWakeWordSupplementsCustomPersona(t *testing.T) {
	const persona = "你现在是 Sakura-Bot 驱动的智能资讯助手。所有回答必须依据给定资料，不得凭空编造。"
	for _, wake := range []string{"小妲", "资讯助手"} {
		for _, fallback := range []bool{false, true} {
			t.Run(wake+strconv.FormatBool(fallback), func(t *testing.T) {
				entries := []scriptEntry{{lines: []string{contentLine("解释原帖")}}}
				if fallback {
					entries = append([]scriptEntry{{status: 500}}, entries...)
				}
				h := newHarness(t, entries)
				h.engine.persona = constPersona(persona)
				ctx := WithCommentWakeWord(WithScope(context.Background(), "chat:-1001:post:10"), wake)
				ctx = WithPublicContext(ctx, func(context.Context) string {
					return "帖子署名：投稿者；原文未说明旧测试发生了什么。"
				})
				ctx = WithTurn(ctx, emptyCommentInbox{})
				query := "　" + wake + "这什么意思？ "
				collectChunks(t, h.engine.ProcessQueryStream(ctx, query, 7, ""))
				for i := range entries {
					req := h.llm.requestBody(t, i)
					system := req.Messages[0].text()
					for _, want := range []string{persona, strconv.Quote(wake), "用户对当前助手的称呼", "不代表帖子作者、投稿者或消息来源", "缺少证据", "系统权限或工具权限"} {
						if !strings.Contains(system, want) {
							t.Fatalf("system context missing %q", want)
						}
					}
					if req.Messages[len(req.Messages)-1].text() != query {
						t.Fatal("original user query was rewritten")
					}
					if h.engine.persona.Persona() != persona {
						t.Fatal("custom persona replaced")
					}
				}
			})
		}
	}
}

func TestPrivatePromptDoesNotGainCommentPersona(t *testing.T) {
	h := newHarness(t, nil)
	h.engine.persona = constPersona("自定义私聊人格")
	prompt := h.engine.buildSystemPrompt(context.Background(), "", true)
	if !strings.Contains(prompt, "自定义私聊人格") || strings.Contains(prompt, "【评论区交互上下文】") {
		t.Fatal("comment context leaked into private chat")
	}
}

func TestTelegramDiscussionFactsReachModelWithoutRewritingQuery(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(strconv.FormatBool(fallback), func(t *testing.T) {
			entries := []scriptEntry{{lines: []string{contentLine("当前关联频道是 Nahida")}}}
			if fallback {
				entries = append([]scriptEntry{{status: 500}}, entries...)
			}
			h := newHarness(t, entries)
			facts := contracts.TelegramContext{
				ChatID: -1001, ChatTitle: "Firefly Chat", AssistantID: 123, AssistantUsername: "NahidaQABot",
				Channels: []contracts.TelegramChannel{{ID: -1005, Title: "Nahida", Username: "Nahida", URL: "https://t.me/Nahida"}},
			}
			ctx := contracts.WithTelegramContext(WithScope(context.Background(), "chat:-1001:thread:0"), facts)
			ctx = WithTurn(ctx, emptyCommentInbox{})
			query := "@NahidaQABot 你是区"
			collectChunks(t, h.engine.ProcessQueryStream(ctx, query, -1001, ""))
			for i := range entries {
				req := h.llm.requestBody(t, i)
				system := req.Messages[0].text()
				for _, want := range []string{"Firefly Chat", "NahidaQABot", "https://t.me/Nahida", `"chat_id":-1001`, "assistant_username", "当前聊天"} {
					if !strings.Contains(system, want) {
						t.Fatalf("Telegram discussion fact missing from system prompt: %q", want)
					}
				}
				if req.Messages[len(req.Messages)-1].text() != query {
					t.Fatal("bot mention rewrote original query")
				}
			}
		})
	}
}
