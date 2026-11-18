// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

func (s *Server) registerImageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/images/stats", s.imageStats)
	mux.HandleFunc("GET /api/images/{channel_id}/{message_id}", s.imageStatus)
	mux.HandleFunc("POST /api/images/{channel_id}/{message_id}/retry", s.imageRetry)
	mux.HandleFunc("POST /api/images/backfill", s.imageBackfill)
}

func (s *Server) imagesActive(w http.ResponseWriter) bool {
	if s.deps.DB != nil && s.deps.Images != nil && s.deps.Images.IsRunning() {
		return true
	}
	writeHTTPError(w, http.StatusServiceUnavailable, i18n.T("images.unavailable", nil))
	return false
}

func (s *Server) imageStats(w http.ResponseWriter, r *http.Request) {
	if !s.imagesActive(w) {
		return
	}
	stats, err := s.deps.DB.GetImageJobStats(r.Context())
	if err != nil {
		s.imageFailure(w, err)
		return
	}
	writeEnvelopeOK(w, stats, "")
}

func imageRouteMessage(w http.ResponseWriter, r *http.Request) (string, int64, bool) {
	channel := media.ChannelKey(r.PathValue("channel_id"))
	messageID, err := strconv.ParseInt(r.PathValue("message_id"), 10, 64)
	if err != nil || channel == "" || messageID <= 0 {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的 Telegram 消息")
		return "", 0, false
	}
	return channel, messageID, true
}

func (s *Server) imageFailure(w http.ResponseWriter, err error) {
	s.logger.Error("处理图片任务失败", "error", err)
	writeHTTPError(w, http.StatusInternalServerError, "处理图片任务失败")
}

func (s *Server) imageStatus(w http.ResponseWriter, r *http.Request) {
	ch, id, ok := imageRouteMessage(w, r)
	if !ok || !s.imagesActive(w) {
		return
	}
	job, err := s.deps.DB.GetMessageImage(r.Context(), ch, id)
	switch {
	case err != nil:
		s.imageFailure(w, err)
	case job == nil:
		writeHTTPError(w, http.StatusNotFound, i18n.T("images.not_found", nil))
	default:
		writeEnvelopeOK(w, job, "")
	}
}

func (s *Server) imageRetry(w http.ResponseWriter, r *http.Request) {
	ch, id, ok := imageRouteMessage(w, r)
	if !ok || !s.imagesActive(w) {
		return
	}
	var body struct {
		Force bool `json:"force"`
	}
	if err := readJSONBody(r, &body, 8192); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的重试请求")
		return
	}
	retried, err := s.deps.DB.RetryMessageImage(r.Context(), ch, id, body.Force)
	switch {
	case err != nil:
		s.imageFailure(w, err)
	case !retried:
		writeHTTPError(w, http.StatusNotFound, i18n.T("images.not_found", nil))
	default:
		writeEnvelopeOK(w, nil, "")
	}
}

func (s *Server) imageBackfill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel string    `json:"channel"`
		Start   time.Time `json:"start"`
		End     time.Time `json:"end"`
		Limit   int       `json:"limit"`
	}
	body.Limit = 1000
	if err := readJSONBody(r, &body, 8192); err != nil || body.Limit < 1 || body.Limit > 100000 {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的图片补录请求")
		return
	}
	channels := make(map[string]bool)
	for _, ch := range s.deps.Config.Channels() {
		channels[ch] = true
	}
	channel, ok := config.FindEquivalentChannelKey(channels, body.Channel)
	if !ok {
		writeHTTPError(w, http.StatusBadRequest, i18n.T("images.channel_invalid", nil))
		return
	}
	if body.Start.IsZero() || body.End.IsZero() || !body.Start.Before(body.End) {
		writeHTTPError(w, http.StatusBadRequest, i18n.T("images.range_invalid", nil))
		return
	}
	if !s.imagesActive(w) {
		return
	}
	if s.deps.HistoryClient == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "Telegram 历史读取不可用")
		return
	}
	client, ok := s.deps.HistoryClient().(*mtproto.Client)
	if !ok || client == nil || !client.IsConnected() {
		writeHTTPError(w, http.StatusServiceUnavailable, "Telegram 历史读取不可用")
		return
	}
	scheduled := 0
	err := client.WalkChannelHistoryRange(r.Context(), channel, body.Start, body.End, body.Limit, func(msg *mtproto.IncomingMessage) error {
		if msg == nil {
			return errors.New("无法解析 Telegram 历史消息")
		}
		if !msg.Media.IsStaticImage() {
			return nil
		}
		var grouped string
		if msg.GroupedID != 0 {
			grouped = strconv.FormatInt(msg.GroupedID, 10)
		}
		enqueued, err := s.deps.Images.Capture(r.Context(), media.RemoteImageMessage{
			ChannelID: media.ChannelKey(strconv.FormatInt(msg.ChatID, 10)), ChannelTitle: msg.ChatTitle,
			ChannelUsername: msg.ChatUsername, MessageID: int64(msg.ID), GroupedID: grouped,
			MediaID: msg.Media.MediaID, FileSize: msg.Media.FileSize, Caption: msg.Text, Date: msg.Date, RawMedia: msg.Media.Raw,
		})
		if enqueued {
			scheduled++
		}
		return err
	})
	if err != nil {
		s.imageFailure(w, err)
		return
	}
	writeEnvelopeOK(w, map[string]int{"scheduled": scheduled, "limit": body.Limit}, "")
}
