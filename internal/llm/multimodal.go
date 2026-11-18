package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

type ImageURL struct {
	URL string `json:"url"`
}
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// Preserve string/null content for old callers; image messages use a typed array.
func (m Message) MarshalJSON() ([]byte, error) {
	var content any = m.Content
	if m.Parts != nil {
		content = m.Parts
	}
	return json.Marshal(struct {
		Role       string     `json:"role"`
		Content    any        `json:"content"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}{m.Role, content, m.ToolCalls, m.ToolCallID})
}
func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  []ToolCall      `json:"tool_calls"`
		ToolCallID string          `json:"tool_call_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = Message{Role: raw.Role, ToolCalls: raw.ToolCalls, ToolCallID: raw.ToolCallID}
	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}
	if raw.Content[0] == '"' {
		var s string
		if err := json.Unmarshal(raw.Content, &s); err != nil {
			return err
		}
		m.Content = Str(s)
		return nil
	}
	if err := json.Unmarshal(raw.Content, &m.Parts); err != nil {
		return fmt.Errorf("invalid multimodal content")
	}
	for _, p := range m.Parts {
		if p.Type != "text" && (p.Type != "image_url" || p.ImageURL == nil) {
			return fmt.Errorf("invalid multimodal content part")
		}
	}
	return nil
}

type configKey struct{}
type imagesKey struct{}

func WithConfig(ctx context.Context, cfg AIConfig) context.Context {
	return context.WithValue(ctx, configKey{}, cfg)
}
func PinnedConfig(ctx context.Context) (AIConfig, bool) {
	c, ok := ctx.Value(configKey{}).(AIConfig)
	return c, ok
}
func (c *Client) Pin(ctx context.Context) context.Context {
	if _, ok := PinnedConfig(ctx); ok {
		return ctx
	}
	return WithConfig(ctx, c.provider.EffectiveAIConfig())
}
func WithImages(ctx context.Context, urls []string) context.Context {
	return context.WithValue(ctx, imagesKey{}, append([]string(nil), urls...))
}
func UserMessage(ctx context.Context, text string) Message {
	urls, _ := ctx.Value(imagesKey{}).([]string)
	if len(urls) == 0 {
		return Message{Role: "user", Content: Str(text)}
	}
	parts := []ContentPart{{Type: "text", Text: text}}
	for _, url := range urls {
		parts = append(parts, ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: url}})
	}
	return Message{Role: "user", Parts: parts}
}
func HasImages(ctx context.Context) bool {
	urls, _ := ctx.Value(imagesKey{}).([]string)
	return len(urls) > 0
}
