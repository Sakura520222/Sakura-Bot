// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：测试辅助代码，随源码分发，遵循项目 AGPL-3.0 许可
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package engine

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
)

// TestProcessQueryStreamFallbackVerbatim 降级路径与逐字文案断言。
func TestProcessQueryStreamFallbackVerbatim(t *testing.T) {
	t.Run("双方失败_外层ERROR逐字", func(t *testing.T) {
		h := newHarness(t, []scriptEntry{
			{status: 500},
			{status: 500},
		})
		chunks := collectChunks(t, h.engine.ProcessQueryStream(context.Background(), "问题", 1, ""))
		assertChunks(t, chunks, []Chunk{
			CtrlChunk(contracts.CtrlNewSession),
			ErrChunk(errQueryFailedText),
		})
		// 仅 user 消息持久化；assistant 不落库（对齐 Python except 内抛出即中断）
		h.store.mu.Lock()
		n := len(h.store.saved)
		h.store.mu.Unlock()
		if n != 1 {
			t.Fatalf("持久化条数 = %d, 期望 1（仅 user）", n)
		}
	})

	t.Run("agentic失败_直连成功", func(t *testing.T) {
		h := newHarness(t, []scriptEntry{
			{status: 500},
			{lines: []string{contentLine("降级回答")}},
		})
		chunks := collectChunks(t, h.engine.ProcessQueryStream(context.Background(), "问题", 2, ""))
		assertChunks(t, chunks, []Chunk{
			CtrlChunk(contracts.CtrlNewSession),
			TextChunk("降级回答"),
			CtrlChunk(contracts.CtrlDone),
		})
		// 直连请求不带工具
		req2 := h.llm.requestBody(t, 1)
		if req2.Tools != nil || req2.ToolChoice != "" {
			t.Fatalf("直连请求不应携带工具: tools=%s choice=%q", string(req2.Tools), req2.ToolChoice)
		}
		// 持久化含前缀
		h.store.mu.Lock()
		saved := append([]db.HistoryItem(nil), h.store.saved...)
		h.store.mu.Unlock()
		if saved[1].Content != "🍃 *开始新的对话。*\n\n降级回答" {
			t.Fatalf("assistant 持久化 = %q", saved[1].Content)
		}
	})

	t.Run("部分输出后中断_逐字中断提示", func(t *testing.T) {
		h := newHarness(t, []scriptEntry{
			{lines: []string{contentLine("部分")}, truncate: true},
		})
		chunks := collectChunks(t, h.engine.ProcessQueryStream(context.Background(), "问题", 3, ""))
		assertChunks(t, chunks, []Chunk{
			CtrlChunk(contracts.CtrlNewSession),
			TextChunk("部分"),
			TextChunk(interruptedNoticeText),
			CtrlChunk(contracts.CtrlDone),
		})
		// 只有一次请求（已有部分输出 → 不再直连重试）
		if got := h.llm.requestCount(); got != 1 {
			t.Fatalf("请求数 = %d, 期望 1", got)
		}
		// 持久化 = 前缀 + 部分内容 + 中断提示
		h.store.mu.Lock()
		saved := append([]db.HistoryItem(nil), h.store.saved...)
		h.store.mu.Unlock()
		want := "🍃 *开始新的对话。*\n\n部分" + interruptedNoticeText
		if saved[1].Content != want {
			t.Fatalf("assistant 持久化 = %q, 期望 %q", saved[1].Content, want)
		}
	})

	t.Run("直连空回答_逐字空兜底", func(t *testing.T) {
		h := newHarness(t, []scriptEntry{
			{status: 500},
			{lines: nil},
		})
		chunks := collectChunks(t, h.engine.ProcessQueryStream(context.Background(), "问题", 4, ""))
		assertChunks(t, chunks, []Chunk{
			CtrlChunk(contracts.CtrlNewSession),
			TextChunk(emptyAnswerText),
			CtrlChunk(contracts.CtrlDone),
		})
		h.store.mu.Lock()
		saved := append([]db.HistoryItem(nil), h.store.saved...)
		h.store.mu.Unlock()
		want := "🍃 *开始新的对话。*\n\n" + emptyAnswerText
		if saved[1].Content != want {
			t.Fatalf("assistant 持久化 = %q, 期望 %q", saved[1].Content, want)
		}
	})
}

// TestProcessQueryStreamConsumerCancel 消费端取消等价 GeneratorExit：
// 无降级、无 __ERROR__，通道静默关闭。
func TestProcessQueryStreamConsumerCancel(t *testing.T) {
	h := newHarness(t, []scriptEntry{
		{lines: []string{contentLine("a")}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 启动前取消
	ch := h.engine.ProcessQueryStream(ctx, "问题", 5, "")
	for c := range ch {
		t.Fatalf("取消后不应有输出块: %+v", c)
	}
}

// TestHistoryToMessages 历史转换：角色过滤、空白跳过、1500 rune 截断加省略号、
// 只保留最近 20 条。
func TestHistoryToMessages(t *testing.T) {
	long := strings.Repeat("汉", 1501)
	items := []db.HistoryItem{
		{Role: "system", Content: "应被跳过"},
		{Role: "user", Content: "   "},
		{Role: "user", Content: "问题一"},
		{Role: "assistant", Content: long},
	}
	msgs := historyToMessages(items)
	if len(msgs) != 2 {
		t.Fatalf("消息数 = %d, 期望 2", len(msgs))
	}
	if msgs[0].Role != "user" || *msgs[0].Content != "问题一" {
		t.Fatalf("msgs[0] = %+v", msgs[0])
	}
	wantTruncated := strings.Repeat("汉", 1500) + "…"
	if msgs[1].Role != "assistant" || *msgs[1].Content != wantTruncated {
		t.Fatalf("超长历史应截断为 1500 rune + …, 实际长度 %d", len([]rune(*msgs[1].Content)))
	}

	var many []db.HistoryItem
	for i := 0; i < 25; i++ {
		many = append(many, db.HistoryItem{Role: "user", Content: "m" + string(rune('a'+i))})
	}
	kept := historyToMessages(many)
	if len(kept) != MaxHistoryMessages {
		t.Fatalf("保留条数 = %d, 期望 %d", len(kept), MaxHistoryMessages)
	}
	if *kept[0].Content != "mf" || *kept[19].Content != "my" {
		t.Fatalf("应保留最近 20 条: 首=%q 尾=%q", *kept[0].Content, *kept[19].Content)
	}
}

// TestConversationsSessionLifecycle 会话生命周期：30 分钟 UTC 超时、
// 活跃刷新、全清驱逐缓存。
func TestConversationsSessionLifecycle(t *testing.T) {
	store := &fakeStore{}
	c := NewConversations(store)
	c.log = discardLogger()
	current := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return current }
	seq := 0
	c.newID = func() string {
		seq++
		return "sess-" + string(rune('0'+seq))
	}

	sid1, isNew := c.GetOrCreateSession(1)
	if !isNew || sid1 != "sess-1" {
		t.Fatalf("首次会话 = %s/%v, 期望 sess-1/true", sid1, isNew)
	}
	sid2, isNew := c.GetOrCreateSession(1)
	if isNew || sid2 != "sess-1" {
		t.Fatalf("续用会话 = %s/%v, 期望 sess-1/false", sid2, isNew)
	}

	// 29 分钟：仍续用
	current = current.Add(29 * time.Minute)
	sid3, isNew := c.GetOrCreateSession(1)
	if isNew || sid3 != "sess-1" {
		t.Fatalf("29 分钟应续用: %s/%v", sid3, isNew)
	}

	// 31 分钟：活跃时间在 +29min 被刷新，仍在 30 分钟窗口内 → 续用
	current = current.Add(2 * time.Minute)
	sid4, isNew := c.GetOrCreateSession(1)
	if isNew || sid4 != "sess-1" {
		t.Fatalf("+31 分钟应续用（+29 时已刷新活跃）: %s/%v", sid4, isNew)
	}

	// 62 分钟（距 +31 刷新已 31 分钟）：超时新会话
	current = current.Add(31 * time.Minute)
	sid5, isNew := c.GetOrCreateSession(1)
	if !isNew || sid5 == "sess-1" {
		t.Fatalf("超时应新建会话: %s/%v", sid5, isNew)
	}

	// SaveMessage 成功刷新 lastActive：再过 2 分钟仍在 30 分钟窗口内
	current = current.Add(29 * time.Minute)
	if !c.SaveMessage(context.Background(), 1, sid5, "user", "hi", nil) {
		t.Fatal("SaveMessage 应成功")
	}
	current = current.Add(2 * time.Minute)
	sid6, isNew := c.GetOrCreateSession(1)
	if isNew || sid6 != sid5 {
		t.Fatalf("保存消息后应续用: %s/%v", sid6, isNew)
	}

	// ClearUserHistory 全清 → 驱逐缓存 → 新会话
	if n := c.ClearUserHistory(context.Background(), 1, ""); n != 3 {
		t.Fatalf("ClearUserHistory 返回 = %d, 期望 3", n)
	}
	sid7, isNew := c.GetOrCreateSession(1)
	if !isNew || sid7 == sid6 {
		t.Fatalf("全清后应新建会话: %s/%v", sid7, isNew)
	}
	// 驱逐的实参应为空串（等价 Python None）
	if len(store.cleared) != 1 || store.cleared[0] != "" {
		t.Fatalf("清除调用记录 = %v", store.cleared)
	}
}

// TestGetOrCreateSessionDegrade 时钟生成器异常 → 降级随机 uuid 当新会话
// （对齐 conversation_manager.py:77-80 的 except 分支）。
func TestGetOrCreateSessionDegrade(t *testing.T) {
	c := NewConversations(&fakeStore{})
	c.log = discardLogger()
	c.now = func() time.Time { panic("时钟故障") }

	sid1, isNew := c.GetOrCreateSession(9)
	if !isNew {
		t.Fatal("降级路径应返回新会话")
	}
	if len(sid1) != 36 || sid1[8] != '-' {
		t.Fatalf("降级会话 ID 应为 uuid 形态: %q", sid1)
	}
	// Python 降级分支不写缓存：再次调用仍是新会话且 ID 不同
	sid2, isNew2 := c.GetOrCreateSession(9)
	if !isNew2 || sid2 == sid1 {
		t.Fatalf("降级不应写缓存: %s/%v", sid2, isNew2)
	}
}

// TestGetSessionInfo 会话信息查询。
func TestGetSessionInfo(t *testing.T) {
	store := &fakeStore{}
	c := NewConversations(store)
	c.log = discardLogger()

	if info := c.GetSessionInfo(context.Background(), 1); info != nil {
		t.Fatalf("未缓存用户应返回 nil: %+v", info)
	}

	c.GetOrCreateSession(1)
	store.mu.Lock()
	store.history = []db.HistoryItem{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
	}
	store.mu.Unlock()

	info := c.GetSessionInfo(context.Background(), 1)
	if info == nil {
		t.Fatal("缓存命中应返回会话信息")
	}
	if !info.IsActive || info.MessageCount != 3 || len(info.SessionID) != 36 {
		t.Fatalf("会话信息 = %+v", info)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{6})?\+00:00$`).MatchString(info.LastActive) {
		t.Fatalf("LastActive 应为 Python aware-UTC isoformat 形态: %q", info.LastActive)
	}
}

// TestBuildSystemPrompt 系统提示词组装：占位符替换、/ask 上下文、工具说明、
// 频道上下文错误降级为空串。
func TestBuildSystemPrompt(t *testing.T) {
	newEngineWithMemory := func(mem ChannelContextProvider) *Engine {
		h := newHarness(t, nil)
		h.engine.memory = mem
		return h.engine
	}

	t.Run("完整占位替换", func(t *testing.T) {
		e := newEngineWithMemory(constMemory("这是一个多频道总结系统。"))
		sp := e.buildSystemPrompt(context.Background(), "@频道", true)
		for _, want := range []string{
			"测试人格",
			"这是一个多频道总结系统。",
			"\n【命令上下文】\n用户通过 /ask 命令显式限定了查询频道：@频道。涉及频道内容时请先调用 resolve_channel 解析该频道，再以解析出的 channel_id 限定检索。\n",
			"6. 最多进行10轮工具调用，之后必须基于已收集的信息给出最终回答",
		} {
			if !strings.Contains(sp, want) {
				t.Fatalf("系统提示词缺少片段 %q", want)
			}
		}
		if strings.Contains(sp, "{") || strings.Contains(sp, "}") {
			t.Fatal("系统提示词不应残留占位符")
		}
		if !regexp.MustCompile(`当前日期时间：\d{4}-\d{2}-\d{2} \d{2}:\d{2} \(UTC\)`).MatchString(sp) {
			t.Fatalf("系统提示词缺少 UTC 时间行")
		}
	})

	t.Run("直连形态无工具说明", func(t *testing.T) {
		e := newEngineWithMemory(constMemory("这是一个多频道总结系统。"))
		sp := e.buildSystemPrompt(context.Background(), "", false)
		// 基础模板正文含"检索工具"字样，工具说明段以标题"## 检索工具"与
		// {max_iterations} 行为特征
		if strings.Contains(sp, "## 检索工具") || strings.Contains(sp, "最多进行") {
			t.Fatal("降级直连的系统提示词不应包含工具说明段")
		}
		if strings.Contains(sp, "【命令上下文】") {
			t.Fatal("降级直连的系统提示词不应包含命令上下文")
		}
	})

	t.Run("频道上下文异常降级为空", func(t *testing.T) {
		e := newEngineWithMemory(errMemory{})
		sp := e.buildSystemPrompt(context.Background(), "", true)
		if strings.Contains(sp, "这是一个多频道总结系统。") {
			t.Fatal("频道上下文失败时应降级为空串")
		}
		if strings.Contains(sp, "{channel_context}") {
			t.Fatal("降级后不应残留占位符")
		}
	})
}

// 编译期接口对齐检查：*llm.Client 必须满足 ChatCompleter。
var _ ChatCompleter = (*llm.Client)(nil)
