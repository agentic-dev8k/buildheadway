package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	freePlatformsEnv   = "BUILDHEADWAY_FREE_PLATFORMS"
	clineFreeCatalog   = "https://api.cline.bot/api/v1/ai/cline/recommended-models"
	openRouterCatalog  = "https://openrouter.ai/api/v1/models?supported_parameters=tools"
	commandCodeCatalog = "https://api.commandcode.ai/provider/v1/models"
)

var freeCatalogClient = &http.Client{
	Timeout: 12 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("free-model catalog redirected")
	},
}

var providerModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}/[A-Za-z0-9][A-Za-z0-9._:-]{0,160}$`)
var decimalPricing = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

type freePlatformAuthError struct {
	platform string
	err      error
}

func (e freePlatformAuthError) Error() string {
	if e.platform == "cline" {
		return e.err.Error() + " Run buildheadway freebies platforms login cline, then resume."
	}
	return e.err.Error() + " Check OPENROUTER_API_KEY in the terminal running BuildHeadway, then resume."
}

func (e freePlatformAuthError) Unwrap() error { return e.err }

func selectedFreePlatforms(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var names []string
	for _, part := range strings.Split(value, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		switch name {
		case "cline", "openrouter", "ollama":
			names = append(names, name)
		default:
			return nil, fmt.Errorf("%s accepts cline, openrouter and ollama", freePlatformsEnv)
		}
	}
	return Unique(names), nil
}

func zeroCatalogPrice(v json.RawMessage) bool {
	cost := strings.TrimSpace(string(v))
	if len(cost) == 0 {
		return false
	}
	if cost[0] == '"' && json.Unmarshal(v, &cost) != nil {
		return false
	}
	if !decimalPricing.MatchString(cost) {
		return false
	}
	if i := strings.IndexAny(cost, "eE"); i >= 0 {
		cost = cost[:i]
	}
	return !strings.ContainsAny(cost, "123456789")
}

func hasZeroCatalogPricing(pricing map[string]json.RawMessage) bool {
	if !zeroCatalogPrice(pricing["prompt"]) || !zeroCatalogPrice(pricing["completion"]) {
		return false
	}
	for _, cost := range pricing {
		if !zeroCatalogPrice(cost) {
			return false
		}
	}
	return true
}

func clinePromotionID(id string) bool {
	return providerModelID.MatchString(id) && (strings.HasPrefix(id, "cline-free/") ||
		strings.HasSuffix(id, ":free") || strings.HasSuffix(id, "-free"))
}

func readAdditionalFreeCatalog(ctx context.Context, client *http.Client, platform string) ([]string, error) {
	var endpoint string
	switch platform {
	case "cline":
		endpoint = clineFreeCatalog
	case "openrouter":
		endpoint = openRouterCatalog
	case "commandcode":
		endpoint = commandCodeCatalog
	default:
		return nil, errors.New("unknown free platform")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "BuildHeadway/"+Version)
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s catalog returned HTTP %d", platform, resp.StatusCode)
	}
	data, err := ReadLimited(resp.Body, 4<<20)
	if err != nil {
		return nil, fmt.Errorf("reading %s catalog: %w", platform, err)
	}
	var feed struct {
		Free []struct {
			ID            string `json:"id"`
			SupportsTools *bool  `json:"supportsTools"`
		} `json:"free"`
		Models []struct {
			ID         string                     `json:"id"`
			Pricing    map[string]json.RawMessage `json:"pricing"`
			Parameters []string                   `json:"supported_parameters"`
			Endpoints  []string                   `json:"supported_endpoints"`
			Owner      string                     `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("invalid %s catalog: %w", platform, err)
	}
	var models []string
	if platform == "cline" {
		if feed.Free == nil {
			return nil, errors.New("Cline catalog did not include a free list")
		}
		for _, row := range feed.Free {
			if clinePromotionID(row.ID) && (row.SupportsTools == nil || *row.SupportsTools) {
				models = append(models, "cline/"+row.ID)
			}
		}
	} else if platform == "openrouter" {
		if feed.Models == nil {
			return nil, errors.New("OpenRouter catalog did not include a model list")
		}
		for _, row := range feed.Models {
			if providerModelID.MatchString(row.ID) && strings.HasSuffix(row.ID, ":free") &&
				contains(row.Parameters, "tools") && hasZeroCatalogPricing(row.Pricing) {
				models = append(models, "openrouter/"+row.ID)
			}
		}
	} else {
		if feed.Models == nil {
			return nil, errors.New("Command Code catalog did not include a model list")
		}
		for _, row := range feed.Models {
			if row.Owner == "command-code" && providerModelID.MatchString(row.ID) &&
				strings.HasSuffix(row.ID, ":free") && contains(row.Endpoints, "/chat/completions") {
				models = append(models, row.ID)
			}
		}
	}
	return Unique(models), nil
}

func freePlatformForModel(model string) string {
	id, _, _ := strings.Cut(model, "/")
	return id
}

func optionalFreeRoute(model string) bool {
	platform := freePlatformForModel(model)
	return platform == "cline" || platform == "openrouter" || platform == "ollama"
}

func clineFreeProfile(paths Paths, platform string) string {
	return filepath.Join(paths.Config, "free-platforms", platform)
}

func clineFreeEnv(env []string, platform string) []string {
	clean := make([]string, 0, len(env))
	for _, item := range env {
		name, _, _ := strings.Cut(item, "=")
		name = strings.ToUpper(name)
		if strings.HasPrefix(name, "CLINE_") || (platform == "cline" && name == "OPENROUTER_API_KEY") {
			continue
		}
		clean = append(clean, item)
	}
	clean = setEnv(clean, "CLINE_SESSION_BACKEND_MODE", "local")
	return setEnv(clean, "CLINE_NO_AUTO_UPDATE", "1")
}

func compatibleFreeCline(ctx context.Context, paths Paths) error {
	connection := Connection{Backend: "cline"}
	help, err := AgentHelp(ctx, connection, paths)
	if err != nil {
		return err
	}
	for _, flag := range []string{"--config", "--json", "--plan", "--yolo", "--model", "--provider", "--compaction"} {
		if !strings.Contains(help.Global, flag) {
			return fmt.Errorf("the installed Cline needs %s for free routing", flag)
		}
	}
	return nil
}

func freePlatformPrerequisite(platform string, paths Paths) error {
	switch platform {
	case "cline":
		profile := clineFreeProfile(paths, "cline")
		if _, err := os.Stat(filepath.Join(profile, "native-login")); err != nil {
			return errors.New("sign in with buildheadway freebies platforms login cline")
		}
		return checkClineProfileURL(profile)
	case "openrouter":
		if strings.TrimSpace(envValue(os.Environ(), "OPENROUTER_API_KEY")) == "" {
			return errors.New("set OPENROUTER_API_KEY for this terminal")
		}
	}
	return nil
}

func checkClineProfileURL(profile string) error {
	file, err := os.Open(filepath.Join(profile, "data", "settings", "providers.json"))
	if err != nil {
		return errors.New("Cline profile is unavailable. Sign in again with buildheadway freebies platforms login cline")
	}
	defer file.Close()
	data, err := ReadLimited(file, 512<<10)
	if err != nil {
		return errors.New("Cline profile could not be read")
	}
	var profileSettings struct {
		Providers map[string]struct {
			Settings struct {
				BaseURL string `json:"baseUrl"`
			} `json:"settings"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &profileSettings); err != nil {
		return errors.New("Cline profile could not be parsed")
	}
	clineSettings, exists := profileSettings.Providers["cline"]
	baseURL := strings.TrimSuffix(clineSettings.Settings.BaseURL, "/")
	if !exists || (baseURL != "" && baseURL != "https://api.cline.bot" && baseURL != "https://api.cline.bot/api/v1") {
		return errors.New("the managed Cline profile must use the official Cline provider endpoint")
	}
	return nil
}

func freeModelConnection(requested Connection, model string) Connection {
	platform, id, _ := strings.Cut(model, "/")
	switch platform {
	case "cline", "openrouter":
		return Connection{Backend: "cline", Provider: platform, Model: id, RequireFree: true}
	case "ollama":
		return Connection{Backend: "commandcode", Provider: commandCodeOllamaProvider, Model: id, RequireFree: true}
	default:
		requested.Model, requested.RequireFree = model, true
		return requested
	}
}

type FreePlatformStatus struct {
	Platform string   `json:"platform"`
	Status   string   `json:"status"`
	Enabled  bool     `json:"enabled"`
	Models   []string `json:"models,omitempty"`
	Detail   string   `json:"detail"`
}

func freePlatformStatuses(ctx context.Context, paths Paths, check bool) ([]FreePlatformStatus, error) {
	enabled, err := selectedFreePlatforms(os.Getenv(freePlatformsEnv))
	if err != nil {
		return nil, err
	}
	rows := []FreePlatformStatus{
		{Platform: "opencode", Status: "automatic", Enabled: true, Detail: "Check the live catalog with buildheadway scan --models --agent opencode."},
		{Platform: "cline", Status: "optional", Enabled: contains(enabled, "cline"), Detail: "Native Cline promotions. Sign in with buildheadway freebies platforms login cline."},
		{Platform: "openrouter", Status: "optional", Enabled: contains(enabled, "openrouter"), Detail: "Verified tool-capable :free models via Cline and OPENROUTER_API_KEY."},
		{Platform: "commandcode", Status: "discovery", Detail: "Published Command Code hosted variants. Local execution is available through the Ollama route."},
		{Platform: "ollama", Status: "optional", Enabled: contains(enabled, "ollama"), Detail: "Installed tool-capable Ollama models via Command Code in local-only mode. Configure with buildheadway freebies platforms setup ollama."},
		{Platform: "codex", Status: "account_allowance", Detail: "Use ChatGPT sign-in with the Codex senior connection. Usage is assigned by the account plan."},
	}
	if !check {
		return rows, nil
	}
	for i := range rows {
		switch rows[i].Platform {
		case "cline", "openrouter", "commandcode":
			models, err := readAdditionalFreeCatalog(ctx, freeCatalogClient, rows[i].Platform)
			rows[i].Models = models
			if err != nil {
				rows[i].Status = "catalog_unavailable"
				rows[i].Detail = freebieTerminalLine(err.Error(), 500)
			} else {
				rows[i].Status = "catalog_checked"
				if rows[i].Platform == "commandcode" {
					rows[i].Status = "offers_listed"
				} else {
					rows[i].Detail += " Catalog check only. Account access and quota are checked by the native agent."
				}
			}
		case "codex":
			rows[i].Detail = codexPlanStatus(ctx, paths)
		case "ollama":
			models, err := configuredLocalOllamaOffers(ctx, paths, "")
			rows[i].Models = models
			rows[i].Status = "local_models_configured"
			if err != nil {
				rows[i].Status = "local_setup_needed"
				rows[i].Detail = freebieTerminalLine(err.Error(), 500)
			}
		}
	}
	return rows, ctx.Err()
}

func codexPlanStatus(ctx context.Context, paths Paths) string {
	path := FindTool("codex", paths)
	if path == "" {
		return "Install Codex and use ChatGPT sign-in to use the account's included allowance."
	}
	dir, err := os.MkdirTemp("", "buildheadway-codex-status-")
	if err != nil {
		return "Codex sign-in could not be inspected."
	}
	defer os.RemoveAll(dir)
	result, err := RunProcess(ctx, []string{path, "login", "status"}, dir, filepath.Join(dir, "status.log"), "", ToolEnv(paths), 8*time.Second, nil)
	if err == nil && result.Code == 0 && completeAgentOutput(result) == nil {
		for _, line := range strings.Split(Clean(result.Output), "\n") {
			if strings.EqualFold(strings.TrimSpace(line), "Logged in using ChatGPT") {
				return "ChatGPT sign-in detected. Check the plan and remaining usage in Codex."
			}
		}
	}
	return "ChatGPT sign-in could not be confirmed. Use codex login status and select ChatGPT sign-in in Codex."
}

func formatFreePlatforms(rows []FreePlatformStatus) string {
	var out strings.Builder
	out.WriteString("Free platforms\n")
	for _, row := range rows {
		fmt.Fprintf(&out, "\n%s · %s\n%s\n", row.Platform, strings.ReplaceAll(row.Status, "_", " "), row.Detail)
		if optionalFreeRoute(row.Platform) {
			if row.Enabled {
				out.WriteString("Enabled for full-access routing.\n")
			} else {
				out.WriteString("Enable in BUILDHEADWAY_FREE_PLATFORMS for full-access routing.\n")
			}
		}
		for _, model := range row.Models {
			fmt.Fprintf(&out, "  %s\n", model)
		}
	}
	return strings.TrimSpace(out.String())
}

func runFreePlatformsCommand(paths Paths, args []string) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if len(args) == 2 && args[0] == "setup" && args[1] == "ollama" {
		if err := setupCommandCodeOllama(ctx, paths); err != nil {
			return fail(err)
		}
		fmt.Println("Configured local Ollama models in Command Code. Add ollama to BUILDHEADWAY_FREE_PLATFORMS to enable routing.")
		return 0
	}
	if len(args) == 2 && args[0] == "login" && args[1] == "cline" {
		if err := compatibleFreeCline(ctx, paths); err != nil {
			return fail(err)
		}
		profile := clineFreeProfile(paths, "cline")
		if err := os.MkdirAll(profile, 0700); err != nil {
			return fail(err)
		}
		argv := []string{FindTool("cline", paths), "auth", "cline", "--config", profile}
		env := clineFreeEnv(ToolEnv(paths), "cline")
		cmd, err := PreparedCommand(ctx, argv, env)
		if err != nil {
			return fail(err)
		}
		cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fail(err)
		}
		if err := checkClineProfileURL(profile); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(filepath.Join(profile, "native-login"), nil, 0600); err != nil {
			return fail(err)
		}
		return 0
	}
	f := flag.NewFlagSet("buildheadway freebies platforms", flag.ContinueOnError)
	check := f.Bool("check", false, "Refresh public catalogs, local Ollama readiness and Codex sign-in")
	asJSON := f.Bool("json", false, "Output JSON")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 {
		return fail(errors.New("use freebies platforms [--check] [--json], freebies platforms login cline or freebies platforms setup ollama"))
	}
	rows, err := freePlatformStatuses(ctx, paths, *check)
	if err != nil {
		return fail(err)
	}
	if err := outputValue(rows, formatFreePlatforms(rows), *asJSON); err != nil {
		return fail(err)
	}
	return 0
}
