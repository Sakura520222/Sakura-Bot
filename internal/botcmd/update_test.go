// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package botcmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/internal/process"
)

func TestDockerUpdateUsesDeployerMarkerAndReportsCheckUnavailable(t *testing.T) {
	t.Setenv("SAKURA_DOCKER", "1")
	root := t.TempDir()
	router := NewRouter(Deps{
		WorkDir: root,
		TriggerReleaseRestart: func(_, _, _ string) {
			t.Fatal("Docker update tried to restart the local binary")
		},
	})
	dataDir := filepath.Join(root, "data")
	request := filepath.Join(dataDir, ".image_update_request")
	run := func(check bool, chatID int64) applicationUpdate {
		var result applicationUpdate
		router.updateApplication(context.Background(), check, "test", chatID, func(update applicationUpdate) {
			result = update
		})
		return result
	}

	if result := run(false, 0); result.success {
		t.Fatalf("accepted update without host deployer: %+v", result)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	installation := filepath.Join(dataDir, ".image_deployer_installed")
	if err := os.WriteFile(installation, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if result := run(true, 0); result.success {
		t.Fatalf("reported a successful check without inspecting the host: %+v", result)
	}
	if _, err := os.Stat(request); !os.IsNotExist(err) {
		t.Fatalf("check wrote update request: %v", err)
	}
	if _, err := process.ReadImageUpdatePending(dataDir); err != nil {
		t.Fatalf("check wrote pending report record: %v", err)
	}
	if result := run(false, 4242); !result.success {
		t.Fatalf("did not submit host update: %+v", result)
	}
	if content, err := os.ReadFile(request); err != nil || string(content) != "requested\n" {
		t.Fatalf("update request = %q, %v", content, err)
	}
	pending, err := process.ReadImageUpdatePending(dataDir)
	if err != nil || pending == nil || pending.ChatID != 4242 {
		t.Fatalf("pending report record = %+v, %v", pending, err)
	}
}

func TestDockerUpdateSubmitMessagePromisesInChatReport(t *testing.T) {
	t.Setenv("SAKURA_DOCKER", "1")
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, ".image_deployer_installed"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Deps{WorkDir: root})
	var result applicationUpdate
	router.updateApplication(context.Background(), false, "test", 1, func(update applicationUpdate) {
		result = update
	})
	if !result.success || !strings.Contains(result.message, "汇报结果") {
		t.Fatalf("submit reply should promise an in-chat report: %+v", result)
	}
}

func TestUpdateRejectsConcurrentWork(t *testing.T) {
	router := NewRouter(Deps{})
	router.updateMu.Lock()
	defer router.updateMu.Unlock()
	var result applicationUpdate
	router.updateApplication(context.Background(), false, "test", 0, func(update applicationUpdate) {
		result = update
	})
	if result.success || result.message == "" {
		t.Fatalf("unexpected busy response: %+v", result)
	}
}

func TestFailedLocalUpdateDiscardsStagingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("invalid VERSION"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stageLocalRelease(context.Background(), root); err == nil {
		t.Fatal("expected invalid build version")
	}
	entries, err := os.ReadDir(filepath.Join(root, "bin", ".updates"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("leftover staging directories: %v, %v", entries, err)
	}
}
