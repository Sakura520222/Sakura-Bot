// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/internal/poll"
	"github.com/Sakura520222/Sakura-Bot/internal/summary"
	"github.com/Sakura520222/Sakura-Bot/internal/vectors"
)

func TestSchedulesAndInteractionPreserveLegacyContracts(t *testing.T) {
	cfg := webTestConfig(t)
	const channel = "https://t.me/testfixture"
	if err := cfg.SaveConfigMap(map[string]any{
		"channels": []string{channel},
		"channel_poll_settings": map[string]any{
			channel: map[string]any{"enabled": false, "public_voters": true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	manager := summary.NewTimeManager(filepath.Join(t.TempDir(), "summary_time.json"))
	regenerations := poll.NewRegenStore(filepath.Join(t.TempDir(), "poll_regenerations.json"))
	srv := New(Deps{Config: cfg, TimeManager: manager, PollRegenerations: regenerations, Logger: webTestLogger()})
	token := webTestAdmin(t, srv)
	pathChannel := url.PathEscape(channel)
	webTestResponse(t, srv, http.MethodPut, "/api/schedules/"+pathChannel,
		`{"frequency":"weekly","hour":12,"minute":30,"days":["FRI"]}`, token, http.StatusOK)
	schedule := cfg.ChannelSchedule(channel)
	if schedule.Hour != 12 || schedule.Minute != 30 || len(schedule.Days) != 1 || schedule.Days[0] != "fri" {
		t.Fatalf("schedule not persisted: %+v", schedule)
	}
	webTestResponse(t, srv, http.MethodPut, "/api/schedules/summary-times/"+pathChannel,
		`{"time":"2026-10-01T12:00:00+08:00","poll_message_ids":[10]}`, token, http.StatusOK)
	state, found, err := manager.LoadChannel(channel)
	if err != nil || !found || state.Time.UTC().Hour() != 4 || len(state.PollMessageIDs) != 1 || state.PollMessageIDs[0] != 10 {
		t.Fatalf("summary cutoff not persisted: %+v, found %v, err %v", state, found, err)
	}
	webTestResponse(t, srv, http.MethodPut, "/api/interaction/poll-settings/"+pathChannel,
		`{"public_voters":null}`, token, http.StatusOK)
	prefs := cfg.ChannelPollConfig(channel)
	if prefs.Enabled == nil || *prefs.Enabled || prefs.PublicVoters != nil {
		t.Fatalf("null patch did not clear the override: %+v", prefs)
	}
}

func TestVectorDocumentListAndDetailShareSQLite(t *testing.T) {
	store := vectors.NewReadWrite(t.TempDir(), 2)
	defer store.Close()
	_, err := store.UpsertMany(t.Context(), "messages", []vectors.WriteRow{{
		ID: "12345:10", Vector: []float32{1, 0}, Document: "测试内容",
		Metadata: map[string]any{"message_id": 10},
	}})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{Config: webTestConfig(t), Vector: store, Logger: webTestLogger()})
	token := webTestAdmin(t, srv)
	path := "/api/vector-store/collections/messages/" + url.PathEscape("12345:10")
	w := webTestResponse(t, srv, http.MethodGet, path, "", token, http.StatusOK)
	var result struct {
		Data vectorDocumentPayload `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Dimension == nil || *result.Data.Dimension != 2 || result.Data.Document != "测试内容" {
		t.Fatalf("unexpected document detail: %+v", result.Data)
	}
	webTestResponse(t, srv, http.MethodDelete, path, "", token, http.StatusOK)
	webTestResponse(t, srv, http.MethodGet, path, "", token, http.StatusNotFound)
}
