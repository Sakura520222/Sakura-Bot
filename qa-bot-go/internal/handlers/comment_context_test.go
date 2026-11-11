package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/tg"
)

type commentContextProbe struct{ seen chan [2]string }

func (p *commentContextProbe) ProcessQueryStream(ctx context.Context, query string, user int64, hint string) <-chan engine.Chunk {
	p.seen <- [2]string{engine.CommentWakeWord(ctx), query}
	fake := fakeEngine{chunks: []engine.Chunk{engine.TextChunk("ok"), engine.CtrlChunk(contracts.CtrlDone)}}
	return fake.ProcessQueryStream(ctx, query, user, hint)
}
func TestAIWorkerPreservesCommentWakeContext(t *testing.T) {
	pool := NewAIPool(context.Background(), 1, 1, time.Minute)
	defer pool.Close()
	c := newTestCommands(&fakeTG{sendOK: true}, &fakeQuota{result: contracts.QuotaCheckResult{Allowed: true}}, &fakeSessions{}, &fakeEngine{})
	probe := &commentContextProbe{seen: make(chan [2]string, 1)}
	c.engine = probe
	c.pool = pool
	ctx := engine.WithCommentWakeWord(engine.WithScope(context.Background(), "chat:-1001:post:10"), "小妲")
	query := " 小妲这什么意思 "
	msg := qaTextUpdate(query, 7).Message
	c.submitAI(ctx, msg, 7, query, "", tg.PlaceholderThinking, "test")
	select {
	case seen := <-probe.seen:
		if seen != [2]string{"小妲", query} {
			t.Fatalf("worker lost request semantics: %v", seen)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not receive query")
	}
}
