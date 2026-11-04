package handlers

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/media"
	"github.com/go-telegram/bot/models"
)

type albumKey struct {
	scope media.Scope
	group string
}
type imageAlbum struct {
	timer    *time.Timer
	query    string
	update   *models.Update
	captions map[int]string
}
type imageAlbums struct {
	mu      sync.Mutex
	pending map[albumKey]*imageAlbum
}

func (a *imageAlbums) clearUser(id int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, album := range a.pending {
		if key.scope.UserID == id {
			album.timer.Stop()
			delete(a.pending, key)
		}
	}
}
func (a *imageAlbums) clearScope(scope media.Scope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, album := range a.pending {
		if key.scope == scope {
			album.timer.Stop()
			delete(a.pending, key)
		}
	}
}
func (c *Commands) scheduleImageAlbum(ctx context.Context, upd *models.Update, query string) {
	msg := upd.Message
	key := albumKey{media.Scope{UserID: msg.From.ID, ChatID: msg.Chat.ID}, msg.MediaGroupID}
	c.albums.mu.Lock()
	defer c.albums.mu.Unlock()
	if c.albums.pending == nil {
		c.albums.pending = make(map[albumKey]*imageAlbum)
	}
	captions := make(map[int]string)
	if previous := c.albums.pending[key]; previous != nil {
		previous.timer.Stop()
		for id, caption := range previous.captions {
			captions[id] = caption
		}
	}
	if strings.TrimSpace(query) != "" {
		captions[msg.ID] = strings.TrimSpace(query)
	}
	ids := make([]int, 0, len(captions))
	for id := range captions {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, captions[id])
	}
	album := &imageAlbum{query: strings.Join(parts, "\n"), update: upd, captions: captions}
	c.albums.pending[key] = album
	album.timer = time.AfterFunc(900*time.Millisecond, func() {
		c.albums.mu.Lock()
		if c.albums.pending[key] != album {
			c.albums.mu.Unlock()
			return
		}
		delete(c.albums.pending, key)
		c.albums.mu.Unlock()
		if ctx.Err() != nil || strings.TrimSpace(album.query) == "" {
			return
		}
		c.answerMessage(ctx, album.update, album.query)
	})
}
func (c *Commands) imageRequestContext(ctx context.Context, msg *models.Message, userID int64) context.Context {
	if c.aiConfig != nil {
		ctx = llm.WithConfig(ctx, c.aiConfig())
	}
	return media.WithRequest(ctx, &media.Request{
		Store:         c.images,
		Scope:         media.Scope{UserID: userID, ChatID: msg.Chat.ID},
		Reply:         msg.ReplyToMessage != nil,
		RequireImages: len(msg.Photo) > 0 || msg.Document != nil,
		Download: func(ctx context.Context, ref media.Ref) (string, error) {
			if c.files == nil {
				return "", media.InputError("图片下载服务不可用，请稍后重试。")
			}
			return media.Download(ctx, c.files, ref)
		},
	})
}
