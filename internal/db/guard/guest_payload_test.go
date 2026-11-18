// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import "testing"

func TestDecodeLegacyTelegramIDsWithoutFloatRounding(t *testing.T) {
	decoded, err := decodeRow(Row{"payload": `{
		"guest_actor":{"user_id":9007199254740993,"chat_id":-9007199254740995,"message_id":7},
		"guest_source":{"user_id":9223372036854775808},
		"join_request_query":{"id":9223372036854775807},
		"attempts":1,"confidence":0.95
	}`})
	if err != nil {
		t.Fatal(err)
	}
	payload := decoded["payload"].(map[string]any)
	source := payload["guest_actor"].(map[string]any)
	if source["user_id"] != int64(9007199254740993) || source["chat_id"] != int64(-9007199254740995) {
		t.Fatalf("guest identity was rounded: %v", source)
	}
	if _, valid := payload["guest_source"].(map[string]any)["user_id"]; valid {
		t.Fatal("out-of-range user ID remained valid")
	}
	query := payload["join_request_query"].(map[string]any)
	if query["id"] != int64(9223372036854775807) {
		t.Fatalf("request query ID lost: %v", query)
	}
	if payload["attempts"] != float64(1) || payload["confidence"] != 0.95 {
		t.Fatalf("legacy non-ID JSON values changed: %v", payload)
	}
}
