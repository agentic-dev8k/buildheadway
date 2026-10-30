package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type BrowserEvidence struct {
	Provider   string `json:"provider"`
	Steps      int    `json:"steps"`
	Assertions int    `json:"assertions"`
	Screenshot string `json:"screenshot"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

func (b *BrowserEvidence) valid() bool {
	if b == nil {
		return false
	}
	decoded, e := hex.DecodeString(b.SHA256)
	if e != nil || len(decoded) != 32 {
		return false
	}
	return b != nil && b.Provider == browserProvider && b.Steps > 0 && b.Assertions > 0 && b.Steps <= 64 && b.Assertions <= b.Steps && b.Bytes > 0 && b.Bytes <= 8<<20 && len(b.SHA256) == 64 && filepath.IsLocal(b.Screenshot) && strings.HasSuffix(b.Screenshot, ".png") && b.Width > 0 && b.Height > 0 && b.Width <= 8192 && b.Height <= 8192 && b.Width*b.Height <= 16<<20
}
func browserPNG(dir, name string) (*BrowserEvidence, error) {
	root, e := os.OpenRoot(dir)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	st, e := root.Lstat(name)
	if e != nil || !st.Mode().IsRegular() {
		return nil, errors.New("browser screenshot must be a regular PNG file")
	}
	f, e := root.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	data, e := ReadLimited(f, 8<<20)
	if e != nil {
		return nil, errors.New("browser screenshot exceeds 8 MiB")
	}
	cfg, e := png.DecodeConfig(strings.NewReader(string(data)))
	if e != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || cfg.Width*cfg.Height > 16<<20 {
		return nil, errors.New("browser screenshot has invalid PNG dimensions")
	}
	if _, e = png.Decode(strings.NewReader(string(data))); e != nil {
		return nil, errors.New("browser screenshot is incomplete or invalid")
	}
	h := sha256.Sum256(data)
	return &BrowserEvidence{Provider: browserProvider, Screenshot: name, Bytes: int64(len(data)), SHA256: hex.EncodeToString(h[:]), Width: cfg.Width, Height: cfg.Height}, nil
}

// Only the provider's Result section is evidence; echoed code is never proof.
func browserResult(r *mcp.CallToolResult) string {
	for _, v := range r.Content {
		if t, ok := v.(*mcp.TextContent); ok {
			_, after, ok := strings.Cut(t.Text, "### Result\n")
			if ok {
				if i := strings.Index(after, "\n### "); i >= 0 {
					after = after[:i]
				}
				return strings.TrimSpace(after)
			}
		}
	}
	return ""
}
func browserProof(r *mcp.CallToolResult, nonce string) bool {
	var got string
	return r != nil && !r.IsError && json.Unmarshal([]byte(browserResult(r)), &got) == nil && got == nonce
}
func waitBrowserApp(ctx context.Context, paths Paths, repo string, p BrowserCheck) (func(), error) {
	if p.StartCommand == "" {
		return func() {}, nil
	}
	u, _ := url.Parse(p.URL)
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	if c, e := net.DialTimeout("tcp", net.JoinHostPort(u.Hostname(), port), 200*time.Millisecond); e == nil {
		c.Close()
		return nil, errors.New("browser app port is already occupied; stop the other server or omit start_command")
	}
	cmd, e := PreparedCommand(ctx, ShellCommand(p.StartCommand), ToolEnv(paths))
	if e != nil {
		return nil, errors.New("browser start command cannot execute")
	}
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = repo, setEnv(ToolEnv(paths), "CI", "1"), io.Discard, io.Discard
	configureProcess(cmd)
	cmd.Cancel = func() error { return terminateProcess(cmd) }
	cmd.WaitDelay = time.Second
	if e = cmd.Start(); e != nil {
		return nil, errors.New("browser app failed to start")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	cleanup := func() { _ = terminateProcess(cmd); <-done }
	client := http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		select {
		case <-ctx.Done():
			cleanup()
			return nil, ctx.Err()
		case <-done:
			cleanup()
			return nil, errors.New("browser app exited before readiness")
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
		resp, e := client.Do(req)
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return cleanup, nil
			}
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			cleanup()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func RunBrowserCheck(parent context.Context, paths Paths, repo, dir string, config ProjectConfig, p BrowserCheck) (check CheckResult, runErr error) {
	check.Command = "@browser:" + p.Name
	started := time.Now()
	seconds := p.TimeoutSeconds
	if seconds == 0 {
		seconds = 60
	}
	if config.CheckTimeoutSeconds > 0 {
		seconds = min(seconds, config.CheckTimeoutSeconds)
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(seconds)*time.Second)
	defer cancel()
	defer func() {
		check.DurationMillis = max(1, time.Since(started).Milliseconds())
		if runErr == nil {
			return
		}
		var setup mcpSetupError
		if errors.As(runErr, &setup) {
			check.ExitCode = -1
			check.FailureKind = "execution"
			check.Error = setup.Error()
			return
		}
		if parent.Err() != nil {
			check.ExitCode = 130
			check.FailureKind = "cancelled"
			check.Error = parent.Err().Error()
			runErr = parent.Err()
			return
		}
		if ctx.Err() != nil {
			check.ExitCode = 124
			check.FailureKind = "timeout"
			check.Error = "browser command timed out"
			runErr = nil
			return
		}
		check.ExitCode = 1
		check.FailureKind = "exit"
		check.Error = runErr.Error()
		check.Output = check.Error
		runErr = nil
	}()
	if !enabledBrowser(config) {
		return check, mcpSetupError{"browser checks require an enabled built-in browser MCP profile; use browser enable"}
	}
	if e := validateBrowserChecks(ProjectConfig{BrowserChecks: []BrowserCheck{p}, MCPServers: map[string]MCPServer{browserServerName: browserProfile()}}); e != nil {
		return check, e
	}
	for _, s := range p.Steps {
		if s.ValueEnv != "" && os.Getenv(s.ValueEnv) == "" {
			return check, mcpSetupError{"browser fill requires environment variable " + s.ValueEnv}
		}
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return check, e
	}
	attempt, e := os.MkdirTemp(dir, "browser-"+p.Name+"-")
	if e != nil {
		return check, e
	}
	server, e := resolveBrowserServer(paths, browserProfile(), attempt, false)
	if e != nil {
		return check, e
	}
	cleanup, e := waitBrowserApp(ctx, paths, repo, p)
	if e != nil {
		return check, e
	}
	defer cleanup()
	session, e := connectMCP(ctx, paths, repo, browserServerName, server)
	if e != nil {
		if ctx.Err() != nil {
			return check, ctx.Err()
		}
		return check, mcpSetupError{"browser MCP could not initialize; check runtime, OS dependencies and sandbox support"}
	}
	defer session.Close()
	call := func(name string, args map[string]any) (*mcp.CallToolResult, error) {
		r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, mcpSetupError{"browser MCP connection failed; inspect runtime and response limits"}
		}
		if r.IsError {
			return r, errors.New("browser action failed; inspect the application and browser environment")
		}
		return r, nil
	}

	navigation, navErr := call("browser_navigate", map[string]any{"url": p.URL})
	if navErr != nil {
		if navigation != nil && navigation.IsError {
			return check, mcpSetupError{"browser initial navigation failed; check runtime, app readiness, OS dependencies and sandbox support"}
		}
		var setup mcpSetupError
		if errors.As(navErr, &setup) {
			return check, navErr
		}
		if ctx.Err() != nil {
			return check, ctx.Err()
		}
		return check, mcpSetupError{"browser initial navigation failed; check runtime, app readiness, OS dependencies and sandbox support"}
	}

	origin, _ := browserOrigin(p.URL)
	assertions := 0
	for i, step := range p.Steps {
		var observed string
		r, e := call("browser_evaluate", map[string]any{"function": "() => window.location.origin"})
		if e != nil {
			return check, e
		}
		if json.Unmarshal([]byte(browserResult(r)), &observed) != nil || observed != origin {
			return check, errors.New("browser page left the configured origin; remaining actions were refused")
		}
		selector, _ := json.Marshal(step.Selector)
		value := step.Value
		if step.ValueEnv != "" {
			value = os.Getenv(step.ValueEnv)
		}
		if len(value) > 4096 {
			return check, mcpSetupError{"browser credential value exceeds 4096 bytes"}
		}
		v, _ := json.Marshal(value)
		nonce, e := newID("proof-")
		if e != nil {
			return check, e
		}
		n, _ := json.Marshal(nonce)
		action := ""
		switch step.Action {
		case "fill":
			action = "await target.fill(" + string(v) + ");"
		case "click":
			action = "await target.click();"
		case "press":
			action = "await target.press(" + string(v) + ");"
		case "expect_visible":
			action = "if (!(await target.isVisible())) throw new Error('expected visible element');"
			assertions++
		case "expect_text":
			action = "if (!(await target.isVisible()) || !(await target.innerText()).includes(" + string(v) + ")) throw new Error('expected visible text');"
			assertions++
		}
		code := "async page => { const target = page.locator(" + string(selector) + "); if ((await target.count()) !== 1) throw new Error('selector must identify one element'); " + action + " return " + string(n) + "; }"
		r, e = call("browser_run_code_unsafe", map[string]any{"code": code})
		if e != nil {
			return check, e
		}
		if !browserProof(r, nonce) {
			return check, errors.New("browser action lacked positive execution evidence")
		}
		check.Output += fmt.Sprintf("Step %d: %s passed\n", i+1, step.Action)
	}
	name := "browser.png"
	if _, e = call("browser_take_screenshot", map[string]any{"type": "png", "filename": filepath.Join(attempt, name)}); e != nil {
		return check, e
	}
	relative, e := filepath.Rel(dir, filepath.Join(attempt, name))
	if e != nil {
		return check, e
	}
	proof, e := browserPNG(dir, relative)
	if e != nil {
		return check, e
	}
	proof.Steps = len(p.Steps)
	proof.Assertions = assertions
	check.Browser = proof
	check.ExitCode = 0
	return check, nil
}
