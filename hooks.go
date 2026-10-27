package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Verification hooks are repeatable local checks/formatters, not notifications
// or one-shot actions. They run again after interruption, just like test commands.
type ProjectHook struct {
	Name           string   `json:"name"`
	Event          string   `json:"event"`
	Command        string   `json:"command"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	Platforms      []string `json:"platforms,omitempty"`
}
type HookResult struct {
	ExecutionEvidence
	Name            string `json:"name"`
	Event           string `json:"event"`
	Command         string `json:"command"`
	ExitCode        int    `json:"exit_code"`
	Output          string `json:"output"`
	Log             string `json:"log,omitempty"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	SourceChanged   bool   `json:"source_changed,omitempty"`
}

func (h HookResult) Passed() bool {
	return h.ExitCode == 0 && !h.SourceChanged && h.FailureKind == "" && h.Error == ""
}

type HookInput struct {
	Version int           `json:"version"`
	Event   string        `json:"event"`
	Name    string        `json:"name"`
	Project string        `json:"project"`
	RunID   string        `json:"run_id"`
	Goal    string        `json:"goal,omitempty"`
	Checks  []CheckResult `json:"checks"`
}

var errHookChangedFiles = errors.New("project files changed during verification; all checks must run again")

func validateHooks(hooks []ProjectHook) error {
	if len(hooks) > 20 {
		return errors.New("configure at most 20 verification hooks")
	}
	names := map[string]bool{}
	for _, hook := range hooks {
		if !workflowName.MatchString(hook.Name) || names[hook.Name] {
			return errors.New("hooks need unique lowercase names using letters, digits and hyphens")
		}
		names[hook.Name] = true
		if hook.Event != "before_check" && hook.Event != "after_check" {
			return fmt.Errorf("hook %s: event must be before_check or after_check", hook.Name)
		}
		if strings.TrimSpace(hook.Command) == "" || len(hook.Command) > 16000 || strings.ContainsRune(hook.Command, 0) {
			return fmt.Errorf("hook %s: invalid command", hook.Name)
		}
		if hook.TimeoutSeconds < 0 || hook.TimeoutSeconds > 3600 {
			return fmt.Errorf("hook %s: timeout must be 0 (120 seconds) or 1–3600 seconds", hook.Name)
		}
		for _, platform := range hook.Platforms {
			if platform != "linux" && platform != "darwin" && platform != "windows" {
				return fmt.Errorf("hook %s: invalid platform %q", hook.Name, platform)
			}
		}
	}
	return nil
}
func activeHooks(config ProjectConfig, event string) []ProjectHook {
	var out []ProjectHook
	for _, hook := range config.Hooks {
		if (event == "" || hook.Event == event) && (len(hook.Platforms) == 0 || contains(hook.Platforms, runtime.GOOS)) {
			out = append(out, hook)
		}
	}
	return out
}
func hooksPassed(config ProjectConfig, results []HookResult) bool {
	for _, hook := range activeHooks(config, "") {
		found := false
		for _, result := range results {
			if result.Name == hook.Name && result.Event == hook.Event && result.Command == hook.Command {
				if !result.Passed() {
					return false
				}
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func runHooks(ctx context.Context, paths Paths, repo, dir string, config ProjectConfig, input HookInput, emit Emit, before func(ProjectHook) error, record func(HookResult) error) error {
	for _, hook := range activeHooks(config, input.Event) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if before != nil {
			if err := before(hook); err != nil {
				return err
			}
		}
		input.Version, input.Name, input.Project = 1, hook.Name, repo
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		seconds := hook.TimeoutSeconds
		if seconds == 0 {
			seconds = 120
		}
		emit(Event{Kind: "check", Text: "Hook " + hook.Name + " · " + hook.Event})
		env := setEnv(setEnv(setEnv(ToolEnv(paths), "CI", "1"), "BUILDHEADWAY_HOOK_EVENT", hook.Event), "BUILDHEADWAY_PROJECT", repo)
		env = setEnv(setEnv(env, "FORGE_HOOK_EVENT", hook.Event), "FORGE_PROJECT", repo)
		env = setEnv(setEnv(env, "CODENTRAIL_HOOK_EVENT", hook.Event), "CODENTRAIL_PROJECT", repo)
		log := fmt.Sprintf("%s/%s-hook-%s.log", dir, time.Now().UTC().Format("150405.000000000"), hook.Name)
		started := time.Now()
		result, runErr := RunProcess(ctx, ShellCommand(hook.Command), repo, log, string(data)+"\n", env, time.Duration(seconds)*time.Second, nil)
		result, evidence := verificationEvidence(ctx, result, runErr, started)
		recordErr := record(HookResult{ExecutionEvidence: evidence, Name: hook.Name, Event: hook.Event, Command: hook.Command, ExitCode: result.Code, Output: Tail(result.Output, 7000), Log: filepath.Base(log), OutputTruncated: result.OutputTruncated || len(result.Output) > 7000})
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), recordErr)
		}
		if recordErr != nil {
			return recordErr
		}
		emit(Event{Kind: "check-result", Text: fmt.Sprintf("Hook %s · exit %d", hook.Name, result.Code)})
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}
func verificationRound(ctx context.Context, paths Paths, repo, dir string, config ProjectConfig, input HookInput, commands []string, demo bool, emit Emit, before func(ProjectHook) error, recordHook func(HookResult) error, recordCheck func(CheckResult) error) (string, error) {
	if emit == nil {
		emit = noEmit
	}
	input.Event, input.Checks = "before_check", []CheckResult{}
	if err := runHooks(ctx, paths, repo, dir, config, input, emit, before, recordHook); err != nil {
		return "", err
	}
	// Pre-check formatters can create a manifest; discover its checks as well.
	commands = Unique(append(commands, SuggestedChecks(repo, paths)...))
	checkedSnapshot, err := WorkspaceSnapshot(ctx, repo)
	if err != nil {
		return "", err
	}
	unchanged := func(boundary string) error {
		snapshot, err := WorkspaceSnapshot(ctx, repo)
		if err != nil {
			return err
		}
		if snapshot != checkedSnapshot {
			return fmt.Errorf("%w (%s)", errHookChangedFiles, boundary)
		}
		return nil
	}
	err = runVerification(ctx, paths, repo, dir, commands, time.Duration(config.CheckTimeoutSeconds)*time.Second, demo, emit,
		func(command string) error { return unchanged("before check " + command) }, func(result CheckResult) error {
			boundaryErr := unchanged("after check " + result.Command)
			result.SourceChanged = errors.Is(boundaryErr, errHookChangedFiles)
			input.Checks = append(input.Checks, result)
			if err := recordCheck(result); err != nil {
				return err
			}
			if result.SourceChanged {
				emit(Event{Kind: "check-result", Text: "Invalidated: project source changed during " + result.Command})
			}
			return boundaryErr
		})
	if err != nil {
		return "", err
	}
	if err := unchanged("after recording checks"); err != nil {
		return "", err
	}
	if len(activeHooks(config, "after_check")) == 0 {
		return checkedSnapshot, nil
	}
	input.Event = "after_check"
	if err := runHooks(ctx, paths, repo, dir, config, input, emit, func(hook ProjectHook) error {
		if err := unchanged("before after_check hook " + hook.Name); err != nil {
			return err
		}
		if before != nil {
			return before(hook)
		}
		return nil
	}, func(result HookResult) error {
		boundaryErr := unchanged("after after_check hook " + result.Name)
		result.SourceChanged = errors.Is(boundaryErr, errHookChangedFiles)
		if err := recordHook(result); err != nil {
			return err
		}
		if result.SourceChanged {
			emit(Event{Kind: "check-result", Text: "Invalidated: project source changed during hook " + result.Name})
		}
		return boundaryErr
	}); err != nil {
		return "", err
	}
	if err := unchanged("after recording hooks"); err != nil {
		return "", err
	}
	return checkedSnapshot, nil
}

func sourceChangeNotice(changed bool) string {
	if changed {
		return "[Source changed during this command; verification evidence is invalid.]\n"
	}
	return ""
}
func FormatHooks(config ProjectConfig) string {
	var out strings.Builder
	out.WriteString("Verification hooks from .buildheadway.json (pinned when each goal starts).\n")
	for _, hook := range config.Hooks {
		platforms := "all platforms"
		if len(hook.Platforms) > 0 {
			platforms = strings.Join(hook.Platforms, ", ")
		}
		fmt.Fprintf(&out, "%s · %s · %s\n  %s\n", hook.Name, hook.Event, platforms, hook.Command)
	}
	if len(config.Hooks) == 0 {
		out.WriteString("No hooks configured. See docs/extensions.md for examples.\n")
	}
	return strings.TrimSpace(out.String())
}
