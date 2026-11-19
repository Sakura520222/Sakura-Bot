package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type FileClient interface {
	GetFile(context.Context, *bot.GetFileParams) (*models.File, error)
	FileDownloadLink(*models.File) string
}

func TelegramRef(msg *models.Message) (*Ref, error) {
	fileID := ""
	size := int64(0)
	if len(msg.Photo) > 0 {
		p := msg.Photo[0]
		for _, candidate := range msg.Photo {
			if candidate.Width*candidate.Height > p.Width*p.Height {
				p = candidate
			}
		}
		if int64(p.Width)*int64(p.Height) > MaxPixels || p.Width > MaxSide || p.Height > MaxSide {
			return nil, InputError("图片像素过大，最多 1200 万像素、单边 8192 像素。")
		}
		fileID, size = p.FileID, int64(p.FileSize)
	} else if d := msg.Document; d != nil && (strings.HasPrefix(d.MimeType, "image/") || strings.HasSuffix(strings.ToLower(d.FileName), ".png") || strings.HasSuffix(strings.ToLower(d.FileName), ".jpg") || strings.HasSuffix(strings.ToLower(d.FileName), ".jpeg")) {
		if d.MimeType != "image/png" && d.MimeType != "image/jpeg" && d.MimeType != "application/octet-stream" && d.MimeType != "" {
			return nil, InputError("目前只支持 PNG / JPEG 图片。")
		}
		fileID, size = d.FileID, d.FileSize
	}
	if fileID == "" {
		return nil, nil
	}
	if size > MaxImageBytes {
		return nil, InputError("单张图片不能超过 8 MiB。")
	}
	return &Ref{FileID: fileID, MessageID: msg.ID}, nil
}

func ImageDataURL(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return "", InputError("单张图片不能超过 8 MiB，且不能为空。")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return "", InputError("图片已损坏或格式不受支持，请重新发送 PNG / JPEG。")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels || cfg.Width > MaxSide || cfg.Height > MaxSide {
		return "", InputError("图片像素过大，请缩小后重新发送。")
	}
	if format == "png" {
		for offset := 8; offset+12 <= len(data); {
			size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			if size > len(data)-offset-12 {
				break
			}
			if string(data[offset+4:offset+8]) == "acTL" {
				return "", InputError("请发送静态 PNG / JPEG 图片。")
			}
			offset += size + 12
		}
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return "", InputError("图片已损坏，请重新发送。")
	}
	return "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// Download uses only the official Bot API file endpoint; no user URL is fetched.
func Download(ctx context.Context, client FileClient, ref Ref) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	remote, err := client.GetFile(ctx, &bot.GetFileParams{FileID: ref.FileID})
	if err != nil || remote == nil {
		return "", InputError("下载 Telegram 图片失败或超时，请重新发送后重试。")
	}
	if remote.FileSize > MaxImageBytes {
		return "", InputError("单张图片不能超过 8 MiB。")
	}
	link := client.FileDownloadLink(remote)
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Hostname() != "api.telegram.org" || u.User != nil || (u.Port() != "" && u.Port() != "443") || !strings.HasPrefix(u.Path, "/file/bot") {
		return "", InputError("Telegram 图片文件地址无效，请重新发送。")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", InputError("Telegram 图片文件地址无效，请重新发送。")
	}
	httpClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", InputError("下载 Telegram 图片失败或超时，请重新发送后重试。")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", InputError("Telegram 图片文件不存在或下载失败，请重新发送。")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxImageBytes+1))
	if err != nil {
		return "", InputError("下载 Telegram 图片失败或超时，请重新发送后重试。")
	}
	return ImageDataURL(data)
}
