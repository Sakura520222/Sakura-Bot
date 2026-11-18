// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

func TestBotIDFromToken(t *testing.T) {
	cases := []struct {
		token  string
		wantID int64
		wantOK bool
	}{
		{"123456789:test-only", 123456789, true},
		{"  9876543210:secret_token  ", 9876543210, true},
		{"0:secret", 0, false},
		{"0123:secret", 0, false},
		{"-123:secret", 0, false},
		{"+123:secret", 0, false},
		{"123:", 0, false},
		{":secret", 0, false},
		{"notanumber:secret", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		id, ok := BotIDFromToken(tc.token)
		if ok != tc.wantOK || id != tc.wantID {
			t.Fatalf("BotIDFromToken(%q) = (%d, %v), want (%d, %v)", tc.token, id, ok, tc.wantID, tc.wantOK)
		}
	}
}

func TestDirectBotAPIMethods(t *testing.T) {
	const testToken = "555666777:AA_SECRET_TOKEN_XYZ"
	var lastBanPayload map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/bot" + testToken + "/sendMessage":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["text"] != "你好" || body["parse_mode"] != "HTML" {
				t.Errorf("unexpected sendMessage payload: %+v", body)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":4242}}`))

		case "/bot" + testToken + "/banChatMember":
			_ = json.NewDecoder(r.Body).Decode(&lastBanPayload)
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))

		case "/bot" + testToken + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":555666777,"is_bot":true,"first_name":"Sakura","username":"sakura_bot"}}`))

		case "/bot" + testToken + "/getChat":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":-100123456,"type":"channel","title":"Sakura News","linked_chat_id":-100999888}}`))

		case "/bot" + testToken + "/getChatMember":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"status":"administrator","user":{"id":1001,"is_bot":false,"first_name":"Admin"},"can_delete_messages":true}}`))

		case "/bot" + testToken + "/getUserProfilePhotos":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"total_count":1,"photos":[[{"file_id":"f_small","file_unique_id":"u1","width":160,"height":160,"file_size":4096}]]}}`))

		case "/bot" + testToken + "/getFile":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"f_small","file_unique_id":"u1","file_size":11,"file_path":"photos/file_1.jpg"}}`))

		case "/file/bot" + testToken + "/photos/file_1.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("avatar-data"))

		case "/file/bot" + testToken + "/photos/redirect.jpg":
			http.Redirect(w, r, "http://127.0.0.1:1/evil", http.StatusFound)

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	api := NewDirectBotAPI(WithDirectBaseURL(srv.URL), WithDirectHTTPClient(srv.Client()))
	ctx := context.Background()

	// SendMessage
	msgID, err := api.SendMessage(ctx, testToken, DirectSendMessageParams{
		ChatID:    int64(-100123456),
		Text:      "你好",
		ParseMode: "HTML",
	})
	if err != nil || msgID != 4242 {
		t.Fatalf("SendMessage = (%d, %v), want (4242, nil)", msgID, err)
	}

	// BanChatMember（无 untilDate 与有 untilDate）
	if err := api.BanChatMember(ctx, testToken, -100123456, 7788, true, 0); err != nil {
		t.Fatalf("BanChatMember 失败: %v", err)
	}
	if _, hasUntil := lastBanPayload["until_date"]; hasUntil || lastBanPayload["revoke_messages"] != true {
		t.Fatalf("banChatMember payload 不符: %+v", lastBanPayload)
	}
	if err := api.BanChatMember(ctx, testToken, -100123456, 7788, false, 1700000100); err != nil {
		t.Fatalf("BanChatMember(untilDate) 失败: %v", err)
	}
	if lastBanPayload["until_date"] != float64(1700000100) {
		t.Fatalf("until_date 未透传: %+v", lastBanPayload)
	}

	// GetMe
	me, err := api.GetMe(ctx, testToken)
	if err != nil || me.ID != 555666777 || me.Username != "sakura_bot" {
		t.Fatalf("GetMe = (%+v, %v)", me, err)
	}

	// GetChat
	chat, err := api.GetChat(ctx, testToken, "@sakura_news")
	if err != nil || chat.ID != -100123456 || chat.LinkedChatID != -100999888 {
		t.Fatalf("GetChat = (%+v, %v)", chat, err)
	}

	// GetChatMember
	member, err := api.GetChatMember(ctx, testToken, -100123456, 1001)
	if err != nil || member.Type != models.ChatMemberTypeAdministrator || member.Administrator == nil {
		t.Fatalf("GetChatMember = (%+v, %v)", member, err)
	}

	// GetUserProfilePhotos
	photos, err := api.GetUserProfilePhotos(ctx, testToken, 1001, 0, 1)
	if err != nil || photos.TotalCount != 1 || len(photos.Photos) != 1 || photos.Photos[0][0].FileID != "f_small" {
		t.Fatalf("GetUserProfilePhotos = (%+v, %v)", photos, err)
	}

	// GetFile & DownloadFile
	file, err := api.GetFile(ctx, testToken, "f_small")
	if err != nil || file.FilePath != "photos/file_1.jpg" {
		t.Fatalf("GetFile = (%+v, %v)", file, err)
	}

	data, err := api.DownloadFile(ctx, testToken, file.FilePath, 1024)
	if err != nil || !bytes.Equal(data, []byte("avatar-data")) {
		t.Fatalf("DownloadFile = (%q, %v)", data, err)
	}

	// DownloadFile 超出 maxBytes 拒绝
	if _, err := api.DownloadFile(ctx, testToken, file.FilePath, 5); err == nil {
		t.Fatal("DownloadFile 超出 maxBytes 应失败")
	}

	// DownloadFile 禁止跟随重定向
	if _, err := api.DownloadFile(ctx, testToken, "photos/redirect.jpg", 1024); err == nil {
		t.Fatal("DownloadFile 遇到 302 重定向应拒绝")
	}

	// DownloadFile 拒绝路径穿越
	if _, err := api.DownloadFile(ctx, testToken, "../secret", 1024); err == nil {
		t.Fatal("DownloadFile 路径穿越应拒绝")
	}
}

func TestDirectBotAPIErrorClassificationAndTokenRedaction(t *testing.T) {
	const secretToken = "999888777:SUPER_SENSITIVE_BOT_SECRET"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/badReq"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message to delete not found"}`))
		case strings.HasSuffix(r.URL.Path, "/rateLimit"):
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 12","parameters":{"retry_after":12}}`))
		case strings.HasSuffix(r.URL.Path, "/forbidden"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
		case strings.HasSuffix(r.URL.Path, "/badJSON"):
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`<html>502 Bad Gateway</html>`))
		case strings.HasSuffix(r.URL.Path, "/slow"):
			time.Sleep(120 * time.Millisecond)
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}
	}))
	defer srv.Close()

	api := NewDirectBotAPI(
		WithDirectBaseURL(srv.URL),
		WithDirectHTTPClient(srv.Client()),
		WithDirectTimeout(40*time.Millisecond),
	)
	ctx := context.Background()

	// 1. 400 BadRequestError 剥离 "Bad Request: " 前缀，且同时满足 *BotAPIError
	err := api.CallMethod(ctx, secretToken, "badReq", nil, nil)
	var badErr *BadRequestError
	var baseErr *BotAPIError
	if !errors.As(err, &badErr) || !errors.As(err, &baseErr) {
		t.Fatalf("400 应匹配 BadRequestError 与 BotAPIError: %T %v", err, err)
	}
	if err.Error() != "message to delete not found" {
		t.Fatalf("Bad Request 前缀剥离不符: %q", err.Error())
	}

	// 2. 429 RetryAfterError
	err = api.CallMethod(ctx, secretToken, "rateLimit", nil, nil)
	var retryErr *RetryAfterError
	if !errors.As(err, &retryErr) || !errors.As(err, &baseErr) || retryErr.RetryAfter != 12 {
		t.Fatalf("429 解析不符: %T %+v", err, retryErr)
	}

	// 3. 403 BotAPIError 保持原文供子串匹配
	err = api.CallMethod(ctx, secretToken, "forbidden", nil, nil)
	if !errors.As(err, &baseErr) || baseErr.ErrorCode != 403 || !strings.Contains(err.Error(), "bot was blocked by the user") {
		t.Fatalf("403 解析不符: %T %v", err, err)
	}

	// 4. 非 JSON 响应 → NetworkAPIError
	err = api.CallMethod(ctx, secretToken, "badJSON", nil, nil)
	var netAPIErr *NetworkAPIError
	if !errors.As(err, &netAPIErr) || !strings.Contains(err.Error(), "Invalid JSON (HTTP 502)") {
		t.Fatalf("非 JSON 错误归类不符: %T %v", err, err)
	}

	// 5. 超时 → NetworkAPIError，包装 context.DeadlineExceeded，且绝不泄漏 secretToken！
	err = api.CallMethod(ctx, secretToken, "slow", nil, nil)
	if !errors.As(err, &netAPIErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("超时错误应为包装 DeadlineExceeded 的 NetworkAPIError: %T %v", err, err)
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Fatalf("严重安全缺陷：传输错误泄漏了 Bot Token: %q", err.Error())
	}
}
