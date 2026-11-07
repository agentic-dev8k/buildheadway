package main

import (
	"errors"
	"flag"
	"fmt"
	"runtime"
	"strings"
)

const projectRepository = "https://github.com/agentic-dev8k/buildheadway"

type ProjectCredit struct {
	Name string `json:"name"`
	Role string `json:"role"`
	URL  string `json:"url"`
}

type AgentCredit struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	URL                  string   `json:"url"`
	Roles                []string `json:"roles"`
	FreeWorkerAdapter    bool     `json:"free_worker_adapter"`
	MCPProfileForwarding bool     `json:"mcp_profile_forwarding"`
}

type AboutReport struct {
	Name          string          `json:"name"`
	Version       string          `json:"version"`
	OS            string          `json:"os"`
	Arch          string          `json:"arch"`
	License       string          `json:"license"`
	Repository    string          `json:"repository"`
	Contributors  string          `json:"contributors"`
	Maintainers   []ProjectCredit `json:"maintainers"`
	AgentProjects []AgentCredit   `json:"agent_projects"`
	Libraries     []ProjectCredit `json:"libraries"`
	Notes         []string        `json:"notes"`
}

// This offline description identifies adapters and upstream projects. It does
// not probe installed agents, assert endorsement, or fabricate commit authors.
func BuildAboutReport(agent string) (AboutReport, error) {
	report := AboutReport{
		Name: "BuildHeadway", Version: Version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		License: "MIT", Repository: projectRepository, Contributors: projectRepository + "/graphs/contributors",
		Maintainers:   []ProjectCredit{{"agentic-dev8k", "Project maintainer", "https://github.com/agentic-dev8k"}},
		AgentProjects: []AgentCredit{},
		Libraries: []ProjectCredit{
			{"Bubble Tea", "Terminal application framework", "https://github.com/charmbracelet/bubbletea"},
			{"Bubbles", "Terminal input and viewport components", "https://github.com/charmbracelet/bubbles"},
			{"Lip Gloss", "Terminal styling", "https://github.com/charmbracelet/lipgloss"},
			{"MCP Go SDK", "MCP client and protocol", "https://github.com/modelcontextprotocol/go-sdk"},
		},
		Notes: []string{
			"Credits acknowledge upstream projects; actual commit authorship is recorded in repository history.",
			"Capabilities require a compatible installed CLI and its own account access. Use buildheadway scan --check for diagnostics.",
			"OpenCode 1 workers require freshly verified zero prices before every dispatch and retry; a catalog scan is not proof of successful inference.",
			"MCP/browser profiles are forwarded to supported full-access seniors only. Workers and restricted consultations keep their tool limits.",
		},
	}
	urls := map[string]string{
		"codex":    "https://github.com/openai/codex",
		"claude":   "https://github.com/anthropics/claude-code",
		"opencode": "https://github.com/anomalyco/opencode",
		"pi":       "https://github.com/earendil-works/pi",
		"cline":    "https://github.com/cline/cline",
		"gemini":   "https://github.com/google-gemini/gemini-cli",
	}
	for _, definition := range Registry {
		if !definition.Adapter || (agent != "" && definition.ID != agent) {
			continue
		}
		roles := []string{"planning", "review", "consultation"}
		freeWorker := definition.ID == "opencode"
		if freeWorker {
			roles = append(roles, "implementation worker")
		}
		report.AgentProjects = append(report.AgentProjects, AgentCredit{definition.ID, definition.Name, urls[definition.ID], roles, freeWorker, supportsMCPProfileForwarding(definition.ID)})
	}
	if agent != "" && len(report.AgentProjects) == 0 {
		return AboutReport{}, fmt.Errorf("unknown supported agent %q; use scan for detection-only agents", agent)
	}
	return report, nil
}

func FormatAbout(report AboutReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s %s · %s/%s\n%s · %s\n\nContributors and credits\n", report.Name, report.Version, report.OS, report.Arch, report.License, report.Repository)
	for _, credit := range report.Maintainers {
		fmt.Fprintf(&out, "%s · %s\n  %s\n", credit.Name, credit.Role, credit.URL)
	}
	fmt.Fprintf(&out, "Commit contributors: %s\n\nAgent projects\n", report.Contributors)
	for _, credit := range report.AgentProjects {
		fmt.Fprintf(&out, "%s [%s] · %s\n  %s\n", credit.Name, credit.ID, strings.Join(credit.Roles, ", "), credit.URL)
		if credit.MCPProfileForwarding {
			out.WriteString("  Tools: MCP/browser profiles for full-access seniors.\n")
		} else {
			out.WriteString("  Tools: native agent tools; BuildHeadway MCP forwarding unavailable.\n")
		}
		if credit.FreeWorkerAdapter {
			out.WriteString("  Workers: compatible OpenCode 1 and a fresh zero-price catalog required.\n")
		}
	}
	out.WriteString("\nOpen-source libraries\n")
	for _, credit := range report.Libraries {
		fmt.Fprintf(&out, "%s · %s\n  %s\n", credit.Name, credit.Role, credit.URL)
	}
	out.WriteString("Release packages include dependency notices in THIRD_PARTY_LICENSES.\n")
	for _, note := range report.Notes {
		fmt.Fprintf(&out, "\n%s\n", note)
	}
	return strings.TrimSpace(out.String())
}

func runAboutCommand(args []string) int {
	flags := flag.NewFlagSet("buildheadway about", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "Print project credits and adapter capabilities as JSON")
	agent := flags.String("agent", "", "Show one supported agent, such as claude")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		return fail(errors.New("about/credits take no positional arguments; use --agent NAME"))
	}
	empty := false
	flags.Visit(func(f *flag.Flag) { empty = empty || f.Name == "agent" && *agent == "" })
	if empty {
		return fail(errors.New("--agent requires a nonempty agent name"))
	}
	report, err := BuildAboutReport(*agent)
	if err != nil {
		return fail(err)
	}
	if err := outputValue(report, FormatAbout(report), *asJSON); err != nil {
		return fail(err)
	}
	return 0
}
