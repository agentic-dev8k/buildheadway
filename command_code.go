package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const commandCodeOllamaProvider = "buildheadway-ollama"

type commandCodeLocalProvider struct {
	Name    string                     `json:"name,omitempty"`
	BaseURL string                     `json:"baseURL"`
	APIKey  json.RawMessage            `json:"apiKey"`
	Models  map[string]json.RawMessage `json:"models"`
}

func commandCodeLocalHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".commandcode"), nil
}

func compatibleLocalCommandCode(ctx context.Context, paths Paths) error {
	help, err := AgentHelp(ctx, Connection{Backend: "commandcode"}, paths)
	if err != nil {
		return err
	}
	for _, flag := range []string{"--local-only", "--print", "--output-format", "--model", "--safe-mode",
		"--no-skills", "--no-session", "--skip-onboarding", "--no-auto-update", "--max-turns", "--yolo"} {
		if !strings.Contains(help.Global, flag) {
			return fmt.Errorf("the installed Command Code needs %s for local routing", flag)
		}
	}
	return nil
}

func readCommandCodeConfig(path string) (map[string]json.RawMessage, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]json.RawMessage), nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := ReadLimited(file, 4<<20)
	if err != nil {
		return nil, err
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(data, &config) != nil || config == nil {
		return nil, fmt.Errorf("Command Code needs a JSON object in %s", path)
	}
	return config, nil
}

func readCommandCodeProviders(home string) (map[string]json.RawMessage, map[string]json.RawMessage, string, error) {
	config, err := readCommandCodeConfig(filepath.Join(home, "providers.json"))
	if err != nil {
		return nil, nil, "", err
	}
	key := "provider"
	if _, exists := config[key]; !exists || bytes.Equal(config[key], []byte("null")) {
		if _, exists := config["providers"]; exists {
			key = "providers"
		}
	}
	providers := make(map[string]json.RawMessage)
	if raw := config[key]; len(raw) != 0 && json.Unmarshal(raw, &providers) != nil {
		return nil, nil, "", errors.New("the provider map in Command Code providers.json is invalid")
	}
	if providers == nil {
		providers = make(map[string]json.RawMessage)
	}
	return config, providers, key, nil
}

func decodeCommandCodeLocalProvider(raw json.RawMessage) (commandCodeLocalProvider, error) {
	var provider commandCodeLocalProvider
	var fields map[string]json.RawMessage
	invalid := errors.New("the buildheadway-ollama provider in Command Code must use the keyless loopback setup described in the free platform guide")
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &provider) != nil ||
		provider.BaseURL != ollamaAPI || !bytes.Equal(provider.APIKey, []byte("false")) || provider.Models == nil {
		return provider, invalid
	}
	for key := range fields {
		switch key {
		case "name", "baseURL", "apiKey", "models":
		default:
			return provider, invalid
		}
	}
	for id, raw := range provider.Models {
		var settings map[string]json.RawMessage
		if !localOllamaModelID(id) || json.Unmarshal(raw, &settings) != nil || settings == nil || len(settings) != 0 {
			return provider, invalid
		}
	}
	return provider, nil
}

func checkCommandCodeLocalSettings(home, repo string, models []string) error {
	if repo == "" {
		var err error
		repo, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	project := filepath.Join(repo, ".commandcode")
	for _, path := range []string{filepath.Join(home, "config.json"), filepath.Join(home, "settings.json"),
		filepath.Join(project, "settings.json"), filepath.Join(project, "settings.local.json")} {
		settings, err := readCommandCodeConfig(path)
		if err != nil {
			return err
		}
		var overrides map[string]string
		if raw := settings["featureModels"]; len(raw) > 0 && json.Unmarshal(raw, &overrides) != nil {
			return fmt.Errorf("invalid Command Code featureModels in %s", path)
		}
		for _, model := range overrides {
			if model == "" {
				continue
			}
			id, local := strings.CutPrefix(model, commandCodeOllamaProvider+"/")
			if !local || !contains(models, id) {
				return fmt.Errorf("select a local Ollama model for Command Code featureModels in %s", path)
			}
		}
		var modules map[string]json.RawMessage
		if raw := settings["providers"]; len(raw) > 0 && json.Unmarshal(raw, &modules) != nil {
			return fmt.Errorf("invalid Command Code providers in %s", path)
		}
		if modules[commandCodeOllamaProvider] != nil {
			return fmt.Errorf("reserve the buildheadway-ollama provider for the local entry in providers.json; check %s", path)
		}
	}
	return nil
}

func commandCodeLocalProfile(home string) (commandCodeLocalProvider, error) {
	_, providers, _, err := readCommandCodeProviders(home)
	if err != nil {
		return commandCodeLocalProvider{}, err
	}
	raw, exists := providers[commandCodeOllamaProvider]
	if !exists {
		return commandCodeLocalProvider{}, errors.New("run buildheadway freebies platforms setup ollama to configure Command Code")
	}
	return decodeCommandCodeLocalProvider(raw)
}

func configuredLocalOllamaOffers(ctx context.Context, paths Paths, repo string) ([]string, error) {
	if err := compatibleLocalCommandCode(ctx, paths); err != nil {
		return nil, err
	}
	home, err := commandCodeLocalHome()
	if err != nil {
		return nil, err
	}
	provider, err := commandCodeLocalProfile(home)
	if err != nil {
		return nil, err
	}
	models, err := readLocalOllamaModels(ctx, ollamaClient)
	if err != nil {
		return nil, err
	}
	var configured []string
	for _, model := range models {
		if provider.Models[model] != nil {
			configured = append(configured, model)
		}
	}
	if len(configured) == 0 {
		return nil, errors.New("no configured local Ollama models support tools. Pull a tool-capable model, then run buildheadway freebies platforms setup ollama")
	}
	if err := checkCommandCodeLocalSettings(home, repo, configured); err != nil {
		return nil, err
	}
	for i, model := range configured {
		configured[i] = "ollama/" + model
	}
	return configured, nil
}

func setupCommandCodeOllama(ctx context.Context, paths Paths) error {
	if err := compatibleLocalCommandCode(ctx, paths); err != nil {
		return err
	}
	models, err := readLocalOllamaModels(ctx, ollamaClient)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return errors.New("pull a tool-capable local model into Ollama before running setup")
	}
	home, err := commandCodeLocalHome()
	if err != nil {
		return err
	}
	return writeCommandCodeLocalProvider(home, models)
}

func writeCommandCodeLocalProvider(home string, models []string) error {
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(home, ".buildheadway-providers.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lockFile(lock); err != nil {
		return errors.New("another BuildHeadway process is configuring Command Code; try again shortly")
	}
	config, providers, key, err := readCommandCodeProviders(home)
	if err != nil {
		return err
	}
	provider := commandCodeLocalProvider{
		Name: "BuildHeadway Ollama", BaseURL: ollamaAPI, APIKey: json.RawMessage("false"),
		Models: make(map[string]json.RawMessage, len(models)),
	}
	if raw, exists := providers[commandCodeOllamaProvider]; exists {
		old, err := decodeCommandCodeLocalProvider(raw)
		if err != nil {
			return err
		}
		provider.Name = old.Name
	}
	for _, model := range models {
		provider.Models[model] = json.RawMessage("{}")
	}
	providers[commandCodeOllamaProvider], err = json.Marshal(provider)
	if err != nil {
		return err
	}
	config[key], err = json.Marshal(providers)
	if err != nil {
		return err
	}
	return AtomicJSON(filepath.Join(home, "providers.json"), config)
}

type commandCodeResult struct {
	Type       string                     `json:"type"`
	Subtype    string                     `json:"subtype"`
	StopReason string                     `json:"stopReason"`
	FinalText  string                     `json:"finalText"`
	Usage      map[string]json.RawMessage `json:"usage"`
	Event      struct {
		Type  string                     `json:"type"`
		Model string                     `json:"model"`
		Usage map[string]json.RawMessage `json:"usage"`
	} `json:"event"`
}

func commandCodeCharge(usage map[string]json.RawMessage) bool {
	for _, key := range []string{"cost", "totalCost", "costUsd", "totalCostUsd"} {
		if amount, ok := usage[key]; ok && !zeroCatalogPrice(amount) {
			return true
		}
	}
	return false
}

func checkCommandCodeEvent(row commandCodeResult, model string) error {
	if commandCodeCharge(row.Usage) || commandCodeCharge(row.Event.Usage) {
		return costError{"Command Code"}
	}
	if row.Type == "event" && (row.Event.Type == "model_request_start" || row.Event.Type == "model_request_end") &&
		row.Event.Model != commandCodeOllamaProvider+"/"+model {
		return freeRouteMismatchError{}
	}
	return nil
}

func decodeCommandCodeOutput(raw, model string) (string, error) {
	var result commandCodeResult
	seen, modelConfirmed := false, false
	for _, line := range strings.Split(raw, "\n") {
		var row commandCodeResult
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		if err := checkCommandCodeEvent(row, model); err != nil {
			return "", err
		}
		if row.Type == "event" && row.Event.Type == "model_request_end" {
			modelConfirmed = true
		}
		if row.Type != "result" {
			continue
		}
		if seen {
			return "", errors.New("Command Code returned multiple results")
		}
		result, seen = row, true
	}
	if !seen || result.Subtype != "success" || result.StopReason != "end_turn" {
		return "", errors.New("Command Code did not complete the local run")
	}
	if !modelConfirmed {
		return "", freeRouteMismatchError{}
	}
	if strings.TrimSpace(result.FinalText) == "" {
		return "", errors.New("Command Code returned an empty local result")
	}
	return result.FinalText, nil
}

func localCommandCodeEnv(env []string) []string {
	env = setEnv(env, "CMD_LOCAL_ONLY", "1")
	env = setEnv(env, "CMD_DISABLE_REMOTE_MODEL_CATALOG", "1")
	env = setEnv(env, "COMMANDCODE_DISABLE_CRON", "1")
	env = setEnv(env, "COMMANDCODE_DISABLE_DURABLE_CRON", "1")
	env = setEnv(env, "COMMANDCODE_API_ENV", "prod")
	proxyExclusions := "127.0.0.1,localhost"
	if current := envValue(env, "NO_PROXY"); current != "" {
		proxyExclusions += "," + current
	}
	return setEnv(env, "NO_PROXY", proxyExclusions)
}

func commandCodeExitError(code int, output string) error {
	switch code {
	case 3:
		return authError{"Command Code", "run command-code login once to enable native headless runs, then resume"}
	case 5:
		return freeQuotaError{"ollama"}
	case 6, 7:
		return fmt.Errorf("local Ollama temporarily unavailable (Command Code exit %d): %s", code, Tail(output, 1800))
	case 10:
		return authError{"Command Code", "the native CLI reported an account limit while running a local model"}
	default:
		return fmt.Errorf("Command Code exited %d: %s", code, Tail(output, 1800))
	}
}
