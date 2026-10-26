package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenamePreservesLegacySettingsAndNewEnvironmentPrecedence(t *testing.T) {
	t.Setenv("FORGE_HOME", t.TempDir())
	// Unset only the new variable so the legacy fallback can be exercised.
	old, had := os.LookupEnv("BUILDHEADWAY_HOME")
	os.Unsetenv("BUILDHEADWAY_HOME")
	t.Cleanup(func() {
		if had {
			os.Setenv("BUILDHEADWAY_HOME", old)
		} else {
			os.Unsetenv("BUILDHEADWAY_HOME")
		}
	})
	paths, err := UserPaths()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(paths.Config) != os.Getenv("FORGE_HOME") {
		t.Fatal("legacy home lost")
	}
	settings := Settings{Planner: Connection{Backend: "demo"}, Worker: Connection{Backend: "demo"}}
	if err := SaveSettings(paths, settings); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSettings(paths); err != nil || got.Planner.Backend != "demo" {
		t.Fatal("legacy settings lost", err)
	}
	current := t.TempDir()
	t.Setenv("BUILDHEADWAY_HOME", current)
	got, err := UserPaths()
	if err != nil || filepath.Dir(got.Config) != current {
		t.Fatal("new home precedence lost", err)
	}
	t.Setenv("BUILDHEADWAY_HOME", "")
	if brandEnv("BUILDHEADWAY_HOME") != "" {
		t.Fatal("explicit empty new variable ignored")
	}
}
func TestRenameRetainsProjectConfigGuidanceAndRunDirectory(t *testing.T) {
	repo := t.TempDir()
	config := DefaultProjectConfig()
	config.Instructions = "legacy preferences"
	config.Checks = []string{"go test ./..."}
	if err := AtomicJSON(filepath.Join(repo, ".forge.json"), config); err != nil {
		t.Fatal(err)
	}
	got, err := LoadProjectConfig(repo)
	if err != nil || got.Instructions != config.Instructions || len(got.Checks) != 1 {
		t.Fatal("legacy config lost", err)
	}
	os.WriteFile(filepath.Join(repo, "FORGE.md"), []byte("legacy guidance"), 0600)
	os.WriteFile(filepath.Join(repo, "BUILDHEADWAY.md"), []byte("current guidance"), 0600)
	context, err := ProjectContext(repo, got)
	if err != nil || !strings.Contains(context, "legacy guidance") || !strings.Contains(context, "current guidance") {
		t.Fatal("guidance lost", err)
	}
	current := DefaultProjectConfig()
	current.Instructions = "current preferences"
	if err := AtomicJSON(filepath.Join(repo, ".buildheadway.json"), current); err != nil {
		t.Fatal(err)
	}
	got, err = LoadProjectConfig(repo)
	if err != nil || got.Instructions != current.Instructions {
		t.Fatal("new config precedence lost", err)
	}
	r, _ := fixtureRunner(t)
	if !strings.Contains(r.Dir, string(filepath.Separator)+".forge"+string(filepath.Separator)) {
		t.Fatalf("checkpoint directory changed: %s", r.Dir)
	}
}
func TestLegacyBrandPathChangesOnlyApplicationComponent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "buildheadway-parent")
	if got := legacyBrandPath(filepath.Join(parent, "buildheadway")); got != filepath.Join(parent, "forge") {
		t.Fatal(got)
	}
}

func TestExistingForgeDirectoriesAreReusedWithoutMovingData(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	t.Setenv("BUILDHEADWAY_HOME", "")
	current, err := userPathsForBrand(false)
	if err != nil {
		t.Fatal(err)
	}
	legacy := legacyBrandPath(current.Config)
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacy, "keep.txt")
	os.WriteFile(marker, []byte("saved"), 0600)
	got, err := UserPaths()
	if err != nil || got.Config != legacy {
		t.Fatal("old settings location ignored", got, err)
	}
	if err := os.MkdirAll(current.Config, 0700); err != nil {
		t.Fatal(err)
	}
	got, err = UserPaths()
	if err != nil || got.Config != current.Config {
		t.Fatal("new settings location ignored", got, err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "saved" {
		t.Fatal("old settings overwritten")
	}
}

func TestCodentrailEnvironmentRemainsSupported(t *testing.T) {
	key := "BUILDHEADWAY_AGENT_PATH"
	old, exists := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if exists {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
	t.Setenv("CODENTRAIL_AGENT_PATH", "codentrail-tools")
	t.Setenv("FORGE_AGENT_PATH", "forge-tools")
	if got := brandEnv(key); got != "codentrail-tools" {
		t.Fatal("Codentrail alias lost", got)
	}
	t.Setenv(key, "new-tools")
	if got := brandEnv(key); got != "new-tools" {
		t.Fatal("current environment lost precedence", got)
	}
	t.Setenv(key, "")
	if got := brandEnv(key); got != "" {
		t.Fatal("explicit empty current value lost", got)
	}
	os.Unsetenv(key)
	t.Setenv("CODENTRAIL_AGENT_PATH", "")
	if got := brandEnv(key); got != "" {
		t.Fatal("explicit empty Codentrail value lost", got)
	}
}
func TestCodentrailProjectConfigAndGuidanceRemainSupported(t *testing.T) {
	repo := t.TempDir()
	old := DefaultProjectConfig()
	old.Instructions = "forge instructions"
	if err := AtomicJSON(filepath.Join(repo, ".forge.json"), old); err != nil {
		t.Fatal(err)
	}
	previous := old
	previous.Instructions = "codentrail instructions"
	previousPath := filepath.Join(repo, ".codentrail.json")
	if err := AtomicJSON(previousPath, previous); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(previousPath)
	got, err := LoadProjectConfig(repo)
	if err != nil || got.Instructions != previous.Instructions {
		t.Fatal("Codentrail config ignored", got, err)
	}
	for _, name := range []string{"FORGE.md", "CODENTRAIL.md", "BUILDHEADWAY.md"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+" guidance"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	text, err := ProjectContext(repo, got)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"FORGE.md", "CODENTRAIL.md", "BUILDHEADWAY.md"} {
		if !strings.Contains(text, name+" guidance") {
			t.Fatal("guidance lost", name)
		}
	}
	current := previous
	current.Instructions = "current instructions"
	path := filepath.Join(repo, ".buildheadway.json")
	if err := AtomicJSON(path, current); err != nil {
		t.Fatal(err)
	}
	got, err = LoadProjectConfig(repo)
	if err != nil || got.Instructions != current.Instructions {
		t.Fatal("current config ignored", got, err)
	}
	os.WriteFile(path, []byte("{invalid"), 0600)
	if _, err := LoadProjectConfig(repo); err == nil {
		t.Fatal("invalid newer config silently fell back")
	}
	after, _ := os.ReadFile(previousPath)
	if string(before) != string(after) {
		t.Fatal("older config rewritten")
	}
}
func TestCodentrailDirectoriesPreferredOverForgeWithoutMerging(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	t.Setenv("BUILDHEADWAY_HOME", "")
	current, err := userPathsForBrand(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, brand := range []string{"Codentrail", "Forge"} {
		path := previousBrandPath(current.Config, brand)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(path, "keep.txt"), []byte(brand), 0600)
	}
	got, err := UserPaths()
	if err != nil || got.Config != previousBrandPath(current.Config, "Codentrail") {
		t.Fatal("Codentrail location lost", got, err)
	}
	if err := os.MkdirAll(current.Config, 0700); err != nil {
		t.Fatal(err)
	}
	got, err = UserPaths()
	if err != nil || got.Config != current.Config {
		t.Fatal("current location lost", got, err)
	}
	for _, brand := range []string{"Codentrail", "Forge"} {
		data, err := os.ReadFile(filepath.Join(previousBrandPath(current.Config, brand), "keep.txt"))
		if err != nil || string(data) != brand {
			t.Fatal("old directory changed", brand, err)
		}
	}
}
