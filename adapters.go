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
var errNoVerifiedFreeModels = errors.New("no models have verifiable zero pricing; BuildHeadway will not choose a paid worker")

func AgentHelp(ctx context.Context, connection Connection, paths Paths) (helpCacheEntry, error) {
	executable, err := ResolveConnection(connection, paths)
	if err != nil {
		return helpCacheEntry{}, err
	}
	info, err := os.Stat(executable)
	if err != nil {
		return helpCacheEntry{}, err
	}
	key := connection.Backend + "\x00" + executable + info.ModTime().String() + itoa(int(info.Size()))
	localPi := connection.Backend == "pi" && connection.Provider == localPiProvider
	if localPi {
		key += "\x00local"
	}
	if cached, ok := helpCache.Load(key); ok {
		return cached.(helpCacheEntry), nil
	}
	result := helpCacheEntry{}
	env := ToolEnv(paths)
	probeDir := ""
	if connection.Backend == "commandcode" {
		env = localCommandCodeEnv(env)
	} else if localPi {
		probeDir, err = os.MkdirTemp("", "buildheadway-pi-help-")
		if err != nil {
			return result, err
		}
		defer os.RemoveAll(probeDir)
		env = localPiEnv(env, probeDir)
	} else if connection.Backend == "kilo" {
		probeDir, err = os.MkdirTemp("", "buildheadway-kilo-help-")
		if err != nil {
			return result, err
		}
		defer os.RemoveAll(probeDir)
		env = kiloFreeEnv(env, probeDir)
	}
	for index, args := range [][]string{{"--help"}, {"exec", "--help"}} {
		if index == 1 && connection.Backend != "codex" && connection.Backend != "opencode" && connection.Backend != "kilo" {
			break
		}
		if index == 1 && (connection.Backend == "opencode" || connection.Backend == "kilo") {
			args = []string{"run", "--help"}
		}
		if connection.Backend == "kilo" {
			args = append([]string{"--pure"}, args...)
		}
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		cmd, err := PreparedCommand(probeCtx, append([]string{executable}, args...), env)
		if err != nil {
			cancel()
			return result, err
		}
		cmd.Env, cmd.Dir = env, probeDir
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
			return errors.New("this OpenCode version lacks --pure (plugin-free runs); use OpenCode 1 from opencode-ai, not OpenCode 2 from @opencode/cli, or select a supported executable with BUILDHEADWAY_AGENT_PATH")
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
	platforms := []string{"opencode"}
	if r.State.RunID == "" || r.fullAccess() {
		platforms = append(platforms, r.freePlatforms...)
	}
	type discovery struct {
		models []string
		err    error
	}
	results := make([]discovery, len(platforms))
	checkCline := sync.OnceValue(func() error { return compatibleFreeCline(ctx, r.Paths) })
	checkPi := sync.OnceValue(func() error { return compatibleLocalPi(ctx, r.Paths) })
	var probes sync.WaitGroup
	for i, platform := range platforms {
		if _, held := r.limitedPlatforms[platform]; held {
			if platform == "opencode" {
				results[i].err = errNoVerifiedFreeModels
			}
			continue
		}
		probes.Go(func() {
			if platform == "opencode" {
				results[i].models, results[i].err = r.discoverOpenCodeModels(ctx)
			} else {
				results[i].models, results[i].err = r.discoverOptionalModels(ctx, platform, checkCline, checkPi)
			}
		})
	}
	probes.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var models []string
	for i, result := range results {
		if i > 0 && result.err != nil {
			r.Emit(Event{Kind: "status", Text: "Skipping " + platforms[i] + " · " + freebieTerminalLine(result.err.Error(), 500)})
		}
		models = append(models, result.models...)
	}
	if len(models) == 0 {
		if results[0].err != nil {
			return nil, results[0].err
		}
		return nil, errNoVerifiedFreeModels
	}
	return models, nil
}

func (r *Runner) discoverOptionalModels(ctx context.Context, platform string, checkCline, checkPi func() error) ([]string, error) {
	switch {
	case platform == "ollama":
		return configuredLocalOllamaOffers(ctx, r.Paths, r.Repo)
	case localPiOrigin(platform) != "":
		return configuredLocalPiOffers(ctx, platform, checkPi)
	case platform == "kilo":
		if err := compatibleFreeKilo(ctx, r.Paths); err != nil {
			return nil, err
		}
	default:
		if err := checkCline(); err != nil {
			return nil, errors.New("compatible Cline unavailable")
		}
		if err := freePlatformPrerequisite(platform, r.Paths); err != nil {
			return nil, err
		}
	}
	models, err := readAdditionalFreeCatalog(ctx, freeCatalogClient, platform)
	if err != nil {
		return nil, fmt.Errorf("fresh catalog unavailable: %w", err)
	}
	return models, nil
}

func (r *Runner) discoverOpenCodeModels(ctx context.Context) ([]string, error) {
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
	return readFreeModelCatalog(ctx, argv, r.Paths, r.Repo, r.logPath("models"))
}

// Shared by dispatch and explicit diagnostics: never infer pricing from model
// names or reuse an earlier scan as authorization for a future model call.
func readFreeModelCatalog(ctx context.Context, argv []string, paths Paths, repo, logPath string) ([]string, error) {
	result, err := RunProcess(ctx, argv, repo, logPath, "", ToolEnv(paths), 90*time.Second, nil)
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
		return nil, errNoVerifiedFreeModels
	}
	return models, nil
}
func opencodeEnv(paths Paths, model string, planner bool, executedChecks ...string) []string {
	return opencodeAccessEnv(paths, model, planner, false, executedChecks...)
}
func opencodeAccessEnv(paths Paths, model string, planner, full bool, executedChecks ...string) []string {
	config := openCodeRunConfig(model, planner, full, executedChecks...)
	data, _ := json.Marshal(config)
	env := setEnv(ToolEnv(paths), "OPENCODE_CONFIG_CONTENT", string(data))
	permissions, _ := json.Marshal(config["permission"])
	env = setEnv(env, "OPENCODE_PERMISSION", string(permissions))
	return setEnv(env, "OPENCODE_DISABLE_DEFAULT_PLUGINS", "true")
}

func openCodeRunConfig(model string, planner, full bool, executedChecks ...string) map[string]any {
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
	return map[string]any{
		"model": model, "small_model": model, "enabled_providers": []string{provider},
		"permission": permission, "agent": map[string]any{agent: agentConfig},
	}
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
func (r *Runner) requiresZeroCost(connection Connection, label string, planner bool) bool {
	if !planner || connection.RequireFree {
		return true
	}
	if connection.Backend != "opencode" {
		return false
	}
	if r.State.Mode == "figureout" {
		for _, member := range r.State.Team {
			if strings.Contains(label, "-"+member.Name+"-") && member.Connection.Backend == "opencode" && (member.Connection.Model == "auto" || member.Connection.Model == "") {
				return true
			}
		}
		return false
	}
	return r.Settings.Planner.Backend == "opencode" && (r.Settings.Planner.Model == "auto" || r.Settings.Planner.Model == "")
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
	processDir, processInput := r.Repo, prompt
	piModelID := ""
	var piRelay *localPiRelay
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
		if strings.Contains(help.Global, "--print-logs") && strings.Contains(help.Global, "--log-level") {
			// Some OpenCode versions retain a session after a provider rejects
			// inference. Request only error diagnostics so the harness can stop
			// promptly instead of waiting for the full invocation deadline.
			args = append([]string{executable, "--print-logs", "--log-level", "ERROR"}, args[1:]...)
		}
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
		if connection.Provider == localPiProvider {
			platform, id, qualified := strings.Cut(model, "/")
			if !fullAccess || !connection.RequireFree || !qualified || localPiOrigin(platform) == "" || !localPiModelID.MatchString(id) {
				return "", errors.New("Pi requires a verified local model in full-access goal mode")
			}
			if err := compatibleLocalPi(ctx, r.Paths); err != nil {
				return "", err
			}
			models, err := readLocalPiModels(ctx, localPiClient, platform)
			if err != nil {
				return "", errors.Join(freeOfferUnavailableError{model}, err)
			}
			var selected localPiModel
			for _, candidate := range models {
				if candidate.id == id {
					selected = candidate
					break
				}
			}
			if selected.id == "" {
				return "", freeOfferUnavailableError{model}
			}
			profile, err := os.MkdirTemp("", "buildheadway-pi-local-")
			if err != nil {
				return "", err
			}
			defer os.RemoveAll(profile)
			relay, err := startLocalPiRelay(localPiOrigin(platform), id)
			if err != nil {
				return "", err
			}
			defer relay.server.Close()
			piRelay = relay
			if err := writeLocalPiProfile(profile, selected, relay); err != nil {
				return "", err
			}
			env = localPiEnv(env, profile)
			piModelID = id
			args = append(args, "--no-context-files", "--no-prompt-templates", "--models", localPiProvider+"/"+id)
		}
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
		cliModel := model
		if piModelID != "" {
			cliModel = piModelID
		}
		if cliModel != "" {
			args = append(args, "--model", cliModel)
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
		if connection.RequireFree {
			if !fullAccess {
				return "", errors.New("free Cline runs require full-access goal mode")
			}
			switch connection.Provider {
			case "cline":
				if !clinePromotionID(model) {
					return "", errors.New("choose an eligible Cline promotion")
				}
			case "openrouter":
				if !freeGatewayModelID("openrouter", model) {
					return "", errors.New("choose an eligible OpenRouter free variant")
				}
			default:
				return "", errors.New("choose a supported free Cline provider")
			}
			if err := freePlatformPrerequisite(connection.Provider, r.Paths); err != nil {
				return "", err
			}
			profile := clineFreeProfile(r.Paths, connection.Provider)
			if connection.Provider == "openrouter" {
				// The API key comes from the environment. An empty profile
				// cannot inherit a custom endpoint or model from another login.
				profile, err = os.MkdirTemp("", "buildheadway-cline-openrouter-")
				if err != nil {
					return "", err
				}
				defer os.RemoveAll(profile)
			}
			env = clineFreeEnv(env, connection.Provider)
			env = setEnv(env, "CLINE_NO_INTERACTIVE", "1")
			args = append(args, "--config", profile, "--compaction", "off")
		}
	case "kilo":
		id, explicit := strings.CutPrefix(model, "kilo/")
		if !fullAccess || !connection.RequireFree || connection.Provider != "kilo" || !explicit || !freeGatewayModelID("kilo", id) {
			return "", errors.New("Kilo requires a verified free Gateway model in full-access goal mode")
		}
		if err := compatibleFreeKilo(ctx, r.Paths); err != nil {
			return "", err
		}
		offer, err := readKiloFreeOffer(ctx, freeCatalogClient, id)
		if err != nil {
			return "", err
		}
		profile, err := os.MkdirTemp("", "buildheadway-kilo-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(profile)
		processDir, processInput = profile, ""
		env = kiloFreeRunEnv(env, profile, offer, planner)
		args = openCodeRunArgs(executable, model, promptPath, r.Repo, planner, true)
		args = append([]string{executable, "--print-logs", "--log-level", "ERROR"}, args[1:]...)
		if planner {
			args = append(args, "--auto")
		}
	case "commandcode":
		if !fullAccess || !connection.RequireFree || connection.Provider != commandCodeOllamaProvider || !localOllamaModelID(model) {
			return "", errors.New("Command Code requires a verified local Ollama model in full-access goal mode")
		}
		offers, err := configuredLocalOllamaOffers(ctx, r.Paths, r.Repo)
		if err != nil || !contains(offers, "ollama/"+model) {
			return "", errors.Join(freeOfferUnavailableError{"ollama/" + model}, err)
		}
		args = append(args, "--local-only", "--print", "--output-format", "json", "--model", commandCodeOllamaProvider+"/"+model,
			"--safe-mode", "--no-skills", "--no-session", "--skip-onboarding", "--no-auto-update", "--max-turns", "24", "--yolo")
		env = localCommandCodeEnv(env)
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
	processCtx, stopProcess := context.WithCancel(ctx)
	defer stopProcess()
	var blockerMu sync.Mutex
	var providerBlocker, unavailableOffer error
	zeroCost := r.requiresZeroCost(connection, label, planner)
	observe := func(line string) {
		var err error
		switch connection.Backend {
		case "commandcode":
			var event commandCodeResult
			if json.Unmarshal([]byte(line), &event) == nil {
				err = checkCommandCodeEvent(event, model)
			}
		case "pi":
			if piModelID != "" {
				_, err = localPiReceipt(line, piModelID)
			}
		case "opencode", "cline", "kilo":
			if connection.Backend == "cline" && connection.RequireFree {
				err = clineFreeStreamError(connection.Provider, model, line)
			} else if connection.Backend == "kilo" {
				err = kiloFreeStreamError(model, line)
			}
			if err == nil {
				err = agentStreamError(connection.Backend, model, line, zeroCost)
			}
			var unavailable freeOfferUnavailableError
			if errors.As(err, &unavailable) {
				// Keep reading this process so a later charge or blocker
				// wins over a recoverable availability report.
				blockerMu.Lock()
				unavailableOffer = unavailable
				blockerMu.Unlock()
				err = nil
			}
			if err == nil && zeroCost && connection.Backend == "opencode" && openCodeReportedCharge(line) {
				err = costError{}
			}
		}
		if err != nil {
			blockerMu.Lock()
			var charge costError
			if providerBlocker == nil || errors.As(err, &charge) {
				providerBlocker = err
			}
			blockerMu.Unlock()
			stopProcess()
		}
		if stream != nil {
			stream(line)
		}
	}
	result, err := RunProcess(processCtx, args, processDir, logPath, processInput, env, timeout, observe)
	blockerMu.Lock()
	blocker, unavailable := providerBlocker, unavailableOffer
	blockerMu.Unlock()
	if blocker != nil {
		// A genuine provider blocker keeps priority over the cancellation it
		// caused, including a concurrent pause. Never turn it into fallback.
		var account authError
		if connection.Backend == "cline" && connection.RequireFree && errors.As(blocker, &account) {
			blocker = freePlatformAuthError{connection.Provider, blocker}
		}
		return "", errors.Join(blocker, completeAgentOutput(result))
	}
	if piRelay != nil {
		if failure := piRelay.failure.Load(); failure != nil && (err == nil || failure.failureKind() == "policy") {
			return "", errors.Join(failure, completeAgentOutput(result))
		}
	}
	if captureErr := completeAgentOutput(result); captureErr != nil {
		return "", errors.Join(err, captureErr)
	}
	if err != nil {
		return "", err
	}
	if unavailable != nil {
		return "", unavailable
	}
	if connection.Backend == "commandcode" {
		if result.Code != 0 {
			return "", commandCodeExitError(result.Code, result.Output)
		}
		return decodeCommandCodeOutput(result.Output, model)
	}
	if result.Code != 0 {
		if piModelID != "" {
			return "", fmt.Errorf("Pi exited %d during local inference; inspect the call log", result.Code)
		}
		return "", fmt.Errorf("%s exited %d: %s", connection.Backend, result.Code, Tail(result.Output, 2200))
	}
	if piModelID != "" {
		if err := verifyLocalPiOutput(result.Output, piModelID); err != nil {
			return "", err
		}
		return result.Output, nil
	}
	if connection.Backend == "kilo" {
		if err := verifyKiloRun(ctx, executable, processDir, env, model, result.Output); err != nil {
			return "", err
		}
		return result.Output, nil
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
	if connection.Backend == "cline" && connection.RequireFree && !selectedClineModelReported(raw, connection.Provider, model) {
		return "", freeRouteMismatchError{}
	}
	if zeroCost {
		for _, line := range strings.Split(raw, "\n") {
			if openCodeReportedCharge(line) {
				return "", costError{}
			}
		}
	}
	return raw, nil
}
