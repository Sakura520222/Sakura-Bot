package db

import (
	"context"
	"strings"
	"testing"
)

func TestDiscussionBackfillPreservesLiveEditsAndMapsTheSourcePost(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if err := m.InitComments(ctx); err != nil {
		t.Fatal(err)
	}
	chat, channel := -uniqueUserID(), -uniqueUserID()
	t.Cleanup(func() {
		_, _ = m.DB.Exec("DELETE FROM qa_posts WHERE channel_id=?", channel)
		_, _ = m.DB.Exec("DELETE FROM qa_comments WHERE chat_id=?", chat)
	})
	const root, post = 21, 45
	snapshot := []CommentRecord{
		{ChatID: chat, MessageID: root, RootID: root, Channel: "https://t.me/testchan", Text: "historical copy", Date: 200},
		{ChatID: chat, MessageID: 22, RootID: root, ReplyID: root, Channel: "https://t.me/testchan", Text: "historical reply", Date: 201},
	}
	live := snapshot[0]
	live.Text = "live edit"
	live.Date = 210
	live.Edited = true
	if err := m.SaveComment(ctx, live); err != nil {
		t.Fatal(err)
	}
	inserted, err := m.BackfillComments(ctx, channel, post, snapshot)
	if err != nil || inserted != 1 {
		t.Fatalf("backfill inserted %d records, err=%v", inserted, err)
	}
	inserted, err = m.BackfillComments(ctx, channel, post, snapshot)
	if err != nil || inserted != 0 {
		t.Fatalf("repeated backfill inserted %d records, err=%v", inserted, err)
	}
	transcript, err := m.CommentContext(ctx, chat, root)
	if err != nil || !strings.Contains(transcript, "live edit") || !strings.Contains(transcript, "historical reply") ||
		strings.Contains(transcript, "historical copy") {
		t.Fatalf("unexpected initial transcript %q, err=%v", transcript, err)
	}
	if err := m.EditPost(ctx, channel, post, "new source edit"); err != nil {
		t.Fatal(err)
	}
	transcript, err = m.CommentContext(ctx, chat, root)
	if err != nil || !strings.Contains(transcript, "new source edit") || strings.Contains(transcript, "live edit") {
		t.Fatalf("mapped post was not edited, transcript=%q err=%v", transcript, err)
	}
}
