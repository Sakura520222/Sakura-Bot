// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"testing"
	"time"
)

func TestMainSchemaAndAssistantLeaseCanCoexist(t *testing.T) {
	manager := testManager(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := manager.InitMainDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitMainDatabase(ctx); err != nil {
		t.Fatalf("repeated startup failed: %v", err)
	}
	main, err := manager.HoldMainBotLease(ctx, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer main()
	assistant, err := manager.HoldAssistantLease(ctx, cancel)
	if err != nil {
		t.Fatalf("assistant lease conflicted with main lease: %v", err)
	}
	defer assistant()
	if redundant, err := manager.HoldMainBotLease(ctx, cancel); err == nil {
		redundant()
		t.Fatal("a duplicate main Bot was allowed to start")
	}
}
