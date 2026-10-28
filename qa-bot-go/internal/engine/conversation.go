// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// 本文件是 conversation_manager.py（会话管理器）的移植：
// 30 分钟 UTC 超时的会话生命周期（进程内缓存）+ MySQL 持久化。
// 时区纪律（规格 R3）：一律 time.Now().UTC()，超时窗口按 UTC 计算。

package engine

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
)

// 会话生命周期常量（conversation_manager.py:28-32 逐项对齐）。
const (
	// SessionTimeoutMinutes 会话超时时间（分钟）。
	SessionTimeoutMinutes = 30
	// MaxMessagesPerSession 取历史时的消息条数上限（10轮对话 = 20条消息）。
	MaxMessagesPerSession = 20
)

// ConversationStore 会话持久化的窄接口（消费者包内定义；生产实现为 *db.Manager，
// 其方法签名与本接口逐字对应）。sessionID 语义对齐 Python 的 None：空串表示
// 不限定会话（ClearUserConversations 清除该用户全部会话）。
type ConversationStore interface {
	SaveConversation(ctx context.Context, userID int64, sessionID, role, content string, metadata map[string]any) error
	GetConversationHistory(ctx context.Context, userID int64, sessionID string, limit int) ([]db.HistoryItem, error)
	ClearUserConversations(ctx context.Context, userID int64, sessionID string) (int64, error)
}

// sessionEntry 内存缓存的会话条目（conversation_manager.py:38）。
type sessionEntry struct {
	sessionID  string
	lastActive time.Time
}

// Conversations 会话管理器（对齐 ConversationManager）。
// 内存缓存非并发安全（Python 单协程事件循环），Go 侧以互斥锁保护。
type Conversations struct {
	mu    sync.Mutex
	cache map[int64]*sessionEntry

	store ConversationStore
	log   *slog.Logger

	// now / newID 可注入（测试用）；生产为 time.Now / uuid4String。
	now   func() time.Time
	newID func() string
}

// NewConversations 构造会话管理器（store 不可为 nil），并记录与 Python 版
// 一致的初始化日志（conversation_manager.py:39）。
func NewConversations(store ConversationStore) *Conversations {
	log := slog.Default()
	log.Info("会话管理器初始化完成")
	return &Conversations{
		cache: make(map[int64]*sessionEntry),
		store: store,
		log:   log,
		now:   func() time.Time { return time.Now().UTC() },
		newID: uuid4String,
	}
}

// GetOrCreateSession 获取或创建用户会话（conversation_manager.py:41-80）。
//
// 返回 (sessionID, isNew)。缓存命中且距上次活跃不足 30 分钟（UTC）→ 续用；
// 超时或无缓存 → 创建新会话。任何异常降级为随机 uuid 当新会话
// （对齐 Python except 分支）。
func (c *Conversations) GetOrCreateSession(userID int64) (sessionID string, isNew bool) {
	// Python 整个方法体包在 try/except 中，异常时返回随机 uuid；
	// Go 以 recover 复刻该兜底（注入的时钟/生成器可触发，便于测试）。
	defer func() {
		if r := recover(); r != nil {
			c.log.Error(fmt.Sprintf("获取/创建会话失败: %T: %v", r, r))
			sessionID = uuid4String()
			isNew = true
		}
	}()

	now := c.now()

	c.mu.Lock()
	cached, ok := c.cache[userID]
	if ok {
		sessionID = cached.sessionID
		lastActive := cached.lastActive

		// 检查是否超时
		if now.Sub(lastActive) < SessionTimeoutMinutes*time.Minute {
			// 会话仍然有效，更新活动时间
			cached.lastActive = now
			c.mu.Unlock()
			c.log.Debug(fmt.Sprintf("用户 %d 继续使用会话 %s", userID, sessionID))
			return sessionID, false
		}
		// 会话超时，创建新会话（落到下方创建分支）
		c.mu.Unlock()
		c.log.Info(fmt.Sprintf("用户 %d 的会话 %s 已超时", userID, sessionID))
	} else {
		c.mu.Unlock()
	}

	// 创建新会话
	newSessionID := c.newID()
	c.mu.Lock()
	c.cache[userID] = &sessionEntry{sessionID: newSessionID, lastActive: now}
	c.mu.Unlock()

	c.log.Info(fmt.Sprintf("为用户 %d 创建新会话 %s", userID, newSessionID))
	return newSessionID, true
}

// SaveMessage 保存对话消息并刷新缓存活跃时间（conversation_manager.py:82-117）。
// 持久化失败仅记日志并返回 false，不向调用方传播异常（对齐 Python）。
// metadata 为 nil 等价 Python 的 None。
func (c *Conversations) SaveMessage(ctx context.Context, userID int64, sessionID, role, content string, metadata map[string]any) bool {
	if err := c.store.SaveConversation(ctx, userID, sessionID, role, content, metadata); err != nil {
		c.log.Error(fmt.Sprintf("保存消息失败: %T: %s", err, err))
		return false
	}

	// 保存成功才更新缓存的活动时间（conversation_manager.py:108-111）
	c.mu.Lock()
	if entry, ok := c.cache[userID]; ok {
		entry.lastActive = c.now()
	}
	c.mu.Unlock()
	return true
}

// GetHistory 获取对话历史（用于 RAG 上下文；conversation_manager.py:119-138）。
// LIMIT 取 MaxMessagesPerSession；失败记日志返回空切片（对齐 Python except）。
func (c *Conversations) GetHistory(ctx context.Context, userID int64, sessionID string) []db.HistoryItem {
	history, err := c.store.GetConversationHistory(ctx, userID, sessionID, MaxMessagesPerSession)
	if err != nil {
		c.log.Error(fmt.Sprintf("获取对话历史失败: %T: %s", err, err))
		return nil
	}
	return history
}

// ClearUserHistory 清除用户对话历史（conversation_manager.py:140-169）。
//
// sessionID 传空串等价 Python 的 None：清除该用户全部会话并驱逐缓存条目；
// 指定 sessionID 时仅当缓存中的会话与之匹配才驱逐（下一步消息将开启新会话、
// 下发 __NEW_SESSION__，这是 /clear 行为的关键一环）。失败记日志返回 0、
// 不传播错误（对齐 Python except 返回 0）。
func (c *Conversations) ClearUserHistory(ctx context.Context, userID int64, sessionID string) (deleted int64) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error(fmt.Sprintf("清除对话历史失败: %T: %v", r, r))
			deleted = 0
		}
	}()

	n, err := c.store.ClearUserConversations(ctx, userID, sessionID)
	if err != nil {
		c.log.Error(fmt.Sprintf("清除对话历史失败: %T: %s", err, err))
		return 0
	}
	deleted = n

	c.mu.Lock()
	if sessionID == "" {
		delete(c.cache, userID)
	} else if entry, ok := c.cache[userID]; ok && entry.sessionID == sessionID {
		delete(c.cache, userID)
	}
	c.mu.Unlock()
	return deleted
}

// SessionInfo 会话信息快照（get_session_info 返回字典的字段化）。
type SessionInfo struct {
	// SessionID 当前会话 ID。
	SessionID string
	// LastActive 最近活跃时间（Python aware UTC isoformat 字符串形态）。
	LastActive string
	// DurationSeconds 距最近活跃的秒数（int(total_seconds()) 截断语义）。
	DurationSeconds int64
	// MessageCount 当前会话消息数（以 limit=1000 查询计数，对齐 Python）。
	MessageCount int
	// IsActive 会话是否仍然活跃（< 30 分钟）。
	IsActive bool
}

// GetSessionInfo 获取用户的会话信息（conversation_manager.py:201-237）。
// 用户不在缓存中返回 nil；失败记日志返回 nil（对齐 Python except）。
func (c *Conversations) GetSessionInfo(ctx context.Context, userID int64) (info *SessionInfo) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error(fmt.Sprintf("获取会话信息失败: %T: %v", r, r))
			info = nil
		}
	}()

	c.mu.Lock()
	entry, ok := c.cache[userID]
	c.mu.Unlock()
	if !ok {
		return nil
	}

	now := c.now()
	duration := now.Sub(entry.lastActive)

	// 获取会话中的消息数（Python 以 limit=1000 查询）
	history, err := c.store.GetConversationHistory(ctx, userID, entry.sessionID, 1000)
	if err != nil {
		c.log.Error(fmt.Sprintf("获取会话信息失败: %T: %s", err, err))
		return nil
	}

	return &SessionInfo{
		SessionID:       entry.sessionID,
		LastActive:      pythonISOUTC(entry.lastActive),
		DurationSeconds: int64(duration.Seconds()),
		MessageCount:    len(history),
		IsActive:        duration < SessionTimeoutMinutes*time.Minute,
	}
}

// pythonISOUTC 输出 Python aware-UTC datetime 的 isoformat 形态：
// "YYYY-MM-DDTHH:MM:SS[.ffffff]+00:00"（微秒非零时以 6 位零填充出现）。
func pythonISOUTC(t time.Time) string {
	base := t.UTC().Format("2006-01-02T15:04:05")
	if micros := t.Nanosecond() / 1000; micros != 0 {
		base += fmt.Sprintf(".%06d", micros)
	}
	return base + "+00:00"
}

// uuidFallbackCounter crypto/rand 失败（极端罕见）时的兜底序号。
var uuidFallbackCounter atomic.Uint64

// uuid4String 生成 RFC 4122 v4 随机 UUID 字符串
// （str(uuid.uuid4()) 的等价物，用作会话 ID）。仅依赖 stdlib。
func uuid4String() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 兜底：时间纳秒 + 自增序号，保证会话创建不被阻塞
		n := uuidFallbackCounter.Add(1)
		binary.BigEndian.PutUint64(b[0:8], uint64(time.Now().UnixNano()))
		binary.BigEndian.PutUint64(b[8:16], n)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}
