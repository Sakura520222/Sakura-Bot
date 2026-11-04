package handlers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/media"
	"github.com/go-telegram/bot/models"
)

type imageEngine struct {
	calls   chan []media.Ref
	mu      sync.Mutex
	queries []string
}

func (e *imageEngine) ProcessQueryStream(ctx context.Context, query string, _ int64, _ string) <-chan engine.Chunk {
	req := media.RequestFrom(ctx)
	lease, _ := req.Store.Begin(req.Scope, "session", query, req.Reply)
	var refs []media.Ref
	if lease != nil {
		refs = lease.Refs
		req.Store.Finish(lease, true)
	}
	e.mu.Lock()
	e.queries = append(e.queries, query)
	e.mu.Unlock()
	e.calls <- refs
	ch := make(chan engine.Chunk, 2)
	ch <- engine.TextChunk("answer")
	ch <- engine.CtrlChunk(contracts.CtrlDone)
	close(ch)
	return ch
}
func imageUpdate(id int, caption, album string) *models.Update {
	upd := qaTextUpdate("", 1)
	upd.Message.ID = id
	upd.Message.Photo = []models.PhotoSize{{FileID: "telegram", Width: 10, Height: 10, FileSize: 100}}
	upd.Message.Caption, upd.Message.MediaGroupID = caption, album
	return upd
}
func TestPhotoThenQuestionAndCaptionDocumentRoute(t *testing.T) {
	c := newTestCommands(&fakeTG{sendOK: true}, &fakeQuota{result: contracts.QuotaCheckResult{Allowed: true, IsAdmin: true}}, &fakeSessions{}, &fakeEngine{})
	e := &imageEngine{calls: make(chan []media.Ref, 4)}
	c.engine = e
	c.aiConfig = func() llm.AIConfig { return llm.AIConfig{SupportsVision: true} }
	c.Route(context.Background(), imageUpdate(1, "", ""))
	select {
	case <-e.calls:
		t.Fatal("standalone photo ran a query")
	default:
	}
	c.Route(context.Background(), qaTextUpdate("这是什么", 1))
	if refs := <-e.calls; len(refs) != 1 || refs[0].MessageID != 1 {
		t.Fatal("pending photo lost")
	}
	upd := qaTextUpdate("", 1)
	upd.Message.ID = 3
	upd.Message.Document = &models.Document{FileID: "doc", FileName: "photo.png", MimeType: "image/png", FileSize: 100}
	upd.Message.Caption = "这是什么颜色"
	c.Route(context.Background(), upd)
	if refs := <-e.calls; len(refs) != 1 || refs[0].MessageID != 3 {
		t.Fatal("caption document lost")
	}
}
func TestAlbumCombinesCaptionAndMembers(t *testing.T) {
	c := newTestCommands(&fakeTG{sendOK: true}, &fakeQuota{result: contracts.QuotaCheckResult{Allowed: true, IsAdmin: true}}, &fakeSessions{}, &fakeEngine{})
	e := &imageEngine{calls: make(chan []media.Ref, 2)}
	c.engine = e
	c.aiConfig = func() llm.AIConfig { return llm.AIConfig{SupportsVision: true} }
	c.Route(context.Background(), imageUpdate(3, "比较这些图片", "group"))
	c.Route(context.Background(), imageUpdate(1, "第一张的补充", "group"))
	c.Route(context.Background(), imageUpdate(2, "", "group"))
	select {
	case refs := <-e.calls:
		if len(refs) != 3 || refs[0].MessageID != 1 || refs[2].MessageID != 3 {
			t.Fatal("album lost or reordered members")
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.queries[0] != "第一张的补充\n比较这些图片" {
			t.Fatal("album captions lost")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("album did not flush")
	}
}
