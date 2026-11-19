// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/summary"
)

func (s *Server) registerScheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/schedules", s.listSchedules)
	mux.HandleFunc("GET /api/schedules/{channel...}", s.getSchedule)
	mux.HandleFunc("PUT /api/schedules/{channel...}", s.updateSchedule)
	mux.HandleFunc("DELETE /api/schedules/{channel...}", s.deleteSchedule)
	mux.HandleFunc("GET /api/schedules/summary-times", s.listSummaryTimes)
	mux.HandleFunc("PUT /api/schedules/summary-times/{channel...}", s.updateSummaryTime)
	mux.HandleFunc("DELETE /api/schedules/summary-times/{channel...}", s.deleteSummaryTime)
	mux.HandleFunc("DELETE /api/schedules/summary-times", s.clearSummaryTimes)
	mux.HandleFunc("DELETE /api/schedules/poll-regenerations", s.clearPollRegenerations)
}

type scheduleItem struct {
	Channel   string   `json:"channel"`
	Frequency string   `json:"frequency"`
	Hour      int      `json:"hour"`
	Minute    int      `json:"minute"`
	Days      []string `json:"days"`
}

func newScheduleItem(channel string, v config.SummarySchedule) scheduleItem {
	return scheduleItem{channel, v.Frequency, v.Hour, v.Minute, append([]string{}, v.Days...)}
}

func (s *Server) listSchedules(w http.ResponseWriter, _ *http.Request) {
	cfg := s.deps.Config
	schedules := cfg.SummarySchedules()
	items := make([]scheduleItem, 0, len(schedules))
	for ch, value := range schedules {
		items = append(items, newScheduleItem(ch, value))
	}
	slices.SortFunc(items, func(a, b scheduleItem) int { return strings.Compare(a.Channel, b.Channel) })
	unscheduled := []string{}
	for _, ch := range cfg.Channels() {
		if _, ok := config.FindEquivalentChannelKey(schedules, ch); !ok {
			unscheduled = append(unscheduled, ch)
		}
	}
	writeEnvelopeOK(w, map[string]any{"schedules": items, "unscheduled_channels": unscheduled, "total": len(items)}, "")
}

func (s *Server) getSchedule(w http.ResponseWriter, r *http.Request) {
	channel := config.NormalizeChannelID(r.PathValue("channel"))
	schedules := s.deps.Config.SummarySchedules()
	key, ok := config.FindEquivalentChannelKey(schedules, channel)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": nil, "message": "该频道未配置定时任务"})
		return
	}
	writeEnvelopeOK(w, newScheduleItem(channel, schedules[key]), "")
}

var errUnmonitoredChannel = errors.New("频道不在监控列表中")
var errMissingSchedule = errors.New("该频道未配置定时任务")

func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	body := config.SummarySchedule{Hour: 9, Days: []string{"mon"}}
	if err := readJSONBody(r, &body, 8192); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的定时任务配置")
		return
	}
	if body.Frequency == "daily" {
		body.Days = []string{}
	}
	for i, value := range body.Days {
		body.Days[i] = strings.ToLower(value)
	}
	if err := body.Validate(); err != nil {
		writeEnvelopeFail(w, err.Error(), nil)
		return
	}
	channel := config.NormalizeChannelID(r.PathValue("channel"))
	cfg := s.deps.Config
	err := cfg.UpdateConfig(func(doc map[string]any) error {
		if !slices.ContainsFunc(cfg.Channels(), func(ch string) bool {
			_, ok := config.FindEquivalentChannelKey(map[string]bool{ch: true}, channel)
			return ok
		}) {
			return errUnmonitoredChannel
		}
		schedules := settingsMap(doc, "summary_schedules")
		if key, ok := config.FindEquivalentChannelKey(schedules, channel); ok {
			channel = key
		}
		schedules[channel] = map[string]any{
			"frequency": body.Frequency, "hour": body.Hour, "minute": body.Minute, "days": body.Days,
		}
		return nil
	})
	switch {
	case errors.Is(err, errUnmonitoredChannel):
		writeEnvelopeFail(w, errUnmonitoredChannel.Error()+": "+channel, nil)
	case err != nil:
		writeHTTPError(w, http.StatusInternalServerError, "保存定时任务失败")
	default:
		writeEnvelopeOK(w, nil, "定时任务已设置: "+channel)
	}
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	channel := config.NormalizeChannelID(r.PathValue("channel"))
	err := s.deps.Config.UpdateConfig(func(doc map[string]any) error {
		schedules := settingsMap(doc, "summary_schedules")
		key, ok := config.FindEquivalentChannelKey(schedules, channel)
		if !ok {
			return errMissingSchedule
		}
		delete(schedules, key)
		return nil
	})
	switch {
	case errors.Is(err, errMissingSchedule):
		writeEnvelopeFail(w, errMissingSchedule.Error()+": "+channel, nil)
	case err != nil:
		writeHTTPError(w, http.StatusInternalServerError, "清除定时任务失败")
	default:
		writeEnvelopeOK(w, nil, "定时任务已清除: "+channel)
	}
}

func (s *Server) summaryTimeManager(w http.ResponseWriter) *summary.TimeManager {
	if s.deps.TimeManager == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "总结时间管理器不可用")
	}
	return s.deps.TimeManager
}

func (s *Server) listSummaryTimes(w http.ResponseWriter, _ *http.Request) {
	m := s.summaryTimeManager(w)
	if m == nil {
		return
	}
	values, err := m.LoadAll()
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "读取上次总结时间失败")
		return
	}
	type entry struct {
		Channel string `json:"channel"`
		summary.ChannelSummaryState
	}
	items := make([]entry, 0, len(values))
	for channel, state := range values {
		state.SummaryMessageIDs = append([]int{}, state.SummaryMessageIDs...)
		state.PollMessageIDs = append([]int{}, state.PollMessageIDs...)
		state.ButtonMessageIDs = append([]int{}, state.ButtonMessageIDs...)
		items = append(items, entry{channel, state})
	}
	slices.SortFunc(items, func(a, b entry) int { return strings.Compare(a.Channel, b.Channel) })
	_, err = os.Stat(m.Path())
	writeEnvelopeOK(w, map[string]any{
		"items": items, "total": len(items), "file_exists": err == nil, "file_path": filepath.Base(m.Path()),
	}, "")
}

func cleanSummaryChannel(channel string) string {
	channel = strings.TrimSpace(channel)
	if at := strings.LastIndex(channel, " ("); at >= 0 && strings.HasSuffix(channel, ")") {
		channel = channel[:at]
	}
	return config.NormalizeChannelID(channel)
}

func (s *Server) updateSummaryTime(w http.ResponseWriter, r *http.Request) {
	m := s.summaryTimeManager(w)
	if m == nil {
		return
	}
	var body struct {
		Time string `json:"time"`
		summary.ChannelSummaryState
	}
	if err := readJSONBody(r, &body, 64*1024); err != nil {
		writeHTTPError(w, http.StatusUnprocessableEntity, "无效的总结时间")
		return
	}
	t, err := time.Parse(time.RFC3339Nano, body.Time)
	if err != nil {
		t, err = time.ParseInLocation("2006-01-02T15:04:05", body.Time, time.UTC)
	}
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, "时间格式无效，请使用 ISO 8601 格式")
		return
	}
	channel := cleanSummaryChannel(r.PathValue("channel"))
	body.ChannelSummaryState.Time = t
	if err := m.SaveChannel(channel, body.ChannelSummaryState); err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "更新上次总结时间失败")
		return
	}
	writeEnvelopeOK(w, nil, "上次总结时间已更新: "+channel)
}

func (s *Server) deleteSummaryTime(w http.ResponseWriter, r *http.Request) {
	m := s.summaryTimeManager(w)
	if m == nil {
		return
	}
	channel := cleanSummaryChannel(r.PathValue("channel"))
	removed, err := m.DeleteChannel(channel)
	switch {
	case err != nil:
		writeHTTPError(w, http.StatusConflict, "无法删除上次总结时间记录")
	case !removed:
		writeEnvelopeFail(w, "该频道无上次总结时间记录: "+channel, nil)
	default:
		writeEnvelopeOK(w, nil, "上次总结时间已删除: "+channel)
	}
}

func (s *Server) clearSummaryTimes(w http.ResponseWriter, r *http.Request) {
	m := s.summaryTimeManager(w)
	if m == nil {
		return
	}
	removed, err := m.RemoveFile()
	writeRemovedFile(w, removed, err, "上次总结时间记录")
}

func (s *Server) clearPollRegenerations(w http.ResponseWriter, r *http.Request) {
	if s.deps.PollRegenerations == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "投票记录管理器不可用")
		return
	}
	removed, err := s.deps.PollRegenerations.RemoveFile()
	writeRemovedFile(w, removed, err, "投票重新生成记录文件")
}

func writeRemovedFile(w http.ResponseWriter, removed bool, err error, label string) {
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "删除"+label+"失败")
		return
	}
	message := label + "不存在"
	if removed {
		message = "已删除" + label
	}
	writeEnvelopeOK(w, map[string]bool{"removed": removed}, message)
}

func settingsMap(doc map[string]any, key string) map[string]any {
	out, ok := doc[key].(map[string]any)
	if !ok {
		out = map[string]any{}
		doc[key] = out
	}
	return out
}
