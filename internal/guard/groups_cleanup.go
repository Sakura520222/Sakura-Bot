// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package guard

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
)

// AuditFunc 供退群清理与历史清理回调写入系统审计日志。
type AuditFunc func(ctx context.Context, action, actor, target string, success bool, message string)

// DirectoryEntry 单条会话目录元数据（对齐 groups.py:entries[chat_id]）。
type DirectoryEntry struct {
	ChatID           int64     `json:"chat_id"`
	Title            string    `json:"title"`
	Username         *string   `json:"username"`
	ChatType         string    `json:"chat_type"` // "unknown"|"group"|"supergroup"|"channel"|"channel_dm"
	IsStandalone     bool      `json:"is_standalone"`
	ParentChatID     *int64    `json:"parent_chat_id"`
	IdentityVerified bool      `json:"identity_verified"`
	Joined           *bool     `json:"joined"`
	IsAdmin          *bool     `json:"is_admin"`
	Role             string    `json:"role"` // "unknown"|"creator"|"admin"|"member"
	Error            *string   `json:"error"`
	CheckedAt        time.Time `json:"-"`
}

func (e DirectoryEntry) toMap() map[string]any {
	return map[string]any{
		"chat_id":           e.ChatID,
		"title":             e.Title,
		"username":          e.Username,
		"chat_type":         e.ChatType,
		"is_standalone":     e.IsStandalone,
		"parent_chat_id":    e.ParentChatID,
		"identity_verified": e.IdentityVerified,
		"joined":            e.Joined,
		"is_admin":          e.IsAdmin,
		"role":              e.Role,
		"error":             e.Error,
	}
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

// ────────────────────────────── GroupDirectory ──────────────────────────────

// GroupDirectory 发现主 Bot 所在群组/频道并向 Telegram 核实成员与管理员权限（对齐 groups.py:GroupDirectory）。
type GroupDirectory struct {
	svc    *Service
	logger *slog.Logger

	mu               sync.Mutex
	entries          map[int64]*DirectoryEntry
	cleanedHistory   map[int64]bool
	restoreHistory   map[int64]bool
	observedVersions map[int64]int64
	lastScan         time.Time
	checked          int
	total            int
	discovering      bool
	mutating         bool
	stopped          bool
	cancelScan       context.CancelFunc
}

// NewGroupDirectory 创建会话发现与权限核实目录。
func NewGroupDirectory(svc *Service, logger *slog.Logger) *GroupDirectory {
	if logger == nil {
		logger = slog.Default()
	}
	return &GroupDirectory{
		svc:              svc,
		logger:           logger,
		entries:          make(map[int64]*DirectoryEntry),
		cleanedHistory:   make(map[int64]bool),
		restoreHistory:   make(map[int64]bool),
		observedVersions: make(map[int64]int64),
	}
}

// Remember 在目录中登记负整数群组/频道候选。
func (d *GroupDirectory) Remember(chatID int64, title string) {
	if chatID >= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rememberLocked(chatID, title)
}

func (d *GroupDirectory) rememberLocked(chatID int64, title string) *DirectoryEntry {
	ent, ok := d.entries[chatID]
	if !ok {
		ent = &DirectoryEntry{
			ChatID:       chatID,
			ChatType:     "unknown",
			IsStandalone: true,
			Role:         "unknown",
		}
		d.entries[chatID] = ent
	}
	if t := strings.TrimSpace(title); t != "" {
		runes := []rune(t)
		if len(runes) > 256 {
			runes = runes[:256]
		}
		ent.Title = string(runes)
	}
	return ent
}

// Observe 在收到实时消息或退群事件时更新目录观测状态。
func (d *GroupDirectory) Observe(chatID int64, title string, chatType string, left bool) {
	if chatID >= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ent := d.rememberLocked(chatID, title)
	d.observedVersions[chatID]++
	if d.cleanedHistory[chatID] {
		delete(d.cleanedHistory, chatID)
		d.restoreHistory[chatID] = true
	}
	if left {
		ent.Joined = boolPtr(false)
		ent.IsAdmin = nil
		ent.Role = "unknown"
		ent.Error = nil
		ent.CheckedAt = time.Now()
		return
	}
	if chatType != "" && chatType != "unknown" {
		ent.ChatType = chatType
		ent.IsStandalone = chatType != "channel_dm"
		ent.IdentityVerified = true
		if ent.Joined == nil || !*ent.Joined {
			ent.IsAdmin = nil
			ent.Role = "unknown"
		}
		ent.Joined = boolPtr(true)
		ent.Error = nil
		ent.CheckedAt = time.Now()
	}
}

// Invalidate 在收到成员或机器人自身权限变动时清除旧判断以便后台重查。
func (d *GroupDirectory) Invalidate(chatID int64) {
	if chatID >= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ent := d.rememberLocked(chatID, "")
	d.observedVersions[chatID]++
	if d.cleanedHistory[chatID] {
		delete(d.cleanedHistory, chatID)
		d.restoreHistory[chatID] = true
	}
	ent.Joined = nil
	ent.IsAdmin = nil
	ent.Role = "unknown"
	ent.IdentityVerified = false
	ent.CheckedAt = time.Time{}
	ent.Error = nil
}

// CanLeave 判断目录条目是否满足「非管理员且已确认身份」的安全退出条件。
func CanLeaveEntry(e *DirectoryEntry) bool {
	if e == nil {
		return false
	}
	if !e.IsStandalone || !e.IdentityVerified || e.Error != nil {
		return false
	}
	if e.ChatType != "group" && e.ChatType != "supergroup" && e.ChatType != "channel" {
		return false
	}
	return e.Joined != nil && *e.Joined && e.IsAdmin != nil && !*e.IsAdmin
}

// CanCleanHistory 判断目录条目是否满足「明确 ChannelPrivateError 且未配置」的不可访问历史隐藏条件。
func (d *GroupDirectory) CanCleanHistory(e *DirectoryEntry, configuredSet map[int64]bool) bool {
	if e == nil || d.cleanedHistory[e.ChatID] || configuredSet[e.ChatID] {
		return false
	}
	if !e.IsStandalone || e.Joined != nil || (e.IsAdmin != nil && *e.IsAdmin) {
		return false
	}
	return e.Error != nil && *e.Error == "ChannelPrivateError"
}

// Refresh 触发异步目录核实扫描（距离上次超过 300s、有未核实会话或 force=true 时启动）。
func (d *GroupDirectory) Refresh(force bool) {
	d.mu.Lock()
	if d.stopped || d.mutating || d.discovering {
		d.mu.Unlock()
		return
	}
	cfgGroups := d.svc.Config().Groups
	for k := range cfgGroups {
		if cid, err := strconv.ParseInt(k, 10, 64); err == nil && cid < 0 {
			d.rememberLocked(cid, "")
		}
	}
	hasUnchecked := false
	for _, ent := range d.entries {
		if ent.CheckedAt.IsZero() {
			hasUnchecked = true
			break
		}
	}
	needScan := force || d.lastScan.IsZero() || time.Since(d.lastScan) > 300*time.Second || hasUnchecked
	if !needScan {
		d.mu.Unlock()
		return
	}
	d.discovering = true
	scanCtx, cancel := context.WithCancel(context.Background())
	d.cancelScan = cancel
	d.mu.Unlock()

	go d.runScan(scanCtx)
}

// ScanSync 同步完成一次目录扫描（供测试确定性调用）。
func (d *GroupDirectory) ScanSync(ctx context.Context) {
	d.mu.Lock()
	if d.stopped || d.mutating || d.discovering {
		d.mu.Unlock()
		return
	}
	cfgGroups := d.svc.Config().Groups
	for k := range cfgGroups {
		if cid, err := strconv.ParseInt(k, 10, 64); err == nil && cid < 0 {
			d.rememberLocked(cid, "")
		}
	}
	d.discovering = true
	d.mu.Unlock()
	d.runScan(ctx)
}

func (d *GroupDirectory) runScan(ctx context.Context) {
	defer func() {
		d.syncHistoryDB(ctx, false)
		d.mu.Lock()
		d.discovering = false
		d.lastScan = time.Now()
		d.mu.Unlock()
	}()

	d.syncHistoryDB(ctx, true)

	if d.svc.ops != nil {
		for _, peer := range d.svc.ops.KnownGroupPeers() {
			if peer.MarkedID < 0 {
				d.Remember(peer.MarkedID, peer.Title)
			}
		}
	}

	d.mu.Lock()
	candidates := make([]int64, 0, len(d.entries))
	for cid := range d.entries {
		candidates = append(candidates, cid)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
	d.total = len(candidates)
	d.checked = 0
	d.mu.Unlock()

	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for _, cid := range candidates {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(chatID int64) {
			defer wg.Done()
			defer func() { <-sem }()
			d.CheckSingle(ctx, chatID, true)
		}(cid)
	}
	wg.Wait()
}

// CheckSingle 向 Telegram 实时核实单个会话身份与机器人权限。
func (d *GroupDirectory) CheckSingle(ctx context.Context, chatID int64, countProgress bool) {
	d.mu.Lock()
	ent := d.rememberLocked(chatID, "")
	ver := d.observedVersions[chatID]
	d.mu.Unlock()

	defer func() {
		if countProgress {
			d.mu.Lock()
			d.checked++
			d.mu.Unlock()
		}
	}()

	if d.svc.ops == nil {
		return
	}

	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var resolvedKind string
	var resolvedTitle string
	var resolvedUser *string
	var monoforumParent *int64

	if isBasicGroupID(chatID) {
		resolvedKind = "group"
	} else {
		info, err := d.svc.ops.ResolveChannel(checkCtx, strconv.FormatInt(chatID, 10))
		if err != nil || info == nil {
			errCode := classifyDirectoryError(err)
			d.mu.Lock()
			if d.observedVersions[chatID] == ver {
				ent.Joined = nil
				ent.IsAdmin = nil
				ent.Role = "unknown"
				ent.IdentityVerified = false
				ent.CheckedAt = time.Now()
				ent.Error = strPtr(errCode)
			}
			d.mu.Unlock()
			return
		}
		if info.MarkedID != 0 && info.MarkedID != chatID {
			d.mu.Lock()
			if d.observedVersions[chatID] == ver {
				ent.Joined = nil
				ent.IsAdmin = nil
				ent.Role = "unknown"
				ent.IdentityVerified = false
				ent.CheckedAt = time.Now()
				ent.Error = strPtr("ChatIdentityMismatch")
			}
			d.mu.Unlock()
			return
		}
		resolvedTitle = info.Title
		if info.Username != "" {
			u := info.Username
			resolvedUser = &u
		}
		if info.Monoforum {
			resolvedKind = "channel_dm"
			if info.LinkedChatID < 0 {
				p := info.LinkedChatID
				monoforumParent = &p
			}
		} else if info.Megagroup {
			resolvedKind = "supergroup"
		} else {
			resolvedKind = "channel"
		}
	}

	if resolvedKind == "channel_dm" {
		d.mu.Lock()
		if d.observedVersions[chatID] == ver {
			if resolvedTitle != "" {
				ent.Title = resolvedTitle
			}
			ent.Username = resolvedUser
			ent.ChatType = "channel_dm"
			ent.IsStandalone = false
			ent.ParentChatID = monoforumParent
			ent.IdentityVerified = true
			ent.Joined = nil
			ent.IsAdmin = nil
			ent.Role = "unknown"
			ent.Error = nil
			ent.CheckedAt = time.Now()
			if monoforumParent != nil {
				d.rememberLocked(*monoforumParent, "")
			}
		}
		d.mu.Unlock()
		return
	}

	botID := d.svc.BotID()
	perm, perr := d.svc.ops.Permissions(checkCtx, chatID, botID)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.observedVersions[chatID] != ver {
		return
	}
	if resolvedTitle != "" {
		ent.Title = resolvedTitle
	}
	if resolvedUser != nil {
		ent.Username = resolvedUser
	}
	ent.ChatType = resolvedKind
	ent.IsStandalone = true
	ent.ParentChatID = nil
	ent.IdentityVerified = true
	ent.CheckedAt = time.Now()

	if perr != nil {
		code := classifyDirectoryError(perr)
		if code == "UserNotParticipantError" {
			ent.Joined = boolPtr(false)
			ent.IsAdmin = nil
			ent.Role = "unknown"
			ent.Error = nil
			return
		}
		ent.Joined = nil
		ent.IsAdmin = nil
		ent.Role = "unknown"
		ent.Error = strPtr(code)
		return
	}

	if perm == nil || !perm.IsMember || perm.BannedRights.ViewMessages {
		ent.Joined = boolPtr(false)
		ent.IsAdmin = nil
		ent.Role = "unknown"
		ent.Error = nil
		return
	}

	isAdmin := perm.IsCreator || perm.IsAdmin
	role := "member"
	if perm.IsCreator {
		role = "creator"
	} else if isAdmin {
		role = "admin"
	}
	ent.Joined = boolPtr(true)
	ent.IsAdmin = boolPtr(isAdmin)
	ent.Role = role
	ent.Error = nil
	if d.cleanedHistory[chatID] {
		delete(d.cleanedHistory, chatID)
		d.restoreHistory[chatID] = true
	}
}

func classifyDirectoryError(err error) string {
	if err == nil {
		return "UnknownError"
	}
	s := err.Error()
	for _, known := range []string{
		"ChannelPrivateError",
		"UserNotParticipantError",
		"FloodWaitError",
		"ChatAdminRequiredError",
		"ChannelInvalidError",
	} {
		if strings.Contains(s, known) {
			return known
		}
	}
	if strings.Contains(s, "CHANNEL_PRIVATE") {
		return "ChannelPrivateError"
	}
	if strings.Contains(s, "USER_NOT_PARTICIPANT") {
		return "UserNotParticipantError"
	}
	if strings.Contains(s, "FLOOD_WAIT") {
		return "FloodWaitError"
	}
	return "TelegramError"
}

func (d *GroupDirectory) syncHistoryDB(ctx context.Context, load bool) {
	if d.svc == nil || d.svc.repo == nil || d.svc.repo.DB == nil {
		return
	}
	botID := d.svc.BotID()
	if botID == 0 {
		return
	}
	d.mu.Lock()
	var toRestore []int64
	for cid := range d.restoreHistory {
		toRestore = append(toRestore, cid)
	}
	d.mu.Unlock()

	if len(toRestore) > 0 {
		if err := d.svc.repo.RestoreChatHistory(ctx, botID, toRestore); err == nil {
			d.mu.Lock()
			for _, cid := range toRestore {
				delete(d.restoreHistory, cid)
			}
			d.mu.Unlock()
		}
	}
	if load {
		if cleaned, err := d.svc.repo.CleanedChatHistory(ctx, botID); err == nil {
			d.mu.Lock()
			for k := range d.cleanedHistory {
				delete(d.cleanedHistory, k)
			}
			for cid := range cleaned {
				if !d.restoreHistory[cid] {
					d.cleanedHistory[cid] = true
				}
			}
			d.mu.Unlock()
		}
	}
}

// Snapshot 返回与 Python GroupDirectory.snapshot 完全一致的 JSON 映射结构。
func (d *GroupDirectory) Snapshot() map[string]any {
	cfgSet := make(map[int64]bool)
	for k := range d.svc.Config().Groups {
		if cid, err := strconv.ParseInt(k, 10, 64); err == nil {
			cfgSet[cid] = true
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	metadata := make(map[string]map[string]any, len(d.entries))
	var joined []map[string]any
	relatedCount := 0
	cleanableCount := 0

	for cid, ent := range d.entries {
		isJoinedTrue := ent.Joined != nil && *ent.Joined
		if d.cleanedHistory[cid] && !isJoinedTrue && ent.IsStandalone && !cfgSet[cid] {
			continue
		}
		m := ent.toMap()
		metadata[strconv.FormatInt(cid, 10)] = m
		if !ent.IsStandalone {
			relatedCount++
		}
		if d.CanCleanHistory(ent, cfgSet) {
			cleanableCount++
		}
		if isJoinedTrue && ent.IsStandalone && ent.IdentityVerified {
			joined = append(joined, m)
		}
	}

	sort.Slice(joined, func(i, j int) bool {
		ti := strings.ToLower(rowStr(joined[i], "title"))
		tj := strings.ToLower(rowStr(joined[j], "title"))
		if ti != tj {
			return ti < tj
		}
		return rowInt64(joined[i], "chat_id") < rowInt64(joined[j], "chat_id")
	})

	groupsOnly := make([]map[string]any, 0, len(joined))
	for _, row := range joined {
		ct := rowStr(row, "chat_type")
		if ct == "group" || ct == "supergroup" {
			groupsOnly = append(groupsOnly, row)
		}
	}
	if joined == nil {
		joined = []map[string]any{}
	}

	return map[string]any{
		"groups":                     groupsOnly,
		"chats":                      joined,
		"related_chat_count":         relatedCount,
		"history_cleanable_count":    cleanableCount,
		"metadata":                   metadata,
		"discovering":                d.discovering,
		"checked":                    d.checked,
		"total":                      d.total,
		"complete_history_available": false,
	}
}

// Stop 停止正在执行的后台发现扫描。
func (d *GroupDirectory) Stop() {
	d.mu.Lock()
	d.stopped = true
	cancel := d.cancelScan
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ────────────────────────────── ChatCleanup & HistoryCleanup ──────────────────────────────

type cleanupPreview struct {
	Actor   string
	BotID   int64
	Expires time.Time
	Chats   []map[string]any
}

// ChatCleanup 管理批量退出非管理员群组与清理不可访问历史记录（mode="leave" | "history"）。
type ChatCleanup struct {
	dir  *GroupDirectory
	mode string // "leave" | "history"

	mu       sync.Mutex
	previews map[string]cleanupPreview
	lastTok  string
	job      map[string]any
	stopped  bool
}

// NewChatCleanup 创建非管理员退群或历史清理控制器。
func NewChatCleanup(dir *GroupDirectory, mode string) *ChatCleanup {
	return &ChatCleanup{
		dir:      dir,
		mode:     mode,
		previews: make(map[string]cleanupPreview),
	}
}

// Status 返回最近一次任务的深拷贝视图（无任务返回 nil）。
func (c *ChatCleanup) Status() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job == nil {
		return nil
	}
	return deepCloneAnyMap(c.job)
}

// Preview 生成 300 秒有效期的候选名单预览与确认 token。
func (c *ChatCleanup) Preview(actor string) (map[string]any, error) {
	c.dir.mu.Lock()
	stopped := c.dir.stopped
	mutating := c.dir.mutating
	discovering := c.dir.discovering
	lastScan := c.dir.lastScan
	checked := c.dir.checked
	total := c.dir.total
	c.dir.mu.Unlock()

	if stopped || mutating {
		return nil, ChatCleanupError("正在处理会话，请等待执行结束")
	}
	if discovering || lastScan.IsZero() {
		return nil, ChatCleanupError("正在核实会话，请等待列表刷新完成后预览")
	}
	if checked < total {
		return nil, ChatCleanupError("会话核实尚未完成，请稍后刷新列表再预览")
	}
	if time.Since(lastScan) > 300*time.Second {
		c.dir.Refresh(true)
		return nil, ChatCleanupError("会话列表已过期，请等待刷新完成后重新预览")
	}

	snap := c.dir.Snapshot()
	candidates, err := c.selectCandidates()
	if err != nil {
		return nil, err
	}

	var tokBytes [32]byte
	_, _ = rand.Read(tokBytes[:])
	token := base64.RawURLEncoding.EncodeToString(tokBytes[:])

	now := time.Now()
	c.mu.Lock()
	for k, p := range c.previews {
		if !now.Before(p.Expires) || p.Actor == actor {
			delete(c.previews, k)
		}
	}
	c.previews[token] = cleanupPreview{
		Actor:   actor,
		BotID:   c.dir.svc.BotID(),
		Expires: now.Add(300 * time.Second),
		Chats:   candidates,
	}
	c.mu.Unlock()

	chatsAll, _ := snap["chats"].([]map[string]any)
	adminCount := 0
	for _, row := range chatsAll {
		if adm, ok := row["is_admin"].(*bool); ok && adm != nil && *adm {
			adminCount++
		} else if admB, ok := row["is_admin"].(bool); ok && admB {
			adminCount++
		}
	}
	metaMap, _ := snap["metadata"].(map[string]map[string]any)
	unknownCount := 0
	for _, row := range metaMap {
		if !rowBool(row, "is_standalone") {
			continue
		}
		if row["joined"] == nil || (rowBool(row, "joined") && row["is_admin"] == nil) {
			unknownCount++
		}
	}

	return map[string]any{
		"token":              token,
		"expires_in_seconds": 300,
		"chats":              candidates,
		"total":              len(candidates),
		"admin_count":        adminCount,
		"unknown_count":      unknownCount,
	}, nil
}

func (c *ChatCleanup) selectCandidates() ([]map[string]any, error) {
	if c.mode == "history" && (c.dir.svc.repo == nil || c.dir.svc.repo.DB == nil || c.dir.svc.BotID() == 0) {
		return nil, ChatCleanupError("历史记录存储尚未就绪，请稍后重试")
	}
	cfgSet := make(map[int64]bool)
	for k := range c.dir.svc.Config().Groups {
		if cid, err := strconv.ParseInt(k, 10, 64); err == nil {
			cfgSet[cid] = true
		}
	}

	c.dir.mu.Lock()
	defer c.dir.mu.Unlock()
	out := make([]map[string]any, 0)
	for _, ent := range c.dir.entries {
		if c.mode == "leave" {
			if CanLeaveEntry(ent) {
				out = append(out, ent.toMap())
			}
		} else {
			if c.dir.CanCleanHistory(ent, cfgSet) {
				out = append(out, ent.toMap())
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ti := strings.ToLower(rowStr(out[i], "title"))
		tj := strings.ToLower(rowStr(out[j], "title"))
		if ti != tj {
			return ti < tj
		}
		return rowInt64(out[i], "chat_id") < rowInt64(out[j], "chat_id")
	})
	return out, nil
}

// Start 确认并异步启动退群或历史清理任务。
func (c *ChatCleanup) Start(token, actor string, audit AuditFunc) (map[string]any, error) {
	c.mu.Lock()
	if token == c.lastTok && c.job != nil && rowStr(c.job, "actor") == actor {
		res := deepCloneAnyMap(c.job)
		c.mu.Unlock()
		return res, nil
	}
	prev, ok := c.previews[token]
	if c.stopped || !ok || prev.Actor != actor || prev.BotID != c.dir.svc.BotID() || !time.Now().Before(prev.Expires) {
		c.mu.Unlock()
		return nil, ChatCleanupError("预览已失效，请重新预览并确认")
	}

	c.dir.mu.Lock()
	if c.dir.mutating {
		c.dir.mu.Unlock()
		c.mu.Unlock()
		return nil, ChatCleanupError("已有会话处理任务正在执行，请等待完成")
	}
	if c.dir.discovering {
		c.dir.mu.Unlock()
		c.mu.Unlock()
		return nil, ChatCleanupError("正在刷新会话，请等待刷新完成后确认")
	}
	c.dir.mutating = true
	c.dir.mu.Unlock()

	delete(c.previews, token)
	c.lastTok = token
	c.job = map[string]any{
		"actor":   actor,
		"status":  "running",
		"total":   len(prev.Chats),
		"checked": 0,
		"results": []map[string]any{},
		"error":   nil,
	}
	res := deepCloneAnyMap(c.job)
	c.mu.Unlock()

	go c.runJob(context.Background(), prev, audit)
	return res, nil
}

func (c *ChatCleanup) runJob(ctx context.Context, prev cleanupPreview, audit AuditFunc) {
	var jobErr *string
	defer func() {
		c.dir.mu.Lock()
		c.dir.mutating = false
		c.dir.mu.Unlock()

		c.mu.Lock()
		if jobErr != nil {
			c.job["status"] = "interrupted"
			c.job["error"] = *jobErr
		} else {
			c.job["status"] = "completed"
		}
		c.mu.Unlock()
	}()

	auditAction := "guard.chat.leave"
	successStatus := "left"
	if c.mode == "history" {
		auditAction = "guard.chat.history.clear"
		successStatus = "cleaned"
	}

	for _, row := range prev.Chats {
		chatID := rowInt64(row, "chat_id")
		if c.dir.svc.BotID() != prev.BotID {
			msg := "操作者权限或机器人身份已变化，剩余会话未处理"
			jobErr = &msg
			break
		}
		st, msg, fatal := c.processOneChat(ctx, chatID, prev.Actor)
		resRow := deepCloneAnyMap(row)
		resRow["status"] = st
		resRow["message"] = msg

		c.mu.Lock()
		curResults, _ := c.job["results"].([]map[string]any)
		c.job["results"] = append(curResults, resRow)
		c.job["checked"] = rowInt64(c.job, "checked") + 1
		c.mu.Unlock()

		if audit != nil {
			audit(ctx, auditAction, prev.Actor, strconv.FormatInt(chatID, 10), st == successStatus, msg)
		}
		if fatal != "" {
			jobErr = &fatal
			break
		}
	}
}

func (c *ChatCleanup) processOneChat(ctx context.Context, chatID int64, actor string) (status, message, fatalErr string) {
	if c.mode == "history" {
		c.dir.CheckSingle(ctx, chatID, false)
		cfgSet := make(map[int64]bool)
		for k := range c.dir.svc.Config().Groups {
			if cid, err := strconv.ParseInt(k, 10, 64); err == nil {
				cfgSet[cid] = true
			}
		}
		c.dir.mu.Lock()
		ent := c.dir.entries[chatID]
		eligible := c.dir.CanCleanHistory(ent, cfgSet)
		c.dir.mu.Unlock()
		if !eligible {
			return "skipped", "已加入、已恢复访问或无法确认不可访问，已保留", ""
		}
		if err := c.dir.svc.repo.MarkChatHistoryCleaned(ctx, c.dir.svc.BotID(), chatID, actor); err != nil {
			return "failed", "历史清理记录保存失败", err.Error()
		}
		c.dir.mu.Lock()
		c.dir.cleanedHistory[chatID] = true
		c.dir.mu.Unlock()
		return "cleaned", "已清理历史记录；重新收到活动或确认加入后会自动显示", ""
	}

	c.dir.CheckSingle(ctx, chatID, false)
	c.dir.mu.Lock()
	ent := c.dir.entries[chatID]
	var snap DirectoryEntry
	if ent != nil {
		snap = *ent
	}
	c.dir.mu.Unlock()

	if !snap.IsStandalone {
		return "skipped", "频道私信随所属频道管理，已保留", ""
	}
	if snap.Joined != nil && !*snap.Joined {
		return "skipped", "已不在该会话中", ""
	}
	if snap.IsAdmin != nil && *snap.IsAdmin {
		return "skipped", "机器人当前为管理员，已保留", ""
	}
	if !CanLeaveEntry(&snap) {
		return "skipped", "无法确认成员身份或权限，已保留", ""
	}
	if err := c.dir.svc.ops.LeaveChat(ctx, chatID); err != nil {
		if strings.Contains(err.Error(), "USER_NOT_PARTICIPANT") || strings.Contains(err.Error(), "UserNotParticipant") {
			c.dir.Observe(chatID, "", "", true)
			return "skipped", "已不在该会话中", ""
		}
		return "failed", fmt.Sprintf("退出会话失败：%v", err), err.Error()
	}
	c.dir.Observe(chatID, "", "", true)
	return "left", "已退出", ""
}

// Stop 标记清理器停止。
func (c *ChatCleanup) Stop() {
	c.mu.Lock()
	c.stopped = true
	c.previews = map[string]cleanupPreview{}
	c.mu.Unlock()
}

// ────────────────────────────── DeletedUserCleanup ──────────────────────────────

// DeletedUserCleanup 分页扫描并移除管理群组中的已销号成员及残留消息（对齐 deleted_cleanup.py）。
type DeletedUserCleanup struct {
	svc    *Service
	wakeCh chan struct{}
	logger *slog.Logger

	mu            sync.Mutex
	running       bool
	ownsDirectory bool
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

// NewDeletedUserCleanup 创建已销号成员清理引擎。
func NewDeletedUserCleanup(svc *Service, logger *slog.Logger) *DeletedUserCleanup {
	if logger == nil {
		logger = slog.Default()
	}
	return &DeletedUserCleanup{
		svc:    svc,
		wakeCh: make(chan struct{}, 1),
		logger: logger,
	}
}

// StartWorker 启动后台状态机协程。
func (dc *DeletedUserCleanup) StartWorker(ctx context.Context) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.running {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	dc.cancel = cancel
	dc.running = true
	dc.wg.Add(1)
	go func() {
		defer dc.wg.Done()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-dc.wakeCh:
				dc.Step(runCtx)
			case <-ticker.C:
				dc.Step(runCtx)
			}
		}
	}()
}

// Stop 停止后台状态机协程。
func (dc *DeletedUserCleanup) Stop() {
	dc.mu.Lock()
	if !dc.running {
		dc.mu.Unlock()
		return
	}
	dc.running = false
	cancel := dc.cancel
	dc.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	dc.wg.Wait()
	dc.releaseDirectory()
}

// Wake 唤醒后台执行。
func (dc *DeletedUserCleanup) Wake() {
	select {
	case dc.wakeCh <- struct{}{}:
	default:
	}
}

func (dc *DeletedUserCleanup) available() bool {
	return dc != nil && dc.svc != nil && dc.svc.repo != nil && dc.svc.repo.DB != nil && dc.svc.BotID() != 0
}

func (dc *DeletedUserCleanup) authorized(actor string) bool {
	if actor == "admin" {
		return true
	}
	uid, err := strconv.ParseInt(actor, 10, 64)
	return err == nil && uid > 0 && slices.Contains(dc.svc.DefaultAdmins(), uid)
}

func (dc *DeletedUserCleanup) releaseDirectory() {
	dc.mu.Lock()
	owned := dc.ownsDirectory
	dc.ownsDirectory = false
	dc.mu.Unlock()
	if owned && dc.svc != nil && dc.svc.directory != nil {
		dc.svc.directory.mu.Lock()
		dc.svc.directory.mutating = false
		dc.svc.directory.mu.Unlock()
	}
}

func (dc *DeletedUserCleanup) eligibleGroups(requestedChatIDs []int64) ([]map[string]any, error) {
	dir := dc.svc.directory
	dir.mu.Lock()
	mutating := dir.mutating
	discovering := dir.discovering
	lastScan := dir.lastScan
	dir.mu.Unlock()

	if mutating || discovering || lastScan.IsZero() || time.Since(lastScan) > 300*time.Second {
		dir.Refresh(true)
		return nil, ChatCleanupError("请等待群组列表核实完成后再清理")
	}

	snap := dir.Snapshot()
	chats, _ := snap["chats"].([]map[string]any)
	eligible := make(map[int64]map[string]any)
	for _, row := range chats {
		ct := rowStr(row, "chat_type")
		isAdmin := false
		switch v := row["is_admin"].(type) {
		case *bool:
			isAdmin = v != nil && *v
		case bool:
			isAdmin = v
		}
		if isAdmin && (ct == "group" || ct == "supergroup") && row["error"] == nil {
			eligible[rowInt64(row, "chat_id")] = row
		}
	}

	var chosen []int64
	if len(requestedChatIDs) > 0 {
		for _, cid := range requestedChatIDs {
			if _, ok := eligible[cid]; !ok {
				return nil, ChatCleanupError("只能清理已确认机器人为管理员的群组，请刷新列表")
			}
			if !slices.Contains(chosen, cid) {
				chosen = append(chosen, cid)
			}
		}
	} else {
		for cid := range eligible {
			chosen = append(chosen, cid)
		}
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i] < chosen[j] })

	out := make([]map[string]any, 0, len(chosen))
	for _, cid := range chosen {
		out = append(out, map[string]any{
			"chat_id": cid,
			"title":   rowStr(eligible[cid], "title"),
			"status":  "scanning",
			"offset":  0,
			"scanned": 0,
			"error":   nil,
		})
	}
	return out, nil
}

// Status 返回与 GET /api/guard/chats/deleted-users 对齐的状态对象。
func (dc *DeletedUserCleanup) Status(ctx context.Context) (map[string]any, error) {
	settings := dc.svc.Config().DeletedUserCleanup
	avail := dc.available()
	res := map[string]any{
		"available":   avail,
		"settings":    StructToMap(settings),
		"job":         nil,
		"history":     []map[string]any{},
		"next_run_at": nil,
	}
	if !avail {
		return res, nil
	}
	botID := dc.svc.BotID()
	err := dc.svc.repo.Reader(ctx, func(st *guarddb.Store) error {
		hist, herr := st.DeletedCleanupHistory(ctx, botID, 10)
		if herr != nil {
			return herr
		}
		if len(hist) > 0 {
			view, verr := st.DeletedCleanupJobView(ctx, hist[0])
			if verr == nil {
				res["job"] = view
			}
			hList := make([]map[string]any, 0, len(hist))
			for _, r := range hist {
				ts := parseRowTimeOrNow(r, "created_at", time.Now().UTC()).Format("2006-01-02T15:04:05.000000") + "Z"
				hList = append(hList, map[string]any{
					"id":           rowStr(r, "id"),
					"status":       rowStr(r, "status"),
					"trigger_type": rowStr(r, "trigger_type"),
					"created_at":   ts,
				})
			}
			res["history"] = hList
		}
		if settings.Enabled {
			last, _ := st.LatestScheduledDeletedCleanup(ctx, botID)
			due := time.Now().UTC()
			if last != nil {
				due = parseRowTimeOrNow(last, "created_at", due).Add(time.Duration(settings.IntervalHours) * time.Hour)
			}
			res["next_run_at"] = due.Format("2006-01-02T15:04:05.000000") + "Z"
		}
		return nil
	})
	return res, err
}

// Job 按 ID 查询单条清理任务详情。
func (dc *DeletedUserCleanup) Job(ctx context.Context, jobID string) (map[string]any, error) {
	if !dc.available() {
		return nil, ChatCleanupError("已销号清理服务尚未就绪")
	}
	return dc.svc.repo.DeletedCleanupJob(ctx, dc.svc.BotID(), jobID)
}

// Start 手动创建并启动已销号成员清理任务（幂等 request_id）。
func (dc *DeletedUserCleanup) Start(ctx context.Context, actor, requestID string, chatIDs []int64) (map[string]any, error) {
	if !dc.available() {
		return nil, ChatCleanupError("已销号清理服务尚未就绪")
	}
	if !dc.authorized(actor) {
		return nil, ChatCleanupError("只有系统管理员可以清理已销号成员")
	}
	botID := dc.svc.BotID()
	var view map[string]any
	locked, err := dc.svc.repo.WithBotLock(ctx, botID, func(st *guarddb.Store) error {
		prev, _ := st.RequestedDeletedCleanupJob(ctx, botID, requestID)
		if prev != nil {
			if rowStr(prev, "actor") != actor {
				return ChatCleanupError("请求标识已用于其他清理操作")
			}
			var verr error
			view, verr = st.DeletedCleanupJobView(ctx, prev)
			return verr
		}
		if active, _ := st.ActiveDeletedCleanupJob(ctx, botID); active != nil {
			return ChatCleanupError("已有已销号清理任务，请等待完成")
		}
		groups, gerr := dc.eligibleGroups(chatIDs)
		if gerr != nil {
			return gerr
		}
		if len(groups) == 0 {
			return ChatCleanupError("当前没有可清理的管理群组")
		}
		job, cerr := st.CreateDeletedCleanupJob(ctx, botID, actor, requestID, groups, chatIDs, "manual")
		if cerr != nil {
			return cerr
		}
		dc.svc.directory.mu.Lock()
		dc.svc.directory.mutating = true
		dc.svc.directory.mu.Unlock()
		dc.mu.Lock()
		dc.ownsDirectory = true
		dc.mu.Unlock()

		var verr error
		view, verr = st.DeletedCleanupJobView(ctx, job)
		return verr
	})
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, ChatCleanupError("正在处理清理任务，请稍后重试")
	}
	dc.Wake()
	return view, nil
}

// Step 单步推进当前活跃或到期的定期已销号成员清理任务。
func (dc *DeletedUserCleanup) Step(ctx context.Context) {
	if !dc.available() || dc.svc.ops == nil {
		return
	}
	botID := dc.svc.BotID()
	_, _ = dc.svc.repo.WithBotLock(ctx, botID, func(st *guarddb.Store) error {
		job, _ := st.ActiveDeletedCleanupJob(ctx, botID)
		if job == nil {
			dc.releaseDirectory()
			settings := dc.svc.Config().DeletedUserCleanup
			if !settings.Enabled {
				return nil
			}
			last, _ := st.LatestScheduledDeletedCleanup(ctx, botID)
			if last != nil {
				nextAt := parseRowTimeOrNow(last, "created_at", time.Now().UTC()).Add(time.Duration(settings.IntervalHours) * time.Hour)
				if time.Now().UTC().Before(nextAt) {
					return nil
				}
			}
			groups, err := dc.eligibleGroups(nil)
			if err != nil || len(groups) == 0 {
				return nil
			}
			job, _ = st.CreateDeletedCleanupJob(ctx, botID, "schedule", newUUIDHex(), groups, nil, "scheduled")
			if job == nil {
				return nil
			}
		}

		payload := rowPayload(job)
		rawGroups, _ := payload["groups"].([]any)
		idx := rowInt64(payload, "group_index")
		jobID := rowStr(job, "id")

		if idx < 0 || idx >= int64(len(rawGroups)) {
			job["status"] = "completed"
			_ = st.SaveDeletedCleanupJob(ctx, job)
			dc.releaseDirectory()
			return nil
		}

		grpMap, ok := rawGroups[idx].(map[string]any)
		if !ok {
			payload["group_index"] = idx + 1
			job["payload"] = payload
			_ = st.SaveDeletedCleanupJob(ctx, job)
			return nil
		}
		chatID := rowInt64(grpMap, "chat_id")
		gStatus := rowStr(grpMap, "status")

		switch gStatus {
		case "scanning":
			offset := rowInt(grpMap, "offset")
			page, err := dc.svc.ops.ScanDeletedParticipants(ctx, chatID, offset, 200)
			if err != nil || page == nil {
				grpMap["status"] = "failed"
				if err != nil {
					grpMap["error"] = err.Error()
				}
				payload["group_index"] = idx + 1
				job["payload"] = payload
				_ = st.SaveDeletedCleanupJob(ctx, job)
				return nil
			}
			cands := make([]guarddb.DeletedUserCandidate, 0, len(page.DeletedUsers))
			for _, u := range page.DeletedUsers {
				ah := u.AccessHash
				cands = append(cands, guarddb.DeletedUserCandidate{UserID: u.ID, AccessHash: &ah})
			}
			_ = st.AddDeletedCleanupCandidates(ctx, jobID, chatID, cands)
			grpMap["offset"] = page.NextOffset
			grpMap["scanned"] = rowInt64(grpMap, "scanned") + int64(page.Scanned)
			if page.Done {
				grpMap["status"] = "removing"
			}
			job["payload"] = payload
			_ = st.SaveDeletedCleanupJob(ctx, job)

		case "removing":
			item, _ := st.NextPendingDeletedCleanupItem(ctx, jobID, chatID)
			if item == nil {
				grpMap["status"] = "completed"
				payload["group_index"] = idx + 1
				job["payload"] = payload
				_ = st.SaveDeletedCleanupJob(ctx, job)
				return nil
			}
			uid := rowInt64(item, "user_id")
			var kickErr error
			if isBasicGroupID(chatID) {
				kickErr = dc.svc.ops.KickBasicGroup(ctx, chatID, uid)
			} else {
				kickErr = dc.svc.ops.Ban(ctx, chatID, uid)
				if kickErr == nil {
					_ = dc.svc.ops.Unban(ctx, chatID, uid)
				}
			}
			if kickErr != nil {
				_ = st.UpdateDeletedCleanupItem(ctx, jobID, chatID, uid, "failed", kickErr.Error())
			} else {
				if !isBasicGroupID(chatID) && dc.svc.appCfg != nil && dc.svc.appCfg.TelegramBotToken() != "" {
					_ = dc.svc.ops.RevokeMessagesViaBotAPI(ctx, dc.svc.appCfg.TelegramBotToken(), chatID, uid)
				}
				_ = st.UpdateDeletedCleanupItem(ctx, jobID, chatID, uid, "removed", "已移除已销号成员")
			}
		default:
			payload["group_index"] = idx + 1
			job["payload"] = payload
			_ = st.SaveDeletedCleanupJob(ctx, job)
		}
		return nil
	})
}
