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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot"
)

// newTestClient 启动假 Telegram HTTP 服务并包装为 Client；返回调用计数指针。
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *int32) {
	t.Helper()
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	b, err := bot.New("123456:TEST-TOKEN", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("构造 bot 失败: %v", err)
	}
	return NewClient(b, NopLogger{}, 0), &count
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// TestSendMessageOK 验证正常发送解析出 message_id。
func TestSendMessageOK(t *testing.T) {
	c, count := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"ok":true,"result":{"message_id":42,"date":0,"text":"hi","chat":{"id":7,"type":"private","first_name":"u"}}}`)
	})
	msg, err := c.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: 7, Text: "hi"})
	if err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	if msg.ID != 42 || msg.Chat.ID != 7 {
		t.Fatalf("解析结果不符: id=%d chat=%d", msg.ID, msg.Chat.ID)
	}
	if atomic.LoadInt32(count) != 1 {
		t.Fatalf("应恰好调用 1 次，实际 %d", atomic.LoadInt32(count))
	}
}

// TestEditTextImplementsEditor 验证 EditText/ReplyText 走统一封装并可当 Editor 使用。
func TestEditTextImplementsEditor(t *testing.T) {
	c, count := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"ok":true,"result":{"message_id":1,"date":0,"text":"x","chat":{"id":100,"type":"private","first_name":"u"}}}`)
	})
	var ed Editor = c
	ref, err := ed.EditText(context.Background(), baseRef, "x", EditPlain)
	if err != nil || ref == nil {
		t.Fatalf("EditText 失败: %v", err)
	}
	if atomic.LoadInt32(count) != 1 {
		t.Fatalf("应恰好调用 1 次，实际 %d", atomic.LoadInt32(count))
	}
}

// TestFloodRetryOnce 验证 429 且 RetryAfter<=60s：睡眠后仅重试一次并成功。
func TestFloodRetryOnce(t *testing.T) {
	var seen int32
	c, count := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&seen, 1) == 1 {
			writeJSON(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 0","parameters":{"retry_after":0}}`)
			return
		}
		writeJSON(w, `{"ok":true,"result":{"message_id":7,"date":0,"text":"ok","chat":{"id":7,"type":"private","first_name":"u"}}}`)
	})
	msg, err := c.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: 7, Text: "ok"})
	if err != nil {
		t.Fatalf("429 重试后应成功: %v", err)
	}
	if msg.ID != 7 {
		t.Fatalf("重试结果 message_id = %d, want 7", msg.ID)
	}
	if got := atomic.LoadInt32(count); got != 2 {
		t.Fatalf("应恰好调用 2 次，实际 %d", got)
	}
}

// TestFloodRetryAfterTooLarge 验证 RetryAfter>60s：不睡眠不重试，直接按错误返回。
func TestFloodRetryAfterTooLarge(t *testing.T) {
	c, count := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3600","parameters":{"retry_after":3600}}`)
	})
	start := time.Now()
	_, err := c.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: 7, Text: "x"})
	if err == nil {
		t.Fatal("大 RetryAfter 应按错误返回")
	}
	var flood *bot.TooManyRequestsError
	if !errors.As(err, &flood) || flood.RetryAfter != 3600 {
		t.Fatalf("错误应为 TooManyRequestsError(3600)，实际 %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("不应睡眠重试，耗时 %v", elapsed)
	}
	if atomic.LoadInt32(count) != 1 {
		t.Fatalf("不应重试，调用次数 = %d", atomic.LoadInt32(count))
	}
}

// TestNotModifiedDetected 验证 400 "message is not modified" 可被 IsNotModified 识别。
func TestNotModifiedDetected(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`)
	})
	_, err := c.EditMessageText(context.Background(), &bot.EditMessageTextParams{ChatID: 100, MessageID: 1, Text: "x"})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if !IsNotModified(err) {
		t.Fatalf("应识别为 not modified: %v", err)
	}
	if IsNotModified(nil) {
		t.Fatal("nil 错误不应识别为 not modified")
	}
	if IsNotModified(errors.New("Bad Request: chat not found")) {
		t.Fatal("其他错误不应误判")
	}
}

// TestPTBStyleNotModified 验证 PTB 首字母大写描述同样被识别。
func TestPTBStyleNotModified(t *testing.T) {
	if !IsNotModified(errors.New("Message is not modified")) {
		t.Fatal("PTB 风格描述应被识别")
	}
}

// TestCallTimeout 验证统一超时：慢服务在 deadline 后报 context 取消。
func TestCallTimeout(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		time.Sleep(300 * time.Millisecond)
		writeJSON(w, `{"ok":true,"result":{"message_id":1,"date":0,"text":"x","chat":{"id":7,"type":"private","first_name":"u"}}}`)
	}))
	defer srv.Close()
	b, err := bot.New("123456:TEST-TOKEN", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("构造 bot 失败: %v", err)
	}
	c := NewClient(b, NopLogger{}, 50*time.Millisecond)
	_, err = c.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: 7, Text: "x"})
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误应包含 DeadlineExceeded: %v", err)
	}
}

// TestAnswerCallbackQuery 验证应答回调封装。
func TestAnswerCallbackQuery(t *testing.T) {
	c, count := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"ok":true,"result":true}`)
	})
	ok, err := c.AnswerCallbackQuery(context.Background(), &bot.AnswerCallbackQueryParams{CallbackQueryID: "cq1", Text: "已收到"})
	if err != nil || !ok {
		t.Fatalf("应答回调失败: %v ok=%v", err, ok)
	}
	if atomic.LoadInt32(count) != 1 {
		t.Fatalf("应恰好调用 1 次，实际 %d", atomic.LoadInt32(count))
	}
}
