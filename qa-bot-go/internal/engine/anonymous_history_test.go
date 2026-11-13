package engine

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/go-sql-driver/mysql"
)

// Exercise the production persistence path instead of fakeStore's preseeded history.
func TestAnonymousDiscussionHistoryPersistsAcrossTurnsAndRestart(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not set")
	}
	mysqlConfig, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	mysqlConfig.ParseTime = true
	mysqlConfig.Loc = time.UTC
	database, err := sql.Open("mysql", mysqlConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	manager := &db.Manager{DB: database}
	ctx := context.Background()
	if err := manager.InitDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	actor := -time.Now().UnixNano()
	otherActor := actor - 1
	defer func() {
		if _, err := database.ExecContext(ctx, "DELETE FROM conversation_history WHERE user_id IN (?,?)", actor, otherActor); err != nil {
			t.Error(err)
		}
	}()
	const firstQuestion = "小妲目前这是哪个频道"
	const firstAnswer = "这里是频道 A 的讨论群。"
	const followupQuestion = "我刚刚问了你什么"
	const topicQuestion = "仅属于另一个话题的问题"
	const otherGroupQuestion = "仅属于另一个群组的问题"
	responses := []string{firstAnswer, "你刚才问这里是哪个频道。", "继续讨论频道 A。", "另一个话题的回答。", "另一个群组的回答。", "回到原群继续。"}
	entries := make([]scriptEntry, len(responses))
	for i, response := range responses {
		entries[i] = scriptEntry{lines: []string{contentLine(response)}}
	}
	h := newHarness(t, entries)
	h.engine.conversations = NewConversations(manager)
	h.engine.conversations.log = discardLogger()
	groupScope := fmt.Sprintf("chat:%d:thread:0", actor)
	groupContext := WithTurn(WithScope(ctx, groupScope), emptyCommentInbox{})
	firstChunks := collectChunks(t, h.engine.ProcessQueryStream(groupContext, firstQuestion, actor, ""))
	if len(firstChunks) == 0 || firstChunks[len(firstChunks)-1].Ctrl != contracts.CtrlDone {
		t.Fatalf("first anonymous turn did not complete: %+v", firstChunks)
	}
	session, fresh := h.engine.conversations.GetOrCreateScopedSessionContext(groupContext, actor, groupScope)
	if fresh || session == "" {
		t.Fatal("completed anonymous turn lost its session", session, fresh)
	}
	collectChunks(t, h.engine.ProcessQueryStream(groupContext, followupQuestion, actor, ""))
	secondRequest := h.llm.requestBody(t, 1)
	if len(secondRequest.Messages) != 4 || secondRequest.Messages[1].Role != "user" || secondRequest.Messages[1].text() != firstQuestion ||
		secondRequest.Messages[2].Role != "assistant" || !strings.Contains(secondRequest.Messages[2].text(), firstAnswer) ||
		secondRequest.Messages[3].text() != followupQuestion {
		t.Fatalf("completed negative actor transcript missing from second request: %+v", secondRequest.Messages)
	}
	// A fresh Conversations instance must recover the exact negative actor/scope.
	h.engine.conversations = NewConversations(manager)
	h.engine.conversations.log = discardLogger()
	restartChunks := collectChunks(t, h.engine.ProcessQueryStream(groupContext, "请继续", actor, ""))
	for _, chunk := range restartChunks {
		if chunk.Ctrl == contracts.CtrlNewSession {
			t.Fatal("restart discarded the active anonymous session")
		}
	}
	if restored, fresh := h.engine.conversations.GetOrCreateScopedSessionContext(groupContext, actor, groupScope); fresh || restored != session {
		t.Fatal("restart recovered another anonymous session", restored, session, fresh)
	}
	if request := h.llm.requestBody(t, 2); len(request.Messages) != 6 || request.Messages[1].text() != firstQuestion || request.Messages[3].text() != followupQuestion {
		t.Fatalf("restart omitted prior anonymous user turns: %+v", request.Messages)
	}
	// Topics in one group and distinct anonymous groups retain separate sessions.
	topicScope := fmt.Sprintf("chat:%d:thread:44", actor)
	topicContext := WithTurn(WithScope(ctx, topicScope), emptyCommentInbox{})
	collectChunks(t, h.engine.ProcessQueryStream(topicContext, topicQuestion, actor, ""))
	otherScope := fmt.Sprintf("chat:%d:thread:0", otherActor)
	otherContext := WithTurn(WithScope(ctx, otherScope), emptyCommentInbox{})
	collectChunks(t, h.engine.ProcessQueryStream(otherContext, otherGroupQuestion, otherActor, ""))
	for requestIndex, question := range map[int]string{3: topicQuestion, 4: otherGroupQuestion} {
		request := h.llm.requestBody(t, requestIndex)
		if len(request.Messages) != 2 || request.Messages[1].text() != question {
			t.Fatalf("anonymous group/topic history leaked into request %d: %+v", requestIndex, request.Messages)
		}
	}
	collectChunks(t, h.engine.ProcessQueryStream(groupContext, "回到群里", actor, ""))
	lastRequest := h.llm.requestBody(t, 5)
	if len(lastRequest.Messages) != 8 || lastRequest.Messages[1].text() != firstQuestion {
		t.Fatalf("returning to group lost its original transcript: %+v", lastRequest.Messages)
	}
	for _, message := range lastRequest.Messages {
		if message.text() == topicQuestion || message.text() == otherGroupQuestion {
			t.Fatal("another group/topic transcript contaminated original session")
		}
	}
	history, err := manager.GetConversationHistory(ctx, actor, session, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 8 || history[0].Role != "user" || history[0].Content != firstQuestion || history[1].Role != "assistant" || !strings.Contains(history[1].Content, firstAnswer) {
		t.Fatalf("production database did not preserve signed actor history: %+v", history)
	}
}
