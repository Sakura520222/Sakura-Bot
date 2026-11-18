// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"testing"
	"time"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

func TestMembershipSequenceDoesNotCancelNewerVerification(t *testing.T) {
	join := time.Unix(1800000000, 0)
	token := "membership:1800000000:raw:17"
	for _, tc := range []struct {
		at   time.Time
		qts  int
		want bool
	}{
		{join.Add(-time.Second), 25, true},
		{join, 16, true},
		{join, 17, true},
		{join, 18, false},
		{join.Add(time.Second), 16, false},
	} {
		if got := staleMembershipEvent(mtproto.ParticipantEvent{Date: tc.at, QTS: tc.qts}, token); got != tc.want {
			t.Fatalf("membership date=%v QTS=%d stale=%t, want %t", tc.at, tc.qts, got, tc.want)
		}
	}
}

func TestAssistantReceiptRequiresBoundActorSessionAndUniqueAnswers(t *testing.T) {
	now := time.Unix(1800000000, 0).UTC()
	assistant := map[string]any{
		"version": 1, "state": "completed", "steps": 2, "actor_id": int64(17),
		"session_id": "test-session", "chat_id": int64(-1000000002000), "bot_id": int64(18),
		"started_at": now.Unix() - 90, "completed_at": now.Unix() - 1,
		"receipts": []string{"000000000000000000000001", "000000000000000000000002"},
	}
	session := guarddb.Row{
		"id": "test-session", "chat_id": int64(-1000000002000), "user_id": int64(17),
		"payload": map[string]any{"assistant": assistant}, "expires_at": now.Add(time.Minute),
	}
	if !verifiedAssistant(session, matchingAssistant(session, 18), now) {
		t.Fatal("valid assistant result rejected")
	}
	if matchingAssistant(session, 19) != nil {
		t.Fatal("unconfigured assistant accepted")
	}
	for _, key := range []string{"actor_id", "session_id", "chat_id"} {
		saved := assistant[key]
		assistant[key] = ""
		if matchingAssistant(session, 18) != nil {
			t.Fatalf("mismatched %s accepted", key)
		}
		assistant[key] = saved
	}
	assistant["receipts"] = []string{"000000000000000000000001", "000000000000000000000001"}
	if verifiedAssistant(session, matchingAssistant(session, 18), now) {
		t.Fatal("duplicate assistant answer accepted")
	}
	assistant["receipts"] = []string{"000000000000000000000001", "000000000000000000000002"}
	if verifiedAssistant(session, matchingAssistant(session, 18), now.Add(time.Minute)) {
		t.Fatal("expired session released")
	}
}

func TestLegacyPythonChallengeValidatesContextAndSolvedValue(t *testing.T) {
	p := map[string]any{
		"question": "3 + 4", "options": []any{float64(4), float64(7), float64(8), float64(11)},
		"answer_tokens": map[string]any{
			"000000000000000000000001": float64(7),
			"000000000000000000000002": float64(4),
		},
		"answer_context": sha256Hex(`["test-session",-1000000002000,17,2,"3 + 4",[4,7,8,11]]`),
		"generation":     float64(2),
	}
	session := guarddb.Row{"id": "test-session", "chat_id": int64(-1000000002000), "user_id": int64(17), "payload": p}
	if correct, valid := pythonChallengeAnswer(session, "000000000000000000000001"); !correct || !valid {
		t.Fatal("bound correct answer rejected")
	}
	if correct, valid := pythonChallengeAnswer(session, "000000000000000000000002"); correct || !valid {
		t.Fatal("bound incorrect answer was not classified")
	}
	now := time.Unix(1800000000, 0).UTC()
	session["expires_at"] = now.Add(time.Minute)
	session["message_id"] = int64(99)
	p["handled_queries"] = []string{"9007199254740993"}
	p["answer_receipt"] = map[string]any{
		"actor_id": int64(17), "chat_id": int64(-1000000002000), "message_id": int64(99),
		"session_id": "test-session", "context": p["answer_context"],
		"query_id": int64(9007199254740993), "token": "000000000000000000000001",
		"value": int64(7), "accepted_at": now.Add(-time.Second),
	}
	if !verifiedPythonReceipt(session, now) {
		t.Fatal("persisted receipt would not resume a verified release")
	}
	session["message_id"] = int64(100)
	if verifiedPythonReceipt(session, now) {
		t.Fatal("receipt tied to a different challenge message accepted")
	}
	p["generation"] = float64(3)
	if _, valid := pythonChallengeAnswer(session, "000000000000000000000001"); valid {
		t.Fatal("callback from the previous question generation accepted")
	}
}
