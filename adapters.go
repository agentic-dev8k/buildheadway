package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type helpCacheEntry struct{ Global, Run string }

var helpCache sync.Map

func AgentHelp(ctx context.Context, connection Connection, paths Paths) (helpCacheEntry, error) {
	executable, err := ResolveConnection(connection, paths)
	if err != nil {
		return helpCacheEntry{}, err
	}
	info, err := os.Stat(executable)
	if err != nil {
		return helpCacheEntry{}, err
	}
	key := executable + info.ModTime().String() + itoa(int(info.Size()))
	if cached, ok := helpCache.Load(key); ok {
		return cached.(helpCacheEntry), nil
	}
	result := helpCacheEntry{}
	for index, args := range [][]string{{"--help"}, {"exec", "--help"}} {
		if index == 1 && connection.Backend != "codex" && connection.Backend != "opencode" {
			break
		}
		if index == 1 && connection.Backend == "opencode" {
			args = []string{"run", "--help"}
		}
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		cmd, err := PreparedCommand(probeCtx, append([]string{executable}, args...), ToolEnv(paths))
		if err != nil {
			cancel()
			return result, err
		}
		cmd.Env = ToolEnv(paths)
		data, runErr := cmd.CombinedOutput()
		cancel()
		if runErr != nil {
			return result, fmt.Errorf("%s help check failed: %w", connection.Backend, runErr)
		}
		if index == 0 {
			result.Global = Clean(string(data))
		} else {
			result.Run = Clean(string(data))
		}
	}
	helpCache.Store(key, result)
	return result, nil
}
func Compatible(ctx context.Context, connection Connection, paths Paths) error {
	help, err := AgentHelp(ctx, connection, paths)
	if err != nil {
		return err
	}
	needed := []string{}
	text := help.Global
	switch connection.Backend {
	case "codex":
		needed = []string{"--output-schema", "--output-last-message", "--sandbox"}
		text = help.Run
	case "claude":
		needed = []string{"--output-format", "--json-schema", "--tools", "--bare"}
	case "opencode":
		if !strings.Contains(help.Global, "--pure") {
			return errors.New("this OpenCode version lacks plugin-free runs")
		}
		needed = []string{"--format", "--model", "--file", "--dir"}
		text = help.Run
	case "pi":
		needed = []string{"--mode", "--print", "--tools", "--no-session", "--no-extensions"}
	case "cline":
		needed = []string{"--json", "--plan", "--yolo"}
	case "gemini":
		needed = []string{"--output-format", "--approval-mode", "--prompt"}
	default:
		return errors.New("no adapter is registered for this agent")
	}
	for _, flag := range needed {
		if !strings.Contains(text, flag) {
			return fmt.Errorf("the installed %s does not support %s", connection.Backend, flag)
		}
	}
	return nil
}
func (r *Runner) discoverModels(ctx context.Context) ([]string, error) {
	connection := r.Settings.Worker
	if connection.Backend != "opencode" {
		return nil, errors.New("automatic free-price verification currently requires the OpenCode worker adapter")
	}
	executable, err := ResolveConnection(connection, r.Paths)
	if err != nil {
		return nil, err
	}
	if err := Compatible(ctx, connection, r.Paths); err != nil {
		return nil, err
	}
	argv := []string{executable, "--pure", "models", "opencode", "--verbose", "--refresh"}
	r.Emit(Event{Kind: "status", Text: "Checking available free models"})
	result, err := RunProcess(ctx, argv, r.Repo, r.logPath("models"), "", ToolEnv(r.Paths), 90*time.Second, nil)
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("free-model discovery failed: %s", Tail(result.Output, 1600))
	}
	if err := completeAgentOutput(result); err != nil {
		return nil, err
	}
	models := ParseFreeModels(result.Output)
	if len(models) == 0 {
		return nil, errors.New("no models have verifiable zero pricing; BuildHeadway will not choose a paid worker")
	}
	return models, nil
}
func opencodeEnv(paths Paths, model string, planner bool, executedChecks ...string) []string {
	return opencodeAccessEnv(paths, model, planner, false, executedChecks...)
}
func opencodeAccessEnv(paths Paths, model string, planner, full bool, executedChecks ...string) []string {
	permission := map[string]any{"*": "allow", "question": "deny", "task": "deny", "external_directory": "deny"}
	agent := "build"
	if planner {
		agent = "buildheadway-plan"
		// Keep the native plan agent's approval-gated tools visible. OpenCode's
		// free service rejects some tool-stripped requests. Headless runs without
		// --auto reject approval requests. File-edit tools remain approval-gated;
		// a reviewer may repeat the verification commands BuildHeadway already executed.
		// Native todo bookkeeping changes the agent's session, not project
		// files. Gating it can terminate a headless reply before its final JSON.
		permission = map[string]any{"*": "ask", "read": "allow", "glob": "allow", "grep": "allow", "list": "allow", "todowrite": "allow", "todoread": "allow", "question": "deny", "task": "deny", "external_directory": "deny"}
		bash := map[string]string{"*": "ask", "pwd": "allow", "ls": "allow", "ls -a": "allow", "ls -l": "allow", "ls -la": "allow", "ls -la .": "allow"}
		for _, command := range executedChecks {
			// Permission rules are glob patterns; never turn a check containing
			// wildcards into a broader command grant.
			if command != "" && !strings.ContainsAny(command, "*?[]") {
				bash[command] = "allow"
			}
		}
		permission["bash"] = bash
		permission["shell"] = bash
	}
	if full {
		permission = map[string]any{"*": "allow", "question": "deny", "task": "deny", "external_directory": "allow"}
	}
	provider := strings.SplitN(model, "/", 2)[0]
	agentConfig := map[string]any{"model": model, "permission": permission}
	if planner {
		agentConfig["mode"] = "primary"
		agentConfig["description"] = "BuildHeadway planning and review"
		agentConfig["prompt"] = "You plan, advise and review code for BuildHeadway. Read the actual project files with read, glob and grep. Do not write a plan file or request file edits, arbitrary shell commands, native delegation or user questions. Internal team messages in the attached JSON schema are supported: BuildHeadway routes them to your teammates and returns their answers. Use executed verification evidence; during review you may repeat exact executed checks. Separate workers perform implementation. Follow the attached JSON schema and return that JSON object as your final answer."
	}
	config := map[string]any{
		"model": model, "small_model": model, "enabled_providers": []string{provider},
		"permission": permission, "agent": map[string]any{agent: agentConfig},
	}
	data, _ := json.Marshal(config)
	env := setEnv(ToolEnv(paths), "OPENCODE_CONFIG_CONTENT", string(data))
	permissions, _ := json.Marshal(permission)
	env = setEnv(env, "OPENCODE_PERMISSION", string(permissions))
	return setEnv(env, "OPENCODE_DISABLE_DEFAULT_PLUGINS", "true")
}

func openCodeRunArgs(executable, model, promptPath, repo string, planner, supportsAuto bool) []string {
	args := []string{executable, "--pure", "run", "Complete the instructions in the attached file.", "--model", model, "--format", "json", "--dir", repo}
	agent := "build"
	if planner {
		agent = "buildheadway-plan"
	}
	args = append(args, "--agent", agent)
	if !planner && supportsAuto {
		args = append(args, "--auto")
	}
	return append(args, "--file", promptPath)
}
func (r *Runner) invokeCLI(ctx context.Context, connection Connection, prompt, label string, planner bool, model string) (string, error) {
	if connection.Backend == "demo" {
		return r.invokeDemo(ctx, prompt, planner)
	}
	executable, err := ResolveConnection(connection, r.Paths)
	if err != nil {
		return "", err
	}
	if model == "" {
		model = connection.Model
	}
	logPath := r.logPath(label)
	promptPath := strings.TrimSuffix(logPath, ".log") + ".prompt.txt"
	if err := os.WriteFile(promptPath, []byte(prompt), 0600); err != nil {
		return "", err
	}
	schemaPath := filepath.Join(r.Dir, "decision.schema.json")
	schema := r.decisionOutputSchema()
	fullAccess := r.fullAccess() && !strings.HasPrefix(label, "dialogue-")
	if planner {
		if err := os.WriteFile(schemaPath, []byte(schema), 0600); err != nil {
			return "", err
		}
	}
	resultPath := strings.TrimSuffix(logPath, ".log") + ".result.json"
	args := []string{executable}
	env := ToolEnv(r.Paths)
	switch connection.Backend {
	case "codex":
		sandbox := "workspace-write"
		if planner {
			sandbox = "read-only"
		}
		if fullAccess {
			args = append(args, "exec", "--dangerously-bypass-approvals-and-sandbox")
		} else {
			args = append(args, "-a", "never", "exec", "--sandbox", sandbox)
		}
		args = append(args, "--skip-git-repo-check", "--json", "--output-last-message", resultPath)
		if connection.Reasoning != "" {
			args = append(args, "-c", "model_reasoning_effort=\""+connection.Reasoning+"\"")
		}
		if planner {
			args = append(args, "--output-schema", schemaPath)
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		args = append(args, "-")
	case "claude":
		permissionMode := "dontAsk"
		if fullAccess {
			permissionMode = "bypassPermissions"
		}
		args = append(args, "--bare", "-p", "--output-format", "json", "--permission-mode", permissionMode, "--no-session-persistence")
		if planner {
			if !fullAccess {
				args = append(args, "--tools", "Read,Glob,Grep", "--disallowedTools", "mcp__*")
			}
			args = append(args, "--json-schema", schema)
		}
		if model != "" {
			args = append(args, "--model", model)
		}
	case "opencode":
		if model == "" || !strings.Contains(model, "/") {
			return "", errors.New("choose an explicit OpenCode provider/model")
		}
		help, err := AgentHelp(ctx, connection, r.Paths)
		if err != nil {
			return "", err
		}
		args = openCodeRunArgs(executable, model, promptPath, r.Repo, planner, strings.Contains(help.Run, "--auto"))
		if planner && fullAccess && strings.Contains(help.Run, "--auto") {
			args = append(args, "--auto")
		}
		checks := []string{}
		if planner {
			for _, result := range r.State.CheckResults {
				checks = append(checks, result.Command)
			}
		}
		env = opencodeAccessEnv(r.Paths, model, planner, fullAccess, checks...)
	case "pi":
		args = append(args, "--print", "--mode", "json", "--no-session", "--no-extensions", "--no-skills")
		if planner && !fullAccess {
			args = append(args, "--tools", "read,grep,find,ls")
		}
		help, _ := AgentHelp(ctx, connection, r.Paths)
		if strings.Contains(help.Global, "--no-mcp") {
			args = append(args, "--no-mcp")
		}
		if strings.Contains(help.Global, "--no-approve") {
			args = append(args, "--no-approve")
		}
		if model != "" {
			args = append(args, "--model", model)
			if connection.Provider != "" {
				args = append(args, "--provider", connection.Provider)
			}
		}
	case "cline":
		args = append(args, "--json", "--yolo")
		if planner && !fullAccess {
			args = append(args, "--plan")
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		if connection.Provider != "" {
			args = append(args, "--provider", connection.Provider)
		}
	case "gemini":
		mode := "plan"
		if fullAccess {
			mode = "yolo"
		}
		args = append(args, "--prompt", "Complete the instructions supplied on stdin. Return only the requested JSON decision.", "--output-format", "json", "--approval-mode", mode)
		if model != "" {
			args = append(args, "--model", model)
		}
	default:
		return "", fmt.Errorf("unsupported agent adapter %s", connection.Backend)
	}
	args, env, err = r.prepareMCP(ctx, connection, planner, fullAccess, args, env)
	if err != nil {
		return "", err
	}
	timeout := 30 * time.Minute
	if r.State.Config.WorkerTimeoutSeconds > 0 {
		timeout = time.Duration(r.State.Config.WorkerTimeoutSeconds) * time.Second
	}
	if planner {
		timeout = 15 * time.Minute
		if r.State.Config.PlannerTimeoutSeconds > 0 {
			timeout = time.Duration(r.State.Config.PlannerTimeoutSeconds) * time.Second
		}
	}
	var stream func(string)
	if !planner {
		stream = func(line string) {
			var event map[string]any
			if json.Unmarshal([]byte(line), &event) != nil {
				return
			}
			if event["type"] == "tool_use" {
				if part, ok := event["part"].(map[string]any); ok {
					if tool, ok := part["tool"].(string); ok {
						r.Emit(Event{Kind: "tool", Text: tool})
					}
				}
			}
		}
	}
	result, err := RunProcess(ctx, args, r.Repo, logPath, prompt, env, timeout, stream)
	if captureErr := completeAgentOutput(result); captureErr != nil {
		return "", errors.Join(err, captureErr)
	}
	if err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("%s exited %d: %s", connection.Backend, result.Code, Tail(result.Output, 2200))
	}
	raw := result.Output
	if file, err := os.Open(resultPath); err == nil {
		info, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return "", statErr
		}
		if info.Size() > processOutputLimit {
			file.Close()
			return "", outputLimitError{}
		}
		data, readErr := ReadLimited(file, processOutputLimit)
		file.Close()
		if readErr != nil {
			return "", fmt.Errorf("reading agent result: %w", readErr)
		}
		raw = string(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("the agent returned no result")
	}
	freePlanner := r.Settings.Planner.Backend == "opencode" && (r.Settings.Planner.Model == "auto" || r.Settings.Planner.Model == "")
	if r.State.Mode == "figureout" {
		freePlanner = false
		for _, member := range r.State.Team {
			if strings.Contains(label, "-"+member.Name+"-") && member.Connection.Backend == "opencode" && (member.Connection.Model == "auto" || member.Connection.Model == "") {
				freePlanner = true
			}
		}
	}
	if !planner || (connection.Backend == "opencode" && (freePlanner || connection.RequireFree)) {
		for _, line := range strings.Split(raw, "\n") {
			var event struct {
				Type string `json:"type"`
				Part struct {
					Cost float64 `json:"cost"`
				} `json:"part"`
			}
			if json.Unmarshal([]byte(line), &event) == nil && event.Type == "step_finish" && event.Part.Cost > 0 {
				return "", costError{}
			}
		}
	}
	return raw, nil
}
