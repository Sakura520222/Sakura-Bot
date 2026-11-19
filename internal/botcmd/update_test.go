// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package botcmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
	request := filepath.Join(root, "data", ".image_update_request")
	run := func(check bool) applicationUpdate {
		var result applicationUpdate
		router.updateApplication(context.Background(), check, "test", func(update applicationUpdate) {
			result = update
		})
		return result
	}

	if result := run(false); result.success {
		t.Fatalf("accepted update without host deployer: %+v", result)
	}
	if err := os.MkdirAll(filepath.Dir(request), 0755); err != nil {
		t.Fatal(err)
	}
	installation := filepath.Join(filepath.Dir(request), ".image_deployer_installed")
	if err := os.WriteFile(installation, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if result := run(true); result.success {
		t.Fatalf("reported a successful check without inspecting the host: %+v", result)
	}
	if _, err := os.Stat(request); !os.IsNotExist(err) {
		t.Fatalf("check wrote update request: %v", err)
	}
	if result := run(false); !result.success {
		t.Fatalf("did not submit host update: %+v", result)
	}
	if content, err := os.ReadFile(request); err != nil || string(content) != "requested\n" {
		t.Fatalf("update request = %q, %v", content, err)
	}
}

func TestUpdateRejectsConcurrentWork(t *testing.T) {
	router := NewRouter(Deps{})
	router.updateMu.Lock()
	defer router.updateMu.Unlock()
	var result applicationUpdate
	router.updateApplication(context.Background(), false, "test", func(update applicationUpdate) {
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
