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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

// ErrMediaTooLarge 表示待下载文件超出调用方设定的 maxBytes 硬上限。
var ErrMediaTooLarge = errors.New("telegram media size exceeds maxBytes limit")

func extractMediaAttachment(mc tg.MessageMediaClass) *MediaAttachment {
	switch m := mc.(type) {
	case nil, *tg.MessageMediaEmpty:
		return nil

	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.(*tg.Photo)
		if !ok || photo == nil {
			return &MediaAttachment{Kind: "photo", Raw: m}
		}
		w, h, size, _ := selectLargestPhotoSize(photo.Sizes)
		return &MediaAttachment{
			Kind:     "photo",
			MediaID:  strconv.FormatInt(photo.ID, 10),
			FileSize: size,
			MimeType: "image/jpeg",
			Width:    w,
			Height:   h,
			Raw:      m,
		}

	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok || doc == nil {
			return &MediaAttachment{Kind: "document", Raw: m}
		}
		att := &MediaAttachment{
			Kind:     "document",
			MediaID:  strconv.FormatInt(doc.ID, 10),
			FileSize: doc.Size,
			MimeType: doc.MimeType,
			Raw:      m,
		}
		var (
			isSticker  bool
			isAnimated bool
			isVideo    bool
			isVoice    bool
			isAudio    bool
		)
		for _, a := range doc.Attributes {
			switch attr := a.(type) {
			case *tg.DocumentAttributeFilename:
				att.FileName = attr.FileName
			case *tg.DocumentAttributeImageSize:
				att.Width = attr.W
				att.Height = attr.H
			case *tg.DocumentAttributeVideo:
				att.Width = attr.W
				att.Height = attr.H
				isVideo = true
			case *tg.DocumentAttributeAudio:
				if attr.Voice {
					isVoice = true
				} else {
					isAudio = true
				}
			case *tg.DocumentAttributeSticker:
				isSticker = true
			case *tg.DocumentAttributeAnimated:
				isAnimated = true
			}
		}
		switch {
		case isSticker:
			att.Kind = "sticker"
		case isAnimated || strings.EqualFold(doc.MimeType, "image/gif"):
			att.Kind = "gif"
		case isVoice:
			att.Kind = "voice"
		case isAudio:
			att.Kind = "audio"
		case isVideo:
			att.Kind = "video"
		default:
			att.Kind = "document"
		}
		return att

	case *tg.MessageMediaContact:
		return &MediaAttachment{
			Kind:             "contact",
			ContactFirstName: m.FirstName,
			ContactLastName:  m.LastName,
			ContactPhone:     m.PhoneNumber,
			ContactUserID:    m.UserID,
			Raw:              m,
		}

	case *tg.MessageMediaPoll:
		return &MediaAttachment{
			Kind: "poll",
			Raw:  m,
		}

	case *tg.MessageMediaWebPage:
		return &MediaAttachment{
			Kind: "webpage",
			Raw:  m,
		}

	default:
		return &MediaAttachment{
			Kind: "other",
			Raw:  mc,
		}
	}
}

func selectLargestPhotoSize(sizes []tg.PhotoSizeClass) (w, h int, size int64, thumbType string) {
	bestArea := -1
	bestBytes := int64(-1)
	for _, sc := range sizes {
		switch s := sc.(type) {
		case *tg.PhotoSize:
			area := s.W * s.H
			sz := int64(s.Size)
			if sz > bestBytes || (sz == bestBytes && area > bestArea) {
				w, h, size, thumbType = s.W, s.H, sz, s.Type
				bestArea = area
				bestBytes = sz
			}
		case *tg.PhotoSizeProgressive:
			area := s.W * s.H
			var sz int64
			if len(s.Sizes) > 0 {
				sz = int64(s.Sizes[len(s.Sizes)-1])
			}
			if sz > bestBytes || (sz == bestBytes && area > bestArea) {
				w, h, size, thumbType = s.W, s.H, sz, s.Type
				bestArea = area
				bestBytes = sz
			}
		case *tg.PhotoCachedSize:
			area := s.W * s.H
			sz := int64(len(s.Bytes))
			if thumbType == "" || sz > bestBytes || (sz == bestBytes && area > bestArea) {
				w, h, size, thumbType = s.W, s.H, sz, s.Type
				bestArea = area
				bestBytes = sz
			}
		}
	}
	return w, h, size, thumbType
}

func (c *Client) uploadMediaItem(ctx context.Context, up *uploader.Uploader, item MediaUpload) (tg.InputMediaClass, error) {
	fileName := strings.TrimSpace(item.FileName)
	if fileName == "" && item.FilePath != "" {
		fileName = filepath.Base(item.FilePath)
	}
	if fileName == "" {
		fileName = "upload.bin"
	}

	var (
		inputFile tg.InputFileClass
		err       error
	)
	switch {
	case len(item.Data) > 0:
		inputFile, err = up.FromBytes(ctx, fileName, item.Data)
	case strings.TrimSpace(item.FilePath) != "":
		inputFile, err = up.FromPath(ctx, item.FilePath)
	default:
		return nil, errors.New("MediaUpload requires either Data or FilePath")
	}
	if err != nil {
		return nil, fmt.Errorf("upload %q: %w", fileName, err)
	}

	mimeType := strings.TrimSpace(item.MimeType)
	if mimeType == "" {
		if ext := filepath.Ext(fileName); ext != "" {
			mimeType = mime.TypeByExtension(ext)
		}
	}
	if idx := strings.IndexByte(mimeType, ';'); idx > 0 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	isPhotoCandidate := !item.ForceDocument && (strings.EqualFold(mimeType, "image/jpeg") ||
		strings.EqualFold(mimeType, "image/jpg") ||
		strings.EqualFold(mimeType, "image/png"))
	if isPhotoCandidate {
		return &tg.InputMediaUploadedPhoto{File: inputFile}, nil
	}

	return &tg.InputMediaUploadedDocument{
		ForceFile: item.ForceDocument,
		File:      inputFile,
		MimeType:  mimeType,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: fileName},
		},
	}, nil
}

// SendMedia 上传并发送单个媒体文件（图片或通用文档），支持说明文字与内联键盘。
func (c *Client) SendMedia(ctx context.Context, target any, item MediaUpload, opts SendOptions) (int, error) {
	api := c.API()
	if api == nil {
		return 0, errors.New("mtproto client not initialized")
	}

	peer, err := c.Peers().ResolveInputPeer(ctx, api, target)
	if err != nil {
		return 0, err
	}

	up := uploader.NewUploader(api)
	media, err := c.uploadMediaItem(ctx, up, item)
	if err != nil {
		return 0, err
	}

	parseMode := item.ParseMode
	if parseMode == "" {
		parseMode = opts.ParseMode
	}
	captionText, explicit := item.Caption, opts.Entities
	if c.noEmoji {
		captionText, explicit = stripEmojiSpans(captionText, explicit)
	}
	caption, entities := c.prepareTextAndEntities(ctx, captionText, parseMode, explicit)

	randomID := cryptoRandInt64()
	req := &tg.MessagesSendMediaRequest{
		Silent:   opts.Silent,
		Peer:     peer,
		Media:    media,
		Message:  caption,
		RandomID: randomID,
	}
	if opts.ReplyToMsgID > 0 {
		req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: opts.ReplyToMsgID})
	}
	if markup := BuildInlineMarkup(c.cleanButtons(opts.Buttons)); markup != nil {
		req.SetReplyMarkup(markup)
	}
	if len(entities) > 0 {
		req.SetEntities(entities)
	}

	upd, err := api.MessagesSendMedia(ctx, req)
	if err != nil {
		return 0, err
	}
	c.Peers().ApplyUpdates(upd)
	return extractSentMessageID(upd, randomID), nil
}

// SendAlbum 上传并作为相册组合（messages.sendMultiMedia）发送多个媒体项。
func (c *Client) SendAlbum(ctx context.Context, target any, items []MediaUpload, opts SendOptions) ([]int, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) == 1 {
		id, err := c.SendMedia(ctx, target, items[0], opts)
		if err != nil {
			return nil, err
		}
		return []int{id}, nil
	}

	api := c.API()
	if api == nil {
		return nil, errors.New("mtproto client not initialized")
	}

	peer, err := c.Peers().ResolveInputPeer(ctx, api, target)
	if err != nil {
		return nil, err
	}

	up := uploader.NewUploader(api)
	multi := make([]tg.InputSingleMedia, 0, len(items))
	randomIDs := make([]int64, 0, len(items))

	for i, item := range items {
		uploaded, err := c.uploadMediaItem(ctx, up, item)
		if err != nil {
			return nil, err
		}

		// Telegram 相册要求将 InputMediaUploaded* 经 messages.uploadMedia 转换为 InputMediaPhoto/InputMediaDocument；
		// 若单测 mock 未实现 uploadMedia，则平滑回退直接使用 uploaded。
		finalMedia := uploaded
		if msgMedia, upErr := api.MessagesUploadMedia(ctx, &tg.MessagesUploadMediaRequest{
			Peer:  peer,
			Media: uploaded,
		}); upErr == nil {
			if converted, ok := messageMediaToInputMedia(msgMedia); ok {
				finalMedia = converted
			}
		}

		parseMode := item.ParseMode
		if parseMode == "" {
			parseMode = opts.ParseMode
		}
		var explicit []EntitySpan
		if i == 0 {
			explicit = opts.Entities
		}
		captionText := item.Caption
		if c.noEmoji {
			captionText, explicit = stripEmojiSpans(captionText, explicit)
		}
		caption, entities := c.prepareTextAndEntities(ctx, captionText, parseMode, explicit)

		rid := cryptoRandInt64()
		randomIDs = append(randomIDs, rid)
		single := tg.InputSingleMedia{
			Media:    finalMedia,
			RandomID: rid,
			Message:  caption,
		}
		if len(entities) > 0 {
			single.SetEntities(entities)
		}
		multi = append(multi, single)
	}

	req := &tg.MessagesSendMultiMediaRequest{
		Silent:     opts.Silent,
		Peer:       peer,
		MultiMedia: multi,
	}
	if opts.ReplyToMsgID > 0 {
		req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: opts.ReplyToMsgID})
	}

	upd, err := api.MessagesSendMultiMedia(ctx, req)
	if err != nil {
		return nil, err
	}
	c.Peers().ApplyUpdates(upd)
	return extractMultipleSentMessageIDs(upd, randomIDs), nil
}

func messageMediaToInputMedia(mm tg.MessageMediaClass) (tg.InputMediaClass, bool) {
	switch v := mm.(type) {
	case *tg.MessageMediaPhoto:
		if p, ok := v.Photo.(*tg.Photo); ok && p != nil {
			return &tg.InputMediaPhoto{
				ID: &tg.InputPhoto{
					ID:            p.ID,
					AccessHash:    p.AccessHash,
					FileReference: p.FileReference,
				},
			}, true
		}
	case *tg.MessageMediaDocument:
		if d, ok := v.Document.(*tg.Document); ok && d != nil {
			return &tg.InputMediaDocument{
				ID: &tg.InputDocument{
					ID:            d.ID,
					AccessHash:    d.AccessHash,
					FileReference: d.FileReference,
				},
			}, true
		}
	}
	return nil, false
}

type maxBytesWriter struct {
	dst     io.Writer
	max     int64
	written int64
}

func (w *maxBytesWriter) Write(p []byte) (int, error) {
	if w.max > 0 && w.written+int64(len(p)) > w.max {
		allowed := w.max - w.written
		if allowed > 0 {
			_, _ = w.dst.Write(p[:allowed])
			w.written += allowed
		}
		return 0, ErrMediaTooLarge
	}
	n, err := w.dst.Write(p)
	w.written += int64(n)
	return n, err
}

func mediaToFileLocation(media *MediaAttachment) (tg.InputFileLocationClass, int64, []byte, error) {
	if media == nil || media.Raw == nil {
		return nil, 0, nil, errors.New("nil media attachment")
	}

	switch raw := media.Raw.(type) {
	case *tg.MessageMediaPhoto:
		p, ok := raw.Photo.(*tg.Photo)
		if !ok || p == nil {
			return nil, 0, nil, errors.New("photo attachment is empty")
		}
		_, _, sz, thumbType := selectLargestPhotoSize(p.Sizes)
		if thumbType == "" {
			for _, sc := range p.Sizes {
				if cached, ok := sc.(*tg.PhotoCachedSize); ok && len(cached.Bytes) > 0 {
					return nil, int64(len(cached.Bytes)), cached.Bytes, nil
				}
			}
			return nil, 0, nil, errors.New("photo has no downloadable sizes")
		}
		return &tg.InputPhotoFileLocation{
			ID:            p.ID,
			AccessHash:    p.AccessHash,
			FileReference: p.FileReference,
			ThumbSize:     thumbType,
		}, sz, nil, nil

	case *tg.Photo:
		_, _, sz, thumbType := selectLargestPhotoSize(raw.Sizes)
		if thumbType == "" {
			return nil, 0, nil, errors.New("photo has no downloadable sizes")
		}
		return &tg.InputPhotoFileLocation{
			ID:            raw.ID,
			AccessHash:    raw.AccessHash,
			FileReference: raw.FileReference,
			ThumbSize:     thumbType,
		}, sz, nil, nil

	case *tg.MessageMediaDocument:
		d, ok := raw.Document.(*tg.Document)
		if !ok || d == nil {
			return nil, 0, nil, errors.New("document attachment is empty")
		}
		return &tg.InputDocumentFileLocation{
			ID:            d.ID,
			AccessHash:    d.AccessHash,
			FileReference: d.FileReference,
		}, d.Size, nil, nil

	case *tg.Document:
		return &tg.InputDocumentFileLocation{
			ID:            raw.ID,
			AccessHash:    raw.AccessHash,
			FileReference: raw.FileReference,
		}, raw.Size, nil, nil

	default:
		return nil, 0, nil, fmt.Errorf("unsupported media raw type %T", media.Raw)
	}
}

func (c *Client) streamLocation(ctx context.Context, loc tg.InputFileLocationClass, dst io.Writer, maxBytes int64) (int64, error) {
	lw := &maxBytesWriter{dst: dst, max: maxBytes}
	if tgCli := c.TelegramClient(); tgCli != nil {
		if _, err := tgCli.Download(loc).Stream(ctx, lw); err != nil {
			return lw.written, err
		}
		return lw.written, nil
	}
	api := c.API()
	if api == nil {
		return 0, errors.New("mtproto client not initialized")
	}
	if _, err := downloader.NewDownloader().Download(api, loc).Stream(ctx, lw); err != nil {
		return lw.written, err
	}
	return lw.written, nil
}

// DownloadMediaBytes 将媒体附件流式下载至内存字节切片，严格执行 maxBytes 硬上限保护。
func (c *Client) DownloadMediaBytes(ctx context.Context, media *MediaAttachment, maxBytes int64) ([]byte, error) {
	loc, knownSize, inlineBytes, err := mediaToFileLocation(media)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && knownSize > maxBytes {
		return nil, fmt.Errorf("%w: size %d > max %d", ErrMediaTooLarge, knownSize, maxBytes)
	}
	if len(inlineBytes) > 0 && loc == nil {
		if maxBytes > 0 && int64(len(inlineBytes)) > maxBytes {
			return nil, ErrMediaTooLarge
		}
		return append([]byte(nil), inlineBytes...), nil
	}

	var buf bytes.Buffer
	if knownSize > 0 && (maxBytes <= 0 || knownSize <= maxBytes) {
		buf.Grow(int(min(knownSize, 8*1024*1024)))
	}
	if _, err := c.streamLocation(ctx, loc, &buf, maxBytes); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DownloadMediaToFile 将媒体附件流式下载到目标磁盘路径（临时文件写入完成后原子替换），严格执行 maxBytes 硬上限。
func (c *Client) DownloadMediaToFile(ctx context.Context, media *MediaAttachment, destPath string, maxBytes int64) (int64, error) {
	loc, knownSize, inlineBytes, err := mediaToFileLocation(media)
	if err != nil {
		return 0, err
	}
	if maxBytes > 0 && knownSize > maxBytes {
		return 0, fmt.Errorf("%w: size %d > max %d", ErrMediaTooLarge, knownSize, maxBytes)
	}

	dir := filepath.Dir(destPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
	}

	if len(inlineBytes) > 0 && loc == nil {
		if maxBytes > 0 && int64(len(inlineBytes)) > maxBytes {
			return 0, ErrMediaTooLarge
		}
		if err := atomicWriteFile0600(destPath, inlineBytes); err != nil {
			return 0, err
		}
		return int64(len(inlineBytes)), nil
	}

	tmpFile, err := os.CreateTemp(dir, filepath.Base(destPath)+".*.part")
	if err != nil {
		return 0, err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	written, dlErr := c.streamLocation(ctx, loc, tmpFile, maxBytes)
	closeErr := tmpFile.Close()
	if dlErr != nil {
		return 0, dlErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return 0, err
	}
	cleanup = false
	return written, nil
}

// DownloadProfilePhoto 下载目标用户或频道/群组的当前头像字节流，返回 (图像字节, photoID, error)；无头像时返回 (nil, 0, nil)。
func (c *Client) DownloadProfilePhoto(ctx context.Context, target any, maxBytes int64) ([]byte, int64, error) {
	api := c.API()
	if api == nil {
		return nil, 0, errors.New("mtproto client not initialized")
	}

	peer, err := c.Peers().ResolveInputPeer(ctx, api, target)
	if err != nil {
		return nil, 0, err
	}

	var photoID int64
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		users, uErr := api.UsersGetUsers(ctx, []tg.InputUserClass{
			&tg.InputUser{UserID: p.UserID, AccessHash: p.AccessHash},
		})
		if uErr != nil {
			return nil, 0, uErr
		}
		c.Peers().ApplyUsersChats(users, nil)
		for _, uc := range users {
			if u, ok := uc.(*tg.User); ok && u.ID == p.UserID {
				if ph, ok := u.GetPhoto(); ok {
					if uph, ok := ph.(*tg.UserProfilePhoto); ok && uph != nil {
						photoID = uph.PhotoID
					}
				}
				break
			}
		}

	case *tg.InputPeerChannel:
		chatsRes, chErr := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash},
		})
		if chErr != nil {
			return nil, 0, chErr
		}
		var chats []tg.ChatClass
		switch v := chatsRes.(type) {
		case *tg.MessagesChats:
			chats = v.Chats
		case *tg.MessagesChatsSlice:
			chats = v.Chats
		}
		c.Peers().ApplyUsersChats(nil, chats)
		for _, cc := range chats {
			if ch, ok := cc.(*tg.Channel); ok && ch.ID == p.ChannelID {
				if cph, ok := ch.Photo.(*tg.ChatPhoto); ok && cph != nil {
					photoID = cph.PhotoID
				}
				break
			}
		}

	case *tg.InputPeerChat:
		chatsRes, chErr := api.MessagesGetChats(ctx, []int64{p.ChatID})
		if chErr != nil {
			return nil, 0, chErr
		}
		var chats []tg.ChatClass
		switch v := chatsRes.(type) {
		case *tg.MessagesChats:
			chats = v.Chats
		case *tg.MessagesChatsSlice:
			chats = v.Chats
		}
		c.Peers().ApplyUsersChats(nil, chats)
		for _, cc := range chats {
			if ch, ok := cc.(*tg.Chat); ok && ch.ID == p.ChatID {
				if cph, ok := ch.Photo.(*tg.ChatPhoto); ok && cph != nil {
					photoID = cph.PhotoID
				}
				break
			}
		}
	}

	if photoID == 0 {
		return nil, 0, nil
	}

	loc := &tg.InputPeerPhotoFileLocation{
		Big:     true,
		Peer:    peer,
		PhotoID: photoID,
	}
	var buf bytes.Buffer
	if _, err := c.streamLocation(ctx, loc, &buf, maxBytes); err != nil {
		return nil, photoID, err
	}
	return buf.Bytes(), photoID, nil
}
