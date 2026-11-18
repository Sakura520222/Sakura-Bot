// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package mtproto

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/filelock"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// BotOptions 配置主 Bot 或问答辅助 Bot 的 MTProto 启动参数。
type BotOptions struct {
	APIID       int
	APIHash     string
	BotToken    string
	SessionPath string
	Logger      *slog.Logger
	Dispatcher  *Dispatcher
	PeerCache   *PeerCache
	OnReady     func(ctx context.Context, self *ProfileSummary)
}

// UserBotOptions 配置长期运行的 UserBot 客户端管理器参数。
type UserBotOptions struct {
	APIID         int
	APIHash       string
	PhoneNumber   string
	SessionPath   string
	Enabled       bool
	FallbackToBot bool
	SessionOnly   bool // Unattended startup must reuse an authorized session.
	Logger        *slog.Logger
	Dispatcher    *Dispatcher
	PeerCache     *PeerCache
	Authenticator auth.UserAuthenticator
	Stdin         io.Reader
	Stdout        io.Writer
}

// BatchJoinFailure 记录批量加入源频道时单个失败条目。
type BatchJoinFailure struct {
	Channel string `json:"channel"`
	Reason  string `json:"reason"`
}

// BatchJoinResult 汇总 UserBot 自动批量加入转发源频道的执行结果（对齐 userbot_client.py:608-615）。
type BatchJoinResult struct {
	Success      bool               `json:"success"`
	Message      string             `json:"message,omitempty"`
	Error        string             `json:"error,omitempty"`
	Total        int                `json:"total"`
	SuccessCount int                `json:"success_count"`
	FailedCount  int                `json:"failed_count"`
	FailedList   []BatchJoinFailure `json:"failed_list,omitempty"`
	Results      []JoinLeaveResult  `json:"results,omitempty"`
}

// ClientOption 定义构建 Client 的可选注入项。
type ClientOption func(*Client)

// WithPeerCache 注入自定义 PeerCache。
func WithPeerCache(cache *PeerCache) ClientOption {
	return func(c *Client) {
		if cache != nil {
			c.peers = cache
		}
	}
}

// WithDispatcher 注入自定义实时事件 Dispatcher。
func WithDispatcher(d *Dispatcher) ClientOption {
	return func(c *Client) {
		if d != nil {
			c.dispatcher = d
		}
	}
}

// WithLogger 注入结构化日志实例。
func WithLogger(l *slog.Logger) ClientOption {
	return func(c *Client) {
		if l != nil {
			c.log = l
		}
	}
}

// WithSessionStorage 注入 HybridSessionStorage。
func WithSessionStorage(s *HybridSessionStorage) ClientOption {
	return func(c *Client) {
		if s != nil {
			c.sessionStore = s
		}
	}
}

// WithSelf 预设当前登录身份（单元测试常用）。
func WithSelf(id int64, username string) ClientOption {
	return func(c *Client) {
		c.SetSelf(id, username)
	}
}

// Client 实现 Messenger 接口，既可驱动在线 telegram.Client，也可包裹任意 tg.Invoker 进行离线单元测试。
type Client struct {
	mu           sync.RWMutex
	tgClient     *telegram.Client
	raw          *tg.Client
	invoker      tg.Invoker
	peers        *PeerCache
	sessionStore *HybridSessionStorage
	dispatcher   *Dispatcher
	log          *slog.Logger

	connected    atomic.Bool
	selfID       atomic.Int64
	selfUsername atomic.Value // string
	selfProfile  atomic.Pointer[ProfileSummary]

	linkedMu        sync.RWMutex
	linkedChatCache map[string]int64
}

var _ Messenger = (*Client)(nil)

// NewClient 创建空的 MTProto Client（后续可调用 StartBot 运行）。
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		peers:           NewMemoryPeerCache(),
		log:             slog.Default(),
		linkedChatCache: make(map[string]int64),
	}
	c.selfUsername.Store("")
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	if c.dispatcher == nil {
		c.dispatcher = NewDispatcher(c.peers, c.log)
	}
	return c
}

// NewClientFromInvoker 基于给定 tg.Invoker（如 mock invoker）构造已就绪的 Client。
func NewClientFromInvoker(invoker tg.Invoker, opts ...ClientOption) *Client {
	c := NewClient(opts...)
	if invoker != nil {
		c.raw = tg.NewClient(invoker)
		c.invoker = invoker
		c.connected.Store(true)
	}
	return c
}

// NewClientFromTG 基于给定 *tg.Client 构造已就绪的 Client（供单元测试或自定义传输使用）。
func NewClientFromTG(raw *tg.Client, opts ...ClientOption) *Client {
	c := NewClient(opts...)
	if raw != nil {
		c.raw = raw
		c.invoker = raw.Invoker()
		c.connected.Store(true)
	}
	return c
}

// API 返回底层 *tg.Client。
func (c *Client) API() *tg.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.raw
}

// TelegramClient 返回在线运行的 *telegram.Client（离线单测模式下为 nil）。
func (c *Client) TelegramClient() *telegram.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tgClient
}

// Peers 返回关联的 PeerCache。
func (c *Client) Peers() *PeerCache {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.peers
}

// Dispatcher 返回关联的实时事件分发器。
func (c *Client) Dispatcher() *Dispatcher {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dispatcher
}

// SessionStorage 返回混合会话存储。
func (c *Client) SessionStorage() *HybridSessionStorage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionStore
}

// SelfID 返回当前已认证账号 ID。
func (c *Client) SelfID() int64 { return c.selfID.Load() }

// SelfUsername 返回当前已认证账号用户名（不含 '@'）。
func (c *Client) SelfUsername() string {
	v, _ := c.selfUsername.Load().(string)
	return v
}

// SelfProfile 返回当前账号摘要副本。
func (c *Client) SelfProfile() *ProfileSummary {
	p := c.selfProfile.Load()
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// IsConnected 报告底层连接是否处于活跃已认证状态。
func (c *Client) IsConnected() bool { return c.connected.Load() }

// SetConnected 手动更新连接状态标记（供测试使用）。
func (c *Client) SetConnected(v bool) { c.connected.Store(v) }

// SetSelf 手动设置当前登录账号信息（供测试使用）。
func (c *Client) SetSelf(id int64, username string) {
	uname := normalizeUsername(username)
	c.selfID.Store(id)
	c.selfUsername.Store(uname)
	c.selfProfile.Store(&ProfileSummary{
		ID:       id,
		Username: uname,
	})
	if d := c.Dispatcher(); d != nil {
		d.SetSelfID(id)
	}
}

func (c *Client) recordSelfUser(u *tg.User) ProfileSummary {
	prof := tgUserToProfileSummary(u)
	c.selfID.Store(prof.ID)
	c.selfUsername.Store(prof.Username)
	c.selfProfile.Store(&prof)
	if peers := c.Peers(); peers != nil {
		peers.ApplyUsersChats([]tg.UserClass{u}, nil)
	}
	if d := c.Dispatcher(); d != nil {
		d.SetSelfID(prof.ID)
	}
	return prof
}

var errBotSessionMismatchRetry = errors.New("imported session mismatch or unregistered; retrying fresh bot auth")

// StartBot 启动 MTProto Bot 客户端：自动加载/导入会话、按需执行 Auth().Bot 鉴权（若旧会话失效或账号不匹配则清理 .gotd.json 重登），并阻塞直至 ctx 结束。
func (c *Client) StartBot(ctx context.Context, opts BotOptions) error {
	if opts.APIID <= 0 || strings.TrimSpace(opts.APIHash) == "" {
		return errors.New("APIID and APIHash are required")
	}
	if strings.TrimSpace(opts.BotToken) == "" {
		return errors.New("BotToken is required")
	}

	logger := opts.Logger
	if logger == nil {
		logger = c.log
	}
	if logger == nil {
		logger = slog.Default()
	}

	release, err := lockSession(ctx, opts.SessionPath)
	if err != nil {
		return err
	}
	defer release()

	sessionStore := c.sessionStore
	if sessionStore == nil || (opts.SessionPath != "" && sessionStore.BasePath() == "") {
		sessionStore = NewHybridSessionStorage(opts.SessionPath)
	}

	peers := opts.PeerCache
	if peers == nil {
		if opts.SessionPath != "" {
			loadedPeers, err := NewPeerCache(opts.SessionPath)
			if err != nil {
				logger.Warn("初始化 PeerCache 失败，降级为内存缓存", "error", err)
				peers = NewMemoryPeerCache()
			} else {
				peers = loadedPeers
			}
		} else if c.peers != nil {
			peers = c.peers
		} else {
			peers = NewMemoryPeerCache()
		}
	}

	disp := opts.Dispatcher
	if disp == nil {
		if c.dispatcher != nil {
			disp = c.dispatcher
			disp.peers = peers
		} else {
			disp = NewDispatcher(peers, logger)
		}
	}

	c.mu.Lock()
	c.log = logger
	c.sessionStore = sessionStore
	c.peers = peers
	c.dispatcher = disp
	c.mu.Unlock()

	expectedBotID := parseBotIDFromToken(opts.BotToken)

	for attempt := 0; attempt < 2; attempt++ {
		err := c.runBotOnce(ctx, opts, sessionStore, disp, expectedBotID, attempt == 0)
		if errors.Is(err, errBotSessionMismatchRetry) && attempt == 0 {
			logger.Warn("检测到旧会话失效或身份不匹配，清理 .gotd.json 后重新执行 Bot 登录")
			_ = sessionStore.Clear()
			continue
		}
		if attempt == 0 && isAuthInvalidError(err) {
			logger.Warn("MTProto 会话密钥已失效，清理 .gotd.json 后重新执行 Bot 登录", "error", err)
			_ = sessionStore.Clear()
			continue
		}
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

func (c *Client) runBotOnce(
	ctx context.Context,
	opts BotOptions,
	sessionStore *HybridSessionStorage,
	disp *Dispatcher,
	expectedBotID int64,
	allowReset bool,
) error {
	gaps := updateManager(sessionStore.BasePath(), disp, c.log)

	tgClient := telegram.NewClient(opts.APIID, opts.APIHash, telegram.Options{
		SessionStorage: sessionStore,
		UpdateHandler:  gaps,
		OnConnectionState: func(state telegram.ConnectionState) {
			if state == telegram.ConnectionStateDisconnected {
				c.connected.Store(false)
			} else if state == telegram.ConnectionStateReady && c.SelfID() > 0 {
				c.connected.Store(true)
			}
		},
	})

	c.mu.Lock()
	c.tgClient = tgClient
	c.raw = tgClient.API()
	c.invoker = tgClient
	c.mu.Unlock()

	defer func() {
		c.connected.Store(false)
	}()

	return tgClient.Run(ctx, func(runCtx context.Context) error {
		status, err := tgClient.Auth().Status(runCtx)
		if err != nil {
			if allowReset && isAuthInvalidError(err) {
				return errBotSessionMismatchRetry
			}
			return fmt.Errorf("check bot auth status: %w", err)
		}

		if !status.Authorized {
			if _, authErr := tgClient.Auth().Bot(runCtx, opts.BotToken); authErr != nil {
				if allowReset && isAuthInvalidError(authErr) {
					return errBotSessionMismatchRetry
				}
				return fmt.Errorf("bot token sign in: %w", authErr)
			}
		} else if status.User != nil {
			if !status.User.Bot || (expectedBotID > 0 && status.User.ID != expectedBotID) {
				if allowReset {
					return errBotSessionMismatchRetry
				}
				return fmt.Errorf("authorized session user %d (bot=%v) does not match expected bot %d", status.User.ID, status.User.Bot, expectedBotID)
			}
		}

		self, err := tgClient.Self(runCtx)
		if err != nil {
			if allowReset && isAuthInvalidError(err) {
				return errBotSessionMismatchRetry
			}
			return fmt.Errorf("fetch bot self: %w", err)
		}
		if !self.Bot || (expectedBotID > 0 && self.ID != expectedBotID) {
			if allowReset {
				return errBotSessionMismatchRetry
			}
			return fmt.Errorf("self user %d (bot=%v) does not match expected bot %d", self.ID, self.Bot, expectedBotID)
		}

		prof := c.recordSelfUser(self)
		c.connected.Store(true)
		c.log.Info("MTProto Bot 客户端启动成功", "bot_id", prof.ID, "username", prof.Username)

		if opts.OnReady != nil {
			opts.OnReady(runCtx, &prof)
		}

		return gaps.Run(runCtx, tgClient.API(), self.ID, updates.AuthOptions{
			IsBot: true,
		})
	})
}

func parseBotIDFromToken(token string) int64 {
	colon := strings.IndexByte(token, ':')
	if colon <= 0 {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimSpace(token[:colon]), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func isAuthInvalidError(err error) bool {
	if err == nil {
		return false
	}
	if auth.IsUnauthorized(err) || auth.IsKeyUnregistered(err) {
		return true
	}
	var lost *telegram.AuthLostError
	if errors.As(err, &lost) {
		return true
	}
	return tgerr.Is(
		err,
		"AUTH_KEY_UNREGISTERED",
		"AUTH_KEY_INVALID",
		"AUTH_KEY_DUPLICATED",
		"SESSION_REVOKED",
		"SESSION_EXPIRED",
		"USER_DEACTIVATED",
		"USER_DEACTIVATED_BAN",
	)
}

// ResolveChannel 解析频道或超级群引用并返回完整的 ChannelInfo。
func (c *Client) ResolveChannel(ctx context.Context, ref string) (*ChannelInfo, error) {
	api := c.API()
	if api == nil {
		return nil, errors.New("mtproto client not initialized")
	}

	parsed, err := parseTargetString(ref)
	if err != nil {
		return nil, err
	}
	if parsed.isInvite {
		return nil, fmt.Errorf("invite link %q cannot be resolved directly", ref)
	}

	peers := c.Peers()
	var inputCh *tg.InputChannel

	if parsed.username != "" {
		cp, err := peers.resolveUsernameCachedOrRPC(ctx, api, parsed.username)
		if err != nil {
			return nil, err
		}
		if cp.Kind != PeerKindChannel {
			return nil, fmt.Errorf("entity @%s is %s, not channel", parsed.username, cp.Kind)
		}
		// 若缓存条目缺少标题或广播属性（例如仅从旧 entities 表导入），补充调用 ChannelsGetChannels
		if cp.Title != "" && (cp.Broadcast || cp.Megagroup) {
			return cp.ToChannelInfo(c.lookupCachedLinkedChat(ref, cp.MarkedID)), nil
		}
		inputCh = &tg.InputChannel{ChannelID: cp.RawID, AccessHash: cp.AccessHash}
	} else if parsed.hasMarked {
		ic, err := peers.resolveIntToInputChannel(parsed.markedID)
		if err != nil {
			return nil, err
		}
		inputCh = ic.(*tg.InputChannel)
		marked := ChannelMarkedID(inputCh.ChannelID)
		if cp, ok := peers.GetByMarkedID(marked); ok && cp.Title != "" && (cp.Broadcast || cp.Megagroup) {
			return cp.ToChannelInfo(c.lookupCachedLinkedChat(ref, marked)), nil
		}
	} else {
		return nil, fmt.Errorf("unsupported channel reference %q", ref)
	}

	chatsRes, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{inputCh})
	if err != nil {
		// 若网络查询失败但本地缓存已有基本 ID，回退返回缓存快照
		marked := ChannelMarkedID(inputCh.ChannelID)
		if cp, ok := peers.GetByMarkedID(marked); ok && cp.Title != "" {
			return cp.ToChannelInfo(c.lookupCachedLinkedChat(ref, marked)), nil
		}
		return nil, err
	}

	var chats []tg.ChatClass
	switch v := chatsRes.(type) {
	case *tg.MessagesChats:
		chats = v.Chats
	case *tg.MessagesChatsSlice:
		chats = v.Chats
	}
	peers.ApplyUsersChats(nil, chats)

	marked := ChannelMarkedID(inputCh.ChannelID)
	if cp, ok := peers.GetByMarkedID(marked); ok {
		return cp.ToChannelInfo(c.lookupCachedLinkedChat(ref, marked)), nil
	}
	for _, cc := range chats {
		if ch, ok := cc.(*tg.Channel); ok && ch.ID == inputCh.ChannelID {
			peers.ApplyUsersChats(nil, []tg.ChatClass{ch})
			if cp, ok := peers.GetByMarkedID(ChannelMarkedID(ch.ID)); ok {
				return cp.ToChannelInfo(c.lookupCachedLinkedChat(ref, cp.MarkedID)), nil
			}
		}
	}
	return nil, fmt.Errorf("channel %q not found", ref)
}

func normalizeLinkedCacheKey(channelRef string) string {
	return strings.ToLower(strings.TrimSpace(channelRef))
}

func (c *Client) lookupCachedLinkedChat(ref string, markedID int64) int64 {
	c.linkedMu.RLock()
	defer c.linkedMu.RUnlock()
	if id, ok := c.linkedChatCache[normalizeLinkedCacheKey(ref)]; ok {
		return id
	}
	if markedID != 0 {
		if id, ok := c.linkedChatCache[strconv.FormatInt(markedID, 10)]; ok {
			return id
		}
	}
	return 0
}

// GetLinkedDiscussionChatID 获取频道绑定的关联讨论组标记 ID（无关联群时返回 0），内置线程安全缓存。
func (c *Client) GetLinkedDiscussionChatID(ctx context.Context, channelRef string) (int64, error) {
	key := normalizeLinkedCacheKey(channelRef)
	if key == "" {
		return 0, errors.New("empty channel reference")
	}

	c.linkedMu.RLock()
	if cached, ok := c.linkedChatCache[key]; ok {
		c.linkedMu.RUnlock()
		return cached, nil
	}
	c.linkedMu.RUnlock()

	api := c.API()
	if api == nil {
		return 0, errors.New("mtproto client not initialized")
	}

	inputCh, err := c.Peers().ResolveInputChannel(ctx, api, channelRef)
	if err != nil {
		return 0, err
	}

	fullRes, err := api.ChannelsGetFullChannel(ctx, inputCh)
	if err != nil {
		return 0, err
	}
	c.Peers().ApplyUsersChats(fullRes.Users, fullRes.Chats)

	var linkedMarkedID int64
	if cf, ok := fullRes.FullChat.(*tg.ChannelFull); ok && cf != nil {
		if linkedRaw, ok := cf.GetLinkedChatID(); ok && linkedRaw > 0 {
			linkedMarkedID = ChannelMarkedID(linkedRaw)
		}
	}

	c.linkedMu.Lock()
	c.linkedChatCache[key] = linkedMarkedID
	if ic, ok := inputCh.(*tg.InputChannel); ok && ic.ChannelID > 0 {
		c.linkedChatCache[strconv.FormatInt(ChannelMarkedID(ic.ChannelID), 10)] = linkedMarkedID
	}
	c.linkedMu.Unlock()

	return linkedMarkedID, nil
}

// ClearLinkedChatCache 清除指定频道（为空字符串时清除全部）的讨论组 ID 缓存，返回移除的缓存键数量。
func (c *Client) ClearLinkedChatCache(channelRef string) int {
	c.linkedMu.Lock()
	defer c.linkedMu.Unlock()

	trimmed := strings.TrimSpace(channelRef)
	if trimmed == "" {
		n := len(c.linkedChatCache)
		clear(c.linkedChatCache)
		return n
	}

	removed := 0
	key := normalizeLinkedCacheKey(trimmed)
	if _, ok := c.linkedChatCache[key]; ok {
		delete(c.linkedChatCache, key)
		removed++
	}
	if parsed, err := parseTargetString(trimmed); err == nil {
		if parsed.username != "" {
			if cp, ok := c.Peers().GetByUsername(parsed.username); ok {
				mKey := strconv.FormatInt(cp.MarkedID, 10)
				if _, exists := c.linkedChatCache[mKey]; exists {
					delete(c.linkedChatCache, mKey)
					removed++
				}
			}
		} else if parsed.hasMarked {
			marked := parsed.markedID
			if marked > 0 {
				marked = ChannelMarkedID(marked)
			}
			mKey := strconv.FormatInt(marked, 10)
			if _, exists := c.linkedChatCache[mKey]; exists {
				delete(c.linkedChatCache, mKey)
				removed++
			}
		}
	}
	return removed
}

// LinkedChatCacheChannels 返回当前已缓存关联讨论组映射的全部频道键列表（排序稳定）。
func (c *Client) LinkedChatCacheChannels() []string {
	c.linkedMu.RLock()
	defer c.linkedMu.RUnlock()

	keys := make([]string, 0, len(c.linkedChatCache))
	for k := range c.linkedChatCache {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// UserBotManager 管理长期运行的 UserBot 客户端生命周期、频道加入/退出与状态上报（对齐 userbot_client.py）。
type UserBotManager struct {
	lifecycle   sync.Mutex
	mu          sync.RWMutex
	opts        UserBotOptions
	log         *slog.Logger
	client      *Client
	cancelRun   context.CancelFunc
	runDone     chan struct{}
	initialized bool
	lastErr     string
}

// NewUserBotManager 创建 UserBot 管理器实例。
func NewUserBotManager(opts UserBotOptions) *UserBotManager {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	var c *Client
	if opts.PeerCache != nil || opts.Dispatcher != nil {
		c = NewClient(
			WithPeerCache(opts.PeerCache),
			WithDispatcher(opts.Dispatcher),
			WithLogger(log),
		)
	}
	return &UserBotManager{
		opts:   opts,
		log:    log,
		client: c,
	}
}

// InjectClient 为单元测试直接注入预构造的 *Client 并标记就绪。
func (m *UserBotManager) InjectClient(c *Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = c
	m.initialized = c != nil
	m.lastErr = ""
}

// Client 返回底层 *Client（未初始化时为 nil）。
func (m *UserBotManager) Client() *Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.initialized {
		return nil
	}
	return m.client
}

// Start 启动 UserBot 并完成会话加载或交互式登录；就绪后在后台维持长连接，直至调用 Stop()。
func (m *UserBotManager) Start(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	if m.initialized && m.client != nil && m.client.IsConnected() {
		m.mu.Unlock()
		return nil
	}
	if m.runDone != nil {
		select {
		case <-m.runDone:
		default:
			// gotd is still running and can reconnect on its own.
			m.mu.Unlock()
			return nil
		}
	}
	opts := m.opts
	m.mu.Unlock()

	if opts.APIID <= 0 || strings.TrimSpace(opts.APIHash) == "" {
		err := errors.New("UserBot APIID/APIHash 未设置")
		m.setError(err)
		return err
	}

	release, err := lockSession(ctx, opts.SessionPath)
	if err != nil {
		m.setError(err)
		return err
	}

	sessionStore := NewHybridSessionStorage(opts.SessionPath)
	peers := opts.PeerCache
	if peers == nil {
		if opts.SessionPath != "" {
			if loaded, err := NewPeerCache(opts.SessionPath); err == nil {
				peers = loaded
			}
		}
		if peers == nil {
			peers = NewMemoryPeerCache()
		}
	}
	disp := opts.Dispatcher
	if disp == nil {
		disp = NewDispatcher(peers, m.log)
	}

	c := NewClient(
		WithSessionStorage(sessionStore),
		WithPeerCache(peers),
		WithDispatcher(disp),
		WithLogger(m.log),
	)

	gaps := updateManager(sessionStore.BasePath(), disp, m.log)

	tgClient := telegram.NewClient(opts.APIID, opts.APIHash, telegram.Options{
		SessionStorage: sessionStore,
		UpdateHandler:  gaps,
		OnConnectionState: func(state telegram.ConnectionState) {
			if state == telegram.ConnectionStateDisconnected {
				c.connected.Store(false)
			} else if state == telegram.ConnectionStateReady && c.SelfID() > 0 {
				c.connected.Store(true)
			}
		},
	})

	c.mu.Lock()
	c.tgClient = tgClient
	c.raw = tgClient.API()
	c.invoker = tgClient
	c.mu.Unlock()

	authenticator := opts.Authenticator
	if authenticator == nil {
		authenticator = NewTerminalAuthenticator(opts.PhoneNumber, opts.Stdin, opts.Stdout)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	readyCh := make(chan error, 1)
	doneCh := make(chan struct{})

	go func() {
		defer close(doneCh)
		defer release()
		defer c.connected.Store(false)

		runErr := tgClient.Run(runCtx, func(cctx context.Context) error {
			status, err := tgClient.Auth().Status(cctx)
			if err != nil {
				select {
				case readyCh <- err:
				default:
				}
				return err
			}

			if !status.Authorized {
				if opts.SessionOnly {
					err := errors.New("UserBot 未授权，请先运行 sakura-main --login-userbot")
					readyCh <- err
					return err
				}
				if strings.TrimSpace(opts.PhoneNumber) == "" {
					noPhoneErr := ErrEmptyPhoneNumber
					select {
					case readyCh <- noPhoneErr:
					default:
					}
					return noPhoneErr
				}
				if authErr := RunUserAuthFlow(cctx, tgClient.Auth(), authenticator); authErr != nil {
					select {
					case readyCh <- authErr:
					default:
					}
					return authErr
				}
			}

			self, err := tgClient.Self(cctx)
			if err != nil {
				select {
				case readyCh <- err:
				default:
				}
				return err
			}
			if self.Bot {
				err := errors.New("UserBot 会话对应机器人账号，请配置用户账号的会话文件")
				readyCh <- err
				return err
			}

			prof := c.recordSelfUser(self)
			c.connected.Store(true)
			m.log.Info("UserBot 客户端启动成功", "user_id", prof.ID, "username", prof.Username)

			select {
			case readyCh <- nil:
			default:
			}

			return gaps.Run(cctx, tgClient.API(), self.ID, updates.AuthOptions{
				IsBot: false,
			})
		})

		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			m.setError(runErr)
			select {
			case readyCh <- runErr:
			default:
			}
		}
	}()

	select {
	case <-ctx.Done():
		cancel()
		<-doneCh
		m.setError(ctx.Err())
		return ctx.Err()
	case err := <-readyCh:
		if err != nil {
			cancel()
			<-doneCh
			m.setError(err)
			return err
		}
	}

	m.mu.Lock()
	m.client = c
	m.cancelRun = cancel
	m.runDone = doneCh
	m.initialized = true
	m.lastErr = ""
	m.mu.Unlock()
	return nil
}

func (m *UserBotManager) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.lastErr = err.Error()
	}
}

// Stop 平滑关闭后台运行的 UserBot 连接。
func (m *UserBotManager) Stop() {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	cancel := m.cancelRun
	done := m.runDone
	c := m.client
	m.cancelRun = nil
	m.runDone = nil
	m.initialized = false
	m.client = nil
	m.mu.Unlock()

	if c != nil {
		c.connected.Store(false)
	}
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func lockSession(ctx context.Context, path string) (func(), error) {
	base, _, _, _ := NormalizeSessionPaths(path)
	if base == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(base), 0o700); err != nil {
		return nil, fmt.Errorf("创建 Telegram 会话目录: %w", err)
	}
	wait, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lock, err := filelock.Acquire(wait, base+".gotd.lock")
	if err != nil {
		return nil, fmt.Errorf("锁定 Telegram 会话 %s: %w", filepath.Base(base), err)
	}
	return func() { _ = lock.Close() }, nil
}

func updateManager(base string, dispatcher *Dispatcher, logger *slog.Logger) *updates.Manager {
	cfg := updates.Config{
		Handler: dispatcher, MaxChannelDifferenceConcurrency: 4,
		OnChannelTooLong: func(channelID int64) {
			logger.Warn("Telegram 频道离线更新超出补偿范围", "channel_id", channelID)
		},
		OnTooLong: func() {
			logger.Warn("Telegram 离线更新超出补偿范围")
		},
	}
	if base != "" {
		storage := updates.NewFileStorage(base + ".updates.gotd.json")
		cfg.Storage = storage
		cfg.AccessHasher = storage
	}
	return updates.New(cfg)
}

// Status 返回当前 UserBot 状态快照（对齐 WebUI /api/userbot/status）。
func (m *UserBotManager) Status() UserBotStatus {
	m.mu.RLock()
	opts := m.opts
	c := m.client
	init := m.initialized
	lastErr := m.lastErr
	m.mu.RUnlock()

	st := UserBotStatus{
		Enabled:       opts.Enabled,
		Initialized:   init,
		SessionPath:   opts.SessionPath,
		PhoneNumber:   opts.PhoneNumber,
		PhonePreview:  maskPhonePreview(opts.PhoneNumber),
		FallbackToBot: opts.FallbackToBot,
		Error:         lastErr,
	}
	if c == nil {
		if st.Error == "" && !init {
			st.Error = "客户端未初始化"
		}
		return st
	}
	st.Connected = c.IsConnected()
	st.Available = init && st.Connected
	st.UserInfo = c.SelfProfile()
	return st
}

// JoinChannel 加入公开频道/群组（channels.JoinChannel）或私有邀请链接（messages.ImportChatInvite）。
func (m *UserBotManager) JoinChannel(ctx context.Context, link string) JoinLeaveResult {
	c := m.Client()
	if c == nil || !c.IsConnected() || c.API() == nil {
		return JoinLeaveResult{
			Success: false,
			Message: "UserBot 不可用",
			Error:   "client_not_available",
			Channel: link,
		}
	}

	parsed, err := parseTargetString(link)
	if err != nil {
		return JoinLeaveResult{
			Success: false,
			Message: "无效的频道链接格式",
			Error:   "invalid_channel_format",
			Channel: link,
		}
	}

	api := c.API()
	if parsed.isInvite {
		joinRes, invErr := api.MessagesImportChatInvite(ctx, parsed.inviteHash)
		if invErr != nil {
			if tgerr.Is(invErr, "USER_ALREADY_PARTICIPANT") {
				return JoinLeaveResult{
					Success:       true,
					Message:       "已经是频道成员",
					Channel:       link,
					AlreadyJoined: true,
				}
			}
			return JoinLeaveResult{
				Success: false,
				Message: "无法加入频道（可能是无效的邀请链接或需要权限）",
				Error:   "invite_link_failed",
				Channel: link,
			}
		}
		upd := extractUpdatesFromJoinResult(joinRes)
		if upd != nil {
			c.Peers().ApplyUpdates(upd)
		}
		return JoinLeaveResult{
			Success:     true,
			Message:     "通过邀请链接加入频道成功",
			Channel:     link,
			ChannelInfo: extractFirstChannelFromUpdates(upd, c.Peers()),
		}
	}

	info, resErr := c.ResolveChannel(ctx, link)
	if resErr != nil {
		switch {
		case tgerr.Is(resErr, "CHANNEL_PRIVATE"):
			return JoinLeaveResult{
				Success: false,
				Message: "无法加入频道（私有频道，需要手动邀请）",
				Error:   "channel_private",
				Channel: link,
			}
		case tgerr.Is(resErr, "CHANNEL_INVALID", "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID"):
			return JoinLeaveResult{
				Success: false,
				Message: "无法加入频道（频道不存在或无效）",
				Error:   "channel_invalid",
				Channel: link,
			}
		default:
			return JoinLeaveResult{
				Success: false,
				Message: fmt.Sprintf("无法加入频道: %v", resErr),
				Error:   "get_entity_failed",
				Channel: link,
			}
		}
	}

	joinRes, joinErr := api.ChannelsJoinChannel(ctx, &tg.InputChannel{
		ChannelID:  info.ID,
		AccessHash: info.AccessHash,
	})
	if joinErr != nil {
		if tgerr.Is(joinErr, "USER_ALREADY_PARTICIPANT") {
			return JoinLeaveResult{
				Success:       true,
				Message:       "已经是频道成员",
				Channel:       link,
				AlreadyJoined: true,
				ChannelInfo:   info,
			}
		}
		return JoinLeaveResult{
			Success: false,
			Message: fmt.Sprintf("加入频道失败: %v", joinErr),
			Error:   "join_failed",
			Channel: link,
		}
	}
	if upd := extractUpdatesFromJoinResult(joinRes); upd != nil {
		c.Peers().ApplyUpdates(upd)
	}
	return JoinLeaveResult{
		Success:     true,
		Message:     "加入频道成功",
		Channel:     link,
		ChannelInfo: info,
	}
}

// LeaveChannel 离开指定频道或超级群（对齐 userbot_client.py:leave_channel）。
func (m *UserBotManager) LeaveChannel(ctx context.Context, link string) JoinLeaveResult {
	c := m.Client()
	if c == nil || !c.IsConnected() || c.API() == nil {
		return JoinLeaveResult{
			Success: false,
			Message: "UserBot 不可用",
			Error:   "client_not_available",
			Channel: link,
		}
	}

	inputCh, err := c.Peers().ResolveInputChannel(ctx, c.API(), link)
	if err != nil {
		switch {
		case tgerr.Is(err, "CHANNEL_PRIVATE"):
			return JoinLeaveResult{
				Success: false,
				Message: "无法离开频道（私有频道或无权限）",
				Error:   "channel_private",
				Channel: link,
			}
		case tgerr.Is(err, "CHANNEL_INVALID", "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID"):
			return JoinLeaveResult{
				Success: false,
				Message: "无法离开频道（频道不存在或无效）",
				Error:   "channel_invalid",
				Channel: link,
			}
		default:
			return JoinLeaveResult{
				Success: false,
				Message: fmt.Sprintf("无法离开频道: %v", err),
				Error:   "get_entity_failed",
				Channel: link,
			}
		}
	}

	upd, leaveErr := c.API().ChannelsLeaveChannel(ctx, inputCh)
	if leaveErr != nil {
		if tgerr.Is(leaveErr, "USER_NOT_PARTICIPANT") {
			return JoinLeaveResult{
				Success:   true,
				Message:   "未加入该频道",
				Channel:   link,
				NotJoined: true,
			}
		}
		return JoinLeaveResult{
			Success: false,
			Message: fmt.Sprintf("离开频道失败: %v", leaveErr),
			Error:   "leave_failed",
			Channel: link,
		}
	}
	c.Peers().ApplyUpdates(upd)
	return JoinLeaveResult{
		Success: true,
		Message: "离开频道成功",
		Channel: link,
	}
}

// ListJoinedChannels 分页遍历对话列表并返回已加入的全部频道/超级群列表。
func (m *UserBotManager) ListJoinedChannels(ctx context.Context) ([]ChannelInfo, error) {
	c := m.Client()
	if c == nil || !c.IsConnected() || c.API() == nil {
		return nil, errors.New("userbot client not available")
	}

	api := c.API()
	const pageLimit = 100
	var (
		offsetDate int
		offsetID   int
		offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
		seen                         = make(map[int64]struct{})
		channels   []ChannelInfo
	)

	for {
		res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: offsetDate,
			OffsetID:   offsetID,
			OffsetPeer: offsetPeer,
			Limit:      pageLimit,
		})
		if err != nil {
			return nil, err
		}

		var (
			dialogs []tg.DialogClass
			msgs    []tg.MessageClass
			chats   []tg.ChatClass
			users   []tg.UserClass
			full    bool
		)
		switch v := res.(type) {
		case *tg.MessagesDialogs:
			dialogs, msgs, chats, users = v.Dialogs, v.Messages, v.Chats, v.Users
			full = true
		case *tg.MessagesDialogsSlice:
			dialogs, msgs, chats, users = v.Dialogs, v.Messages, v.Chats, v.Users
		default:
			full = true
		}

		c.Peers().ApplyUsersChats(users, chats)

		for _, cc := range chats {
			ch, ok := cc.(*tg.Channel)
			if !ok || ch == nil || ch.Left {
				continue
			}
			if _, dup := seen[ch.ID]; dup {
				continue
			}
			seen[ch.ID] = struct{}{}
			marked := ChannelMarkedID(ch.ID)
			if cp, ok := c.Peers().GetByMarkedID(marked); ok {
				channels = append(channels, *cp.ToChannelInfo(0))
			}
		}

		if full || len(dialogs) < pageLimit {
			break
		}

		lastDlg, ok := dialogs[len(dialogs)-1].(*tg.Dialog)
		if !ok {
			break
		}
		nextPeer, pErr := c.Peers().ResolveInputPeer(ctx, nil, lastDlg.Peer)
		if pErr != nil {
			break
		}
		nextDate := 0
		for _, mc := range msgs {
			if msg, ok := mc.(*tg.Message); ok && msg.ID == lastDlg.TopMessage {
				nextDate = msg.Date
				break
			}
		}
		if nextID := lastDlg.TopMessage; nextID == offsetID && nextDate == offsetDate {
			break
		} else {
			offsetID = nextID
			offsetDate = nextDate
			offsetPeer = nextPeer
		}
	}

	return channels, nil
}

// JoinAllForwardingChannels 去重并依次加入转发配置中的所有源频道。
func (m *UserBotManager) JoinAllForwardingChannels(ctx context.Context, sourceChannels []string) BatchJoinResult {
	c := m.Client()
	if c == nil || !c.IsConnected() {
		return BatchJoinResult{
			Success: false,
			Message: "UserBot 不可用",
			Error:   "client_not_available",
		}
	}

	var unique []string
	seen := make(map[string]struct{}, len(sourceChannels))
	for _, ch := range sourceChannels {
		trimmed := strings.TrimSpace(ch)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			unique = append(unique, trimmed)
		}
	}
	if len(unique) == 0 {
		return BatchJoinResult{
			Success: false,
			Message: "转发规则中没有源频道",
			Error:   "no_source_channels",
		}
	}

	out := BatchJoinResult{
		Success: true,
		Total:   len(unique),
		Results: make([]JoinLeaveResult, 0, len(unique)),
	}
	for _, ch := range unique {
		res := m.JoinChannel(ctx, ch)
		out.Results = append(out.Results, res)
		if res.Success {
			out.SuccessCount++
		} else {
			out.FailedCount++
			reason := res.Message
			if reason == "" {
				reason = "未知错误"
			}
			out.FailedList = append(out.FailedList, BatchJoinFailure{
				Channel: ch,
				Reason:  reason,
			})
		}
	}
	return out
}

func extractUpdatesFromJoinResult(res tg.MessagesChatInviteJoinResultClass) tg.UpdatesClass {
	if okRes, ok := res.(*tg.MessagesChatInviteJoinResultOk); ok && okRes != nil {
		return okRes.Updates
	}
	return nil
}

func extractFirstChannelFromUpdates(u tg.UpdatesClass, peers *PeerCache) *ChannelInfo {
	if u == nil {
		return nil
	}
	var chats []tg.ChatClass
	switch v := u.(type) {
	case *tg.Updates:
		chats = v.Chats
	case *tg.UpdatesCombined:
		chats = v.Chats
	}
	for _, cc := range chats {
		if ch, ok := cc.(*tg.Channel); ok && ch != nil {
			if peers != nil {
				if cp, ok := peers.GetByMarkedID(ChannelMarkedID(ch.ID)); ok {
					return cp.ToChannelInfo(0)
				}
			}
			accessHash, _ := ch.GetAccessHash()
			return &ChannelInfo{
				ID:         ch.ID,
				MarkedID:   ChannelMarkedID(ch.ID),
				AccessHash: accessHash,
				Title:      ch.Title,
				Username:   normalizeUsername(ch.Username),
				Broadcast:  ch.Broadcast,
				Megagroup:  ch.Megagroup || ch.Gigagroup,
			}
		}
	}
	return nil
}
