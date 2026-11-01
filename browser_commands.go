package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

type BrowserStatus struct {
	Enabled  bool           `json:"enabled"`
	Ready    bool           `json:"ready"`
	Provider string         `json:"provider"`
	Checks   []BrowserCheck `json:"checks"`
	Problem  string         `json:"problem,omitempty"`
}

func BrowserStatusFor(paths Paths, repo string) (BrowserStatus, error) {
	c, e := LoadProjectConfig(repo)
	if e != nil {
		return BrowserStatus{}, e
	}
	r := BrowserStatus{Enabled: enabledBrowser(c), Provider: browserProvider, Checks: append([]BrowserCheck{}, c.BrowserChecks...)}
	_, e = loadBrowserRuntime(paths)
	r.Ready = e == nil
	if e != nil {
		r.Problem = e.Error()
	}
	return r, nil
}
func FormatBrowser(v BrowserStatus) string {
	s := fmt.Sprintf("Browser MCP %s · installed %t · enabled for new goals %t\n%d required browser checks.", v.Provider, v.Ready, v.Enabled, len(v.Checks))
	if v.Problem != "" {
		s += "\n" + v.Problem
	}
	return s
}
func loadBrowserPlan(file string) (BrowserCheck, error) {
	var p BrowserCheck
	f, e := os.Open(file)
	if e != nil {
		return p, e
	}
	defer f.Close()
	d, e := ReadLimited(f, 128<<10)
	if e != nil {
		return p, e
	}
	dec := json.NewDecoder(strings.NewReader(string(d)))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&p); e != nil {
		return p, e
	}
	if dec.Decode(new(any)) != io.EOF {
		return p, errors.New("browser plan must contain one JSON object")
	}
	return p, validateBrowserChecks(ProjectConfig{BrowserChecks: []BrowserCheck{p}, MCPServers: map[string]MCPServer{browserServerName: browserProfile()}})
}
func editBrowserConfig(repo, action string, p BrowserCheck) error {
	lock, e := acquireProjectLock(repo, "config-lock", true)
	if e != nil {
		return e
	}
	defer lock.Close()
	c, e := LoadProjectConfig(repo)
	if e != nil {
		return e
	}
	switch action {
	case "enable":
		if c.MCPServers == nil {
			c.MCPServers = map[string]MCPServer{}
		}
		name := browserServerName
		for n, v := range c.MCPServers {
			if v.Builtin == "browser" {
				name = n
			}
		}
		if old, ok := c.MCPServers[name]; ok && old.Builtin != "browser" {
			return errors.New("browser server name is already used by another MCP server")
		}
		c.MCPServers[name] = browserProfile()
	case "disable":
		for n, v := range c.MCPServers {
			if v.Builtin == "browser" {
				v.Disabled = true
				c.MCPServers[n] = v
			}
		}
	case "add-check":
		for _, v := range c.BrowserChecks {
			if v.Name == p.Name {
				return errors.New("browser check already exists; remove it explicitly before replacement")
			}
		}
		c.BrowserChecks = append(c.BrowserChecks, p)
	case "remove-check":
		found := false
		out := []BrowserCheck{}
		for _, v := range c.BrowserChecks {
			if v.Name == p.Name {
				found = true
			} else {
				out = append(out, v)
			}
		}
		if !found {
			return errors.New("browser check not found")
		}
		c.BrowserChecks = out
	default:
		return errors.New("unsupported browser configuration action")
	}
	if e = ValidateProjectConfig(c); e != nil {
		return e
	}
	d, e := json.Marshal(c)
	if e != nil || len(d) > 128<<10 {
		return errors.New("project configuration exceeds its size limit")
	}
	return AtomicJSON(filepath.Join(repo, ".buildheadway.json"), c)
}

type BrowserVerifyReport struct {
	VerificationReport
	ReportPath string `json:"report_path"`
}

func VerifyBrowserPlan(ctx context.Context, paths Paths, repo string, c ProjectConfig, p BrowserCheck, diagnostic bool) (r BrowserVerifyReport, runErr error) {
	repo, e := filepath.Abs(repo)
	if e != nil {
		return r, e
	}
	if e = ensureIgnore(repo); e != nil {
		return r, e
	}
	id, e := newID("browser-")
	if e != nil {
		return r, e
	}
	kind := "browser-checks"
	if diagnostic {
		kind = "browser-diagnostics"
	}
	dir := filepath.Join(repo, ".forge", kind, id)
	r.VerificationReport = VerificationReport{Version: 1, ID: id, Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339), Results: []CheckResult{}}
	r.ReportPath = filepath.Join(dir, "report.json")
	before, e := WorkspaceSnapshot(ctx, repo)
	if e != nil {
		return r, e
	}
	check, e := RunBrowserCheck(ctx, paths, repo, dir, c, p)
	r.Results = append(r.Results, check)
	after, snapshotErr := WorkspaceSnapshot(ctx, repo)
	if snapshotErr != nil {
		e = errors.Join(e, snapshotErr)
	} else if before != after {
		r.Results[0].SourceChanged = true
		e = errors.Join(e, errHookChangedFiles)
	}
	if updated, _, evidenceErr := auditBrowserEvidence(ctx, repo, dir, r.Results); evidenceErr != nil {
		if updated != nil {
			r.Results = updated
		}
		e = errors.Join(e, evidenceErr)
	}
	r.Status = "passed"
	if e != nil || !r.Results[0].Passed() {
		r.Status = "failed"
	}
	if e != nil {
		r.Error = e.Error()
	}
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if saveErr := AtomicJSON(r.ReportPath, r); saveErr != nil {
		e = errors.Join(e, saveErr)
	}
	if e == nil && r.Status != "passed" {
		e = errors.New("browser verification failed; inspect the saved report")
	}
	return r, e
}
func runBrowserCommand(paths Paths, args []string) (code int) {
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	if action == "evidence" {
		return runBrowserEvidenceCommand(args)
	}
	failureProject, failureHandled := ".", false
	defer func() {
		if code != 0 && !failureHandled && (action == "verify" || action == "check") {
			saveBrowserFailure(failureProject, errors.New("browser "+action+" command failed; inspect its command log"), "")
		}
	}()
	f := flag.NewFlagSet("buildheadway browser "+action, flag.ContinueOnError)
	repo := f.String("project", ".", "Project folder")
	asJSON := f.Bool("json", false, "Print JSON")
	exe := f.String("executable", "", "Absolute selected Chromium executable")
	file := f.String("file", "", "Browser plan JSON")
	if e := f.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	project, e := filepath.Abs(*repo)
	if e != nil {
		return fail(e)
	}
	failureProject = project
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	finish := func(v any, text string, e error) int {
		if e != nil {
			failureHandled = true
			saveBrowserBlocker(project, e)
			if action == "verify" || action == "check" {
				report := ""
				if value, ok := v.(BrowserVerifyReport); ok {
					report = value.ReportPath
				}
				saveBrowserFailure(project, e, report)
			}
		}
		if v != nil {
			if err := outputValue(v, text, *asJSON); err != nil {
				return fail(err)
			}
		}
		if e != nil {
			return fail(e)
		}
		return 0
	}
	if f.NArg() != 0 && !(action == "remove-check" && f.NArg() == 1) {
		return fail(errors.New("unexpected browser arguments; options precede positional names"))
	}
	switch action {
	case "status":
		v, e := BrowserStatusFor(paths, project)
		return finish(v, FormatBrowser(v), e)
	case "setup", "enable":
		r, e := SetupBrowser(ctx, paths, *exe, noEmit)
		if e == nil && action == "enable" {
			e = editBrowserConfig(project, "enable", BrowserCheck{})
		}
		return finish(r, "Browser MCP installed. Run buildheadway browser check to verify navigation, typing, clicks and screenshots.", e)
	case "disable":
		e := editBrowserConfig(project, "disable", BrowserCheck{})
		return finish(nil, "", e)
	case "add-check":
		if *file == "" {
			return fail(errors.New("use browser add-check --file PLAN.json"))
		}
		p, e := loadBrowserPlan(*file)
		if e == nil {
			e = editBrowserConfig(project, action, p)
		}
		return finish(p, "Browser check configured for new goals.", e)
	case "remove-check":
		if f.NArg() != 1 || f.Arg(0) == "" {
			return fail(errors.New("use browser remove-check NAME"))
		}
		e := editBrowserConfig(project, action, BrowserCheck{Name: f.Arg(0)})
		return finish(nil, "", e)
	case "verify", "check":
		c, e := LoadProjectConfig(project)
		if e != nil {
			return finish(nil, "", mcpSetupError{"browser project configuration is invalid; inspect the project settings before retrying"})
		}
		var p BrowserCheck
		var closeServer func()
		if action == "verify" {
			if *file == "" {
				return fail(errors.New("use browser verify --file PLAN.json"))
			}
			p, e = loadBrowserPlan(*file)
		} else {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				io.WriteString(w, `<!doctype html><input id="value"><button id="apply" onclick="document.getElementById('result').textContent=document.getElementById('value').value">Apply</button><div id="result"></div>`)
			}))
			closeServer = server.Close
			p = BrowserCheck{Name: "diagnostic", URL: server.URL, Steps: []BrowserStep{{Action: "fill", Selector: "#value", Value: "ready"}, {Action: "click", Selector: "#apply"}, {Action: "expect_text", Selector: "#result", Value: "ready"}}}
			c.MCPServers = map[string]MCPServer{browserServerName: browserProfile()}
		}
		if closeServer != nil {
			defer closeServer()
		}
		if e != nil {
			return finish(nil, "", e)
		}
		r, e := VerifyBrowserPlan(ctx, paths, project, c, p, action == "check")
		return finish(r, FormatVerification(r.VerificationReport)+"\nReport: "+r.ReportPath, e)
	default:
		return fail(errors.New("browser actions: status, setup, enable, disable, check, verify, evidence, add-check, remove-check"))
	}
}
