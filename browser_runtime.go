package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

type BrowserReceipt struct {
	Version    int               `json:"version"`
	Provider   string            `json:"provider"`
	Root       string            `json:"root"`
	Node       string            `json:"node"`
	CLI        string            `json:"cli"`
	Executable string            `json:"executable"`
	Hashes     map[string]string `json:"hashes"`
}

func browserReceiptPath(paths Paths) string {
	return filepath.Join(paths.Data, "browser", browserProvider, "current.json")
}
func browserFileHash(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 1<<30 {
		return "", errors.New("browser runtime file is not a bounded regular file")
	}
	h := sha256.New()
	if _, e = io.Copy(h, io.LimitReader(f, 1<<30+1)); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func insidePath(root, p string) bool {
	r, e := filepath.Rel(root, p)
	return e == nil && !filepath.IsAbs(r) && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
func loadBrowserRuntime(paths Paths) (BrowserReceipt, error) {
	fail := func() (BrowserReceipt, error) {
		return BrowserReceipt{}, mcpSetupError{"browser runtime is unavailable or changed; run buildheadway browser setup"}
	}
	f, e := os.Open(browserReceiptPath(paths))
	if e != nil {
		return fail()
	}
	defer f.Close()
	data, e := ReadLimited(f, 64<<10)
	var r BrowserReceipt
	if e != nil || json.Unmarshal(data, &r) != nil || r.Version != 1 || r.Provider != browserProvider || !filepath.IsAbs(r.Root) || !insidePath(filepath.Join(paths.Data, "browser", browserProvider), r.Root) || !insidePath(r.Root, r.CLI) || !filepath.IsAbs(r.Node) || !filepath.IsAbs(r.Executable) || len(r.Hashes) < 5 || len(r.Hashes) > 16 {
		return fail()
	}
	if r.Hashes[r.CLI] == "" || r.Hashes[r.Node] == "" || r.Hashes[r.Executable] == "" {
		return fail()
	}
	for p, h := range r.Hashes {
		if p != r.Node && p != r.Executable && !insidePath(r.Root, p) {
			return fail()
		}
		var got string
		var e error
		if p != r.Node && p != r.Executable {
			got, e = browserManagedHash(r.Root, p)
		} else {
			got, e = browserFileHash(p)
		}
		if e != nil || got != h {
			return fail()
		}
	}
	return r, nil
}
func SetupBrowser(ctx context.Context, paths Paths, executable string, emit Emit) (receipt BrowserReceipt, runErr error) {
	if emit == nil {
		emit = noEmit
	}
	if e := paths.Ensure(); e != nil {
		return receipt, e
	}
	if executable != "" {
		if !filepath.IsAbs(executable) {
			return receipt, errors.New("--executable requires an absolute Chromium path")
		}
		p, e := filepath.EvalSymlinks(executable)
		if e != nil {
			return receipt, e
		}
		executable = p
		if _, e := browserFileHash(p); e != nil {
			return receipt, e
		}
	}
	n, e := EnsureNode(ctx, paths, emit)
	if e != nil {
		return receipt, e
	}
	parent := filepath.Dir(browserReceiptPath(paths))
	if e = os.MkdirAll(parent, 0700); e != nil {
		return receipt, e
	}
	stage, e := os.MkdirTemp(parent, "install-")
	if e != nil {
		return receipt, e
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	env := setEnv(ToolEnv(paths), "PLAYWRIGHT_BROWSERS_PATH", filepath.Join(stage, "browsers"))
	env = setEnv(env, "PATH", filepath.Dir(n.Node)+string(os.PathListSeparator)+envValue(env, "PATH"))
	run := func(label string, args ...string) (string, error) {
		r, e := RunProcess(ctx, append([]string{n.Node}, args...), stage, filepath.Join(paths.Data, "setup", filepath.Base(stage)+"-"+label+".log"), "", env, 10*time.Minute, nil)
		if e != nil || r.Code != 0 {
			return "", fmt.Errorf("browser %s failed; inspect %s", label, filepath.Join(paths.Data, "setup", filepath.Base(stage)+"-"+label+".log"))
		}
		return strings.TrimSpace(r.Output), nil
	}
	emit(Event{Kind: "status", Text: "Installing pinned browser MCP runtime"})
	if _, e = run("npm", n.Npm, "install", "--ignore-scripts", "--no-audit", "--no-fund", "--registry=https://registry.npmjs.org/", "--prefix", stage, "@playwright/mcp@"+browserProvider); e != nil {
		return receipt, e
	}
	rel := []string{"node_modules/@playwright/mcp/package.json", "node_modules/@playwright/mcp/cli.js", "node_modules/playwright-core/package.json", "node_modules/playwright-core/cli.js", "node_modules/playwright/package.json", "node_modules/playwright-core/lib/coreBundle.js"}
	for _, x := range []struct{ file, version string }{{rel[0], browserProvider}, {rel[2], browserCore}, {rel[4], browserCore}} {
		var p struct {
			Version string `json:"version"`
		}
		if ReadJSON(filepath.Join(stage, filepath.FromSlash(x.file)), &p) != nil || p.Version != x.version {
			return receipt, errors.New("browser runtime version does not match pinned dependencies")
		}
	}
	if executable == "" {
		if _, e = run("chromium", filepath.Join(stage, filepath.FromSlash(rel[3])), "install", "--no-shell", "chromium"); e != nil {
			return receipt, e
		}
		executable, e = run("path", "-e", "process.stdout.write(require('playwright-core').chromium.executablePath())")
		if e != nil {
			return receipt, e
		}
	}
	if !filepath.IsAbs(executable) {
		return receipt, errors.New("browser runtime did not return an absolute executable")
	}
	receipt = BrowserReceipt{Version: 1, Provider: browserProvider, Root: stage, Node: n.Node, CLI: filepath.Join(stage, filepath.FromSlash(rel[1])), Executable: executable, Hashes: map[string]string{}}
	for _, p := range append([]string{n.Node, executable}, func() []string {
		out := []string{}
		for _, p := range rel {
			out = append(out, filepath.Join(stage, filepath.FromSlash(p)))
		}
		return out
	}()...) {
		h, e := browserFileHash(p)
		if e != nil {
			return receipt, e
		}
		receipt.Hashes[p] = h
	}
	if e = ctx.Err(); e != nil {
		return receipt, e
	}
	if e = AtomicJSON(browserReceiptPath(paths), receipt); e != nil {
		return receipt, e
	}
	published = true
	return receipt, nil
}
func browserRuntimeArgs(r BrowserReceipt, out string, images bool) []string {
	response := "omit"
	if images {
		response = "allow"
	}
	return []string{r.Node, r.CLI, "--headless", "--isolated", "--sandbox", "--caps", "vision", "--no-webmcp", "--executable-path", r.Executable, "--output-dir", out, "--output-max-size", "33554432", "--viewport-size", "1280x720", "--image-responses", response}
}
func resolveBrowserServer(paths Paths, s MCPServer, out string, images bool) (MCPServer, error) {
	if s.Builtin == "" {
		return s, nil
	}
	if s.Builtin != "browser" || s.BuiltinVersion != browserProvider {
		return s, mcpSetupError{"unsupported pinned browser provider"}
	}
	if _, e := loadBrowserRuntime(paths); e != nil {
		return s, e
	}
	self, e := os.Executable()
	if e != nil {
		return s, e
	}
	data, e := filepath.Abs(paths.Data)
	if e != nil {
		return s, e
	}
	out, e = filepath.Abs(out)
	if e != nil {
		return s, e
	}
	args := []string{"--internal-browser-server", "--data-root", data, "--output", out}
	if images {
		args = append(args, "--images")
	}
	return MCPServer{Command: self, Args: args, TimeoutSeconds: s.TimeoutSeconds}, nil
}
func runBrowserServer(args []string) int {
	flags := flag.NewFlagSet("internal browser", flag.ContinueOnError)
	data := flags.String("data-root", "", "Runtime directory")
	out := flags.String("output", "", "Evidence directory")
	images := flags.Bool("images", false, "Allow image responses")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !filepath.IsAbs(*data) || !filepath.IsAbs(*out) {
		return 2
	}
	paths := Paths{Data: *data, Bin: filepath.Join(*data, "bin")}
	r, e := loadBrowserRuntime(paths)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 2
	}
	if e = os.MkdirAll(*out, 0700); e != nil {
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	argv := browserRuntimeArgs(r, *out, *images)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = mcpProcessEnv(paths, MCPServer{})
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if e = cmd.Run(); e != nil {
		return 1
	}
	return 0
}

func browserManagedHash(dir, path string) (string, error) {
	root, e := os.OpenRoot(dir)
	if e != nil {
		return "", e
	}
	defer root.Close()
	rel, e := filepath.Rel(dir, path)
	if e != nil {
		return "", e
	}
	st, e := root.Lstat(rel)
	if e != nil || !st.Mode().IsRegular() {
		return "", errors.New("managed browser file must be regular")
	}
	f, e := root.Open(rel)
	if e != nil {
		return "", e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<30 {
		return "", errors.New("managed browser file is invalid")
	}
	h := sha256.New()
	_, e = io.Copy(h, io.LimitReader(f, (1<<30)+1))
	return hex.EncodeToString(h.Sum(nil)), e
}
