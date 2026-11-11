package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
)

type CommentAssistant struct {
	Channel  string `json:"channel"`
	Enabled  bool   `json:"enabled"`
	WakeWord string `json:"wake_word"`
}
type AssistantSettings struct {
	Timezone            string             `json:"timezone"`
	CheckinEnabled      bool               `json:"checkin_enabled"`
	RequireSubscription bool               `json:"require_subscription"`
	RequiredChannels    []string           `json:"required_channels"`
	Comments            []CommentAssistant `json:"comments"`
	MaxMessages         int                `json:"max_messages"`
	InboxCapacity       int                `json:"inbox_capacity"`
	PerUserConcurrency  int                `json:"per_user_concurrency"`
}

func (c *Config) AssistantSettings() (AssistantSettings, error) {
	v := AssistantSettings{Timezone: "UTC", CheckinEnabled: true, MaxMessages: 8, InboxCapacity: 8, PerUserConcurrency: 4, RequiredChannels: []string{}, Comments: []CommentAssistant{}}
	if raw, ok := c.readConfig()["qa_assistant"]; ok {
		if _, ok := raw.(map[string]any); !ok {
			return v, fmt.Errorf("qa_assistant must be an object")
		}
		for key, value := range raw.(map[string]any) {
			if value == nil {
				return v, fmt.Errorf("qa_assistant.%s cannot be null", key)
			}
		}
		b, e := json.Marshal(raw)
		if e != nil {
			return v, e
		}
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if e = d.Decode(&v); e != nil {
			return v, e
		}
	}
	if _, e := time.LoadLocation(v.Timezone); e != nil {
		return v, e
	}
	if v.MaxMessages < 1 || v.MaxMessages > 64 || v.InboxCapacity < 1 || v.InboxCapacity > 64 || v.PerUserConcurrency < 1 || v.PerUserConcurrency > 16 {
		return v, fmt.Errorf("invalid turn limits")
	}
	allowed := map[string]bool{}
	for _, ch := range c.Channels() {
		allowed[ch] = true
	}
	for _, ch := range v.RequiredChannels {
		if !allowed[ch] {
			return v, fmt.Errorf("required channel is not configured")
		}
	}
	if v.RequireSubscription && len(v.RequiredChannels) == 0 {
		return v, fmt.Errorf("select required channels")
	}
	seen := map[string]bool{}
	for i, ch := range v.Comments {
		if !allowed[ch.Channel] || seen[ch.Channel] {
			return v, fmt.Errorf("invalid comment channel")
		}
		seen[ch.Channel] = true

		if strings.TrimSpace(v.Comments[i].WakeWord) == "" || len([]rune(v.Comments[i].WakeWord)) > 32 {
			return v, fmt.Errorf("wake word too long")
		}
	}
	return v, nil
}
func (s AssistantSettings) BillingDay(now time.Time) string {
	loc, _ := time.LoadLocation(s.Timezone)
	return now.In(loc).Format("2006-01-02")
}

func (c *CommentAssistant) UnmarshalJSON(data []byte) error {
	type alias CommentAssistant
	v := alias{WakeWord: "小妲"}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(&v); e != nil {
		return e
	}
	if strings.TrimSpace(v.WakeWord) == "" {
		return fmt.Errorf("wake_word cannot be empty")
	}
	*c = CommentAssistant(v)
	return nil
}
