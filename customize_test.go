package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExtensionStartersAreDiscoverableExpandableAndDoNotOverwrite(t *testing.T) {
	paths, repo := testPaths(t), t.TempDir()
	for _, kind := range []string{"skill", "prompt"} {
		for _, personal := range []bool{false, true} {
			name := kind + "-project"
			if personal {
				name = kind + "-personal"
			}
			item, err := InitExtension(paths, repo, name, kind, personal)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(item.Source)
			goal, err := ExpandWorkflow(paths, repo, name, []string{"keep the API"})
			if err != nil || !strings.Contains(goal, "keep the API") || strings.Contains(goal, "$ARGUMENTS") {
				t.Fatalf("starter cannot run: %v %s", err, goal)
			}
			if _, err := InitExtension(paths, repo, name, kind, personal); err == nil {
				t.Fatal("overwrote extension")
			}
			after, _ := os.ReadFile(item.Source)
			if string(before) != string(after) {
				t.Fatal("existing extension changed")
			}
		}
	}
	inventory, err := InspectExtensions(paths, repo)
	if err != nil || len(inventory.Workflows) != len(builtinWorkflows())+4 {
		t.Fatalf("missing custom extensions: %+v %v", inventory, err)
	}
	for _, entry := range inventory.Workflows {
		if entry.Source == "" {
			t.Fatal("extension lost its editable source")
		}
	}
	for _, name := range []string{"../escape", "Bad Name", "", "-bad"} {
		if _, err := InitExtension(paths, repo, name, "skill", false); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}

func TestExtensionStarterRejectsSymlinkDirectories(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, ".buildheadway-skills")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := InitExtension(testPaths(t), repo, "escape", "skill", false); err == nil {
		t.Fatal("followed extension symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote outside the extension root")
	}
}

func TestMCPConfigurationPreservesGatesLegacyBytesAndPinnedGoals(t *testing.T) {
	paths, repo := testPaths(t), t.TempDir()
	check := checkFixtureCommand()
	legacyData, _ := json.Marshal(map[string]any{"version": 1, "checks": []string{check}, "acceptance": []map[string]any{{"criterion": "required", "checks": []string{check}}}, "max_worker_calls": 7})
	legacy := string(legacyData)
	writeFixture(t, filepath.Join(repo, ".forge.json"), legacy)
	server := MCPServer{URL: "https://example.invalid/mcp", BearerTokenEnv: "MCP_TEST_TOKEN"}
	if err := changeMCPServer(repo, "add", "docs", server); err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(paths, Settings{}, repo, "inspect", false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	for _, action := range []string{"disable", "enable", "remove"} {
		if err := changeMCPServer(repo, action, "docs", MCPServer{}); err != nil {
			t.Fatal(err)
		}
	}
	config, err := LoadProjectConfig(repo)
	if err != nil || len(config.MCPServers) != 0 || config.MaxWorkerCalls != 7 || len(config.Acceptance) != 1 || config.Checks[0] != check {
		t.Fatalf("MCP edit changed project gates: %+v %v", config, err)
	}
	after, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	old, _ := os.ReadFile(filepath.Join(repo, ".forge.json"))
	if string(before) != string(after) || string(old) != legacy {
		t.Fatal("MCP configuration rewrote a goal or legacy file")
	}
	loaded, _, err := LoadRun(repo, r.State.RunID)
	if err != nil || !reflect.DeepEqual(loaded.Config.MCPServers["docs"], server) {
		t.Fatalf("resume lost pinned MCP: %v", err)
	}
	// Actual token values are never needed when reading or serializing config.
	t.Setenv("MCP_TEST_TOKEN", "secret-must-not-enter-checkpoint")
	data, _ := json.Marshal(loaded)
	if strings.Contains(string(data), "secret-must-not-enter-checkpoint") {
		t.Fatal("checkpoint contains an expanded credential")
	}
}

func TestMCPConfigurationRejectsInvalidServersWithoutWriting(t *testing.T) {
	cases := []map[string]MCPServer{
		{"bad name": {Command: "node"}}, {"a-b": {Command: "node"}, "a_b": {Command: "node"}},
		{"s": {}}, {"s": {Command: "node", URL: "https://example.invalid"}},
		{"s": {URL: "file:///tmp/server"}}, {"s": {URL: "https://user:secret@example.invalid"}},
		{"s": {URL: "https://example.invalid", Env: []string{"TOKEN"}}},
		{"s": {Command: "node", BearerTokenEnv: "TOKEN"}}, {"s": {Command: "node", Env: []string{"TOKEN=secret"}}},
		{"s": {Command: "node", Env: []string{"TOKEN", "TOKEN"}}}, {"s": {Command: "node", TimeoutSeconds: 301}},
	}
	for _, servers := range cases {
		if err := validateMCPServers(servers); err == nil {
			t.Fatalf("accepted invalid MCP: %+v", servers)
		}
	}
	repo := t.TempDir()
	if err := changeMCPServer(repo, "add", "bad", MCPServer{}); err == nil {
		t.Fatal("saved invalid server")
	}
	if _, err := os.Stat(filepath.Join(repo, ".buildheadway.json")); !os.IsNotExist(err) {
		t.Fatal("invalid edit wrote configuration")
	}
}
