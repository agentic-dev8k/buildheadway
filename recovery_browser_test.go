package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type browserFixtureParams struct {
	Mode string `json:"mode"`
	Repo string `json:"repo"`
}

func browserProtocolFixture() {
	var config browserFixtureParams
	d, _ := os.ReadFile(os.Args[1])
	_ = json.Unmarshal(d, &config)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture-browser", Version: "1"}, nil)
	origin := ""
	var mu sync.Mutex
	proofRE := regexp.MustCompile(`return ("proof-[^"]+");`)
	for _, name := range []string{"browser_navigate", "browser_evaluate", "browser_run_code_unsafe", "browser_take_screenshot"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			mu.Lock()
			defer mu.Unlock()
			var p map[string]any
			_ = json.Unmarshal(r.Params.Arguments, &p)
			tool := r.Params.Name
			_ = os.MkdirAll(filepath.Join(config.Repo, ".forge"), 0700)
			f, _ := os.OpenFile(filepath.Join(config.Repo, ".forge", "browser-calls"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if f != nil {
				fmt.Fprintln(f, tool)
				f.Close()
			}
			text := `"ok"`
			switch tool {
			case "browser_navigate":
				origin, _ = browserOrigin(p["url"].(string))
				if config.Mode == "initial-error" {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "private-error-token"}}}, nil
				}
			case "browser_evaluate":
				v := origin
				if config.Mode == "origin-change" {
					v = "https://other.invalid"
				}
				x, _ := json.Marshal(v)
				text = string(x)
			case "browser_run_code_unsafe":
				if config.Mode == "timeout" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				code := p["code"].(string)
				matches := proofRE.FindStringSubmatch(code)
				if len(matches) != 2 {
					return nil, errors.New("missing proof nonce")
				}
				text = matches[1]
				if config.Mode == "echo-only" {
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "### Result\n\"not-proof\"\n### Ran Playwright code\n" + code}}}, nil
				}
				if config.Mode == "action-error" {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "private-error-token"}}}, nil
				}
				if config.Mode == "project-output" {
					data, _ := os.ReadFile(filepath.Join(config.Repo, "result.txt"))
					if string(data) != "good\n" {
						return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "simulated page assertion failed"}}}, nil
					}
				}
			case "browser_take_screenshot":
				name := p["filename"].(string)
				_ = os.MkdirAll(filepath.Dir(name), 0700)
				f, e := os.Create(name)
				if e != nil {
					return nil, e
				}
				_ = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 2, 2)))
				f.Close()
				if config.Mode == "header-only" {
					b, _ := os.ReadFile(name)
					_ = os.WriteFile(name, b[:33], 0600)
				}
				if config.Mode == "source-change" {
					_ = os.WriteFile(filepath.Join(config.Repo, "changed.txt"), []byte("changed"), 0600)
				}
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "### Result\n" + text + "\n### Ran Playwright code\n[omitted]"}}}, nil
		})
	}
	_ = server.Run(context.Background(), &mcp.StdioTransport{})
}
func fixtureBrowser(t *testing.T, mode string) (Paths, string, ProjectConfig) {
	t.Helper()
	paths := testPaths(t)
	repo := t.TempDir()
	root := filepath.Join(paths.Data, "browser", browserProvider, "install-fixture")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	node, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cli := filepath.Join(root, "provider-fixture.cli")
	data, _ := json.Marshal(browserFixtureParams{mode, repo})
	_ = os.WriteFile(cli, data, 0600)
	exe := filepath.Join(root, "chromium")
	_ = os.WriteFile(exe, []byte("fixture executable"), 0600)
	r := BrowserReceipt{Version: 1, Provider: browserProvider, Root: root, Node: node, CLI: cli, Executable: exe, Hashes: map[string]string{}}
	for _, name := range []string{node, cli, exe, filepath.Join(root, "critical-a"), filepath.Join(root, "critical-b")} {
		if name != node && name != cli && name != exe {
			_ = os.WriteFile(name, []byte("critical"), 0600)
		}
		h, e := browserFileHash(name)
		if e != nil {
			t.Fatal(e)
		}
		r.Hashes[name] = h
	}
	if e = AtomicJSON(browserReceiptPath(paths), r); e != nil {
		t.Fatal(e)
	}
	c := DefaultProjectConfig()
	c.MCPServers = map[string]MCPServer{browserServerName: browserProfile()}
	c.BrowserChecks = []BrowserCheck{{Name: "flow", URL: "http://127.0.0.1:19437", Steps: []BrowserStep{{Action: "fill", Selector: "#field", ValueEnv: "BROWSER_FIXTURE_VALUE"}, {Action: "expect_text", Selector: "#result", Value: "ready"}}}}
	t.Setenv("BROWSER_FIXTURE_VALUE", "private-input-token")
	return paths, repo, c
}
func TestRecoveredBrowserProtocolProofAndScreenshot(t *testing.T) {
	paths, repo, c := fixtureBrowser(t, "")
	dir := filepath.Join(repo, ".forge", "evidence")
	r, e := RunBrowserCheck(context.Background(), paths, repo, dir, c, c.BrowserChecks[0])
	if e != nil || !r.Passed() || !r.Browser.valid() || r.Browser.Steps != 2 || r.Browser.Assertions != 1 {
		t.Fatalf("missing executed proof: %#v %v", r, e)
	}
	if strings.Contains(r.Output, "private-input-token") || strings.Contains(r.Output, "#field") {
		t.Fatal("logs exposed inputs")
	}
	proof, e := browserPNG(dir, r.Browser.Screenshot)
	if e != nil || proof.SHA256 != r.Browser.SHA256 {
		t.Fatal("screenshot evidence missing", e)
	}
}
func TestRecoveredBrowserRejectsFalseEvidenceAndSeparatesBlockers(t *testing.T) {
	for _, mode := range []string{"echo-only", "header-only", "origin-change", "initial-error", "action-error"} {
		t.Run(mode, func(t *testing.T) {
			paths, repo, c := fixtureBrowser(t, mode)
			r, e := RunBrowserCheck(context.Background(), paths, repo, filepath.Join(repo, ".forge", "evidence"), c, c.BrowserChecks[0])
			var blocker mcpSetupError
			if r.Passed() || errors.As(e, &blocker) != (mode == "initial-error") {
				t.Fatalf("false passing evidence or wrong blocker: %s %#v %v", mode, r, e)
			}
			if strings.Contains(r.Error+r.Output, "private-error-token") {
				t.Fatal("provider error leaked")
			}
			if mode == "origin-change" {
				b, _ := os.ReadFile(filepath.Join(repo, ".forge", "browser-calls"))
				if strings.Contains(string(b), "browser_run_code_unsafe") {
					t.Fatal("typed after origin changed")
				}
			}
		})
	}
}
func TestRecoveredBrowserDeadlinesAndSourceConsistency(t *testing.T) {
	t.Run("command", func(t *testing.T) {
		p, repo, c := fixtureBrowser(t, "timeout")
		c.BrowserChecks[0].TimeoutSeconds = 1
		r, e := RunBrowserCheck(context.Background(), p, repo, filepath.Join(repo, ".forge", "e"), c, c.BrowserChecks[0])
		if e != nil || r.FailureKind != "timeout" || r.ExitCode != 124 || r.Passed() {
			t.Fatalf("wrong command deadline: %#v %v", r, e)
		}
	})
	t.Run("parent", func(t *testing.T) {
		p, repo, c := fixtureBrowser(t, "timeout")
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		r, e := RunBrowserCheck(ctx, p, repo, filepath.Join(repo, ".forge", "e"), c, c.BrowserChecks[0])
		if !errors.Is(e, context.DeadlineExceeded) || r.FailureKind != "cancelled" || r.Passed() {
			t.Fatalf("parent deadline lost: %#v %v", r, e)
		}
	})
	t.Run("source", func(t *testing.T) {
		p, repo, c := fixtureBrowser(t, "source-change")
		r, e := VerifyBrowserPlan(context.Background(), p, repo, c, c.BrowserChecks[0], false)
		if !errors.Is(e, errHookChangedFiles) || r.Status == "passed" || !r.Results[0].SourceChanged || r.Results[0].Passed() {
			t.Fatal("source changes were approved", e)
		}
	})
}
func TestRecoveredBrowserNativeProfileFiltersEnvironmentAndDetectsTampering(t *testing.T) {
	p, repo, c := fixtureBrowser(t, "")
	t.Setenv("BUILDHEADWAY_HOME", "/unrelated-home")
	s, e := resolveBrowserServer(p, c.MCPServers[browserServerName], t.TempDir(), true)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, e := connectMCP(ctx, p, repo, browserServerName, s)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != 4 {
		t.Fatal("generated native profile lost explicit runtime directory", e)
	}
	r, _ := loadBrowserRuntime(p)
	_ = os.WriteFile(r.CLI, []byte("tampered"), 0600)
	if _, e = loadBrowserRuntime(p); e == nil {
		t.Fatal("modified runtime was trusted")
	}
}
func TestRecoveredBrowserConfigCoverageIsAtomic(t *testing.T) {
	p, repo, c := fixtureBrowser(t, "")
	_ = p
	if e := AtomicJSON(filepath.Join(repo, ".buildheadway.json"), c); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(repo, ".buildheadway.json"))
	for _, action := range []string{"disable"} {
		if editBrowserConfig(repo, action, BrowserCheck{}) == nil {
			t.Fatal("removed required browser tools")
		}
	}
	if changeMCPServer(repo, "disable", browserServerName, MCPServer{}) == nil {
		t.Fatal("generic MCP edit removed required coverage")
	}
	after, _ := os.ReadFile(filepath.Join(repo, ".buildheadway.json"))
	if string(before) != string(after) {
		t.Fatal("failed edits changed config")
	}
	if e := editBrowserConfig(repo, "add-check", c.BrowserChecks[0]); e == nil {
		t.Fatal("overwrote existing plan")
	}
	c.Acceptance = []AcceptanceCheck{{Criterion: "Browser flow", Checks: []string{"@browser:flow"}}}
	_ = AtomicJSON(filepath.Join(repo, ".buildheadway.json"), c)
	if editBrowserConfig(repo, "remove-check", BrowserCheck{Name: "flow"}) == nil {
		t.Fatal("removed plan still referenced by acceptance")
	}
}
func TestRecoveredBrowserHandoffBlocksSuccessfulWrapperAndStaleNonce(t *testing.T) {
	repo := t.TempDir()
	path, nonce, e := newBrowserHandoff(repo)
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("BUILDHEADWAY_BROWSER_CHECK_HANDOFF", path)
	t.Setenv("BUILDHEADWAY_BROWSER_CHECK_NONCE", nonce)
	saveBrowserBlocker(repo, mcpSetupError{"browser runtime is unavailable"})
	var blocker mcpSetupError
	if !errors.As(readBrowserHandoff(repo, path, nonce), &blocker) {
		t.Fatal("wrapper blocker lost")
	}
	if !errors.As(readBrowserHandoff(repo, path, "wrong"), &blocker) {
		t.Fatal("stale handoff accepted")
	}
	if valid := (CheckResult{Command: "@browser:flow", ExitCode: 0}).Passed(); valid {
		t.Fatal("browser label without proof passed")
	}
	if usefulCheck("buildheadway browser check") {
		t.Fatal("diagnostic substituted for application acceptance")
	}
}

func TestRecoveredBrowserPinnedChecksResumeBeforeApproval(t *testing.T) {
	paths, repo, c := fixtureBrowser(t, "")
	c.Checks = []string{checkFixtureCommand()}
	if e := AtomicJSON(filepath.Join(repo, ".buildheadway.json"), c); e != nil {
		t.Fatal(e)
	}
	settings := Settings{Version: 2, Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode", Model: "auto"}}
	r, e := NewRunner(paths, settings, repo, "Create a correct output file", false, noEmit)
	if e != nil {
		t.Fatal(e)
	}
	r.MaxCalls = 1
	r.Discover = func(context.Context) ([]string, error) { return []string{"opencode/free"}, nil }
	workers, reviews := 0, 0
	invoke := func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if !planner {
			workers++
			return "Changed: output. Verification: pending.", os.WriteFile(filepath.Join(repo, "result.txt"), []byte("good\n"), 0600)
		}
		if workers == 0 {
			return decisionJSON(false, []Task{{ID: "file", Title: "Write output", Instructions: "Write good and preserve all checks"}}, nil), nil
		}
		reviews++
		for _, v := range r.State.CheckResults {
			if !v.Passed() {
				t.Fatal("approval without current passing checks")
			}
		}
		return decisionJSON(true, nil, nil), nil
	}
	r.Invoke = invoke
	var limit limitError
	if !errors.As(r.Run(context.Background()), &limit) || r.State.Phase != "reviewing" {
		t.Fatal("did not retain checked work at review boundary")
	}
	calls, _ := os.ReadFile(filepath.Join(repo, ".forge/browser-calls"))
	c.MCPServers[browserServerName] = MCPServer{Command: "missing-current-config"}
	c.BrowserChecks = nil
	_ = AtomicJSON(filepath.Join(repo, ".buildheadway.json"), c)
	resumed, e := ResumeRunner(paths, settings, repo, r.State.RunID, noEmit)
	if e != nil {
		t.Fatal(e)
	}
	resumed.MaxCalls = 10
	resumed.Discover = r.Discover
	resumed.Invoke = func(ctx context.Context, con Connection, p, l string, planner bool, model string) (string, error) {
		if planner {
			now, _ := os.ReadFile(filepath.Join(repo, ".forge/browser-calls"))
			if len(now) <= len(calls) {
				t.Fatal("resume reused old browser evidence")
			}
		}
		return invoke(ctx, con, p, l, planner, model)
	}
	if e = resumed.Run(context.Background()); e != nil || resumed.State.Status != "complete" || workers != 1 || reviews == 0 || len(resumed.State.Config.BrowserChecks) != 1 {
		t.Fatal("resume lost pinned configuration or repeated completed work", e)
	}
}

func TestRecoveredBrowserOwnedServerCleanupAndExistingServerPreservation(t *testing.T) {
	node := FindTool("node", testPaths(t))
	if node == "" {
		t.Skip("Node unavailable")
	}
	paths, repo, c := fixtureBrowser(t, "")
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	file := filepath.Join(repo, "server.cjs")
	_ = os.WriteFile(file, []byte(fmt.Sprintf("require('http').createServer((q,r)=>r.end('ready')).listen(%d,'127.0.0.1');", port)), 0600)
	plan := c.BrowserChecks[0]
	plan.URL = fmt.Sprintf("http://127.0.0.1:%d", port)
	plan.StartCommand = `"` + node + `" "` + file + `"`
	r, e := RunBrowserCheck(context.Background(), paths, repo, filepath.Join(repo, ".forge/e"), c, plan)
	if e != nil || !r.Passed() {
		t.Fatal("owned app flow failed", e, r)
	}
	if conn, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond); e == nil {
		conn.Close()
		t.Fatal("owned server survived verification")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "existing") }))
	defer server.Close()
	plan.URL = server.URL
	plan.StartCommand = ""
	r, e = RunBrowserCheck(context.Background(), paths, repo, filepath.Join(repo, ".forge/e"), c, plan)
	if e != nil || !r.Passed() {
		t.Fatal(e)
	}
	res, e := http.Get(server.URL)
	if e != nil {
		t.Fatal("existing server was terminated", e)
	}
	res.Body.Close()
	plan.StartCommand = `"` + node + `" "` + file + `"`
	r, e = RunBrowserCheck(context.Background(), paths, repo, filepath.Join(repo, ".forge/e"), c, plan)
	if r.Passed() {
		t.Fatal("occupied app port accepted")
	}
}

func TestRecoveredBrowserSetupIsTransactional(t *testing.T) {
	paths, repo, c := fixtureBrowser(t, "")
	_ = repo
	_ = c
	node := FindTool("node", paths)
	if node == "" {
		t.Skip("Node unavailable")
	}
	node, e := filepath.EvalSymlinks(node)
	if e != nil {
		t.Fatal(e)
	}
	npm := filepath.Join(t.TempDir(), "npm-fixture.cjs")
	script := `const fs=require('fs'),path=require('path');const root=process.argv[process.argv.indexOf('--prefix')+1];const files=['@playwright/mcp/package.json','@playwright/mcp/cli.js','playwright-core/package.json','playwright-core/cli.js','playwright/package.json','playwright-core/lib/coreBundle.js'];for(const file of files){const p=path.join(root,'node_modules',file);fs.mkdirSync(path.dirname(p),{recursive:true});fs.writeFileSync(p,file.endsWith('package.json')?JSON.stringify({version:file.startsWith('@playwright/mcp')?'0.0.83':'1.64.0-alpha-1790635538000'}):'// fixture');}if(process.env.BUILDHEADWAY_SETUP_FAIL==='1')process.exit(1);`
	_ = os.WriteFile(npm, []byte(script), 0600)
	_ = AtomicJSON(filepath.Join(paths.Data, "runtime/current.json"), NodeRuntime{Node: node, Npm: npm})
	before, _ := os.ReadFile(browserReceiptPath(paths))
	t.Setenv("BUILDHEADWAY_SETUP_FAIL", "1")
	if _, e = SetupBrowser(context.Background(), paths, node, noEmit); e == nil {
		t.Fatal("failed npm setup was published")
	}
	after, _ := os.ReadFile(browserReceiptPath(paths))
	if string(before) != string(after) {
		t.Fatal("failed setup replaced previous working receipt")
	}
	t.Setenv("BUILDHEADWAY_SETUP_FAIL", "0")
	if _, e = SetupBrowser(context.Background(), paths, node, noEmit); e != nil {
		t.Fatal(e)
	}
	if _, e = loadBrowserRuntime(paths); e != nil {
		t.Fatal("successful setup not usable", e)
	}
}

func TestRecoveredBrowserSetupBlockerSurvivesZeroExitShellWrapper(t *testing.T) {
	node := FindTool("node", testPaths(t))
	if node == "" {
		t.Skip("Node unavailable")
	}
	binary := nativeTestBinary(t)
	r, _ := fixtureRunner(t)
	plan := BrowserCheck{Name: "flow", URL: "http://127.0.0.1:19437", Steps: []BrowserStep{{Action: "expect_visible", Selector: "body"}}}
	c := DefaultProjectConfig()
	c.MCPServers = map[string]MCPServer{browserServerName: browserProfile()}
	_ = AtomicJSON(filepath.Join(r.Repo, ".buildheadway.json"), c)
	file := filepath.Join(r.Repo, "plan.json")
	_ = AtomicJSON(file, plan)
	script := filepath.Join(r.Repo, "wrapper.cjs")
	argv, _ := json.Marshal([]string{binary, "browser", "verify", "--project", r.Repo, "--file", file})
	body := "const a=" + string(argv) + ";require('child_process').spawnSync(a[0],a.slice(1),{stdio:'pipe'});process.exit(0);"
	_ = os.WriteFile(script, []byte(body), 0600)
	t.Setenv("BUILDHEADWAY_HOME", filepath.Dir(r.Paths.Data))
	var checks []CheckResult
	e := runVerification(context.Background(), r.Paths, r.Repo, r.Dir, []string{`"` + node + `" "` + script + `"`}, 5*time.Second, false, noEmit, nil, func(c CheckResult) error { checks = append(checks, c); return nil })
	var blocker mcpSetupError
	if !errors.As(e, &blocker) || len(checks) != 1 || checks[0].ExitCode != 0 || checks[0].Passed() {
		t.Fatalf("wrapper hid setup blocker: %#v %v", checks, e)
	}
}

func TestRecoveredBrowserCanonicalOrigins(t *testing.T) {
	for raw, want := range map[string]string{"http://127.0.0.1:80/app": "http://127.0.0.1", "https://[0:0:0:0:0:0:0:1]:443/": "https://[::1]", "http://127.0.0.1:8080/": "http://127.0.0.1:8080"} {
		got, err := browserOrigin(raw)
		if err != nil || got != want {
			t.Fatalf("origin %q = %q, %v", raw, got, err)
		}
	}
}
