package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AgentDefinition struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
	Package string `json:"package,omitempty"`
	Adapter bool   `json:"adapter"`
}

var Registry = []AgentDefinition{
	{"codex", "Codex", "codex", "@openai/codex", true},
	{"claude", "Claude Code", "claude", "@anthropic-ai/claude-code", true},
	{"opencode", "OpenCode", "opencode", "opencode-ai", true},
	{"pi", "Pi", "pi", "@earendil-works/pi-coding-agent", true},
	{"cline", "Cline", "cline", "cline", true},
	{"gemini", "Gemini CLI", "gemini", "@google/gemini-cli", true},
	{"qwen", "Qwen Code", "qwen", "@qwen-code/qwen-code", false},
	{"goose", "Goose", "goose", "", false},
	{"aider", "Aider", "aider", "", false},
	{"kilo", "Kilo", "kilo", "", false},
	{"amp", "Amp", "amp", "", false},
	{"droid", "Droid", "droid", "", false},
}

type DetectedAgent struct {
	AgentDefinition
	Installed     bool     `json:"installed"`
	Path          string   `json:"path,omitempty"`
	Version       string   `json:"version,omitempty"`
	ProbeError    string   `json:"probe_error,omitempty"`
	ExtensionOnly bool     `json:"extension_only,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	Help          string   `json:"-"`
}
type Inventory struct {
	OS     string          `json:"os"`
	Arch   string          `json:"arch"`
	Agents []DetectedAgent `json:"agents"`
}

func itoa(value int) string { return strconv.Itoa(value) }
func ExecutableName(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}
func SearchDirs(paths Paths, goos, home, pathEnv string) []string {
	dirs := filepath.SplitList(pathEnv)
	if goos == "windows" {
		dirs = strings.Split(pathEnv, ";")
	}
	// Native per-user locations cover installations omitted by GUI-launched shells.
	extra := []string{
		filepath.Join(paths.Data, "tools", "node_modules", ".bin"),
		filepath.Join(home, ".local", "bin"), filepath.Join(home, ".opencode", "bin"),
		filepath.Join(home, ".bun", "bin"), filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, ".cargo", "bin"), filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".yarn", "bin"), filepath.Join(home, ".local", "share", "mise", "shims"),
	}
	if goos == "windows" {
		for _, base := range []string{os.Getenv("APPDATA"), filepath.Join(home, "AppData", "Roaming")} {
			if base != "" {
				extra = append(extra, filepath.Join(base, "npm"))
			}
		}
		if root := privateGitRoot(paths); root != "" {
			extra = append(extra, filepath.Join(root, "cmd"), filepath.Join(root, "bin"), filepath.Join(root, "usr", "bin"))
		}
		extra = append(extra,
			filepath.Join(home, "scoop", "shims"),
			filepath.Join(home, "AppData", "Local", "Microsoft", "WinGet", "Links"))
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			extra = append(extra, filepath.Join(programFiles, "nodejs"))
		}
	} else {
		extra = append(extra, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin")
	}
	patterns := []string{
		filepath.Join(home, ".nvm", "versions", "node", "*", "bin"),
		filepath.Join(home, ".local", "share", "fnm", "node-versions", "*", "installation", "bin"),
		filepath.Join(paths.Data, "runtime", "node-*", "bin"),
		filepath.Join(paths.Data, "runtime", "node-*"),
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		extra = append(extra, matches...)
	}
	dirs = append(dirs, extra...)
	if custom := brandEnv("BUILDHEADWAY_AGENT_PATH"); custom != "" {
		dirs = append(filepath.SplitList(custom), dirs...)
	}
	out := []string{}
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" || dir == "." {
			continue
		}
		dir = filepath.Clean(dir)
		key := dir
		if goos == "windows" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, dir)
		}
	}
	return out
}
func FindInDirs(name string, dirs []string, goos string) string {
	suffixes := []string{""}
	if goos == "windows" {
		suffixes = []string{".exe", ".cmd", ".bat", ".com", "", ".EXE", ".CMD", ".BAT"}
	}
	for _, dir := range dirs {
		for _, suffix := range suffixes {
			path := filepath.Join(dir, name+suffix)
			info, err := os.Stat(path)
			if err == nil && !info.IsDir() && (goos == "windows" || info.Mode()&0111 != 0) {
				absolute, _ := filepath.Abs(path)
				return absolute
			}
		}
	}
	return ""
}
func FindTool(name string, paths Paths) string {
	home, _ := os.UserHomeDir()
	return FindInDirs(name, SearchDirs(paths, runtime.GOOS, home, os.Getenv("PATH")), runtime.GOOS)
}
func findNodeForShim(shim string, env []string) string {
	if candidate := FindInDirs("node", []string{filepath.Dir(shim)}, "windows"); candidate != "" {
		return candidate
	}
	pathEnv := os.Getenv("PATH")
	for _, entry := range env {
		if strings.HasPrefix(strings.ToUpper(entry), "PATH=") {
			pathEnv = entry[5:]
		}
	}
	paths, _ := UserPaths()
	home, _ := os.UserHomeDir()
	return FindInDirs("node", SearchDirs(paths, "windows", home, pathEnv), "windows")
}
func ToolEnv(paths Paths) []string {
	home, _ := os.UserHomeDir()
	dirs := SearchDirs(paths, runtime.GOOS, home, os.Getenv("PATH"))
	if node := RuntimeNode(paths); node.Node != "" {
		dirs = append([]string{filepath.Dir(node.Node)}, dirs...)
	}
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "PATH=") {
			env = append(env, entry)
		}
	}
	env = append(env, "PATH="+strings.Join(dirs, string(os.PathListSeparator)))
	if bash := FindGitBash(paths); bash != "" {
		env = setEnv(env, "OPENCODE_GIT_BASH_PATH", bash)
		env = setEnv(env, "CLAUDE_CODE_GIT_BASH_PATH", bash)
	}
	return env
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func Clean(text string) string {
	text = ansiRE.ReplaceAllString(text, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 32 {
			return r
		}
		return -1
	}, text)
}
func ScanAgents(ctx context.Context, paths Paths) Inventory {
	out := Inventory{OS: runtime.GOOS, Arch: runtime.GOARCH, Agents: make([]DetectedAgent, len(Registry))}
	var wg sync.WaitGroup
	for index, definition := range Registry {
		wg.Add(1)
		go func(index int, definition AgentDefinition) {
			defer wg.Done()
			agent := DetectedAgent{AgentDefinition: definition}
			agent.Path = FindTool(definition.Command, paths)
			agent.Installed = agent.Path != ""
			if agent.Installed {
				probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				defer cancel()
				cmd, err := PreparedCommand(probeCtx, []string{agent.Path, "--version"}, ToolEnv(paths))
				if err == nil {
					cmd.Env = ToolEnv(paths)
					configureProcess(cmd)
					output, runErr := cmd.CombinedOutput()
					if runErr == nil {
						agent.Version = strings.Split(strings.TrimSpace(Clean(string(output))), "\n")[0]
					}
					if runErr != nil {
						agent.ProbeError = "Version check failed; reconnect to repair this installation."
					}
				} else {
					agent.ProbeError = err.Error()
				}
				if definition.Adapter {
					agent.Capabilities = []string{"planner", "native-auth"}
				}
				if definition.ID == "opencode" {
					agent.Capabilities = append(agent.Capabilities, "verified-free-worker")
				}
			} else if definition.ID == "cline" {
				home, _ := os.UserHomeDir()
				for _, folder := range []string{".vscode", ".vscode-insiders", ".vscode-oss"} {
					matches, _ := filepath.Glob(filepath.Join(home, folder, "extensions", "saoudrizwan.claude-dev-*"))
					if len(matches) > 0 {
						agent.ExtensionOnly = true
					}
				}
			}
			out.Agents[index] = agent
		}(index, definition)
	}
	wg.Wait()
	return out
}
func (inventory Inventory) Find(id string) (DetectedAgent, bool) {
	for _, agent := range inventory.Agents {
		if agent.ID == id {
			return agent, true
		}
	}
	return DetectedAgent{}, false
}
func ResolveConnection(connection Connection, paths Paths) (string, error) {
	if connection.Executable != "" {
		if info, err := os.Stat(connection.Executable); err == nil && !info.IsDir() {
			return connection.Executable, nil
		}
	}
	if path := FindTool(connection.Backend, paths); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("%s is not installed; open /connect and BuildHeadway will install it", connection.Backend)
}
func FeatureHelp(ctx context.Context, connection Connection, paths Paths) string {
	path, err := ResolveConnection(connection, paths)
	if err != nil {
		return ""
	}
	args := []string{path, "--help"}
	if connection.Backend == "opencode" {
		args = []string{path, "run", "--help"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd, err := PreparedCommand(probeCtx, args, ToolEnv(paths))
	if err != nil {
		return ""
	}
	cmd.Env = ToolEnv(paths)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return Clean(string(output))
}
func AuthCommand(ctx context.Context, connection Connection, paths Paths) (*exec.Cmd, error) {
	path, err := ResolveConnection(connection, paths)
	if err != nil {
		return nil, err
	}
	args := []string{path}
	switch connection.Backend {
	case "codex":
		args = append(args, "login")
	case "claude":
		args = append(args, "auth", "login")
	case "opencode":
		args = append(args, "auth", "login")
	case "cline":
		args = append(args, "auth")
	case "pi", "gemini": // Native terminal provides sign-in.
	default:
		return nil, fmt.Errorf("no guided sign-in adapter for %s", connection.Backend)
	}
	cmd, err := PreparedCommand(ctx, args, ToolEnv(paths))
	if err != nil {
		return nil, err
	}
	cmd.Env = ToolEnv(paths)
	return cmd, nil
}
func Authenticated(ctx context.Context, connection Connection, paths Paths) bool {
	path, err := ResolveConnection(connection, paths)
	if err != nil {
		return false
	}
	args := []string{path}
	switch connection.Backend {
	case "codex":
		args = append(args, "login", "status")
	case "claude":
		args = append(args, "auth", "status")
	default:
		return true // Unknown auth status is not presented as verified.
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd, err := PreparedCommand(ctx, args, ToolEnv(paths))
	if err != nil {
		return false
	}
	cmd.Env = ToolEnv(paths)
	return cmd.Run() == nil
}
