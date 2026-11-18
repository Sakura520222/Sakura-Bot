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
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode/utf8"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// ValidateImageBytes 校验静态 PNG/JPEG 字节并返回标准 MIME 类型（对齐 core/media/image_validation.py）。
func ValidateImageBytes(data []byte, maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = MaxImageBytes
	}
	if len(data) == 0 || len(data) > maxBytes {
		return "", InputError("单张图片不能超过限制大小，且不能为空。")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return "", InputError("目前只支持 PNG / JPEG 图片。")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels || cfg.Width > MaxSide || cfg.Height > MaxSide {
		return "", InputError("图片像素过大或为动画，请发送静态 PNG / JPEG。")
	}
	if format == "png" {
		for offset := 8; offset+12 <= len(data); {
			size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			if size < 0 || size > len(data)-offset-12 {
				break
			}
			if string(data[offset+4:offset+8]) == "acTL" {
				return "", InputError("图片像素过大或为动画，请发送静态 PNG / JPEG。")
			}
			offset += size + 12
		}
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return "", InputError("图片已损坏或格式不受支持，请重新发送 PNG / JPEG。")
	}
	if format == "png" {
		return "image/png", nil
	}
	return "image/jpeg", nil
}

// DecodeImageDataURL 解析 data:image/{png,jpeg};base64,... 字符串为 (mime, rawBytes)。
func DecodeImageDataURL(dataURL string) (string, []byte, error) {
	header, encoded, ok := strings.Cut(dataURL, ",")
	if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64") {
		return "", nil, fmt.Errorf("invalid_image_data_url")
	}
	mime := strings.SplitN(header[5:], ";", 2)[0]
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("invalid_image_data_url: %w", err)
	}
	return mime, raw, nil
}

// ScanQR 解码图片中的二维码文本（对齐 core/moderation/observation_media.py:scan_qr）。
// 限制最大 1200 万像素，超出 1800x1800 时先等比采样缩放到 1800 以内，最多返回 2048 个字符。
func ScanQR(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return "", fmt.Errorf("image_pixels_limit")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	img = downscaleMaxSide(img, 1800)

	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", nil
	}
	reader := qrcode.NewQRCodeReader()
	hints := map[gozxing.DecodeHintType]any{
		gozxing.DecodeHintType_TRY_HARDER: true,
	}
	res, err := reader.Decode(bmp, hints)
	if err != nil || res == nil {
		return "", nil
	}
	txt := strings.TrimSpace(res.GetText())
	if utf8.RuneCountInString(txt) > 2048 {
		runes := []rune(txt)
		txt = string(runes[:2048])
	}
	return txt, nil
}

func downscaleMaxSide(src image.Image, maxSide int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxSide && h <= maxSide {
		return src
	}
	newW, newH := w, h
	if w >= h {
		newW = maxSide
		newH = max(1, h*maxSide/w)
	} else {
		newH = maxSide
		newW = max(1, w*maxSide/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	// 最近邻快速降采样，二维码块边缘清晰且零额外依赖。
	for y := 0; y < newH; y++ {
		sy := b.Min.Y + y*h/newH
		for x := 0; x < newW; x++ {
			sx := b.Min.X + x*w/newW
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	_ = draw.Src
	return dst
}
