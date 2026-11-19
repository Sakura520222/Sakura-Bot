// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
)

func TestAIProbeDestinationPolicy(t *testing.T) {
	server := &Server{}
	internalURL := "http://host.docker.internal:3000/v1"
	rec := httptest.NewRecorder()
	if client := server.aiProbeHTTP(rec, config.AIConnection{BaseURL: internalURL}); client != nil || rec.Code != http.StatusBadRequest {
		t.Fatalf("unsaved local provider decision: client=%t status=%d", client != nil, rec.Code)
	}
	savedClient := server.aiProbeHTTP(httptest.NewRecorder(), config.AIConnection{BaseURL: internalURL, SavedOrigin: true})
	if savedClient == nil {
		t.Fatal("saved local provider should remain usable")
	}
	initial := httptest.NewRequest(http.MethodGet, internalURL+"/models", nil)
	sameOrigin := httptest.NewRequest(http.MethodGet, internalURL+"/models/", nil)
	crossOrigin := httptest.NewRequest(http.MethodGet, "https://example.com/models", nil)
	if err := savedClient.CheckRedirect(sameOrigin, []*http.Request{initial}); err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	if err := savedClient.CheckRedirect(crossOrigin, []*http.Request{initial}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("cross-origin redirect should stop: %v", err)
	}
}
