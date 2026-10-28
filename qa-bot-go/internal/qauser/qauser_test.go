// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package qauser

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
)

// fakeStore 实现 Store，按开关注入行为。
type fakeStore struct {
	registered    bool
	registerErr   error
	subscribed    bool
	addOK         bool
	addErr        error
	removed       int64
	removeErr     error
	requestID     int64
	requestErr    error
	channels      []contracts.ChannelInfo
	channelsErr   error
	subs          []db.Subscription
	subsErr       error
	activityCalls int
	paramsSeen    map[string]any
}

func (f *fakeStore) IsUserRegistered(context.Context, int64) (bool, error) {
	return f.registered, nil
}

func (f *fakeStore) UpdateUserActivity(context.Context, int64) error {
	f.activityCalls++
	return nil
}

func (f *fakeStore) RegisterUser(context.Context, int64, *string, *string, bool) error {
	return f.registerErr
}

func (f *fakeStore) GetAllChannels(context.Context) ([]contracts.ChannelInfo, error) {
	if f.channelsErr != nil {
		return nil, f.channelsErr
	}
	return f.channels, nil
}

func (f *fakeStore) IsSubscribed(context.Context, int64, string, string) (bool, error) {
	return f.subscribed, nil
}

func (f *fakeStore) AddSubscription(context.Context, int64, string, string, string) (bool, error) {
	return f.addOK, f.addErr
}

func (f *fakeStore) RemoveSubscription(context.Context, int64, string, string) (int64, error) {
	return f.removed, f.removeErr
}

func (f *fakeStore) GetUserSubscriptions(context.Context, int64, string) ([]db.Subscription, error) {
	if f.subsErr != nil {
		return nil, f.subsErr
	}
	return f.subs, nil
}

func (f *fakeStore) CreateRequest(_ context.Context, _ string, _ int64, _ string, params map[string]any) (int64, error) {
	f.paramsSeen = params
	return f.requestID, f.requestErr
}

func (f *fakeStore) GetRequestStatus(context.Context, int64) (*db.Request, error) {
	return nil, nil
}

// ── 注册 ─────────────────────────────────────────────────────────────────

func TestRegisterNewUser(t *testing.T) {
	s := New(&fakeStore{})
	res := s.RegisterUser(context.Background(), 1, strPtr("u"), strPtr("F"))
	if !res.Success || !res.NewUser || res.Message != "注册成功！欢迎使用智能资讯助手。" {
		t.Fatalf("新用户注册结果不符: %+v", res)
	}
}

func TestRegisterExistingUser(t *testing.T) {
	fs := &fakeStore{registered: true}
	s := New(fs)
	res := s.RegisterUser(context.Background(), 1, nil, nil)
	if !res.Success || res.NewUser || res.Message != "欢迎回来！" {
		t.Fatalf("老用户注册结果不符: %+v", res)
	}
	if fs.activityCalls != 1 {
		t.Fatalf("应更新一次活跃时间")
	}
}

func TestRegisterDBFailureMessage(t *testing.T) {
	// Python db.register_user 自捕获 → False → "注册失败，请稍后重试。"
	s := New(&fakeStore{registerErr: errors.New("db down")})
	res := s.RegisterUser(context.Background(), 1, nil, nil)
	if res.Success || res.Message != "注册失败，请稍后重试。" {
		t.Fatalf("注册失败文案不符: %+v", res)
	}
}

// ── 订阅 ─────────────────────────────────────────────────────────────────

func TestAddSubscriptionAlreadySubscribed(t *testing.T) {
	s := New(&fakeStore{subscribed: true})
	res := s.AddSubscription(context.Background(), 1, "https://t.me/a", "A")
	if res.Success || res.Message != "您已经订阅了此频道的总结推送。" {
		t.Fatalf("已订阅文案不符: %+v", res)
	}
}

func TestAddSubscriptionSuccess(t *testing.T) {
	s := New(&fakeStore{addOK: true})
	res := s.AddSubscription(context.Background(), 1, "https://t.me/a", "A")
	want := "✅ 已订阅 `A` 的总结推送\n\n当该频道有新总结时，我会及时通知您！"
	if !res.Success || res.Message != want {
		t.Fatalf("订阅成功文案不符: %+v", res)
	}
}

func TestAddSubscriptionFailureMessage(t *testing.T) {
	// INSERT IGNORE 撞唯一键（false, nil）与错误（false, err）同文案。
	s := New(&fakeStore{addOK: false})
	res := s.AddSubscription(context.Background(), 1, "https://t.me/a", "A")
	if res.Success || res.Message != "订阅失败，请稍后重试。" {
		t.Fatalf("订阅失败文案不符: %+v", res)
	}
	s2 := New(&fakeStore{addOK: false, addErr: errors.New("db down")})
	res2 := s2.AddSubscription(context.Background(), 1, "https://t.me/a", "A")
	if res2.Success || res2.Message != "订阅失败，请稍后重试。" {
		t.Fatalf("订阅异常文案不符: %+v", res2)
	}
}

func TestRemoveSubscription(t *testing.T) {
	s := New(&fakeStore{removed: 1})
	res := s.RemoveSubscription(context.Background(), 1, "https://t.me/a")
	if !res.Success || res.Message != "✅ 已取消该频道的订阅" {
		t.Fatalf("取消订阅文案不符: %+v", res)
	}
	s2 := New(&fakeStore{removed: 0})
	res2 := s2.RemoveSubscription(context.Background(), 1, "https://t.me/a")
	if res2.Success || res2.Message != "未找到该频道的订阅记录。" {
		t.Fatalf("无订阅记录文案不符: %+v", res2)
	}
	s3 := New(&fakeStore{removed: 0, removeErr: errors.New("db down")})
	res3 := s3.RemoveSubscription(context.Background(), 1, "https://t.me/a")
	if res3.Success || res3.Message != "未找到该频道的订阅记录。" {
		t.Fatalf("移除异常按 0 行处理: %+v", res3)
	}
}

// ── 总结请求 ─────────────────────────────────────────────────────────────

func TestCreateSummaryRequest(t *testing.T) {
	fs := &fakeStore{requestID: 7}
	s := New(fs)
	res := s.CreateSummaryRequest(context.Background(), 1, "https://t.me/a", "A频道")
	want := "📝 已向管理员提交总结请求\n\n频道: A频道\n请求ID: 7\n\n请等待管理员确认并生成总结..."
	if !res.Success || res.RequestID != 7 || res.Message != want {
		t.Fatalf("请求回执不符: %+v", res)
	}
	if v, ok := fs.paramsSeen["channel_name"].(string); !ok || v != "A频道" {
		t.Fatalf("params 应携带 channel_name: %v", fs.paramsSeen)
	}
}

func TestCreateSummaryRequestFailure(t *testing.T) {
	// create_request 异常回退 None → "创建请求失败，请稍后重试。"
	s := New(&fakeStore{requestID: 0, requestErr: errors.New("db down")})
	res := s.CreateSummaryRequest(context.Background(), 1, "https://t.me/a", "A频道")
	if res.Success || res.Message != "创建请求失败，请稍后重试。" {
		t.Fatalf("请求失败文案不符: %+v", res)
	}
}

func TestGetAvailableChannelsErrorReturnsEmpty(t *testing.T) {
	s := New(&fakeStore{channelsErr: errors.New("db down")})
	if got := s.GetAvailableChannels(context.Background()); len(got) != 0 {
		t.Fatalf("异常应返回空列表")
	}
}

// ── 频道目录/订阅列表格式化 ──────────────────────────────────────────────

func TestFormatChannelsList(t *testing.T) {
	s := New(&fakeStore{})
	if got := s.FormatChannelsList(nil); got != "暂无可订阅的频道。" {
		t.Fatalf("空目录文案不符: %q", got)
	}

	channels := []contracts.ChannelInfo{
		{ChannelID: "https://t.me/a", ChannelName: "A频道",
			LastSummaryTime: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
			SummaryCount:    12, MessageCount: 345},
		{ChannelID: "https://t.me/b", ChannelName: "B频道", SummaryCount: 0, MessageCount: 0},
	}
	got := s.FormatChannelsList(channels)
	want := strings.Join([]string{
		"📋 **可订阅频道列表**\n",
		"1. **A频道**",
		"   链接: `https://t.me/a`",
		"   总结数: 12，消息数: 345",
		"   最后更新: 2026-03-04",
		"",
		"2. **B频道**",
		"   链接: `https://t.me/b`",
		"   总结数: 0，消息数: 0",
		"   最后更新: 暂无总结",
		"",
		"💡 使用方法:",
		"`/subscribe <频道链接>`",
		"",
		"示例:",
		"`/subscribe https://t.me/a`",
	}, "\n")
	if got != want {
		t.Fatalf("频道目录文案不符:\n got=%q\nwant=%q", got, want)
	}
}

func TestFormatSubscriptionsList(t *testing.T) {
	s := New(&fakeStore{})
	if got := s.FormatSubscriptionsList(nil); got != "您还没有订阅任何频道。\n\n💡 使用 `/listchannels` 查看可订阅频道" {
		t.Fatalf("空订阅文案不符: %q", got)
	}

	subs := []db.Subscription{
		{ChannelID: "https://t.me/a",
			ChannelName: sql.NullString{String: "A频道", Valid: true},
			CreatedAt:   sql.NullTime{Time: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), Valid: true}},
		{ChannelID: "https://t.me/b"},
	}
	got := s.FormatSubscriptionsList(subs)
	want := strings.Join([]string{
		"📚 **我的订阅**\n",
		"1. `A频道`",
		"   链接: `https://t.me/a`",
		"   订阅时间: 2026-02-01",
		"",
		"2. `None`",
		"   链接: `https://t.me/b`",
		"   订阅时间: ",
		"",
		"💡 使用方法:",
		"`/unsubscribe <频道链接>`",
		"",
		"示例:",
		"`/unsubscribe https://t.me/a`",
	}, "\n")
	if got != want {
		t.Fatalf("订阅列表文案不符:\n got=%q\nwant=%q", got, want)
	}
}

// ── 小工具 ───────────────────────────────────────────────────────────────

func strPtr(s string) *string { return &s }
