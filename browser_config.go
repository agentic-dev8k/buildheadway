package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const browserProvider = "0.0.83"
const browserCore = "1.64.0-alpha-1790635538000"
const browserServerName = "buildheadway-browser"

type BrowserStep struct {
	Action   string `json:"action"`
	Selector string `json:"selector,omitempty"`
	Value    string `json:"value,omitempty"`
	ValueEnv string `json:"value_env,omitempty"`
}
type BrowserCheck struct {
	Name           string        `json:"name"`
	URL            string        `json:"url"`
	StartCommand   string        `json:"start_command,omitempty"`
	TimeoutSeconds int           `json:"timeout_seconds,omitempty"`
	Steps          []BrowserStep `json:"steps"`
}

func browserOrigin(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 4096 {
		return "", errors.New("browser checks require a numeric loopback HTTP(S) URL without credentials or fragment")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("browser acceptance URLs must use numeric loopback hosts")
	}
	host := ip.String()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if port != "" && !(u.Scheme == "http" && port == "80") && !(u.Scheme == "https" && port == "443") {
		host += ":" + port
	}
	return u.Scheme + "://" + host, nil
}
func browserProfile() MCPServer {
	return MCPServer{Builtin: "browser", BuiltinVersion: browserProvider, TimeoutSeconds: 60}
}
func enabledBrowser(c ProjectConfig) bool {
	for _, s := range c.MCPServers {
		if s.Builtin == "browser" && !s.Disabled {
			return true
		}
	}
	return false
}
func validateBrowserChecks(c ProjectConfig) error {
	if len(c.BrowserChecks) > 8 {
		return errors.New("configure at most eight browser checks")
	}
	names := map[string]bool{}
	for _, p := range c.BrowserChecks {
		if !workflowName.MatchString(p.Name) || len(p.Name) > 64 || names[p.Name] {
			return errors.New("browser checks need distinct lowercase names")
		}
		names[p.Name] = true
		if _, e := browserOrigin(p.URL); e != nil {
			return e
		}
		if p.TimeoutSeconds < 0 || p.TimeoutSeconds > 300 || len(p.StartCommand) > 16000 || strings.ContainsRune(p.StartCommand, 0) {
			return errors.New("invalid browser start command or timeout (0 or 1–300 seconds)")
		}
		if len(p.Steps) < 1 || len(p.Steps) > 64 {
			return errors.New("browser plans need 1–64 steps")
		}
		for _, s := range p.Steps {
			if !contains([]string{"fill", "click", "press", "expect_text", "expect_visible"}, s.Action) || strings.TrimSpace(s.Selector) == "" || len(s.Selector) > 2048 || strings.Contains(s.Selector, ">>") || strings.ContainsRune(s.Selector, 0) || len(s.Value) > 4096 || strings.ContainsRune(s.Value, 0) {
				return errors.New("browser steps need a supported action, unique CSS selector and bounded value")
			}
			if s.ValueEnv != "" && (s.Action != "fill" || s.Value != "" || !mcpEnvName.MatchString(s.ValueEnv)) {
				return errors.New("value_env applies only to fill and replaces value")
			}
			if (s.Action == "press" || s.Action == "expect_text") && s.Value == "" {
				return errors.New("press and expect_text require value")
			}
			if (s.Action == "click" || s.Action == "expect_visible") && (s.Value != "" || s.ValueEnv != "") {
				return errors.New("click and expect_visible do not take values")
			}
		}
		last := p.Steps[len(p.Steps)-1].Action
		if last != "expect_text" && last != "expect_visible" {
			return errors.New("browser plans must end with a positive assertion")
		}
	}
	if len(c.BrowserChecks) > 0 && !enabledBrowser(c) {
		return errors.New("browser checks require an enabled built-in browser MCP profile; use browser enable before adding checks")
	}
	all := append(append([]string{}, c.Checks...), acceptanceCommands(c.Acceptance)...)
	for _, x := range c.PlatformChecks {
		all = append(all, x...)
	}
	for _, v := range all {
		if strings.HasPrefix(v, "@browser:") && !names[strings.TrimPrefix(v, "@browser:")] {
			return fmt.Errorf("required browser check %q has no configured plan", v)
		}
	}
	return nil
}
func browserPlan(c ProjectConfig, label string) (BrowserCheck, error) {
	for _, p := range c.BrowserChecks {
		if label == "@browser:"+p.Name {
			return p, nil
		}
	}
	return BrowserCheck{}, mcpSetupError{"required browser plan is missing; restore pinned configuration before resuming"}
}
