package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type ProjectConfig struct {
	MaxPromptBytes        int                  `json:"max_prompt_bytes,omitempty"`
	MCPServers            map[string]MCPServer `json:"mcp_servers,omitempty"`
	Acceptance            []AcceptanceCheck    `json:"acceptance,omitempty"`
	Hooks                 []ProjectHook        `json:"hooks,omitempty"`
	Version               int                  `json:"version"`
	Instructions          string               `json:"instructions,omitempty"`
	Checks                []string             `json:"checks"`
	PlatformChecks        map[string][]string  `json:"platform_checks,omitempty"`
	ContextFiles          []string             `json:"context_files,omitempty"`
	MaxCycles             int                  `json:"max_cycles,omitempty"`
	MaxPlannerCalls       int                  `json:"max_planner_calls,omitempty"`
	MaxWorkerCalls        int                  `json:"max_worker_calls,omitempty"`
	MaxStalledCycles      *int                 `json:"max_stalled_cycles,omitempty"`
	MaxRuntimeSeconds     int                  `json:"max_runtime_seconds,omitempty"`
	CheckTimeoutSeconds   int                  `json:"check_timeout_seconds"`
	PlannerTimeoutSeconds int                  `json:"planner_timeout_seconds"`
	WorkerTimeoutSeconds  int                  `json:"worker_timeout_seconds"`
}

func DefaultProjectConfig() ProjectConfig {
	return ProjectConfig{Version: 1, Checks: []string{}, CheckTimeoutSeconds: 600, PlannerTimeoutSeconds: 900, WorkerTimeoutSeconds: 1800}
}
func LoadProjectConfig(repo string) (ProjectConfig, error) {
	config := DefaultProjectConfig()
	file, err := os.Open(filepath.Join(repo, ".buildheadway.json"))
	if errors.Is(err, os.ErrNotExist) {
		file, err = os.Open(filepath.Join(repo, ".codentrail.json"))
	}
	if errors.Is(err, os.ErrNotExist) {
		file, err = os.Open(filepath.Join(repo, ".forge.json"))
	}
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	defer file.Close()
	data, err := ReadLimited(file, 128<<10)
	if err != nil {
		return config, err
	}
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return config, errors.New(".buildheadway.json must contain one JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("invalid .buildheadway.json: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return config, errors.New(".buildheadway.json must contain one JSON object")
	}
	return config, ValidateProjectConfig(config)
}
func ValidateProjectConfig(config ProjectConfig) error {
	if !validPromptLimit(config.MaxPromptBytes) {
		return errors.New("max_prompt_bytes must be zero or between 16384 and 8388608")
	}
	if err := validateMCPServers(config.MCPServers); err != nil {
		return err
	}
	if err := validateAcceptance(config.Acceptance); err != nil {
		return err
	}
	if err := validateHooks(config.Hooks); err != nil {
		return err
	}
	if config.Version != 1 {
		return errors.New("unsupported .buildheadway.json version")
	}
	if len(config.Instructions) > 16000 || len(config.ContextFiles) > 20 || len(config.Checks) > 30 {
		return errors.New("project configuration exceeds its size limit")
	}
	for _, value := range []int{config.MaxCycles, config.MaxPlannerCalls, config.MaxWorkerCalls, config.MaxRuntimeSeconds} {
		if value < 0 || value > 1000000 {
			return errors.New("project limits must be between zero and 1000000")
		}
	}
	if config.MaxStalledCycles != nil && (*config.MaxStalledCycles < 0 || *config.MaxStalledCycles > 1000000) {
		return errors.New("invalid max_stalled_cycles")
	}
	for _, seconds := range []int{config.CheckTimeoutSeconds, config.PlannerTimeoutSeconds, config.WorkerTimeoutSeconds} {
		if seconds < 1 || seconds > 86400 {
			return errors.New("project command timeouts must be from 1 to 86400 seconds")
		}
	}
	checks := append([]string{}, config.Checks...)
	for platform, commands := range config.PlatformChecks {
		if platform != "windows" && platform != "darwin" && platform != "linux" {
			return fmt.Errorf("unknown platform_checks key %q", platform)
		}
		if len(commands) > 30 {
			return errors.New("too many platform checks")
		}
		checks = append(checks, commands...)
	}
	for _, command := range checks {
		if strings.TrimSpace(command) == "" || len(command) > 16000 {
			return errors.New("invalid project check command")
		}
	}
	for _, name := range config.ContextFiles {
		if !localContextPath(name) {
			return fmt.Errorf("context file must be a relative project path: %s", name)
		}
	}
	return nil
}
func (c ProjectConfig) RequiredChecks() []string {
	return Unique(append(append(append([]string{}, c.Checks...), c.PlatformChecks[runtime.GOOS]...), acceptanceCommands(c.Acceptance)...))
}
func localContextPath(name string) bool {
	name = strings.ReplaceAll(name, `\`, "/")
	if name == "" || strings.Contains(name, ":") || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == ".forge" || part == ".git" {
			return false
		}
	}
	return true
}
func ProjectContext(repo string, config ProjectConfig) (string, error) {
	var out strings.Builder
	if config.Instructions != "" {
		out.WriteString("Project preferences:\n" + config.Instructions + "\n")
	}
	files := append([]string{}, config.ContextFiles...)
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "FORGE.md", "CODENTRAIL.md", "BUILDHEADWAY.md"} {
		if _, err := os.Lstat(filepath.Join(repo, name)); err == nil {
			files = append(files, name)
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return "", err
	}
	for _, name := range Unique(files) {
		if !localContextPath(name) {
			return "", fmt.Errorf("invalid context path: %s", name)
		}
		path, err := filepath.EvalSymlinks(filepath.Join(repo, filepath.FromSlash(strings.ReplaceAll(name, `\`, "/"))))
		if err != nil {
			return "", fmt.Errorf("context file %s: %w", name, err)
		}
		relative, err := filepath.Rel(resolvedRoot, path)
		if err != nil || !localContextPath(filepath.ToSlash(relative)) {
			return "", fmt.Errorf("context file escapes the project: %s", name)
		}
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			return "", fmt.Errorf("context file is not a regular file: %s", name)
		}
		data, err := ReadLimited(file, 64<<10)
		file.Close()
		if err != nil {
			return "", fmt.Errorf("context file %s: %w", name, err)
		}
		if bytes.ContainsRune(data, 0) {
			return "", fmt.Errorf("context file is binary: %s", name)
		}
		out.WriteString("\nFILE " + name + ":\n" + string(data) + "\n")
		if out.Len() > 128<<10 {
			return "", errors.New("project context exceeds 128 KiB; choose smaller context_files")
		}
	}
	return out.String(), nil
}
func InitProject(repo string) ([]string, error) {
	if _, err := LoadProjectConfig(repo); err != nil {
		return nil, err
	}
	config := DefaultProjectConfig()
	data, _ := json.MarshalIndent(config, "", "  ")
	contents := []struct {
		name string
		data []byte
	}{
		{".buildheadway.json", append(data, '\n')},
		{"BUILDHEADWAY.md", []byte("# Project guidance\n\nDescribe this project's purpose, conventions, and useful commands here.\nBuildHeadway includes this file in planning and implementation context.\n\n## Preferences\n\n- Keep changes focused on the requested goal.\n- Verify behavior with the project's meaningful checks.\n")},
	}
	created := []string{}
	for _, item := range contents {
		if err := WriteNewFile(filepath.Join(repo, item.name), item.data); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return created, err
		}
		created = append(created, item.name)
	}
	return created, ensureIgnore(repo)
}
func WriteNewFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func newID(prefix string) (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return prefix + time.Now().UTC().Format("20060102T150405Z-") + hex.EncodeToString(bytes), nil
}
func acquireProjectLock(repo, name string, wait bool) (*os.File, error) {
	if err := os.MkdirAll(filepath.Join(repo, ".forge"), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(repo, ".forge", name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := lockFile(file); err == nil {
			return file, nil
		}
		if !wait || time.Now().After(deadline) {
			file.Close()
			return nil, errors.New("BuildHeadway is already using this project resource; try again shortly")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type MemoryEntry struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}
type ProjectMemory struct {
	Version int           `json:"version"`
	Entries []MemoryEntry `json:"entries"`
}

func ReadMemory(repo string) (ProjectMemory, error) {
	memory := ProjectMemory{Version: 1, Entries: []MemoryEntry{}}
	err := ReadJSON(filepath.Join(repo, ".forge", "memory.json"), &memory)
	if errors.Is(err, os.ErrNotExist) {
		return memory, nil
	}
	if err != nil {
		return memory, err
	}
	if memory.Version != 1 || len(memory.Entries) > 64 {
		return memory, errors.New("invalid project memory")
	}
	ids := map[string]bool{}
	for _, entry := range memory.Entries {
		if !validRunID.MatchString(entry.ID) || ids[entry.ID] || len(entry.Text) > 2000 || strings.TrimSpace(entry.Text) == "" {
			return memory, errors.New("invalid project memory entry")
		}
		ids[entry.ID] = true
	}
	return memory, nil
}
func Remember(repo, text string) (MemoryEntry, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 2000 {
		return MemoryEntry{}, errors.New("a memory note must contain 1 to 2000 bytes")
	}
	lock, err := acquireProjectLock(repo, "memory.lock", true)
	if err != nil {
		return MemoryEntry{}, err
	}
	defer lock.Close()
	memory, err := ReadMemory(repo)
	if err != nil {
		return MemoryEntry{}, err
	}
	for _, entry := range memory.Entries {
		if entry.Text == text {
			return entry, nil
		}
	}
	if len(memory.Entries) >= 64 {
		return MemoryEntry{}, errors.New("project memory is full; buildheadwayt an old note first")
	}
	id, err := newID("m-")
	if err != nil {
		return MemoryEntry{}, err
	}
	entry := MemoryEntry{id, text, time.Now().UTC().Format(time.RFC3339)}
	memory.Entries = append(memory.Entries, entry)
	return entry, AtomicJSON(filepath.Join(repo, ".forge", "memory.json"), memory)
}
func BuildHeadwayt(repo, id string) error {
	lock, err := acquireProjectLock(repo, "memory.lock", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	memory, err := ReadMemory(repo)
	if err != nil {
		return err
	}
	for i, entry := range memory.Entries {
		if entry.ID == id {
			memory.Entries = append(memory.Entries[:i], memory.Entries[i+1:]...)
			return AtomicJSON(filepath.Join(repo, ".forge", "memory.json"), memory)
		}
	}
	return errors.New("memory note not found; use /memory to see IDs")
}
func FormatMemory(memory ProjectMemory) string {
	if len(memory.Entries) == 0 {
		return "No project notes yet. Use /remember TEXT."
	}
	var out strings.Builder
	for _, entry := range memory.Entries {
		fmt.Fprintf(&out, "%s\n  %s\n", entry.ID, entry.Text)
	}
	return strings.TrimSpace(out.String())
}
func (r *Runner) applyConfig() {
	config := r.State.Config
	r.MaxPromptBytes = config.MaxPromptBytes
	r.MaxCycles, r.MaxCalls, r.MaxWorkerCalls = config.MaxCycles, config.MaxPlannerCalls, config.MaxWorkerCalls
	r.MaxRuntime = time.Duration(config.MaxRuntimeSeconds) * time.Second
	r.MaxStalledCycles = 3
	if config.MaxStalledCycles != nil {
		r.MaxStalledCycles = *config.MaxStalledCycles
	}
}
func (r *Runner) refreshProjectContext() error {
	context, err := ProjectContext(r.Repo, r.State.Config)
	if err != nil {
		return err
	}
	memory, err := ReadMemory(r.Repo)
	if err != nil {
		return err
	}
	r.State.ProjectContext, r.State.ProjectMemory = context, memory.Entries
	return nil
}
func (r *Runner) runtimeContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if r.MaxRuntime <= 0 {
		return ctx, func() {}, nil
	}
	remaining := r.MaxRuntime - time.Duration(r.State.ActiveMillis)*time.Millisecond
	if remaining <= 0 {
		return ctx, func() {}, limitError{"runtime budget reached; resume with a higher --max-runtime"}
	}
	next, cancel := context.WithTimeout(ctx, remaining)
	return next, cancel, nil
}
