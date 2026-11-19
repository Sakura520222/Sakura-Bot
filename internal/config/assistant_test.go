package config

import (
	"encoding/json"
	"testing"
)

func TestCommentAssistantAnonymousDefaults(t *testing.T) {
	cfg := newTestConfig(t, nil, `{"channels":["https://t.me/test"],"qa_assistant":{"comments":[{"channel":"https://t.me/test","enabled":true}]}}`, t.TempDir())
	settings, err := cfg.AssistantSettings()
	if err != nil {
		t.Fatal(err)
	}
	rule := settings.Comments[0]
	if rule.AnonymousEnabled || rule.AnonymousDailyLimit != 20 {
		t.Fatalf("unexpected anonymous defaults: %+v", rule)
	}
}

func TestCommentAssistantAnonymousValidation(t *testing.T) {
	for _, tc := range []struct {
		fields string
		valid  bool
	}{
		{`"anonymous_enabled":true,"anonymous_daily_limit":1`, true},
		{`"anonymous_enabled":false,"anonymous_daily_limit":1000`, true},
		{`"anonymous_daily_limit":0`, false},
		{`"anonymous_daily_limit":1001`, false},
		{`"anonymous_daily_limit":1.5`, false},
		{`"anonymous_daily_limit":true`, false},
		{`"anonymous_daily_limit":null`, false},
		{`"anonymous_enabled":null`, false},
		{`"anonymous_enabled":"true"`, false},
	} {
		t.Run(tc.fields, func(t *testing.T) {
			cfg := newTestConfig(t, nil, `{"channels":["https://t.me/test"],"qa_assistant":{"comments":[{"channel":"https://t.me/test",`+tc.fields+`}]}}`, t.TempDir())
			settings, err := cfg.AssistantSettings()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid {
				encoded, err := json.Marshal(settings)
				if err != nil {
					t.Fatal(err)
				}
				var decoded AssistantSettings
				if err := json.Unmarshal(encoded, &decoded); err != nil {
					t.Fatal(err)
				}
				if decoded.Comments[0] != settings.Comments[0] {
					t.Fatal("anonymous policy changed on round trip")
				}
			}
		})
	}
}
