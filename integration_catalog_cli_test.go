package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func integrationCLI(t *testing.T, binary, home string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = testCommandEnv(home)
	return command.CombinedOutput()
}

func TestMCPCatalogCLIIsOfflineAndPreviewPreservesConfiguration(t *testing.T) {
	binary, repo := nativeTestBinary(t), t.TempDir()
	home := filepath.Join(t.TempDir(), "not-created")
	configPath := filepath.Join(repo, ".buildheadway.json")
	invalid := []byte("invalid project settings retained")
	if err := os.WriteFile(configPath, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := integrationCLI(t, binary, home, "mcp", "catalog", "--project", repo, "--json")
	var catalog []struct{ ID, Version string }
	if err != nil || json.Unmarshal(out, &catalog) != nil || len(catalog) != 8 {
		t.Fatalf("MCP catalog is unavailable or reads project settings: %v %s", err, out)
	}
	for _, entry := range catalog {
		if entry.ID == "" || entry.Version == "" {
			t.Fatal("catalog omitted provider identity/version", string(out))
		}
	}
	out, err = integrationCLI(t, binary, home, "mcp", "add", "--project", repo, "--preset", "filesystem", "--preview", "--json", "project-files")
	var plan struct {
		Name, Preset string
		Server       MCPServer
	}
	if err != nil || json.Unmarshal(out, &plan) != nil || plan.Name != "project-files" || plan.Preset != "filesystem" || plan.Server.Command != "npx" || !slices.Contains(plan.Server.Args, "--read-only") || !slices.Contains(plan.Server.Args, "--confine-roots") {
		t.Fatalf("filesystem preview is not bounded/read-only: %v %s", err, out)
	}
	canonical, _ := filepath.EvalSymlinks(repo)
	if !slices.Contains(plan.Server.Args, canonical) {
		t.Fatal("preview lost its selected project directory", string(out))
	}
	for _, args := range [][]string{
		{"mcp", "add", "--preset", "unknown", "--project", repo, "--preview"},
		{"mcp", "add", "--preset", "fetch", "--read-write", "--project", repo},
		{"mcp", "add", "--preset", "git", "--url", "https://example.invalid/mcp", "--project", repo},
		{"mcp", "catalog", "--preset", "filesystem", "--project", repo},
	} {
		if out, err := integrationCLI(t, binary, home, args...); err == nil {
			t.Fatalf("accepted invalid combination %v: %s", args, out)
		}
	}
	if after, _ := os.ReadFile(configPath); !bytes.Equal(after, invalid) {
		t.Fatal("catalog, invalid request or preview changed project settings")
	}
	for _, path := range []string{home, filepath.Join(repo, ".forge")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("inspection created state at %s", path)
		}
	}
}

func TestMCPPresetCLIAndExtensionTemplatesWorkThroughPublicCommands(t *testing.T) {
	binary, repo, home := nativeTestBinary(t), t.TempDir(), t.TempDir()
	legacyPath := filepath.Join(repo, ".forge.json")
	legacy := []byte(`{"version":1,"checks":["echo preserve"],"max_worker_calls":7}`)
	if err := os.WriteFile(legacyPath, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"mcp", "add", "--project", repo, "--preset", "filesystem", "files"},
		{"mcp", "add", "--project", repo, "--preset", "context7", "--token-env", "CATALOG_PRIVATE_KEY", "docs"},
	} {
		if out, err := integrationCLI(t, binary, home, args...); err != nil {
			t.Fatal(err, string(out))
		}
	}
	config, err := LoadProjectConfig(repo)
	if err != nil || config.MaxWorkerCalls != 7 || !slices.Equal(config.Checks, []string{"echo preserve"}) || len(config.MCPServers) != 2 || config.MCPServers["docs"].BearerTokenEnv != "CATALOG_PRIVATE_KEY" {
		t.Fatal("preset lost gates or credential indirection", config, err)
	}
	before, _ := os.ReadFile(filepath.Join(repo, ".buildheadway.json"))
	if out, err := integrationCLI(t, binary, home, "mcp", "add", "--project", repo, "--preset", "fetch", "files"); err == nil {
		t.Fatal("replaced an existing MCP server", string(out))
	}
	if after, _ := os.ReadFile(filepath.Join(repo, ".buildheadway.json")); !bytes.Equal(before, after) {
		t.Fatal("rejected replacement changed settings")
	}
	if after, _ := os.ReadFile(legacyPath); !bytes.Equal(after, legacy) {
		t.Fatal("rewrote legacy settings")
	}
	out, err := integrationCLI(t, binary, home, "extensions", "catalog", "--json")
	var templates []struct{ ID string }
	if err != nil || json.Unmarshal(out, &templates) != nil || len(templates) != 6 {
		t.Fatalf("extension template catalog failed: %v %s", err, out)
	}
	out, err = integrationCLI(t, binary, home, "extensions", "init", "--project", repo, "--kind", "prompt", "--template", "api-review", "api-contract")
	if err != nil {
		t.Fatal(err, string(out))
	}
	created := filepath.Join(repo, ".buildheadway-prompts", "api-contract.md")
	original, err := os.ReadFile(created)
	if err != nil || !strings.Contains(string(original), "compatibility") {
		t.Fatal("template did not generate useful editable instructions", err)
	}
	out, err = integrationCLI(t, binary, home, "workflow", "--project", repo, "--preview", "api-contract", "preserve decimal totals")
	if err != nil || !bytes.Contains(out, []byte("preserve decimal totals")) || bytes.Contains(out, []byte("$ARGUMENTS")) {
		t.Fatal("created extension cannot expand through the public workflow command", err, string(out))
	}
	if _, err := integrationCLI(t, binary, home, "extensions", "init", "--project", repo, "--template", "api-review", "--kind", "prompt", "api-contract"); err == nil {
		t.Fatal("overwrote an existing extension")
	}
	if after, _ := os.ReadFile(created); !bytes.Equal(original, after) {
		t.Fatal("existing extension changed")
	}
}
