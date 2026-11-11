package config

import (
	"testing"
	"time"
)

func TestAIPoolConfiguration(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{}`, true}, {`{"qa_ai_pool":{"workers":2,"queue_capacity":3,"timeout_seconds":20}}`, true},
		{`{"qa_ai_pool":{"workers":0}}`, false}, {`{"qa_ai_pool":{"workers":1.5}}`, false},
		{`{"qa_ai_pool":null}`, false}, {`{"qa_ai_pool":{"queue_capacity":2048}}`, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			c := newTestConfig(t, nil, tc.raw, t.TempDir())
			_, err := c.AIPoolConfig()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestAssistantTimezoneAndCommentValidation(t *testing.T) {
	cfg := newTestConfig(t, nil, `{"channels":["https://t.me/test"],"qa_assistant":{"timezone":"Asia/Shanghai","comments":[{"channel":"https://t.me/test","enabled":true}]}}`, t.TempDir())
	settings, e := cfg.AssistantSettings()
	if e != nil {
		t.Fatal(e)
	}
	if settings.Comments[0].WakeWord != "小妲" {
		t.Fatal(settings)
	}
	now := time.Date(2026, 11, 11, 17, 0, 0, 0, time.UTC)
	if settings.BillingDay(now) != "2026-11-12" {
		t.Fatal("timezone boundary")
	}
	for _, raw := range []string{`{"qa_assistant":{"timezone":null}}`, `{"qa_assistant":{"max_messages":0}}`, `{"channels":["https://t.me/test"],"qa_assistant":{"comments":[{"channel":"https://t.me/test","wake_word":" "}]}}`} {
		c := newTestConfig(t, nil, raw, t.TempDir())
		if _, e := c.AssistantSettings(); e == nil {
			t.Fatal("invalid settings accepted", raw)
		}
	}
}
