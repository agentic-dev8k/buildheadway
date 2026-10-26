package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	paths := Paths{filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "bin")}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	return paths
}
func TestOSSpecificAgentLocationsAndWindowsShims(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Tools With Spaces")
	os.MkdirAll(folder, 0755)
	path := filepath.Join(folder, "codex.CMD")
	os.WriteFile(path, []byte("shim"), 0644)
	if got := FindInDirs("codex", []string{folder}, "windows"); got != path {
		t.Fatalf("Windows executable not discovered: %s", got)
	}
	if got := FindInDirs("codex", []string{folder}, "linux"); got != "" {
		t.Fatal("Linux treated a Windows shim as native")
	}
	paths := Paths{filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "bin")}
	dirs := SearchDirs(paths, "windows", root, folder+";"+filepath.Join(root, "Other"))
	if dirs[0] != folder {
		t.Fatalf("Windows PATH was split incorrectly: %v", dirs)
	}
	macDirs := strings.Join(SearchDirs(paths, "darwin", root, ""), "\n")
	if !strings.Contains(macDirs, "/opt/homebrew/bin") || !strings.Contains(macDirs, "/usr/local/bin") {
		t.Fatal("missing Homebrew locations")
	}
}
func TestShimResolutionTreatsPathsAsData(t *testing.T) {
	for _, text := range []string{
		`@"%dp0%\node.exe" "%dp0%\node_modules\@scope\agent\cli.js" %*`,
		`@"%~dp0\node.exe" "%~dp0\node_modules\agent\cli.js" %*`,
	} {
		path, err := ShimTarget(text, filepath.Join(t.TempDir(), "with spaces & symbols"))
		if err != nil || !strings.HasSuffix(path, "cli.js") {
			t.Fatalf("shim failed: %s %v", path, err)
		}
	}
	if _, err := ShimTarget("echo do-something & exit", t.TempDir()); err == nil {
		t.Fatal("unsafe arbitrary batch wrapper was accepted")
	}
}
func TestRuntimeAssetsCoverAllSixBuildTargets(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, err := NodeArchive("v24.19.0", goos, arch)
			if err != nil || !strings.Contains(name, "node-v24.19.0") {
				t.Fatalf("%s/%s: %s %v", goos, arch, name, err)
			}
		}
	}
	if _, err := NodeArchive("../escape", "linux", "amd64"); err == nil {
		t.Fatal("invalid version accepted")
	}
	if _, err := NodeArchive("v24.19.0", "linux", "386"); err == nil {
		t.Fatal("unsupported architecture accepted")
	}
	if !SuitableNode("v22.19.0") || SuitableNode("v20.10.0") {
		t.Fatal("runtime compatibility gate failed")
	}
}
func TestArchiveExtractionRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../escape", "node/../../escape", `..\escape`, `C:\escape`, "/absolute"} {
		root := t.TempDir()
		archive := filepath.Join(root, "node.zip")
		file, _ := os.Create(archive)
		writer := zip.NewWriter(file)
		entry, _ := writer.Create(name)
		entry.Write([]byte("payload"))
		writer.Close()
		file.Close()
		if err := unpackNode(archive, filepath.Join(root, "destination")); err == nil {
			t.Fatalf("archive traversal accepted: %s", name)
		}
	}
}
func TestChecksumManifestRequiresExactAsset(t *testing.T) {
	hash := strings.Repeat("a", 64)
	got, err := expectedChecksum(hash+"  node-v24.19.0-win-x64.zip\n", "node-v24.19.0-win-x64.zip")
	if err != nil || got != hash {
		t.Fatalf("valid checksum rejected: %s %v", got, err)
	}
	if _, err := expectedChecksum(hash+"  another.zip\n", "node.zip"); err == nil {
		t.Fatal("missing asset checksum was accepted")
	}
}
func TestManagedPathIsIdempotentAndPreservesOtherContent(t *testing.T) {
	original := "# user's shell settings\nexport EDITOR=vim\n"
	directory := filepath.Join(t.TempDir(), "buildheadway's tools")
	first := ManagedPathBlock(original, directory, true, false)
	second := ManagedPathBlock(first, directory, true, false)
	if strings.Count(second, pathStart) != 1 {
		t.Fatal("PATH block duplicated")
	}
	removed := ManagedPathBlock(second, directory, false, false)
	if !strings.Contains(removed, "export EDITOR=vim") || strings.Contains(removed, pathStart) {
		t.Fatal("uninstall damaged unrelated shell settings")
	}
}
func TestOpenCodePinsTitleAndWorkerModel(t *testing.T) {
	env := opencodeEnv(testPaths(t), "opencode/free", false)
	value := envValue(env, "OPENCODE_CONFIG_CONTENT")
	if strings.Count(value, "opencode/free") != 3 {
		t.Fatalf("model not pinned for main, title and worker agent: %s", value)
	}
	if runtime.GOOS == "windows" && strings.Contains(envValue(env, "PATH"), "/nonexistent") {
		t.Fatal("invalid path")
	}
}

func TestOpenCodePlannerKeepsApprovalGateAndOverridesInheritedPermissions(t *testing.T) {
	t.Setenv("OPENCODE_PERMISSION", `{"*":"allow"}`)
	var permission map[string]any
	if err := json.Unmarshal([]byte(envValue(opencodeEnv(testPaths(t), "opencode/free", true), "OPENCODE_PERMISSION")), &permission); err != nil {
		t.Fatal(err)
	}
	if permission["*"] != "ask" || permission["read"] != "allow" || permission["task"] != "deny" {
		t.Fatalf("planner does not retain approval-gated tools: %v", permission)
	}
	args := openCodeRunArgs("opencode", "opencode/free", "prompt with spaces.txt", "project with spaces", true, true)
	if contains(args, "--auto") || !contains(args, "buildheadway-plan") {
		t.Fatalf("planner would auto-approve writes: %v", args)
	}
	if !contains(args, "--dir") || !contains(args, "project with spaces") {
		t.Fatal("selected project was not passed to OpenCode")
	}
	if !contains(openCodeRunArgs("opencode", "opencode/free", "prompt.txt", "project", false, true), "--auto") {
		t.Fatal("worker lost autonomous execution")
	}
}

func TestOpenCodeReviewerMayRepeatExecutedChecksWithoutArbitraryShellApproval(t *testing.T) {
	var permission map[string]any
	env := opencodeEnv(testPaths(t), "opencode/free", true, "npm test", "pytest tests/*.py")
	if err := json.Unmarshal([]byte(envValue(env, "OPENCODE_PERMISSION")), &permission); err != nil {
		t.Fatal(err)
	}
	bash := permission["bash"].(map[string]any)
	if bash["npm test"] != "allow" || bash["*"] != "ask" || permission["*"] != "ask" {
		t.Fatalf("incorrect review permissions: %v", permission)
	}
	if _, exists := bash["pytest tests/*.py"]; exists {
		t.Fatal("check wildcard became a broader shell permission")
	}
}
