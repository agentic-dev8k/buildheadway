package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const Version = "0.19.1"

type Paths struct {
	Config string `json:"config"`
	Data   string `json:"data"`
	Bin    string `json:"bin"`
}

func UserPaths() (Paths, error) {
	return userPathsForBrand(true)
}
func userPathsForBrand(allowLegacy bool) (Paths, error) {
	if home := brandEnv("BUILDHEADWAY_HOME"); home != "" {
		return Paths{filepath.Join(home, "config"), filepath.Join(home, "data"), filepath.Join(home, "bin")}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	if allowLegacy {
		current, err := userPathsForBrand(false)
		if err != nil {
			return Paths{}, err
		}
		if !directoryExists(current.Config) && !directoryExists(current.Data) {
			for _, brand := range []string{"Codentrail", "Forge"} {
				legacy := current
				legacy.Config = previousBrandPath(current.Config, brand)
				legacy.Data = previousBrandPath(current.Data, brand)
				if runtime.GOOS == "windows" {
					legacy.Bin = filepath.Join(legacy.Data, "bin")
				}
				if directoryExists(legacy.Config) || directoryExists(legacy.Data) {
					return legacy, nil
				}
			}
		}
		return current, nil
	}
	switch runtime.GOOS {
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		return Paths{filepath.Join(config, "BuildHeadway"), filepath.Join(local, "BuildHeadway"), filepath.Join(local, "BuildHeadway", "bin")}, nil
	case "darwin":
		return Paths{filepath.Join(config, "BuildHeadway"), filepath.Join(home, "Library", "Application Support", "BuildHeadway"), filepath.Join(home, ".local", "bin")}, nil
	default:
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		return Paths{filepath.Join(config, "buildheadway"), filepath.Join(data, "buildheadway"), filepath.Join(home, ".local", "bin")}, nil
	}
}

func (p Paths) Ensure() error {
	for _, dir := range []string{p.Config, p.Data} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

type Connection struct {
	Backend     string `json:"backend"`
	Model       string `json:"model,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Executable  string `json:"executable,omitempty"`
	Reasoning   string `json:"reasoning,omitempty"`
	RequireFree bool   `json:"-"`
}
type Settings struct {
	Version int           `json:"version"`
	Planner Connection    `json:"planner"`
	Worker  Connection    `json:"worker"`
	Seniors []SeniorAgent `json:"seniors,omitempty"`
}

func LoadSettings(paths Paths) (Settings, error) {
	var settings Settings
	err := ReadJSON(filepath.Join(paths.Config, "connection.json"), &settings)
	if errors.Is(err, os.ErrNotExist) {
		home, _ := os.UserHomeDir()
		legacy := filepath.Join(home, ".config", "forge", "connection.json")
		if legacy != filepath.Join(paths.Config, "connection.json") {
			err = ReadJSON(legacy, &settings)
			if err == nil {
				err = SaveSettings(paths, settings)
			}
		}
	}
	if err != nil {
		return settings, err
	}
	if !SupportedBackend(settings.Planner.Backend) {
		return settings, errors.New("choose a supported planner connection")
	}
	if settings.Worker.Backend == "" {
		settings.Worker = Connection{Backend: "opencode", Model: "auto"}
	}
	_, err = FigureOutTeam(settings)
	return settings, err
}
func SaveSettings(paths Paths, settings Settings) error {
	if _, err := FigureOutTeam(settings); err != nil {
		return err
	}
	settings.Version = 3
	if err := paths.Ensure(); err != nil {
		return err
	}
	return AtomicJSON(filepath.Join(paths.Config, "connection.json"), settings)
}
func SupportedBackend(name string) bool {
	switch name {
	case "codex", "claude", "opencode", "pi", "cline", "gemini", "demo":
		return true
	}
	return false
}

type Task struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Instructions string `json:"instructions"`
}
type Decision struct {
	Acceptance        []AcceptanceCheck  `json:"acceptance,omitempty"`
	Messages          []TeamRequest      `json:"messages,omitempty"`
	Done              bool               `json:"done"`
	Summary           string             `json:"summary"`
	Tasks             []Task             `json:"tasks"`
	Checks            []string           `json:"checks"`
	SuccessCriteria   []string           `json:"success_criteria"`
	CheckReplacements []CheckReplacement `json:"check_replacements,omitempty"`
}
type CheckReplacement struct {
	Old    string `json:"old"`
	New    string `json:"new"`
	Reason string `json:"reason"`
}
type TaskRecord struct {
	Key        string `json:"key"`
	Cycle      int    `json:"cycle"`
	Task       Task   `json:"task"`
	Status     string `json:"status"`
	Attempts   int    `json:"attempts"`
	Model      string `json:"model,omitempty"`
	Report     string `json:"report,omitempty"`
	Error      string `json:"error,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}
type RunEvent struct {
	At   string `json:"at"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}
type CheckResult struct {
	Command         string `json:"command"`
	ExitCode        int    `json:"exit_code"`
	Output          string `json:"output"`
	Log             string `json:"log,omitempty"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	SourceChanged   bool   `json:"source_changed,omitempty"`
}
type WorkerResult struct {
	Reply  *WorkerReply `json:"communication,omitempty"`
	Task   string       `json:"task"`
	Model  string       `json:"model,omitempty"`
	OK     bool         `json:"ok"`
	Report string       `json:"report,omitempty"`
	Error  string       `json:"error,omitempty"`
}
type State struct {
	ModelHealth           []ModelHealth      `json:"model_health,omitempty"`
	RoutingWait           *RoutingWait       `json:"routing_wait,omitempty"`
	Acceptance            []AcceptanceCheck  `json:"acceptance,omitempty"`
	Steering              []SteeringMessage  `json:"steering,omitempty"`
	HookResults           []HookResult       `json:"hook_results,omitempty"`
	ActiveHook            string             `json:"active_hook,omitempty"`
	HookRechecks          int                `json:"hook_rechecks,omitempty"`
	Dialogue              *DialogueState     `json:"dialogue,omitempty"`
	WorkerSession         *WorkerSession     `json:"worker_session,omitempty"`
	Mode                  string             `json:"mode,omitempty"`
	Access                string             `json:"access,omitempty"`
	Team                  []SeniorAgent      `json:"team,omitempty"`
	Council               *CouncilRound      `json:"council,omitempty"`
	Discussion            []AgentMessage     `json:"discussion,omitempty"`
	MessageSequence       int                `json:"message_sequence,omitempty"`
	SeniorModels          map[string]string  `json:"senior_models,omitempty"`
	WorkerConnection      *Connection        `json:"worker_connection,omitempty"`
	Version               int                `json:"version"`
	RunID                 string             `json:"run_id"`
	Goal                  string             `json:"goal"`
	Status                string             `json:"status"`
	Phase                 string             `json:"phase"`
	Cycle                 int                `json:"cycle"`
	PlannerCalls          int                `json:"planner_calls"`
	PlannerModel          string             `json:"planner_model,omitempty"`
	RequiredChecks        []string           `json:"required_checks"`
	SuggestedChecks       []string           `json:"suggested_checks"`
	SuccessCriteria       []string           `json:"success_criteria"`
	Tasks                 []Task             `json:"tasks"`
	NextTask              int                `json:"next_task"`
	WorkerResults         []WorkerResult     `json:"worker_results"`
	CheckResults          []CheckResult      `json:"check_results"`
	History               []string           `json:"history"`
	Summary               string             `json:"summary,omitempty"`
	Error                 string             `json:"error,omitempty"`
	UpdatedAt             string             `json:"updated_at"`
	TaskHistory           []TaskRecord       `json:"task_history"`
	ActiveTask            string             `json:"active_task,omitempty"`
	WorkerCalls           int                `json:"worker_calls"`
	Events                []RunEvent         `json:"events"`
	CheckedSnapshot       string             `json:"checked_snapshot,omitempty"`
	CheckedAt             string             `json:"checked_at,omitempty"`
	VerificationSignature string             `json:"verification_signature,omitempty"`
	StalledCycles         int                `json:"stalled_cycles"`
	RecoveryNote          string             `json:"recovery_note,omitempty"`
	RetiredChecks         []CheckReplacement `json:"retired_checks,omitempty"`
	Config                ProjectConfig      `json:"project_config"`
	ProjectContext        string             `json:"project_context,omitempty"`
	ProjectMemory         []MemoryEntry      `json:"project_memory,omitempty"`
	QueueID               string             `json:"queue_id,omitempty"`
	StartedAt             string             `json:"started_at,omitempty"`
	ActiveMillis          int64              `json:"active_millis"`
}

func (s *State) Save(dir string) error {
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return AtomicJSON(filepath.Join(dir, "state.json"), s)
}
func AtomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".buildheadway-write-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(name, path)
}
func ReadJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
func Tail(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[len(text)-limit:]
}
func Unique(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range values {
		if strings.TrimSpace(item) != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

type Event struct {
	Kind, Text string
	Value      any
}
type Emit func(Event)

func noEmit(Event) {}
