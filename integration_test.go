package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var buildOnce sync.Once
var integrationBinary string
var integrationBuildErr error

func nativeTestBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		directory, err := os.MkdirTemp("", "buildheadway-integration-build-*")
		if err != nil {
			integrationBuildErr = err
			return
		}
		integrationBinary = filepath.Join(directory, ExecutableName("buildheadway", runtime.GOOS))
		goTool := filepath.Join(runtime.GOROOT(), "bin", ExecutableName("go", runtime.GOOS))
		command := exec.Command(goTool, "build", "-buildvcs=false", "-o", integrationBinary, ".")
		output, err := command.CombinedOutput()
		if err != nil {
			integrationBuildErr = fmtError(string(output), err)
		}
	})
	if integrationBuildErr != nil {
		t.Fatal(integrationBuildErr)
	}
	return integrationBinary
}
func fmtError(output string, err error) error { return &testBuildError{output, err} }

type testBuildError struct {
	output string
	err    error
}

func (e *testBuildError) Error() string   { return e.err.Error() + "\n" + e.output }
func testCommandEnv(home string) []string { return setEnv(os.Environ(), "BUILDHEADWAY_HOME", home) }
func TestNativeDemoDoesNotNeedPythonOrNode(t *testing.T) {
	binary := nativeTestBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "--self-test")
	command.Env = setEnv(testCommandEnv(t.TempDir()), "PATH", t.TempDir())
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Self-test passed") {
		t.Fatalf("native self-test failed: %v\n%s", err, output)
	}
}
func TestNativeInstallUpgradeAndUninstallPreserveData(t *testing.T) {
	binary := nativeTestBinary(t)
	home := t.TempDir()
	destination := filepath.Join(home, "Commands With Spaces")
	env := testCommandEnv(home)
	for attempt := 0; attempt < 2; attempt++ {
		command := exec.Command(binary, "install", "--dir", destination)
		command.Env = env
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("install failed: %v\n%s", err, output)
		}
	}
	installed := filepath.Join(destination, ExecutableName("buildheadway", runtime.GOOS))
	if !IsBuildHeadwayBinary(installed) {
		t.Fatal("installed executable lost application identity")
	}
	keep := filepath.Join(home, "data", "keep-user-settings.json")
	os.WriteFile(keep, []byte(`{"keep":true}`), 0600)
	command := exec.Command(binary, "uninstall")
	command.Env = env
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatal("installed binary was not removed")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("uninstall removed user settings")
	}
}

func TestBundledInstallerWithSpacesAndIntegrityFailure(t *testing.T) {
	binary := nativeTestBinary(t)
	root := t.TempDir()
	bundle := filepath.Join(root, "BuildHeadway Download With Spaces")
	if err := os.MkdirAll(filepath.Join(bundle, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	name := "buildheadway-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	asset := filepath.Join(bundle, "bin", name)
	if err := os.WriteFile(asset, data, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("%x  bin/%s\n", sha256.Sum256(data), name)
	os.WriteFile(filepath.Join(bundle, "SHA256SUMS"), []byte(manifest), 0600)
	for _, script := range []string{"install.sh", "install.ps1"} {
		contents, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bundle, script), contents, 0700); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(root, "Installed Commands With Spaces")
	env := setEnv(testCommandEnv(filepath.Join(root, "settings")), "CI", "1")
	command := bundledInstallerCommand(bundle, destination)
	command.Env = env
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("packaged installer failed: %v\n%s", err, output)
	}
	if !IsBuildHeadwayBinary(filepath.Join(destination, ExecutableName("buildheadway", runtime.GOOS))) {
		t.Fatal("packaged installer did not install the native executable")
	}
	replacement := asset + ".changed"
	if err := os.WriteFile(replacement, append(data, []byte("tampered")...), 0755); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(replacement, asset); err != nil {
		t.Fatal(err)
	}
	command = bundledInstallerCommand(bundle, destination)
	command.Env = env
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "integrity verification") {
		t.Fatalf("packaged installer accepted a changed binary: %v\n%s", err, output)
	}
}
func TestInstallDoesNotOverwriteAnotherBuildHeadwayProgram(t *testing.T) {
	binary := nativeTestBinary(t)
	home := t.TempDir()
	destination := filepath.Join(home, "bin")
	os.MkdirAll(destination, 0700)
	target := filepath.Join(destination, ExecutableName("buildheadway", runtime.GOOS))
	original := []byte("another application's buildheadway command")
	os.WriteFile(target, original, 0700)
	command := exec.Command(binary, "install", "--dir", destination, "--no-path")
	command.Env = testCommandEnv(home)
	if err := command.Run(); err == nil {
		t.Fatal("installer overwrote an unrelated application")
	}
	data, _ := os.ReadFile(target)
	if string(data) != string(original) {
		t.Fatal("unrelated executable changed")
	}
}

func TestNoPathInstallPreservesUnmanagedPathOnUninstall(t *testing.T) {
	binary := nativeTestBinary(t)
	root := t.TempDir()
	env := testCommandEnv(root)
	profile := filepath.Join(root, "shell-path.conf")
	original := []byte("user managed configuration\n")
	os.WriteFile(profile, original, 0600)
	command := exec.Command(binary, "install", "--no-path")
	command.Env = env
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("install failed: %v\n%s", err, output)
	}
	var receipt InstallReceipt
	if err := ReadJSON(filepath.Join(root, "data", installReceiptName), &receipt); err != nil || receipt.PathAdded {
		t.Fatalf("no-path installation claimed a PATH entry: %#v %v", receipt, err)
	}
	command = exec.Command(binary, "uninstall")
	command.Env = env
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, output)
	}
	data, _ := os.ReadFile(profile)
	if string(data) != string(original) {
		t.Fatal("uninstall changed an unmanaged PATH configuration")
	}
}
func TestNativeScanReportsAllRequestedAgents(t *testing.T) {
	binary := nativeTestBinary(t)
	command := exec.Command(binary, "scan", "--json")
	command.Env = testCommandEnv(t.TempDir())
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var inventory Inventory
	if err := json.Unmarshal(output, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"codex", "claude", "opencode", "pi", "cline", "gemini", "qwen", "goose", "aider"} {
		if _, ok := inventory.Find(id); !ok {
			t.Fatalf("%s missing from native inventory", id)
		}
	}
}
func TestCancellationTerminatesNativeChild(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_LONG_CHILD") == "yes" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	repo := t.TempDir()
	env := setEnv(os.Environ(), "BUILDHEADWAY_LONG_CHILD", "yes")
	start := time.Now()
	_, err := RunProcess(ctx, []string{os.Args[0], "-test.run=TestCancellationTerminatesNativeChild"}, repo, filepath.Join(repo, "child.log"), "", env, time.Minute, nil)
	if err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("native child did not terminate promptly")
	}
}

func TestChildUsesSelectedProjectDespiteInheritedPWD(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_CWD_CHILD") == "yes" {
		cwd, _ := os.Getwd()
		json.NewEncoder(os.Stdout).Encode(map[string]string{"cwd": cwd, "pwd": os.Getenv("PWD")})
		os.Exit(0)
	}
	repo := t.TempDir()
	env := setEnv(setEnv(os.Environ(), "BUILDHEADWAY_CWD_CHILD", "yes"), "PWD", t.TempDir())
	result, err := RunProcess(context.Background(), []string{os.Args[0], "-test.run=TestChildUsesSelectedProjectDespiteInheritedPWD"}, repo, filepath.Join(repo, "cwd.log"), "", env, time.Minute, nil)
	if err != nil || result.Code != 0 {
		t.Fatalf("child failed: %v %s", err, result.Output)
	}
	var state map[string]string
	if err := json.Unmarshal([]byte(result.Output), &state); err != nil || state["cwd"] != repo || state["pwd"] != repo {
		t.Fatalf("child inspected the wrong project: %v %v", state, err)
	}
}
func TestTerminalConnectionSelectionAndComposer(t *testing.T) {
	paths := testPaths(t)
	repo := t.TempDir()
	model := newApp(paths, repo, "", false, false)
	inventory := Inventory{OS: "windows", Arch: "arm64"}
	for _, definition := range Registry {
		inventory.Agents = append(inventory.Agents, DetectedAgent{AgentDefinition: definition, Installed: definition.ID == "pi", Version: "1.3.0", Path: "C:/Tools/pi.cmd"})
	}
	updated, _ := model.Update(scanMsg{inventory})
	model = updated.(appModel)
	if len(model.choices) == 0 || model.choices[0].Backend != "pi" {
		t.Fatal("installed Pi was not prioritized")
	}
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 35})
	model = updated.(appModel)
	view := model.View()
	if !strings.Contains(view, "BUILDHEADWAY") || !strings.Contains(view, "Use Pi") || !strings.Contains(view, "Set up Cline") {
		t.Fatalf("connection picker missing required choices:\n%s", view)
	}
	model.mode = "chat"
	model.connected = true
	model.settings.Planner = Connection{Backend: "pi"}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Build my project")})
	model = updated.(appModel)
	if model.editor.Value() != "Build my project" {
		t.Fatalf("composer input failed: %q", model.editor.Value())
	}
}

func TestBackgroundScanDoesNotResetActiveGoal(t *testing.T) {
	model := newApp(testPaths(t), t.TempDir(), "", false, false)
	model.busy = true
	model.connected = true
	model.mode = "chat"
	model.stage = "Building project"
	updated, _ := model.Update(scanMsg{Inventory{OS: "linux"}})
	model = updated.(appModel)
	if model.stage != "Building project" || !model.busy || model.mode != "chat" {
		t.Fatal("background scan replaced active goal state")
	}
}

func TestWorkerAuthenticationRecoveryTargetsWorker(t *testing.T) {
	model := newApp(testPaths(t), t.TempDir(), "", false, false)
	model.settings = Settings{Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode"}}
	model.busy = true
	updated, _ := model.Update(Event{Kind: "finished", Value: finishedMsg{Err: authError{"opencode", "login required"}}})
	model = updated.(appModel)
	if model.loginTarget.Backend != "opencode" || !model.resumeAfterLogin || model.busy {
		t.Fatal("worker login recovery selected the wrong account or lost resume state")
	}
}
func TestRealInstalledAdapterContracts(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_REAL_AGENT_PROBES") == "" {
		t.Skip("enable explicitly to probe real installed CLIs without model calls")
	}
	paths := testPaths(t)
	for _, id := range []string{"codex", "claude", "opencode", "pi", "cline"} {
		t.Run(id, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := Compatible(ctx, Connection{Backend: id}, paths); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRealFreeModelCatalog(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_REAL_AGENT_PROBES") == "" {
		t.Skip("enable explicitly for a public model-catalog read")
	}
	runner := Runner{Paths: testPaths(t), Settings: Settings{Worker: Connection{Backend: "opencode", Model: "auto"}}, Repo: t.TempDir(), Dir: t.TempDir(), Emit: noEmit}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	models, err := runner.discoverModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 {
		t.Fatal("no free models parsed")
	}
	t.Logf("Verified zero-price catalog entries: %v", models)
}

func TestBuildHeadwayInstallPreservesPreviousBrandReceipt(t *testing.T) {
	binary := nativeTestBinary(t)
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	os.MkdirAll(dataDir, 0700)
	oldBinary := filepath.Join(root, ExecutableName("codentrail", runtime.GOOS))
	oldBytes := []byte("previous installation stays untouched")
	os.WriteFile(oldBinary, oldBytes, 0700)
	oldReceipt := filepath.Join(dataDir, "installation.json")
	if err := AtomicJSON(oldReceipt, InstallReceipt{Application: "Codentrail", Version: "0.18.0", Executable: oldBinary}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(oldReceipt)
	env := testCommandEnv(root)
	install := exec.Command(binary, "install", "--no-path")
	install.Env = env
	if output, err := install.CombinedOutput(); err != nil {
		t.Fatalf("installation failed: %v\n%s", err, output)
	}
	assertPrevious := func() {
		t.Helper()
		after, err := os.ReadFile(oldReceipt)
		if err != nil || string(before) != string(after) {
			t.Fatal("previous brand receipt overwritten or removed", err)
		}
		after, err = os.ReadFile(oldBinary)
		if err != nil || string(after) != string(oldBytes) {
			t.Fatal("previous brand executable changed", err)
		}
	}
	assertPrevious()
	var current InstallReceipt
	if err := ReadJSON(filepath.Join(dataDir, "buildheadway-installation.json"), &current); err != nil || current.Application != "BuildHeadway" {
		t.Fatal("new receipt missing", err)
	}
	uninstall := exec.Command(binary, "uninstall")
	uninstall.Env = env
	if output, err := uninstall.CombinedOutput(); err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, output)
	}
	assertPrevious()
	if _, err := os.Stat(current.Executable); !os.IsNotExist(err) {
		t.Fatal("new executable was not uninstalled", err)
	}
}
