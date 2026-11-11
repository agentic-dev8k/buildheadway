package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeclinedSetupBlocksPrivateDownloads(t *testing.T) {
	paths := testPaths(t)
	if err := AtomicJSON(filepath.Join(paths.Data, "setup-policy.json"), map[string]any{"version": 1, "decline_install": true}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(paths.Data)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := installAgents(ctx, paths, []string{"opencode"}, noEmit, true)
	if err == nil || !strings.Contains(err.Error(), "was declined") {
		t.Fatalf("declined installation reached runtime/download setup: %v", err)
	}
	after, _ := os.ReadDir(paths.Data)
	if len(after) != len(before) {
		t.Fatal("declined setup created runtime files")
	}
}
