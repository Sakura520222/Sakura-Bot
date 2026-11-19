// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package wiki

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

const (
	// MaxChannelAvatarBytes 是频道头像与封面最大允许字节数（8 MiB，对齐 avatars.py:MAX_AVATAR_BYTES）。
	MaxChannelAvatarBytes = 8 * 1024 * 1024
	// AvatarNegativeTTL 是无头像或获取失败时的负缓存冷却时间（5 分钟）。
	AvatarNegativeTTL = 5 * time.Minute
	// AvatarStaleAfter 是磁盘缓存后台静默刷新的过期时间（7 天）。
	AvatarStaleAfter = 7 * 24 * time.Hour
	// AvatarFetchTimeout 是单次拉取频道头像的超时上限（30 秒）。
	AvatarFetchTimeout = 30 * time.Second
	// DefaultAvatarDir 是频道头像磁盘缓存根目录。
	DefaultAvatarDir = "data/wiki/avatars"
)

// SniffMediaType 仅凭文件头魔数识别 JPEG / PNG 图片类型；不信任任何外部 MIME 声明。
func SniffMediaType(data []byte) string {
	if bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) {
		return "image/jpeg"
	}
	if bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) {
		return "image/png"
	}
	return ""
}

// AvatarClientsProvider 返回首选 UserBot Messenger 与备选主 Bot Messenger。
type AvatarClientsProvider func() (userBot mtproto.Messenger, botClient mtproto.Messenger)

// AvatarService 管理频道头像的磁盘缓存（data/wiki/avatars/{photo_id}.jpg）、7 天过期后台重验与 5 分钟负缓存。
type AvatarService struct {
	store    *Store
	provider AvatarClientsProvider
	root     string
	logger   *slog.Logger

	mu           sync.Mutex
	index        map[string]int64     // channelID -> photoID
	refreshedAt  map[string]time.Time // channelID -> last refreshed time
	negative     map[string]time.Time // channelID -> last failure/empty time
	channelLocks map[string]*sync.Mutex
	revalidating map[string]bool
	wg           sync.WaitGroup
}

// NewAvatarService 创建频道头像缓存服务。
func NewAvatarService(store *Store, provider AvatarClientsProvider, root string) *AvatarService {
	r := strings.TrimSpace(root)
	if r == "" {
		r = DefaultAvatarDir
	}
	return &AvatarService{
		store:        store,
		provider:     provider,
		root:         r,
		logger:       slog.Default(),
		index:        make(map[string]int64),
		refreshedAt:  make(map[string]time.Time),
		negative:     make(map[string]time.Time),
		channelLocks: make(map[string]*sync.Mutex),
		revalidating: make(map[string]bool),
	}
}

func (a *AvatarService) channelLock(channelID string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	lk, ok := a.channelLocks[channelID]
	if !ok {
		lk = &sync.Mutex{}
		a.channelLocks[channelID] = lk
	}
	return lk
}

// Get 获取频道头像（磁盘缓存优先，超 7 天返回旧图并后台单飞刷新；任何异常折叠为 ok=false 并记入 5 分钟负缓存）。
func (a *AvatarService) Get(ctx context.Context, channelID string) ([]byte, string, int64, bool) {
	a.mu.Lock()
	photoID, hasIndex := a.index[channelID]
	fetchedAt, hasFetched := a.refreshedAt[channelID]
	negAt, hasNeg := a.negative[channelID]
	a.mu.Unlock()

	if hasIndex && photoID != 0 {
		if data, mimeType, ok := a.readCached(photoID); ok {
			now := time.Now()
			if !hasFetched {
				a.mu.Lock()
				a.refreshedAt[channelID] = now
				a.mu.Unlock()
			} else if now.Sub(fetchedAt) > AvatarStaleAfter {
				a.scheduleRevalidate(channelID)
			}
			return data, mimeType, photoID, true
		}
	}

	if hasNeg && time.Since(negAt) < AvatarNegativeTTL {
		return nil, "", 0, false
	}

	fetchCtx, cancel := context.WithTimeout(ctx, AvatarFetchTimeout)
	defer cancel()
	data, mimeType, pid, err := a.Refresh(fetchCtx, channelID)
	if err != nil || len(data) == 0 {
		if err != nil {
			a.logger.Warn("频道头像获取失败", "channel_id", channelID, "error", err)
		}
		a.mu.Lock()
		a.negative[channelID] = time.Now()
		a.mu.Unlock()
		return nil, "", 0, false
	}
	return data, mimeType, pid, true
}

// Refresh 持频道单飞锁从 Telegram 回源拉取最新频道头像并原子落盘。
func (a *AvatarService) Refresh(ctx context.Context, channelID string) ([]byte, string, int64, error) {
	lk := a.channelLock(channelID)
	lk.Lock()
	defer lk.Unlock()

	cli, err := a.selectClient()
	if err != nil {
		return nil, "", 0, err
	}

	target := any(channelID)
	if cid, cErr := CanonicalChannelID(channelID); cErr == nil {
		target = "-100" + cid
	}

	// 先检查当前频道实体 photo_id，若未变且本地文件完好则直接零流量复用
	if chInfo, rErr := cli.ResolveChannel(ctx, fmt.Sprint(target)); rErr == nil && chInfo != nil {
		if chInfo.PhotoID == 0 {
			a.mu.Lock()
			a.negative[channelID] = time.Now()
			a.mu.Unlock()
			return nil, "", 0, nil
		}
		a.mu.Lock()
		sameID := a.index[channelID] == chInfo.PhotoID
		a.mu.Unlock()
		if sameID {
			if data, mimeType, ok := a.readCached(chInfo.PhotoID); ok {
				a.mu.Lock()
				a.refreshedAt[channelID] = time.Now()
				delete(a.negative, channelID)
				a.mu.Unlock()
				return data, mimeType, chInfo.PhotoID, nil
			}
		}
	}

	data, photoID, err := cli.DownloadProfilePhoto(ctx, target, MaxChannelAvatarBytes)
	if err != nil {
		return nil, "", 0, err
	}
	if photoID == 0 || len(data) == 0 {
		a.mu.Lock()
		a.negative[channelID] = time.Now()
		a.mu.Unlock()
		return nil, "", 0, nil
	}
	if len(data) > MaxChannelAvatarBytes {
		return nil, "", 0, errors.New("avatar exceeds 8 MiB limit")
	}
	mimeType := SniffMediaType(data)
	if mimeType == "" {
		return nil, "", 0, errors.New("downloaded profile photo is not JPEG or PNG")
	}
	if err := a.persist(photoID, data); err != nil {
		return nil, "", 0, err
	}

	a.mu.Lock()
	a.index[channelID] = photoID
	a.refreshedAt[channelID] = time.Now()
	delete(a.negative, channelID)
	a.mu.Unlock()

	return data, mimeType, photoID, nil
}

func (a *AvatarService) selectClient() (mtproto.Messenger, error) {
	if a.provider == nil {
		return nil, errors.New("wiki_avatar_client_unavailable")
	}
	ub, bot := a.provider()
	if ub != nil && ub.IsConnected() {
		return ub, nil
	}
	if bot != nil && bot.IsConnected() {
		return bot, nil
	}
	return nil, errors.New("wiki_avatar_client_unavailable")
}

func (a *AvatarService) readCached(photoID int64) ([]byte, string, bool) {
	p := filepath.Join(a.root, fmt.Sprintf("%d.jpg", photoID))
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 || len(data) > MaxChannelAvatarBytes {
		return nil, "", false
	}
	mimeType := SniffMediaType(data)
	if mimeType == "" {
		return nil, "", false
	}
	return data, mimeType, true
}

func (a *AvatarService) persist(photoID int64, data []byte) error {
	if err := os.MkdirAll(a.root, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(a.root, fmt.Sprintf("%d.jpg", photoID))
	tmp, err := os.CreateTemp(a.root, "avatar-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (a *AvatarService) scheduleRevalidate(channelID string) {
	a.mu.Lock()
	if a.revalidating[channelID] {
		a.mu.Unlock()
		return
	}
	a.revalidating[channelID] = true
	a.wg.Add(1)
	a.mu.Unlock()

	go func() {
		defer a.wg.Done()
		defer func() {
			a.mu.Lock()
			delete(a.revalidating, channelID)
			a.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), AvatarFetchTimeout)
		defer cancel()
		if _, _, _, err := a.Refresh(ctx, channelID); err != nil {
			a.logger.Warn("频道头像后台刷新失败", "channel_id", channelID, "error", err)
		}
	}()
}

// Wait 等待所有后台头像重验 goroutine 结束（供关停调用）。
func (a *AvatarService) Wait() {
	if a != nil {
		a.wg.Wait()
	}
}

// RefreshCover 校验并刷新 wiki_channel_covers 中的频道自定义封面（image 为空时删除封面）。
func (a *AvatarService) RefreshCover(ctx context.Context, channelID string, image []byte, actor string) (string, error) {
	if a == nil || a.store == nil {
		return "", errors.New("avatar service store is nil")
	}
	if len(image) == 0 {
		ok, err := a.store.SetCover(ctx, channelID, "", nil, actor)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errors.New("channel not found")
		}
		return "", nil
	}
	if len(image) > MaxChannelAvatarBytes {
		return "", errors.New("频道封面不能超过 8 MiB")
	}
	if err := ValidateImageBytes(image, MaxChannelAvatarBytes); err != nil {
		return "", err
	}
	sum := sha256.Sum256(image)
	digestHex := hex.EncodeToString(sum[:])
	ok, err := a.store.SetCover(ctx, channelID, digestHex, image, actor)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("channel not found")
	}
	return digestHex, nil
}
