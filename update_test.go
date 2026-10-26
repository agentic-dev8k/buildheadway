package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseResolutionPinsVersionAndRejectsUnsafeSources(t *testing.T) {
	calls := 0
	fetch := func(_ context.Context, url string, _ int64) ([]byte, error) {
		calls++
		if url != "https://api.github.com/repos/example/tool/releases/latest" {
			t.Fatal(url)
		}
		return []byte(`{"tag_name":"v0.11.0","draft":false,"prerelease":false}`), nil
	}
	tag, err := resolveReleaseTag(context.Background(), "example/tool", "latest", fetch)
	if err != nil || tag != "v0.11.0" || calls != 1 {
		t.Fatalf("bad version resolution: %s %v", tag, err)
	}
	tag, err = resolveReleaseTag(context.Background(), "example/tool", "0.10.0", fetch)
	if err != nil || tag != "v0.10.0" || calls != 1 {
		t.Fatal("pinned version fetched mutable metadata")
	}
	for _, repo := range []string{"", "https://example.com/a/b", "example/tool/../../evil", "a/b?x=1", "a/b\n"} {
		if _, err := resolveReleaseTag(context.Background(), repo, "latest", fetch); err == nil {
			t.Fatal("accepted unsafe repository", repo)
		}
	}
	if _, err := resolveReleaseTag(context.Background(), "example/tool", "../main", fetch); err == nil {
		t.Fatal("accepted unsafe release tag")
	}
}

func TestReleaseIntegrityRejectsTamperingDuplicatesAndWrongPlatform(t *testing.T) {
	data := []byte("binary")
	name := nativeAssetName()
	manifest := []byte(fmt.Sprintf("%x  bin/%s\n", sha256.Sum256(data), name))
	if err := verifyReleaseBinary(manifest, data, name); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		manifest, data []byte
		name           string
	}{
		{manifest, []byte("tampered"), name}, {append(append([]byte{}, manifest...), manifest...), data, name}, {manifest, data, "wrong-platform"},
	} {
		if err := verifyReleaseBinary(scenario.manifest, scenario.data, scenario.name); err == nil {
			t.Fatal("accepted corrupt release")
		}
	}
}

func TestInstalledCommandCanUpdateOfflineWithoutChangingSettings(t *testing.T) {
	binary := nativeTestBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "user data")
	targetDir := filepath.Join(root, "commands with spaces")
	env := testCommandEnv(home)
	install := exec.Command(binary, "install", "--dir", targetDir, "--no-path")
	install.Env = env
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install: %v %s", err, out)
	}
	release := filepath.Join(root, "release with spaces")
	os.MkdirAll(filepath.Join(release, "bin"), 0700)
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	name := nativeAssetName()
	os.WriteFile(filepath.Join(release, "bin", name), data, 0755)
	os.WriteFile(filepath.Join(release, "SHA256SUMS"), []byte(fmt.Sprintf("%x  bin/%s\n", sha256.Sum256(data), name)), 0600)
	marker := filepath.Join(home, "data", "keep.json")
	os.WriteFile(marker, []byte("keep"), 0600)
	installed := filepath.Join(targetDir, ExecutableName("buildheadway", runtime.GOOS))
	update := exec.Command(installed, "update", "--from", release)
	update.Env = env
	if out, err := update.CombinedOutput(); err != nil {
		t.Fatalf("update while old executable is running: %v %s", err, out)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "keep" {
		t.Fatal("update lost settings")
	}
	check := exec.Command(installed, "--version")
	check.Env = env
	if out, err := check.CombinedOutput(); err != nil || !strings.Contains(string(out), Version) {
		t.Fatalf("updated app cannot start: %v %s", err, out)
	}
	// A failed integrity check must leave the installed app usable.
	os.WriteFile(filepath.Join(release, "bin", name), []byte("corrupt"), 0755)
	update = exec.Command(installed, "update", "--from", release)
	update.Env = env
	if out, err := update.CombinedOutput(); err == nil || !strings.Contains(string(out), "integrity verification") {
		t.Fatalf("accepted corrupted update: %v %s", err, out)
	}
	if !IsBuildHeadwayBinary(installed) {
		t.Fatal("failed update damaged installed executable")
	}
}

func TestDoctorDoesNotMigrateOrWriteConfiguration(t *testing.T) {
	paths := testPaths(t)
	repo := t.TempDir()
	// An invalid configuration is reported, not repaired or rewritten.
	configPath := filepath.Join(paths.Config, "connection.json")
	writeFixture(t, configPath, `{"planner":{"backend":"unknown"}}`)
	before, _ := WorkspaceSnapshot(context.Background(), filepath.Dir(paths.Config))
	report := Diagnose(context.Background(), paths, repo)
	after, _ := WorkspaceSnapshot(context.Background(), filepath.Dir(paths.Config))
	if before != after {
		t.Fatal("read-only diagnostics changed user data")
	}
	found := false
	for _, check := range report.Checks {
		if check.Name == "connections" && check.Status == "error" {
			found = true
		}
	}
	if !found {
		t.Fatal("invalid settings were not reported")
	}
}
