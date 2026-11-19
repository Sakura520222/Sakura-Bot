// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package media

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// MaxPollImages 是单次趣味投票支持的最大图片张数（对齐 poll_vision.py:MAX_POLL_IMAGES）。
	MaxPollImages = 10
	// MaxPollTotalBytes 是单次趣味投票图片总字节上限（20 MiB，对齐 poll_images.py:MAX_TOTAL_BYTES）。
	MaxPollTotalBytes = 20 << 20
	// AlbumSettleDelay 是相册消息聚合等待窗口（0.9 秒，对齐 poll_images.py:ALBUM_SETTLE_SECONDS）。
	AlbumSettleDelay = 900 * time.Millisecond
	// AlbumScanRadius 是相册邻近消息扫描半径（20 条，对齐 poll_images.py:ALBUM_SCAN_RADIUS）。
	AlbumScanRadius = 20
)

// PollImageError 表示可安全写入日志的趣味投票图片采集错误（不含图片字节与上游异常体）。
type PollImageError string

func (e PollImageError) Error() string { return string(e) }

// ErrPollImageTooLarge 供底层下载器标记超过 8 MiB 单图上限。
var ErrPollImageTooLarge = errors.New("poll_image_exceeds_max_bytes")

// PollMediaItem 描述讨论群自动转发消息的趣味投票媒体元数据。
type PollMediaItem struct {
	ID               int
	GroupedID        int64
	Text             string
	IsStaticImage    bool
	MediaID          string
	FileSize         int64
	FwdChannelID     string
	FwdChannelPostID int
	RawMedia         any
}

// HasPollMedia 判断消息是否包含静态图片或属于媒体相册（对齐 poll_images.py:has_poll_media）。
func (m PollMediaItem) HasPollMedia() bool {
	return m.IsStaticImage || m.GroupedID != 0
}

// PollMediaClient 抽象趣味投票采集消息与下载媒体所需的客户端能力。
type PollMediaClient interface {
	GetPollMessages(ctx context.Context, discussionID int64, ids []int) ([]PollMediaItem, error)
	FetchOriginalPostImage(ctx context.Context, channelRef string, postID int) (*RemoteImageMessage, error)
	DownloadPollImageBytes(ctx context.Context, item PollMediaItem, maxBytes int64) ([]byte, error)
}

// CollectPollOptions 允许测试覆盖默认等待与超时参数。
type CollectPollOptions struct {
	SettleDelay     time.Duration
	FetchTimeout    time.Duration
	DownloadTimeout time.Duration
	Pipeline        *ImagePipeline
}

// CollectPollImages 聚合讨论群转发消息（含相册）说明文字与验证后的 data URL，并同步登记到共享图片仓（对齐 poll_images.py:collect_poll_images）。
func CollectPollImages(
	ctx context.Context,
	client PollMediaClient,
	message *PollMediaItem,
	discussionID int64,
	caption string,
	opts CollectPollOptions,
) (string, []string, error) {
	if message == nil || !message.HasPollMedia() {
		return caption, nil, nil
	}
	if client == nil {
		return "", nil, PollImageError("无法读取趣味投票原消息，已跳过")
	}

	settleDelay := opts.SettleDelay
	if settleDelay == 0 {
		settleDelay = AlbumSettleDelay
	}
	if settleDelay < 0 {
		settleDelay = 0
	}
	fetchTimeout := opts.FetchTimeout
	if fetchTimeout <= 0 {
		fetchTimeout = 30 * time.Second
	}
	downloadTimeout := opts.DownloadTimeout
	if downloadTimeout <= 0 {
		downloadTimeout = 45 * time.Second
	}

	if message.GroupedID != 0 && settleDelay > 0 {
		timer := time.NewTimer(settleDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", nil, PollImageError("无法读取趣味投票原消息，已跳过")
		case <-timer.C:
		}
	}

	fetchCtx, cancelFetch := context.WithTimeout(ctx, fetchTimeout)
	fetched, err := client.GetPollMessages(fetchCtx, discussionID, []int{message.ID})
	cancelFetch()
	if err != nil {
		return "", nil, PollImageError("无法读取趣味投票原消息，已跳过")
	}
	var current *PollMediaItem
	for i := range fetched {
		if fetched[i].ID == message.ID {
			current = &fetched[i]
			break
		}
	}
	if current == nil {
		return "", nil, PollImageError("趣味投票原消息已删除，已跳过")
	}

	byID := map[int]PollMediaItem{current.ID: *current}
	if current.GroupedID != 0 {
		startID := current.ID - AlbumScanRadius
		if startID < 1 {
			startID = 1
		}
		endID := current.ID + AlbumScanRadius
		ids := make([]int, 0, endID-startID+1)
		for id := startID; id <= endID; id++ {
			ids = append(ids, id)
		}
		albumCtx, cancelAlbum := context.WithTimeout(ctx, fetchTimeout)
		nearby, nerr := client.GetPollMessages(albumCtx, discussionID, ids)
		cancelAlbum()
		if nerr != nil {
			return "", nil, PollImageError("无法完整读取趣味投票相册，已跳过")
		}
		for _, item := range nearby {
			if item.ID > 0 && item.GroupedID == current.GroupedID {
				byID[item.ID] = item
			}
		}
	}

	orderedIDs := make([]int, 0, len(byID))
	for id := range byID {
		orderedIDs = append(orderedIDs, id)
	}
	sort.Ints(orderedIDs)

	seenText := make(map[string]bool)
	var texts []string
	var images []PollMediaItem
	for _, id := range orderedIDs {
		item := byID[id]
		if t := strings.TrimSpace(item.Text); t != "" && !seenText[t] {
			seenText[t] = true
			texts = append(texts, t)
		}
		if item.IsStaticImage {
			images = append(images, item)
		}
	}
	mergedCaption := strings.Join(texts, "\n\n")
	if len(images) > MaxPollImages {
		return "", nil, PollImageError("趣味投票一次最多支持 10 张图片，已跳过")
	}

	urls := make([]string, 0, len(images))
	totalBytes := 0
	for _, item := range images {
		if item.FileSize > MaxImageBytes {
			return "", nil, PollImageError("趣味投票单张图片超过 8 MiB，已跳过")
		}
		dlCtx, cancelDL := context.WithTimeout(ctx, downloadTimeout)
		data, derr := client.DownloadPollImageBytes(dlCtx, item, MaxImageBytes)
		cancelDL()
		if derr != nil {
			if errors.Is(derr, ErrPollImageTooLarge) {
				return "", nil, PollImageError("趣味投票单张图片超过 8 MiB，已跳过")
			}
			return "", nil, PollImageError("趣味投票图片下载或校验失败，已跳过")
		}
		if len(data) > MaxImageBytes {
			return "", nil, PollImageError("趣味投票单张图片超过 8 MiB，已跳过")
		}
		totalBytes += len(data)
		if totalBytes > MaxPollTotalBytes {
			return "", nil, PollImageError("趣味投票图片总量超过 20 MiB，已跳过")
		}
		dataURL, uerr := ImageDataURL(data)
		if uerr != nil {
			return "", nil, PollImageError("趣味投票图片下载或校验失败，已跳过")
		}
		mime, _, perr := ParseImageDataURL(dataURL)
		if perr != nil {
			return "", nil, PollImageError("趣味投票图片下载或校验失败，已跳过")
		}
		if err := RegisterPollImage(ctx, opts.Pipeline, client, data, mime, item); err != nil {
			return "", nil, PollImageError("趣味投票图片下载或校验失败，已跳过")
		}
		urls = append(urls, dataURL)
	}
	return mergedCaption, urls, nil
}

// RegisterPollImage 将投票图片注册到共享持久化图片仓（若讨论群转发早于频道监听到达，则回源抓取原频道帖子登记）。
func RegisterPollImage(
	ctx context.Context,
	pipeline *ImagePipeline,
	client PollMediaClient,
	data []byte,
	mime string,
	item PollMediaItem,
) error {
	if pipeline == nil || pipeline.store == nil {
		return nil
	}
	if item.FwdChannelID != "" && item.FwdChannelPostID > 0 {
		chKey := ChannelKey(item.FwdChannelID)
		rec, err := pipeline.store.GetMessageImage(ctx, chKey, int64(item.FwdChannelPostID))
		if err != nil {
			return err
		}
		if rec == nil || rec.Deleted || rec.MediaID != item.MediaID {
			if client == nil {
				return fmt.Errorf("poll_image_source_unavailable")
			}
			orig, oerr := client.FetchOriginalPostImage(ctx, item.FwdChannelID, item.FwdChannelPostID)
			if oerr != nil {
				return oerr
			}
			if orig == nil || orig.MediaID != item.MediaID {
				return fmt.Errorf("poll_image_source_changed")
			}
			if _, cerr := pipeline.Capture(ctx, *orig); cerr != nil {
				return cerr
			}
		}
	}
	_, err := pipeline.StorePollImage(ctx, data, mime, item.MediaID)
	return err
}

// ParseImageDataURL 校验并解码 data:image/{png,jpeg};base64,... 字符串（对齐 image_descriptions.py:image_data）。
func ParseImageDataURL(url string) (string, []byte, error) {
	header, encoded, ok := strings.Cut(url, ",")
	if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64") {
		return "", nil, fmt.Errorf("invalid_image_data_url")
	}
	mime := strings.SplitN(header[5:], ";", 2)[0]
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("invalid_image_data_url")
	}
	return mime, raw, nil
}

// SavedPollDescriptions 读取或按需生成投票图片的中文描述列表（对齐 image_descriptions.py:saved_poll_descriptions）。
func SavedPollDescriptions(
	ctx context.Context,
	pipeline *ImagePipeline,
	images []string,
	override *VisionSettings,
) ([]string, error) {
	if pipeline == nil || pipeline.store == nil {
		return nil, fmt.Errorf("image_store_unavailable")
	}
	descriptions := make([]string, 0, len(images))
	for idx, u := range images {
		_, data, err := ParseImageDataURL(u)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		asset, derr := pipeline.DescribeForPoll(ctx, digest, override)
		if derr != nil {
			return nil, derr
		}
		desc, terr := DescriptionText(asset)
		if terr != nil {
			return nil, terr
		}
		descriptions = append(descriptions, fmt.Sprintf("图%d：\n%s", idx+1, desc))
	}
	pipeline.logger.Info("趣味投票使用已持久化图片描述", "count", len(descriptions))
	return descriptions, nil
}
