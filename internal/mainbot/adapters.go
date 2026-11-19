// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package mainbot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/process"
	"github.com/Sakura520222/Sakura-Bot/internal/scheduler"
	"github.com/Sakura520222/Sakura-Bot/internal/vectors"
)

func aiConfig(cfg *config.Config, purpose string) llm.AIConfig {
	conf := cfg.EffectiveAIConfig(purpose)
	return llm.AIConfig{
		APIKey: conf.APIKey, BaseURL: conf.BaseURL, Model: conf.Model, SupportsVision: conf.SupportsVision, ReasoningEffort: conf.ReasoningEffort,
	}
}

func visionSettings(cfg *config.Config) media.VisionSettings {
	conf := cfg.EffectiveAIConfig("vision")
	return media.VisionSettings{
		VisionEnabled: conf.VisionEnabled, VisionOCR: conf.VisionOCR, VisionDescription: conf.VisionDescription,
		SupportsVision: conf.SupportsVision, APIKey: conf.APIKey, BaseURL: conf.BaseURL, Model: conf.Model,
		ReasoningEffort: conf.ReasoningEffort,
	}
}

type indexedVectors struct {
	*vectors.Store
	embed vectors.EmbedFunc
}

func (v indexedVectors) AddSummary(ctx context.Context, id int64, channelID, name, text string, start, end, created time.Time, count int) error {
	return v.Store.UpsertSummary(ctx, v.embed, id, channelID, name, text, start, end, created, count)
}

func (v indexedVectors) ClearAllCollections(ctx context.Context) error {
	_, summaryErr := v.ClearCollection(ctx, "summaries")
	_, messageErr := v.ClearCollection(ctx, "messages")
	return errors.Join(summaryErr, messageErr)
}

type schedulerControl struct{ *scheduler.Scheduler }

func (s schedulerControl) Pause()  { s.Scheduler.Pause() }
func (s schedulerControl) Resume() { s.Scheduler.Resume() }

type assistantControl struct{ *process.QAManager }

func (a assistantControl) Stop() process.QAActionResult { return a.QAManager.Stop(5 * time.Second) }

type imageSource struct{ client func() *mtproto.Client }

func (s imageSource) FetchMessageImage(ctx context.Context, channelID string, messageID int64) (*media.RemoteImageMessage, error) {
	client := s.client()
	if client == nil {
		return nil, errors.New("Telegram 客户端未连接")
	}
	msgs, err := client.GetMessages(ctx, "-100"+channelID, []int{int(messageID)})
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("Telegram 消息 %s/%d 不可用", channelID, messageID)
	}
	msg := msgs[0]
	result := &media.RemoteImageMessage{
		ChannelID: channelID, ChannelTitle: msg.ChatTitle, ChannelUsername: msg.ChatUsername,
		MessageID: messageID, Caption: msg.Text, Date: msg.Date,
	}
	if msg.Media.IsStaticImage() {
		result.MediaID = msg.Media.MediaID
		result.FileSize = msg.Media.FileSize
		result.RawMedia = msg.Media.Raw
	}
	return result, nil
}

func (s imageSource) DownloadRawMedia(ctx context.Context, rawMedia any, maxBytes int64) ([]byte, error) {
	client := s.client()
	if client == nil {
		return nil, errors.New("Telegram 客户端未连接")
	}
	return client.DownloadMediaBytes(ctx, &mtproto.MediaAttachment{Raw: rawMedia}, maxBytes)
}
