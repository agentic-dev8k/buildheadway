package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type SetupPolicy struct {
	Version        int      `json:"version"`
	DeclineInstall bool     `json:"decline_install"`
	Approved       []string `json:"approved,omitempty"`
}

func readSetupPolicy(paths Paths) (SetupPolicy, error) {
	p := SetupPolicy{Version: 1}
	name := filepath.Join(paths.Data, "setup-policy.json")
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8192 {
		return p, errors.New("setup policy must be a regular file of at most 8192 bytes")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return p, err
	}
	p = SetupPolicy{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, errors.New("invalid setup policy JSON")
	}
	if dec.Decode(new(any)) != io.EOF {
		return p, errors.New("setup policy must contain one JSON object")
	}
	if p.Version != 1 || len(p.Approved) > len(Registry) {
		return p, errors.New("invalid setup policy")
	}
	for _, id := range p.Approved {
		if !SupportedBackend(id) || id == "demo" {
			return p, errors.New("invalid approved installation")
		}
	}
	return p, nil
}

func updateSetupPolicy(paths Paths, decline bool, approve []string) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	lock, err := acquireProjectLock(paths.Data, "setup-policy", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	p, err := readSetupPolicy(paths)
	if err != nil {
		return err
	}
	if decline {
		p.DeclineInstall = true
		p.Approved = nil
	}
	for _, id := range approve {
		if !SupportedBackend(id) || id == "demo" {
			return errors.New("unknown installation target")
		}
	}
	p.Approved = Unique(append(p.Approved, approve...))
	return AtomicJSON(filepath.Join(paths.Data, "setup-policy.json"), p)
}

// Declining setup remains effective in headless goals and later attachment,
// before installing runtimes or a private Windows shell. Existing tools work.
func checkInstallationPolicy(paths Paths, ids []string, force bool) error {
	p, err := readSetupPolicy(paths)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !SupportedBackend(id) || id == "demo" {
			return fmt.Errorf("unsupported installation target %q", id)
		}
		if !force && FindTool(id, paths) != "" {
			continue
		}
		if p.DeclineInstall && !contains(p.Approved, id) {
			return fmt.Errorf("installation of %s was declined; review /connect or explicitly approve it with buildheadway setup --install %s", id, id)
		}
	}
	return nil
}

type SetupInspection struct {
	OS        string            `json:"os"`
	Arch      string            `json:"arch"`
	Tools     map[string]string `json:"tools"`
	Inventory Inventory         `json:"inventory"`
	Policy    SetupPolicy       `json:"policy"`
}

func inspectSetup(ctx context.Context, paths Paths) (SetupInspection, error) {
	p, err := readSetupPolicy(paths)
	if err != nil {
		return SetupInspection{}, err
	}
	s := SetupInspection{OS: runtime.GOOS, Arch: runtime.GOARCH, Tools: map[string]string{}, Inventory: ScanAgents(ctx, paths), Policy: p}
	for _, id := range []string{"node", "npm", "git"} {
		s.Tools[id] = FindTool(id, paths)
	}
	for i := range s.Inventory.Agents {
		a := &s.Inventory.Agents[i]
		if !a.Installed || !a.Adapter {
			continue
		}
		checkDetectedAgent(ctx, a, paths, a.ID == "opencode")
		if err := ctx.Err(); err != nil {
			return s, err
		}
	}
	return s, nil
}

func formatSetup(s SetupInspection) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Setup inspection · %s/%s\n\n%s\n", s.OS, s.Arch, FormatInventory(s.Inventory))
	for _, id := range []string{"node", "npm", "git"} {
		v := s.Tools[id]
		if v == "" {
			v = "not found (can be set up after approval)"
		}
		fmt.Fprintf(&out, "\n%s: %s", id, v)
	}
	out.WriteString("\n\nInstalled, compatible and zero listed prices are separate checks. Account access and inference have not been tested.\nOpenCode is the currently supported verified-free worker; Cline promotions require its own account and are not automatically price-verified.\nUse /connect to choose a senior. Missing or incompatible packages are reviewed before private installation. /freebies check discovers offers without installing or running models.")
	return out.String()
}

func runSetupCommand(paths Paths, args []string) int {
	f := flag.NewFlagSet("buildheadway setup", flag.ContinueOnError)
	jsonOut := f.Bool("json", false, "Output JSON")
	install := f.String("install", "", "Explicitly approve private installation of comma-separated supported agents")
	decline := f.Bool("decline-install", false, "Decline future automatic agent installations")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || (*decline && *install != "") {
		return fail(errors.New("use setup [--json] [--install AGENT[,AGENT] | --decline-install]"))
	}
	emptyInstall := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == "install" && *install == "" {
			emptyInstall = true
		}
	})
	if emptyInstall {
		return fail(errors.New("--install requires a nonempty supported agent name"))
	}
	if *decline {
		if err := updateSetupPolicy(paths, true, nil); err != nil {
			return fail(err)
		}
	}
	base, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	limit := 2 * time.Minute
	if *install != "" {
		limit = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(base, limit)
	defer cancel()
	if *install != "" {
		ids := strings.Split(*install, ",")
		for i := range ids {
			ids[i] = strings.TrimSpace(ids[i])
			if !SupportedBackend(ids[i]) || ids[i] == "demo" {
				return fail(errors.New("choose a supported agent name; URLs and package names are not accepted"))
			}
		}
		if err := updateSetupPolicy(paths, false, ids); err != nil {
			return fail(err)
		}
		emit := printEvent
		if *jsonOut {
			emit = noEmit
		}
		if _, err := InstallAgents(ctx, paths, ids, emit); err != nil {
			return fail(err)
		}
	}
	s, err := inspectSetup(ctx, paths)
	if err != nil {
		return fail(err)
	}
	if err := outputValue(s, formatSetup(s), *jsonOut); err != nil {
		return fail(err)
	}
	return 0
}

type setupInspectionMsg struct {
	Inspection SetupInspection
	Err        error
}

func (m *appModel) inspectOnboarding() tea.Cmd {
	m.busy = true
	m.mode = "chat"
	m.stage = "Checking installed agents and free-model access"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	m.cancel = cancel
	m.jobDone = make(chan struct{})
	done := m.jobDone
	paths := m.paths
	return func() tea.Msg {
		defer close(done)
		defer cancel()
		s, e := inspectSetup(ctx, paths)
		return setupInspectionMsg{s, e}
	}
}

func (m *appModel) selectSetupChoice(id string) tea.Cmd {
	switch id {
	case "@discover":
		if err := updateSetupPolicy(m.paths, true, nil); err != nil {
			m.add("Setup", err.Error())
			return nil
		}
		m.mode = "chat"
		m.setupBackend = ""
		m.setupNeeded = nil
		m.stage = "Discovery only · no installation approved"
		m.add("Setup", "Agent installation is declined. Free-offer discovery can watch while this app stays open; it cannot supply unlimited quota or resume a blocked goal. Use /connect to review installations later.")
		return m.startFreebieWatch()
	case "@install":
		if m.width < 50 || m.height < 20 {
			m.stage = "Resize to at least 50×20 to review installation"
			return nil
		}
		backend := m.setupBackend
		if backend == "" || len(m.setupNeeded) == 0 {
			m.add("Setup", "Choose a connection before approving installation.")
			return nil
		}
		if err := updateSetupPolicy(m.paths, true, m.setupNeeded); err != nil {
			m.add("Setup", err.Error())
			return nil
		}
		m.setupBackend = ""
		m.setupNeeded = nil
		return m.startSetup(backend)
	case "@back":
		m.setupBackend = ""
		m.connections()
		return nil
	}
	// The choice authorizes using an existing CLI, not silently replacing it or
	// adding a second platform. Native checks still run again during setup.
	needed := []string{}
	for _, backend := range Unique([]string{id, "opencode"}) {
		a, ok := m.inventory.Find(backend)
		if !ok || !a.Installed || a.ProbeError != "" || a.AdapterCheck == nil || a.AdapterCheck.Status != "compatible" {
			needed = append(needed, backend)
		}
	}
	if len(needed) == 0 {
		return m.startSetup(id)
	}
	m.setupBackend = id
	m.setupNeeded = append([]string{}, needed...)
	m.mode = "connect"
	m.cursor = 0
	packages := []string{}
	for _, backend := range needed {
		for _, a := range Registry {
			if a.ID == backend {
				packages = append(packages, a.Package)
			}
		}
	}
	m.choices = []choice{{"@install", "Approve private setup", "Install/repair " + strings.Join(packages, ", ")}, {"@discover", "Skip installation; watch free offers", "Keep existing tools and discover offers without model calls"}, {"@back", "Back to connections", "Choose a different installed agent"}}
	m.add("Review installation", "Planning/review: "+id+". Verified-free implementation workers: OpenCode.\nPrivate packages needed: "+strings.Join(packages, ", ")+". Existing global installations are retained. Node.js or a private Windows shell may also be needed.\nApprove this setup or skip installation. Sign-in and provider quota remain separate.")
	return nil
}
