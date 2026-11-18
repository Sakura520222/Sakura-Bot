// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package forwarding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type fakeFwdRepo struct {
	mu      sync.Mutex
	seen    map[string]bool
	records []db.ForwardedRecord
	stats   map[string]int
}

func (r *fakeFwdRepo) IsMessageKeyForwarded(_ context.Context, src, key, dst string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[src+"|"+key+"|"+dst], nil
}

func (r *fakeFwdRepo) AddForwardedMessage(_ context.Context, rec db.ForwardedRecord) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	r.seen[rec.SourceChannel+"|"+rec.MessageKey+"|"+rec.TargetChannel] = true
	r.records = append(r.records, rec)
	return true, nil
}

func (r *fakeFwdRepo) UpdateForwardingStats(_ context.Context, ch string, inc int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stats == nil {
		r.stats = map[string]int{}
	}
	r.stats[ch] += inc
	return nil
}

type fakeFwdMessenger struct {
	mtproto.Messenger
	mu     sync.Mutex
	albums [][]mtproto.MediaUpload
	texts  []string
}

func (m *fakeFwdMessenger) ResolveChannel(_ context.Context, ref string) (*mtproto.ChannelInfo, error) {
	if strings.Contains(ref, "target_ch") {
		return &mtproto.ChannelInfo{ID: 2, MarkedID: -1002, Title: "精选目标频道", Username: "target_ch"}, nil
	}
	return &mtproto.ChannelInfo{ID: 1, MarkedID: -1001, Title: "源资讯频道", Username: "src_ch"}, nil
}

func (m *fakeFwdMessenger) DownloadMediaBytes(_ context.Context, _ *mtproto.MediaAttachment, _ int64) ([]byte, error) {
	return []byte("fake-jpeg-bytes"), nil
}

func (m *fakeFwdMessenger) SendAlbum(_ context.Context, _ any, items []mtproto.MediaUpload, _ mtproto.SendOptions) ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.albums = append(m.albums, items)
	return []int{901, 902}, nil
}

func (m *fakeFwdMessenger) SendLongMessage(_ context.Context, _ any, text string, _ mtproto.SendOptions) ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.texts = append(m.texts, text)
	return []int{801}, nil
}

func TestThreeStageFiltersAndStrategies(t *testing.T) {
	if ShouldForwardOriginalOnly(true, true) {
		t.Fatalf("forwarded message must be rejected when forward_original_only=true")
	}
	if !ShouldForwardByKeywords("Go 1.27 性能飞跃", []string{"go 1.27"}, []string{"广告"}) {
		t.Fatalf("keyword whitelist should match case-insensitively")
	}
	if ShouldForwardByKeywords("Go 1.27 广告推广", []string{"go"}, []string{"广告"}) {
		t.Fatalf("blacklist must take precedence over whitelist")
	}

	singleBig := []*mtproto.IncomingMessage{{
		ID:    1,
		Media: &mtproto.MediaAttachment{Kind: "video", FileSize: 15 << 20},
	}}
	if dec := DecideForwardStrategy(singleBig, 0, 0); dec.Strategy != StrategyDownload {
		t.Fatalf("expected download strategy for 15MB single video, got %+v", dec)
	}

	purePhotos := []*mtproto.IncomingMessage{
		{ID: 1, Media: &mtproto.MediaAttachment{Kind: "photo", FileSize: 6 << 20}},
		{ID: 2, Media: &mtproto.MediaAttachment{Kind: "photo", FileSize: 6 << 20}},
	}
	if dec := DecideForwardStrategy(purePhotos, 0, 0); dec.Strategy != StrategyMemory {
		t.Fatalf("expected memory strategy for pure photo album, got %+v", dec)
	}
}

func TestUncaptionedFirstPhotoAlbumAndFooter(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	jsonPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(envPath, []byte("QA_BOT_USERNAME=sakura_qa_bot\n"), 0o644)
	_ = os.WriteFile(jsonPath, []byte(`{
		"forwarding": {
			"enabled": true,
			"show_default_footer": true,
			"rules": [{
				"source_channel": "https://t.me/src_ch",
				"target_channel": "https://t.me/target_ch",
				"keywords": ["发布"],
				"copy_mode": true
			}]
		}
	}`), 0o644)

	cfg := config.New(envPath, jsonPath)
	if err := cfg.Init(); err != nil {
		t.Fatalf("cfg.Init: %v", err)
	}

	repo := &fakeFwdRepo{}
	cli := &fakeFwdMessenger{}
	svc := NewService(ServiceDeps{
		Config:           cfg,
		Repo:             repo,
		MonitoringClient: func() mtproto.Messenger { return cli },
		SendingClient:    func() mtproto.Messenger { return cli },
		AlbumSettleDelay: 40 * time.Millisecond,
		CacheDir:         filepath.Join(dir, "cache"),
	})

	// 第一张到达的相册图片无文字说明，第二张才带“重磅发布”——修复了 Python 版漏采相册的历史缺陷！
	svc.HandleIncomingMessage(context.Background(), &mtproto.IncomingMessage{
		ID:           10,
		ChatID:       -1001,
		ChatUsername: "src_ch",
		IsBroadcast:  true,
		GroupedID:    777888,
		Text:         "",
		Media:        &mtproto.MediaAttachment{Kind: "photo", FileSize: 1024},
	})
	svc.HandleIncomingMessage(context.Background(), &mtproto.IncomingMessage{
		ID:           11,
		ChatID:       -1001,
		ChatUsername: "src_ch",
		IsBroadcast:  true,
		GroupedID:    777888,
		Text:         "Sakura-Bot 3.0 重磅发布！",
		Media:        &mtproto.MediaAttachment{Kind: "photo", FileSize: 1024},
	})

	time.Sleep(120 * time.Millisecond)
	svc.Stop()

	cli.mu.Lock()
	defer cli.mu.Unlock()
	if len(cli.albums) != 1 || len(cli.albums[0]) != 2 {
		t.Fatalf("expected 1 forwarded album with 2 items, got %d", len(cli.albums))
	}
	caption := cli.albums[0][0].Caption
	if !strings.Contains(caption, "重磅发布") || !strings.Contains(caption, "[Source](https://t.me/src_ch/10)") || !strings.Contains(caption, "sakura_qa_bot?start=submit") {
		t.Fatalf("unexpected album caption with footer: %q", caption)
	}
	if len(repo.records) != 1 || repo.records[0].MessageKey != "group_777888" {
		t.Fatalf("expected dedup key group_777888, got %+v", repo.records)
	}
}
