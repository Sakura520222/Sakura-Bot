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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"
)

// PeerKind 标识 Telegram 实体类别。
type PeerKind string

const (
	PeerKindUser    PeerKind = "user"
	PeerKindChat    PeerKind = "chat"    // 普通群 (-<chatID>)
	PeerKindChannel PeerKind = "channel" // 频道或超级群 (-100<channelID>)
)

// CachedPeer 表示 PeerCache 持久化的单条实体路由及元数据记录。
type CachedPeer struct {
	MarkedID        int64    `json:"marked_id"`
	RawID           int64    `json:"raw_id"`
	Kind            PeerKind `json:"kind"`
	AccessHash      int64    `json:"access_hash,omitempty"`
	HasAccessHash   bool     `json:"has_access_hash,omitempty"`
	Username        string   `json:"username,omitempty"`
	ActiveUsernames []string `json:"active_usernames,omitempty"`
	Title           string   `json:"title,omitempty"`
	FirstName       string   `json:"first_name,omitempty"`
	LastName        string   `json:"last_name,omitempty"`
	Phone           string   `json:"phone,omitempty"`
	IsBot           bool     `json:"is_bot,omitempty"`
	IsDeleted       bool     `json:"is_deleted,omitempty"`
	HasPhoto        bool     `json:"has_photo,omitempty"`
	Broadcast       bool     `json:"broadcast,omitempty"`
	Megagroup       bool     `json:"megagroup,omitempty"`
	Forum           bool     `json:"forum,omitempty"`
	Monoforum       bool     `json:"monoforum,omitempty"`
	Restricted      bool     `json:"restricted,omitempty"`
	NoForwards      bool     `json:"noforwards,omitempty"`
	PhotoID         int64    `json:"photo_id,omitempty"`
	UpdatedAt       int64    `json:"updated_at,omitempty"`
}

func (p CachedPeer) clone() CachedPeer {
	cp := p
	if len(p.ActiveUsernames) > 0 {
		cp.ActiveUsernames = slices.Clone(p.ActiveUsernames)
	}
	return cp
}

// ToChannelInfo 将频道/群组缓存条目转换为 ChannelInfo。
func (p CachedPeer) ToChannelInfo(linkedChatID int64) *ChannelInfo {
	return &ChannelInfo{
		ID:              p.RawID,
		MarkedID:        p.MarkedID,
		AccessHash:      p.AccessHash,
		Title:           p.Title,
		Username:        p.Username,
		ActiveUsernames: slices.Clone(p.ActiveUsernames),
		Broadcast:       p.Broadcast,
		Megagroup:       p.Megagroup,
		Forum:           p.Forum,
		Monoforum:       p.Monoforum,
		Restricted:      p.Restricted,
		NoForwards:      p.NoForwards,
		LinkedChatID:    linkedChatID,
		PhotoID:         p.PhotoID,
	}
}

// ToProfileSummary 将用户或频道/群组缓存条目转换为 ProfileSummary。
func (p CachedPeer) ToProfileSummary() ProfileSummary {
	first := p.FirstName
	if first == "" && p.Kind != PeerKindUser {
		first = p.Title
	}
	return ProfileSummary{
		ID:              p.RawID,
		AccessHash:      p.AccessHash,
		IsBot:           p.IsBot,
		IsDeleted:       p.IsDeleted,
		FirstName:       first,
		LastName:        p.LastName,
		Username:        p.Username,
		ActiveUsernames: slices.Clone(p.ActiveUsernames),
		Phone:           p.Phone,
		HasPhoto:        p.HasPhoto,
	}
}

// ChannelMarkedID 将原始正整数 Channel ID 转换为 Bot API / TDLib 标记 ID（-100<id>）。
func ChannelMarkedID(channelID int64) int64 {
	if channelID <= 0 {
		return channelID
	}
	var id constant.TDLibPeerID
	id.Channel(channelID)
	return int64(id)
}

// ChatMarkedID 将原始正整数基础群 Chat ID 转换为负数标记 ID（-<id>）。
func ChatMarkedID(chatID int64) int64 {
	if chatID <= 0 {
		return chatID
	}
	var id constant.TDLibPeerID
	id.Chat(chatID)
	return int64(id)
}

// ParseMarkedPeerID 解析 Bot API / TDLib 标记 ID，返回实体类别与原始正整数 ID。
func ParseMarkedPeerID(markedID int64) (PeerKind, int64) {
	if markedID > 0 {
		return PeerKindUser, markedID
	}
	if markedID == 0 {
		return "", 0
	}
	tdID := constant.TDLibPeerID(markedID)
	switch {
	case tdID.IsChannel() || tdID.IsMonoforum():
		return PeerKindChannel, tdID.ToPlain()
	case tdID.IsChat():
		return PeerKindChat, tdID.ToPlain()
	default:
		// 兼容合成测试中形如 -10012345 的短负数标记，其余负值按普通群处理
		s := strconv.FormatInt(markedID, 10)
		if strings.HasPrefix(s, "-100") && len(s) > 4 {
			if raw, err := strconv.ParseInt(s[4:], 10, 64); err == nil && raw > 0 {
				return PeerKindChannel, raw
			}
		}
		return PeerKindChat, -markedID
	}
}

// PeerMarkedID 将 tg.PeerClass 转换为标记 ID。
func PeerMarkedID(peer tg.PeerClass) int64 {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return p.UserID
	case *tg.PeerChat:
		return ChatMarkedID(p.ChatID)
	case *tg.PeerChannel:
		return ChannelMarkedID(p.ChannelID)
	default:
		return 0
	}
}

type peerStoreDisk struct {
	Version int          `json:"version"`
	Peers   []CachedPeer `json:"peers"`
}

// PeerCache 维护线程安全、原子持久化的 Telegram 实体缓存（<basePath>.peers.json）。
type PeerCache struct {
	mu         sync.RWMutex
	peersPath  string
	sqlitePath string
	byMarked   map[int64]CachedPeer
	byUsername map[string]int64 // 小写无 '@' 用户名 -> MarkedID
}

// NewMemoryPeerCache 创建纯内存 PeerCache（供无文件单元测试使用）。
func NewMemoryPeerCache() *PeerCache {
	return &PeerCache{
		byMarked:   make(map[int64]CachedPeer),
		byUsername: make(map[string]int64),
	}
}

// NewPeerCache 创建并加载 <basePath>.peers.json；首次不存在时自动从 <basePath>.session 导入 entities。
func NewPeerCache(sessionPath string) (*PeerCache, error) {
	_, _, sqlitePath, peersPath := NormalizeSessionPaths(sessionPath)
	c := &PeerCache{
		peersPath:  peersPath,
		sqlitePath: sqlitePath,
		byMarked:   make(map[int64]CachedPeer),
		byUsername: make(map[string]int64),
	}
	if peersPath == "" {
		return c, nil
	}
	if err := c.loadInitial(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

// PeersPath 返回持久化文件路径。
func (c *PeerCache) PeersPath() string { return c.peersPath }

func (c *PeerCache) loadInitial(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if data, err := os.ReadFile(c.peersPath); err == nil && len(data) > 0 {
		var disk peerStoreDisk
		if err := json.Unmarshal(data, &disk); err == nil {
			for _, p := range disk.Peers {
				if p.MarkedID != 0 {
					c.putLocked(p)
				}
			}
			return nil
		}
		// 兼容顶层 map 或切片格式
		var list []CachedPeer
		if err := json.Unmarshal(data, &list); err == nil {
			for _, p := range list {
				if p.MarkedID != 0 {
					c.putLocked(p)
				}
			}
			return nil
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read peers cache %q: %w", c.peersPath, err)
	}

	if c.sqlitePath == "" {
		return nil
	}
	fi, err := os.Stat(c.sqlitePath)
	if err != nil || fi.IsDir() {
		return nil
	}

	imported, err := importTelethonEntities(ctx, c.sqlitePath)
	if err != nil {
		return err
	}
	for _, p := range imported {
		c.putLocked(p)
	}
	return c.saveLocked()
}

func importTelethonEntities(ctx context.Context, sqlitePath string) ([]CachedPeer, error) {
	db, err := sql.Open("sqlite", buildReadOnlySQLiteDSN(sqlitePath))
	if err != nil {
		return nil, fmt.Errorf("open telethon sqlite entities %q: %w", sqlitePath, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	rows, err := db.QueryContext(
		ctx,
		`SELECT id, COALESCE(hash, 0), COALESCE(username, ''), COALESCE(name, '') FROM entities`,
	)
	if err != nil {
		if isNoSuchTableErr(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("query telethon entities %q: %w", sqlitePath, err)
	}
	defer rows.Close()

	now := time.Now().UTC().Unix()
	var out []CachedPeer
	for rows.Next() {
		var (
			id       int64
			hash     int64
			username string
			name     string
		)
		if err := rows.Scan(&id, &hash, &username, &name); err != nil {
			return nil, fmt.Errorf("scan telethon entity: %w", err)
		}
		if id == 0 {
			continue
		}
		kind, rawID := ParseMarkedPeerID(id)
		if kind == "" || rawID <= 0 {
			continue
		}
		uname := normalizeUsername(username)
		p := CachedPeer{
			MarkedID:      id,
			RawID:         rawID,
			Kind:          kind,
			AccessHash:    hash,
			HasAccessHash: true,
			Username:      uname,
			Title:         strings.TrimSpace(name),
			UpdatedAt:     now,
		}
		if kind == PeerKindUser {
			p.FirstName = strings.TrimSpace(name)
		}
		if uname != "" {
			p.ActiveUsernames = []string{uname}
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *PeerCache) putLocked(p CachedPeer) bool {
	if p.MarkedID == 0 || p.RawID <= 0 || p.Kind == "" {
		return false
	}
	prev, hadPrev := c.byMarked[p.MarkedID]
	if hadPrev {
		// 清理旧用户名索引
		if prev.Username != "" {
			delete(c.byUsername, strings.ToLower(prev.Username))
		}
		for _, u := range prev.ActiveUsernames {
			delete(c.byUsername, strings.ToLower(u))
		}
	}

	p.Username = normalizeUsername(p.Username)
	if len(p.ActiveUsernames) > 0 {
		cleaned := make([]string, 0, len(p.ActiveUsernames))
		seen := make(map[string]struct{}, len(p.ActiveUsernames))
		for _, u := range p.ActiveUsernames {
			nu := normalizeUsername(u)
			if nu == "" {
				continue
			}
			lu := strings.ToLower(nu)
			if _, ok := seen[lu]; !ok {
				seen[lu] = struct{}{}
				cleaned = append(cleaned, nu)
			}
		}
		p.ActiveUsernames = cleaned
		if p.Username == "" && len(cleaned) > 0 {
			p.Username = cleaned[0]
		}
	}

	if p.Username != "" {
		c.byUsername[strings.ToLower(p.Username)] = p.MarkedID
	}
	for _, u := range p.ActiveUsernames {
		c.byUsername[strings.ToLower(u)] = p.MarkedID
	}

	if hadPrev && cachedPeersEqualIgnoringTime(prev, p) {
		p.UpdatedAt = prev.UpdatedAt
		c.byMarked[p.MarkedID] = p.clone()
		return false
	}
	if p.UpdatedAt == 0 {
		p.UpdatedAt = time.Now().UTC().Unix()
	}
	c.byMarked[p.MarkedID] = p.clone()
	return true
}

func cachedPeersEqualIgnoringTime(a, b CachedPeer) bool {
	return a.MarkedID == b.MarkedID &&
		a.RawID == b.RawID &&
		a.Kind == b.Kind &&
		a.AccessHash == b.AccessHash &&
		a.HasAccessHash == b.HasAccessHash &&
		a.Username == b.Username &&
		slices.Equal(a.ActiveUsernames, b.ActiveUsernames) &&
		a.Title == b.Title &&
		a.FirstName == b.FirstName &&
		a.LastName == b.LastName &&
		a.Phone == b.Phone &&
		a.IsBot == b.IsBot &&
		a.IsDeleted == b.IsDeleted &&
		a.HasPhoto == b.HasPhoto &&
		a.Broadcast == b.Broadcast &&
		a.Megagroup == b.Megagroup &&
		a.Forum == b.Forum &&
		a.Monoforum == b.Monoforum &&
		a.Restricted == b.Restricted &&
		a.NoForwards == b.NoForwards &&
		a.PhotoID == b.PhotoID
}

func (c *PeerCache) saveLocked() error {
	if c.peersPath == "" {
		return nil
	}
	peers := make([]CachedPeer, 0, len(c.byMarked))
	for _, p := range c.byMarked {
		peers = append(peers, p.clone())
	}
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].MarkedID < peers[j].MarkedID
	})
	raw, err := json.MarshalIndent(peerStoreDisk{
		Version: 1,
		Peers:   peers,
	}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile0600(c.peersPath, raw)
}

// Put 手动写入或更新单条缓存记录并原子落盘。
func (c *PeerCache) Put(p CachedPeer) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.putLocked(p) {
		return c.saveLocked()
	}
	return nil
}

// GetByMarkedID 按标记 ID 读取缓存条目。
func (c *PeerCache) GetByMarkedID(markedID int64) (CachedPeer, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.byMarked[markedID]
	if !ok {
		return CachedPeer{}, false
	}
	return p.clone(), true
}

// GetByUsername 按不区分大小写的用户名读取缓存条目。
func (c *PeerCache) GetByUsername(username string) (CachedPeer, bool) {
	uname := strings.ToLower(normalizeUsername(username))
	if uname == "" {
		return CachedPeer{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	markedID, ok := c.byUsername[uname]
	if !ok {
		return CachedPeer{}, false
	}
	p, ok := c.byMarked[markedID]
	if !ok {
		return CachedPeer{}, false
	}
	return p.clone(), true
}

// KnownGroupEntities 返回所有已知负数标记群组/频道条目，供 GroupDirectory 发现使用。
func (c *PeerCache) KnownGroupEntities() []CachedPeer {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var out []CachedPeer
	for _, p := range c.byMarked {
		if p.MarkedID < 0 && (p.Kind == PeerKindChannel || p.Kind == PeerKindChat) {
			out = append(out, p.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].MarkedID < out[j].MarkedID
	})
	return out
}

// ApplyEntities 从 tg.Entities 提取全部 User / Chat / Channel 并更新缓存。
func (c *PeerCache) ApplyEntities(e tg.Entities) {
	c.mu.Lock()
	defer c.mu.Unlock()

	changed := false
	for _, u := range e.Users {
		if u != nil && c.mergeUserLocked(u) {
			changed = true
		}
	}
	for _, ch := range e.Chats {
		if ch != nil && c.mergeBasicChatLocked(ch) {
			changed = true
		}
	}
	for _, ch := range e.Channels {
		if ch != nil && c.mergeChannelLocked(ch) {
			changed = true
		}
	}
	if changed {
		_ = c.saveLocked()
	}
}

// ApplyUsersChats 从任意 RPC 返回的 Users 与 Chats 列表更新缓存。
func (c *PeerCache) ApplyUsersChats(users []tg.UserClass, chats []tg.ChatClass) {
	if len(users) == 0 && len(chats) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	changed := false
	for _, uc := range users {
		if u, ok := uc.(*tg.User); ok && u != nil {
			if c.mergeUserLocked(u) {
				changed = true
			}
		}
	}
	for _, cc := range chats {
		switch ch := cc.(type) {
		case *tg.Chat:
			if ch != nil && c.mergeBasicChatLocked(ch) {
				changed = true
			}
		case *tg.Channel:
			if ch != nil && c.mergeChannelLocked(ch) {
				changed = true
			}
		}
	}
	if changed {
		_ = c.saveLocked()
	}
}

// ApplyUpdates 从 tg.UpdatesClass 响应中提取 Users 与 Chats。
func (c *PeerCache) ApplyUpdates(u tg.UpdatesClass) {
	switch v := u.(type) {
	case *tg.Updates:
		c.ApplyUsersChats(v.Users, v.Chats)
	case *tg.UpdatesCombined:
		c.ApplyUsersChats(v.Users, v.Chats)
	}
}

// ApplyMessages 从消息查询响应中提取 Users 与 Chats。
func (c *PeerCache) ApplyMessages(m tg.MessagesMessagesClass) {
	switch v := m.(type) {
	case *tg.MessagesMessages:
		c.ApplyUsersChats(v.Users, v.Chats)
	case *tg.MessagesMessagesSlice:
		c.ApplyUsersChats(v.Users, v.Chats)
	case *tg.MessagesChannelMessages:
		c.ApplyUsersChats(v.Users, v.Chats)
	}
}

// UpdateUserName 在收到 UpdateUserName 时同步更新本地用户缓存。
func (c *PeerCache) UpdateUserName(ev UserNameEvent) {
	if ev.UserID <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	existing, ok := c.byMarked[ev.UserID]
	if !ok {
		existing = CachedPeer{
			MarkedID: ev.UserID,
			RawID:    ev.UserID,
			Kind:     PeerKindUser,
		}
	}
	existing.FirstName = ev.FirstName
	existing.LastName = ev.LastName
	existing.Title = strings.TrimSpace(ev.FirstName + " " + ev.LastName)
	existing.Username = normalizeUsername(ev.Username)
	existing.ActiveUsernames = slices.Clone(ev.ActiveUsernames)
	existing.UpdatedAt = time.Now().UTC().Unix()
	if c.putLocked(existing) {
		_ = c.saveLocked()
	}
}

func (c *PeerCache) mergeUserLocked(u *tg.User) bool {
	if u.ID <= 0 {
		return false
	}
	existing, had := c.byMarked[u.ID]
	accessHash := existing.AccessHash
	hasHash := existing.HasAccessHash
	if hash, ok := u.GetAccessHash(); ok && (!u.Min || !hasHash) {
		accessHash = hash
		hasHash = true
	}

	active := extractActiveUsernames(u.Usernames)
	uname := normalizeUsername(u.Username)
	if uname == "" && len(active) > 0 {
		uname = active[0]
	} else if uname != "" && len(active) == 0 {
		active = []string{uname}
	}

	hasPhoto := existing.HasPhoto
	if photo, ok := u.GetPhoto(); ok {
		_, hasPhoto = photo.(*tg.UserProfilePhoto)
	} else if !u.Min {
		hasPhoto = false
	}

	firstName := u.FirstName
	lastName := u.LastName
	phone := u.Phone
	if u.Min && had {
		if firstName == "" {
			firstName = existing.FirstName
		}
		if lastName == "" {
			lastName = existing.LastName
		}
		if phone == "" {
			phone = existing.Phone
		}
	}

	title := strings.TrimSpace(firstName + " " + lastName)
	p := CachedPeer{
		MarkedID:        u.ID,
		RawID:           u.ID,
		Kind:            PeerKindUser,
		AccessHash:      accessHash,
		HasAccessHash:   hasHash,
		Username:        uname,
		ActiveUsernames: active,
		Title:           title,
		FirstName:       firstName,
		LastName:        lastName,
		Phone:           phone,
		IsBot:           u.Bot,
		IsDeleted:       u.Deleted,
		HasPhoto:        hasPhoto,
		UpdatedAt:       time.Now().UTC().Unix(),
	}
	return c.putLocked(p)
}

func (c *PeerCache) mergeBasicChatLocked(ch *tg.Chat) bool {
	if ch.ID <= 0 {
		return false
	}
	markedID := ChatMarkedID(ch.ID)
	var photoID int64
	hasPhoto := false
	if cp, ok := ch.Photo.(*tg.ChatPhoto); ok && cp != nil {
		photoID = cp.PhotoID
		hasPhoto = true
	}
	p := CachedPeer{
		MarkedID:   markedID,
		RawID:      ch.ID,
		Kind:       PeerKindChat,
		Title:      ch.Title,
		HasPhoto:   hasPhoto,
		NoForwards: ch.Noforwards,
		PhotoID:    photoID,
		UpdatedAt:  time.Now().UTC().Unix(),
	}
	return c.putLocked(p)
}

func (c *PeerCache) mergeChannelLocked(ch *tg.Channel) bool {
	if ch.ID <= 0 {
		return false
	}
	markedID := ChannelMarkedID(ch.ID)
	existing, _ := c.byMarked[markedID]

	accessHash := existing.AccessHash
	hasHash := existing.HasAccessHash
	if hash, ok := ch.GetAccessHash(); ok && (!ch.Min || !hasHash) {
		accessHash = hash
		hasHash = true
	}

	active := extractActiveUsernames(ch.Usernames)
	uname := normalizeUsername(ch.Username)
	if uname == "" && len(active) > 0 {
		uname = active[0]
	} else if uname != "" && len(active) == 0 {
		active = []string{uname}
	}

	var photoID int64
	hasPhoto := false
	if cp, ok := ch.Photo.(*tg.ChatPhoto); ok && cp != nil {
		photoID = cp.PhotoID
		hasPhoto = true
	}

	p := CachedPeer{
		MarkedID:        markedID,
		RawID:           ch.ID,
		Kind:            PeerKindChannel,
		AccessHash:      accessHash,
		HasAccessHash:   hasHash,
		Username:        uname,
		ActiveUsernames: active,
		Title:           ch.Title,
		HasPhoto:        hasPhoto,
		Broadcast:       ch.Broadcast,
		Megagroup:       ch.Megagroup || ch.Gigagroup,
		Forum:           ch.Forum,
		Monoforum:       ch.Monoforum,
		Restricted:      ch.Restricted,
		NoForwards:      ch.Noforwards,
		PhotoID:         photoID,
		UpdatedAt:       time.Now().UTC().Unix(),
	}
	return c.putLocked(p)
}

func extractActiveUsernames(list []tg.Username) []string {
	if len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item.Active && item.Username != "" {
			out = append(out, normalizeUsername(item.Username))
		}
	}
	return out
}

func normalizeUsername(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "@")
	return s
}

type parsedRef struct {
	isSelf     bool
	isInvite   bool
	inviteHash string
	hasMarked  bool
	markedID   int64
	username   string
}

// parseTargetString 解析字符串形式的频道/群组/用户引用：
// 支持 @username、username、https://t.me/xxx、https://t.me/s/xxx/123、
// https://t.me/c/<rawID>/123、-100xxx、-xxx、正整数 ID，以及邀请链接识别。
func parseTargetString(raw string) (parsedRef, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return parsedRef{}, errors.New("empty peer reference")
	}
	switch strings.ToLower(s) {
	case "me", "self":
		return parsedRef{isSelf: true}, nil
	}

	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n == 0 {
			return parsedRef{}, errors.New("invalid zero peer id")
		}
		return parsedRef{hasMarked: true, markedID: n}, nil
	}

	if strings.HasPrefix(s, "tg://resolve") {
		if u, err := url.Parse(s); err == nil {
			if domain := normalizeUsername(u.Query().Get("domain")); domain != "" {
				return parsedRef{username: domain}, nil
			}
		}
	}
	if strings.HasPrefix(s, "tg://join") {
		if u, err := url.Parse(s); err == nil {
			if invite := strings.TrimSpace(u.Query().Get("invite")); invite != "" {
				return parsedRef{isInvite: true, inviteHash: invite}, nil
			}
		}
	}

	cleaned := s
	if strings.HasPrefix(strings.ToLower(cleaned), "t.me/") || strings.HasPrefix(strings.ToLower(cleaned), "telegram.me/") {
		cleaned = "https://" + cleaned
	}
	if strings.HasPrefix(strings.ToLower(cleaned), "http://") || strings.HasPrefix(strings.ToLower(cleaned), "https://") {
		u, err := url.Parse(cleaned)
		if err != nil {
			return parsedRef{}, fmt.Errorf("invalid telegram url %q: %w", raw, err)
		}
		host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
		if host != "t.me" && host != "telegram.me" && host != "telegram.dog" {
			return parsedRef{}, fmt.Errorf("unsupported telegram host %q", u.Host)
		}
		parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
		if len(parts) == 0 {
			return parsedRef{}, fmt.Errorf("empty path in telegram url %q", raw)
		}
		switch {
		case strings.HasPrefix(parts[0], "+"):
			hash := strings.TrimPrefix(parts[0], "+")
			if hash == "" {
				return parsedRef{}, fmt.Errorf("empty invite hash in %q", raw)
			}
			return parsedRef{isInvite: true, inviteHash: hash}, nil
		case strings.EqualFold(parts[0], "joinchat"):
			if len(parts) < 2 || parts[1] == "" {
				return parsedRef{}, fmt.Errorf("empty joinchat hash in %q", raw)
			}
			return parsedRef{isInvite: true, inviteHash: parts[1]}, nil
		case strings.EqualFold(parts[0], "c"):
			if len(parts) < 2 {
				return parsedRef{}, fmt.Errorf("missing channel id in %q", raw)
			}
			rawID, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil || rawID <= 0 {
				return parsedRef{}, fmt.Errorf("invalid channel id in %q", raw)
			}
			return parsedRef{hasMarked: true, markedID: ChannelMarkedID(rawID)}, nil
		case strings.EqualFold(parts[0], "s"):
			if len(parts) < 2 || parts[1] == "" {
				return parsedRef{}, fmt.Errorf("missing channel username in %q", raw)
			}
			return parsedRef{username: normalizeUsername(parts[1])}, nil
		default:
			return parsedRef{username: normalizeUsername(parts[0])}, nil
		}
	}

	uname := normalizeUsername(s)
	if idx := strings.IndexByte(uname, '/'); idx > 0 {
		uname = uname[:idx]
	}
	if uname == "" {
		return parsedRef{}, fmt.Errorf("invalid peer reference %q", raw)
	}
	return parsedRef{username: uname}, nil
}

// ResolveInputPeer 将任意目标（int64/int/string/*ChannelInfo/ProfileSummary/tg.InputPeerClass 等）
// 解析为 tg.InputPeerClass，缓存未命中用户名时自动调用 contacts.ResolveUsername。
func (c *PeerCache) ResolveInputPeer(ctx context.Context, api *tg.Client, target any) (tg.InputPeerClass, error) {
	switch v := target.(type) {
	case nil:
		return nil, errors.New("nil target peer")
	case tg.InputPeerClass:
		return v, nil
	case *tg.InputChannel:
		return &tg.InputPeerChannel{ChannelID: v.ChannelID, AccessHash: v.AccessHash}, nil
	case *tg.InputUser:
		return &tg.InputPeerUser{UserID: v.UserID, AccessHash: v.AccessHash}, nil
	case *tg.InputUserSelf:
		return &tg.InputPeerSelf{}, nil
	case tg.PeerClass:
		return c.resolveMarkedIDToInputPeer(PeerMarkedID(v))
	case int64:
		return c.resolveMarkedIDToInputPeer(v)
	case int:
		return c.resolveMarkedIDToInputPeer(int64(v))
	case int32:
		return c.resolveMarkedIDToInputPeer(int64(v))
	case uint64:
		return c.resolveMarkedIDToInputPeer(int64(v))
	case *ChannelInfo:
		if v == nil {
			return nil, errors.New("nil *ChannelInfo")
		}
		return c.resolveChannelInfoToInputPeer(ctx, api, *v)
	case ChannelInfo:
		return c.resolveChannelInfoToInputPeer(ctx, api, v)
	case *ProfileSummary:
		if v == nil {
			return nil, errors.New("nil *ProfileSummary")
		}
		return c.resolveProfileToInputPeer(ctx, api, *v)
	case ProfileSummary:
		return c.resolveProfileToInputPeer(ctx, api, v)
	case *CachedPeer:
		if v == nil {
			return nil, errors.New("nil *CachedPeer")
		}
		return cachedPeerToInputPeer(*v)
	case CachedPeer:
		return cachedPeerToInputPeer(v)
	case string:
		ref, err := parseTargetString(v)
		if err != nil {
			return nil, err
		}
		if ref.isSelf {
			return &tg.InputPeerSelf{}, nil
		}
		if ref.isInvite {
			return nil, fmt.Errorf("invite link %q cannot be resolved without joining", v)
		}
		if ref.hasMarked {
			return c.resolveMarkedIDToInputPeer(ref.markedID)
		}
		p, err := c.resolveUsernameCachedOrRPC(ctx, api, ref.username)
		if err != nil {
			return nil, err
		}
		return cachedPeerToInputPeer(p)
	default:
		return nil, fmt.Errorf("unsupported target peer type %T", target)
	}
}

// ResolveInputChannel 将任意频道/超级群目标解析为 tg.InputChannelClass。
func (c *PeerCache) ResolveInputChannel(ctx context.Context, api *tg.Client, target any) (tg.InputChannelClass, error) {
	switch v := target.(type) {
	case tg.InputChannelClass:
		return v, nil
	case *tg.InputPeerChannel:
		return &tg.InputChannel{ChannelID: v.ChannelID, AccessHash: v.AccessHash}, nil
	case *ChannelInfo:
		if v == nil {
			return nil, errors.New("nil *ChannelInfo")
		}
		return c.resolveChannelInfoToInputChannel(ctx, api, *v)
	case ChannelInfo:
		return c.resolveChannelInfoToInputChannel(ctx, api, v)
	case int64:
		return c.resolveIntToInputChannel(v)
	case int:
		return c.resolveIntToInputChannel(int64(v))
	case string:
		ref, err := parseTargetString(v)
		if err != nil {
			return nil, err
		}
		if ref.isInvite {
			return nil, fmt.Errorf("invite link %q requires ImportChatInvite", v)
		}
		if ref.hasMarked {
			return c.resolveIntToInputChannel(ref.markedID)
		}
		p, err := c.resolveUsernameCachedOrRPC(ctx, api, ref.username)
		if err != nil {
			return nil, err
		}
		if p.Kind != PeerKindChannel {
			return nil, fmt.Errorf("peer @%s is %s, not channel", ref.username, p.Kind)
		}
		return &tg.InputChannel{ChannelID: p.RawID, AccessHash: p.AccessHash}, nil
	}

	peer, err := c.ResolveInputPeer(ctx, api, target)
	if err != nil {
		return nil, err
	}
	ch, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, fmt.Errorf("target %v resolved to %T, not channel", target, peer)
	}
	return &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, nil
}

// ResolveInputUser 将任意用户目标解析为 tg.InputUserClass。
func (c *PeerCache) ResolveInputUser(ctx context.Context, api *tg.Client, target any) (tg.InputUserClass, error) {
	switch v := target.(type) {
	case tg.InputUserClass:
		return v, nil
	case *tg.InputPeerUser:
		return &tg.InputUser{UserID: v.UserID, AccessHash: v.AccessHash}, nil
	case *tg.InputPeerSelf:
		return &tg.InputUserSelf{}, nil
	case *ProfileSummary:
		if v == nil {
			return nil, errors.New("nil *ProfileSummary")
		}
		return c.resolveProfileToInputUser(ctx, api, *v)
	case ProfileSummary:
		return c.resolveProfileToInputUser(ctx, api, v)
	}

	peer, err := c.ResolveInputPeer(ctx, api, target)
	if err != nil {
		return nil, err
	}
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		return &tg.InputUser{UserID: p.UserID, AccessHash: p.AccessHash}, nil
	case *tg.InputPeerSelf:
		return &tg.InputUserSelf{}, nil
	default:
		return nil, fmt.Errorf("target %v resolved to %T, not user", target, peer)
	}
}

func (c *PeerCache) resolveMarkedIDToInputPeer(markedID int64) (tg.InputPeerClass, error) {
	if markedID == 0 {
		return nil, errors.New("invalid zero peer id")
	}
	if p, ok := c.GetByMarkedID(markedID); ok {
		return cachedPeerToInputPeer(p)
	}
	kind, rawID := ParseMarkedPeerID(markedID)
	switch kind {
	case PeerKindUser:
		return &tg.InputPeerUser{UserID: rawID, AccessHash: 0}, nil
	case PeerKindChat:
		return &tg.InputPeerChat{ChatID: rawID}, nil
	case PeerKindChannel:
		return &tg.InputPeerChannel{ChannelID: rawID, AccessHash: 0}, nil
	default:
		return nil, fmt.Errorf("cannot resolve peer id %d", markedID)
	}
}

func (c *PeerCache) resolveIntToInputChannel(id int64) (tg.InputChannelClass, error) {
	if id == 0 {
		return nil, errors.New("invalid zero channel id")
	}
	if id > 0 {
		marked := ChannelMarkedID(id)
		if p, ok := c.GetByMarkedID(marked); ok && p.Kind == PeerKindChannel {
			return &tg.InputChannel{ChannelID: p.RawID, AccessHash: p.AccessHash}, nil
		}
		return &tg.InputChannel{ChannelID: id, AccessHash: 0}, nil
	}
	kind, rawID := ParseMarkedPeerID(id)
	if kind != PeerKindChannel {
		return nil, fmt.Errorf("peer id %d is %s, not channel", id, kind)
	}
	if p, ok := c.GetByMarkedID(id); ok {
		return &tg.InputChannel{ChannelID: p.RawID, AccessHash: p.AccessHash}, nil
	}
	return &tg.InputChannel{ChannelID: rawID, AccessHash: 0}, nil
}

func (c *PeerCache) resolveChannelInfoToInputPeer(ctx context.Context, api *tg.Client, info ChannelInfo) (tg.InputPeerClass, error) {
	ch, err := c.resolveChannelInfoToInputChannel(ctx, api, info)
	if err != nil {
		if info.MarkedID < 0 {
			if kind, raw := ParseMarkedPeerID(info.MarkedID); kind == PeerKindChat {
				return &tg.InputPeerChat{ChatID: raw}, nil
			}
		}
		return nil, err
	}
	ic := ch.(*tg.InputChannel)
	return &tg.InputPeerChannel{ChannelID: ic.ChannelID, AccessHash: ic.AccessHash}, nil
}

func (c *PeerCache) resolveChannelInfoToInputChannel(ctx context.Context, api *tg.Client, info ChannelInfo) (tg.InputChannelClass, error) {
	rawID := info.ID
	if rawID <= 0 && info.MarkedID < 0 {
		if kind, r := ParseMarkedPeerID(info.MarkedID); kind == PeerKindChannel {
			rawID = r
		}
	}
	if rawID > 0 {
		hash := info.AccessHash
		if hash == 0 {
			if p, ok := c.GetByMarkedID(ChannelMarkedID(rawID)); ok {
				hash = p.AccessHash
			}
		}
		return &tg.InputChannel{ChannelID: rawID, AccessHash: hash}, nil
	}
	if info.Username != "" {
		return c.ResolveInputChannel(ctx, api, info.Username)
	}
	return nil, errors.New("empty ChannelInfo")
}

func (c *PeerCache) resolveProfileToInputPeer(ctx context.Context, api *tg.Client, prof ProfileSummary) (tg.InputPeerClass, error) {
	u, err := c.resolveProfileToInputUser(ctx, api, prof)
	if err != nil {
		return nil, err
	}
	iu := u.(*tg.InputUser)
	return &tg.InputPeerUser{UserID: iu.UserID, AccessHash: iu.AccessHash}, nil
}

func (c *PeerCache) resolveProfileToInputUser(ctx context.Context, api *tg.Client, prof ProfileSummary) (tg.InputUserClass, error) {
	if prof.ID > 0 {
		hash := prof.AccessHash
		if hash == 0 {
			if p, ok := c.GetByMarkedID(prof.ID); ok {
				hash = p.AccessHash
			}
		}
		return &tg.InputUser{UserID: prof.ID, AccessHash: hash}, nil
	}
	if prof.Username != "" {
		return c.ResolveInputUser(ctx, api, prof.Username)
	}
	return nil, errors.New("empty ProfileSummary")
}

func cachedPeerToInputPeer(p CachedPeer) (tg.InputPeerClass, error) {
	switch p.Kind {
	case PeerKindUser:
		return &tg.InputPeerUser{UserID: p.RawID, AccessHash: p.AccessHash}, nil
	case PeerKindChat:
		return &tg.InputPeerChat{ChatID: p.RawID}, nil
	case PeerKindChannel:
		return &tg.InputPeerChannel{ChannelID: p.RawID, AccessHash: p.AccessHash}, nil
	default:
		return nil, fmt.Errorf("unknown peer kind %q", p.Kind)
	}
}

func (c *PeerCache) resolveUsernameCachedOrRPC(ctx context.Context, api *tg.Client, username string) (CachedPeer, error) {
	uname := normalizeUsername(username)
	if uname == "" {
		return CachedPeer{}, errors.New("empty username")
	}
	if p, ok := c.GetByUsername(uname); ok {
		return p, nil
	}
	if api == nil {
		return CachedPeer{}, fmt.Errorf("username @%s not in peer cache and tg.Client is nil", uname)
	}
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: uname,
	})
	if err != nil {
		return CachedPeer{}, err
	}
	c.ApplyUsersChats(res.Users, res.Chats)
	markedID := PeerMarkedID(res.Peer)
	if p, ok := c.GetByMarkedID(markedID); ok {
		return p, nil
	}
	return CachedPeer{}, fmt.Errorf("contacts.ResolveUsername(@%s) returned empty entity", uname)
}
