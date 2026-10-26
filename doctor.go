package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Diagnostic struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}
type DoctorReport struct {
	Version       string          `json:"version"`
	OS            string          `json:"os"`
	Arch          string          `json:"arch"`
	InstallMethod string          `json:"install_method"`
	Executable    string          `json:"executable"`
	Paths         Paths           `json:"paths"`
	Checks        []Diagnostic    `json:"checks"`
	Agents        []DetectedAgent `json:"agents"`
}

func Diagnose(ctx context.Context, paths Paths, repo string) DoctorReport {
	report := DoctorReport{Version: Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Paths: paths, InstallMethod: "standalone"}
	report.Executable, _ = os.Executable()
	add := func(name, status, detail, fix string) {
		report.Checks = append(report.Checks, Diagnostic{name, status, detail, fix})
	}
	if brandEnv("BUILDHEADWAY_INSTALL_METHOD") == "npm" {
		report.InstallMethod = "npm"
	}
	resolved, err := exec.LookPath("buildheadway")
	if err != nil {
		add("terminal command", "warning", "buildheadway is not on this shell's PATH", "Open a new terminal after installation, or reinstall with install.sh / install.ps1.")
	} else {
		a, aerr := os.Stat(resolved)
		b, berr := os.Stat(report.Executable)
		if report.InstallMethod != "npm" && aerr == nil && berr == nil && !os.SameFile(a, b) {
			add("terminal command", "warning", "Another buildheadway command takes precedence: "+resolved, "Check your PATH order and remove only the obsolete installation you own.")
		} else {
			add("terminal command", "ok", resolved, "")
		}
	}
	if report.InstallMethod != "npm" {
		var receipt InstallReceipt
		err := ReadJSON(filepath.Join(paths.Data, installReceiptName), &receipt)
		if errors.Is(err, os.ErrNotExist) {
			add("installation", "info", "Running a portable or development build", "Run buildheadway install for a per-user installation.")
		} else if err != nil || receipt.Application != "BuildHeadway" || receipt.Executable == "" {
			add("installation", "error", "Installation receipt is invalid", "Reinstall from a verified release.")
		} else if data, err := os.ReadFile(receipt.Executable); err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != receipt.SHA256 {
			add("installation", "error", "Installed binary differs from its recorded checksum or is missing", "Reinstall from a verified release.")
		} else {
			add("installation", "ok", "Recorded version "+receipt.Version+"; binary checksum matches", "")
			if receipt.Repository == "" {
				add("updates", "info", "No public release repository configured in this build", "Use buildheadway update --from RELEASE_FOLDER.")
			} else {
				add("updates", "ok", receipt.Repository, "Use buildheadway update to fetch the latest stable release.")
			}
		}
	} else {
		add("installation", "ok", "npm launcher verified the bundled native binary", "Use npm to upgrade or uninstall this installation.")
	}
	var settings Settings
	err = ReadJSON(filepath.Join(paths.Config, "connection.json"), &settings)
	if err == nil {
		if !SupportedBackend(settings.Planner.Backend) {
			err = errors.New("choose a supported planner connection")
		}
		if settings.Worker.Backend == "" {
			settings.Worker = Connection{Backend: "opencode", Model: "auto"}
		}
		if err == nil {
			_, err = FigureOutTeam(settings)
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		add("connections", "info", "First-run setup has not been completed", "Run buildheadway and choose a connection.")
	} else if err != nil {
		add("connections", "error", err.Error(), "Run buildheadway and use /connect to repair your settings.")
	} else {
		for _, entry := range []struct {
			name string
			c    Connection
		}{{"planner", settings.Planner}, {"worker", settings.Worker}} {
			if path, err := ResolveConnection(entry.c, paths); err != nil {
				add(entry.name, "warning", err.Error(), "Use /connect in BuildHeadway to repair the connection.")
			} else {
				add(entry.name, "ok", entry.c.Backend+" · "+path+" (authentication not checked)", "")
			}
		}
	}
	if config, err := LoadProjectConfig(repo); err != nil {
		add("project", "error", err.Error(), "Fix .buildheadway.json in this project.")
	} else if _, err := ProjectContext(repo, config); err != nil {
		add("project", "error", err.Error(), "Fix the referenced project guidance file.")
	} else {
		add("project", "ok", "Project configuration and guidance are readable", "")
	}
	report.Checks = append(report.Checks, verificationShellDiagnostic(ctx))
	report.Agents = ScanAgents(ctx, paths).Agents
	return report
}

// A resolvable shell can still misparse quoted checks or hide their failure.
// Probe real execution in an isolated temporary directory without user commands,
// project writes, model calls or persistent configuration changes.
func verificationShellDiagnostic(ctx context.Context) Diagnostic {
	const name = "verification shell"
	if err := ctx.Err(); err != nil {
		return Diagnostic{name, "info", "Execution probe interrupted: " + err.Error(), ""}
	}
	shell, err := exec.LookPath(ShellCommand("")[0])
	fix := "Restore the operating system's default command shell; on Windows, check COMSPEC."
	if err != nil {
		return Diagnostic{name, "error", err.Error(), fix}
	}
	dir, err := os.MkdirTemp("", "buildheadway-shell-probe-")
	if err != nil {
		return Diagnostic{name, "warning", "Cannot create the shell probe: " + err.Error(), "Check write access to the operating system's temporary directory."}
	}
	defer os.RemoveAll(dir)
	const marker = "buildheadway-shell-ok"
	command := `test "$(cat "probe file.txt")" = "probe value with spaces" && printf "%s" ` + marker
	if runtime.GOOS == "windows" {
		command = `findstr /x /c:"probe value with spaces" "probe file.txt" >nul && echo ` + marker
	}
	for index, content := range []string{"probe value with spaces\n", "different value\n"} {
		if runtime.GOOS == "windows" {
			content = strings.ReplaceAll(content, "\n", "\r\n")
		}
		if err := os.WriteFile(filepath.Join(dir, "probe file.txt"), []byte(content), 0600); err != nil {
			return Diagnostic{name, "warning", "Cannot prepare the shell probe: " + err.Error(), "Check write access to the operating system's temporary directory."}
		}
		result, err := RunProcess(ctx, ShellCommand(command), dir, filepath.Join(dir, fmt.Sprintf("probe-%d.log", index)), "", nil, 3*time.Second, nil)
		if ctx.Err() != nil {
			return Diagnostic{name, "info", "Execution probe interrupted: " + ctx.Err().Error(), ""}
		}
		output := strings.TrimSpace(result.Output)
		if err != nil || (index == 0 && (result.Code != 0 || output != marker)) || (index == 1 && (result.Code != 1 || output != "")) {
			detail := fmt.Sprintf("%s: quoted-command/failure probe failed (exit %d)", shell, result.Code)
			if err != nil {
				detail += ": " + err.Error()
			}
			if output != "" {
				detail += ": " + Clean(Tail(output, 512))
			}
			return Diagnostic{name, "error", detail, fix}
		}
	}
	return Diagnostic{name, "ok", shell + ": quoted paths, redirection and failure propagation verified", ""}
}
func FormatDoctor(report DoctorReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "BuildHeadway %s · %s/%s · %s\n%s\n", report.Version, report.OS, report.Arch, report.InstallMethod, report.Executable)
	for _, check := range report.Checks {
		fmt.Fprintf(&out, "\n[%s] %s: %s\n", check.Status, check.Name, check.Detail)
		if check.Fix != "" {
			fmt.Fprintln(&out, "  "+check.Fix)
		}
	}
	out.WriteString("\nCoding agents (version probes only; no model calls):\n")
	for _, agent := range report.Agents {
		status := "not installed"
		if agent.Installed {
			status = agent.Version + " · " + agent.Path
		}
		if agent.ExtensionOnly {
			status = "editor extension only; CLI missing"
		}
		if agent.ProbeError != "" {
			status += " · " + agent.ProbeError
		}
		fmt.Fprintf(&out, "  %s: %s\n", agent.Name, status)
	}
	return strings.TrimSpace(out.String())
}
