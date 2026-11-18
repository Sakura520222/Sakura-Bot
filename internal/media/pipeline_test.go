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
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

func makeTinyPNG(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode failed: %v", err)
	}
	return buf.Bytes()
}

func TestValidateImageAndScanQR(t *testing.T) {
	writer := qrcode.NewQRCodeWriter()
	matrix, err := writer.Encode("https://t.me/sakura_test_qr", gozxing.BarcodeFormat_QR_CODE, 256, 256, nil)
	if err != nil {
		t.Fatalf("QR encode failed: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, matrix.GetWidth(), matrix.GetHeight()))
	for y := 0; y < matrix.GetHeight(); y++ {
		for x := 0; x < matrix.GetWidth(); x++ {
			if matrix.Get(x, y) {
				img.Set(x, y, color.Black)
			} else {
				img.Set(x, y, color.White)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode failed: %v", err)
	}
	raw := buf.Bytes()

	mime, err := ValidateImageBytes(raw, MaxImageBytes)
	if err != nil || mime != "image/png" {
		t.Fatalf("ValidateImageBytes = (%q, %v), want image/png", mime, err)
	}

	gotQR, err := ScanQR(raw)
	if err != nil {
		t.Fatalf("ScanQR error: %v", err)
	}
	if gotQR != "https://t.me/sakura_test_qr" {
		t.Fatalf("ScanQR = %q, want https://t.me/sakura_test_qr", gotQR)
	}
}

func TestCombinedAndDescriptionText(t *testing.T) {
	got := CombinedText("原始说明", "OCR文本", "画面描述")
	want := "原始说明\n\n[Image OCR]\nOCR文本\n\n[Image description]\n画面描述"
	if got != want {
		t.Fatalf("CombinedText = %q, want %q", got, want)
	}

	ocr := "标题字"
	desc := "一张风景照"
	dt, err := DescriptionText(&db.ImageAsset{OCR: &ocr, Description: &desc})
	if err != nil {
		t.Fatalf("DescriptionText error: %v", err)
	}
	if dt != "图片描述：一张风景照\n图中文字：标题字" {
		t.Fatalf("unexpected DescriptionText: %q", dt)
	}
}

type fakePollMediaClient struct {
	items map[int]PollMediaItem
	bytes map[int][]byte
}

func (f *fakePollMediaClient) GetPollMessages(_ context.Context, _ int64, ids []int) ([]PollMediaItem, error) {
	out := make([]PollMediaItem, 0, len(ids))
	for _, id := range ids {
		if item, ok := f.items[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakePollMediaClient) FetchOriginalPostImage(_ context.Context, _ string, _ int) (*RemoteImageMessage, error) {
	return nil, nil
}

func (f *fakePollMediaClient) DownloadPollImageBytes(_ context.Context, item PollMediaItem, _ int64) ([]byte, error) {
	return f.bytes[item.ID], nil
}

func TestCollectPollImagesAlbumDedupAndOrder(t *testing.T) {
	png1 := makeTinyPNG(t, color.RGBA{R: 255, A: 255})
	png2 := makeTinyPNG(t, color.RGBA{B: 255, A: 255})
	cli := &fakePollMediaClient{
		items: map[int]PollMediaItem{
			101: {ID: 101, GroupedID: 9001, Text: "相册主文案", IsStaticImage: true, MediaID: "photo:1"},
			102: {ID: 102, GroupedID: 9001, Text: "相册主文案", IsStaticImage: true, MediaID: "photo:2"},
			103: {ID: 103, GroupedID: 9001, Text: "补充说明", IsStaticImage: false},
			104: {ID: 104, GroupedID: 9999, Text: "其他相册", IsStaticImage: true, MediaID: "photo:9"},
		},
		bytes: map[int][]byte{
			101: png1,
			102: png2,
		},
	}

	trigger := cli.items[102]
	caption, urls, err := CollectPollImages(context.Background(), cli, &trigger, -100777, "", CollectPollOptions{
		SettleDelay: -1,
	})
	if err != nil {
		t.Fatalf("CollectPollImages failed: %v", err)
	}
	if caption != "相册主文案\n\n补充说明" {
		t.Fatalf("unexpected caption: %q", caption)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 data URLs, got %d", len(urls))
	}
	for i, u := range urls {
		mime, decoded, perr := ParseImageDataURL(u)
		if perr != nil || mime != "image/png" || len(decoded) == 0 {
			t.Fatalf("invalid data URL[%d]: mime=%q err=%v", i, mime, perr)
		}
	}
}
