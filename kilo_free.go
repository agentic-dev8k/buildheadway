package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const kiloGatewayURL = "https://ai-gateway.kilo.ai/api/v1"

var kiloSessionID = regexp.MustCompile(`^ses_[A-Za-z0-9_-]{1,120}$`)

func compatibleFreeKilo(ctx context.Context, paths Paths) error {
	help, err := AgentHelp(ctx, Connection{Backend: "kilo"}, paths)
	if err != nil {
		return err
	}
	if !strings.Contains(help.Global, "kilo export") {
		return errors.New("the installed Kilo CLI needs session export to verify free runs")
	}
	for _, flag := range []string{"--pure", "--model", "--agent", "--format", "--file", "--dir", "--auto", "--print-logs", "--log-level"} {
		if !strings.Contains(help.Run, flag) {
			return fmt.Errorf("the installed Kilo CLI needs %s for free routing", flag)
		}
	}
	return nil
}

func kiloFreeEnv(env []string, profile string) []string {
	bash := envValue(env, "OPENCODE_GIT_BASH_PATH")
	clean := make([]string, 0, len(env))
	for _, item := range env {
		name, _, _ := strings.Cut(item, "=")
		name = strings.ToUpper(name)
		if strings.HasPrefix(name, "KILO_") || strings.HasPrefix(name, "KILOCODE_") || strings.HasPrefix(name, "OPENCODE_") {
			continue
		}
		clean = append(clean, item)
	}

	// The native auth store, config, databases and caches all use these roots.
	for _, name := range []string{"CONFIG", "DATA", "CACHE", "STATE"} {
		clean = setEnv(clean, "XDG_"+name+"_HOME", filepath.Join(profile, strings.ToLower(name)))
	}
	for _, flag := range []string{"KILO_DISABLE_PROJECT_CONFIG", "KILO_DISABLE_DEFAULT_PLUGINS",
		"KILO_DISABLE_AUTOUPDATE", "KILO_DISABLE_CLAUDE_CODE", "KILO_DISABLE_EXTERNAL_SKILLS",
		"KILO_DISABLE_AUTOCOMPACT", "KILO_DISABLE_MODELS_FETCH"} {
		clean = setEnv(clean, flag, "1")
	}
	if bash != "" {
		clean = setEnv(clean, "KILO_GIT_BASH_PATH", bash)
	}
	return setEnv(clean, "KILO_SESSION_RETRY_LIMIT", "1")
}

type kiloFreeModel struct {
	id              string
	context, output int
}

func (offer hostedFreeOffer) kiloModel() (kiloFreeModel, bool) {
	var model kiloFreeModel
	if !offer.gatewayFree("kilo") || json.Unmarshal(offer.Context, &model.context) != nil || model.context <= 0 {
		return model, false
	}
	var provider struct {
		MaxOutput int `json:"max_completion_tokens"`
	}
	_ = json.Unmarshal(offer.Top, &provider)
	if provider.MaxOutput <= 0 {
		provider.MaxOutput = 8192
	}
	model.id, model.output = offer.ID, min(provider.MaxOutput, model.context)
	return model, true
}

func readKiloFreeOffer(ctx context.Context, client *http.Client, id string) (kiloFreeModel, error) {
	feed, err := fetchAdditionalFreeFeed(ctx, client, "kilo")
	if err != nil {
		return kiloFreeModel{}, errors.Join(freeOfferUnavailableError{"kilo/" + id}, err)
	}
	for _, offer := range feed.Models {
		if offer.ID == id {
			if model, ok := offer.kiloModel(); ok {
				return model, nil
			}
		}
	}
	return kiloFreeModel{}, freeOfferUnavailableError{"kilo/" + id}
}

func kiloFreeRunEnv(env []string, profile string, model kiloFreeModel, planner bool) []string {
	config := openCodeRunConfig("kilo/"+model.id, planner, true)
	config["provider"] = map[string]any{
		"kilo": map[string]any{
			"options": map[string]string{"baseURL": kiloGatewayURL},
			"models": map[string]any{
				model.id: map[string]any{
					"name": model.id, "tool_call": true,
					"limit": map[string]int{"context": model.context, "output": model.output},
					"cost":  map[string]int{"input": 0, "output": 0},
				},
			},
		},
	}
	config["share"] = "disabled"
	data, _ := json.Marshal(config)
	permission, _ := json.Marshal(config["permission"])
	env = setEnv(kiloFreeEnv(env, profile), "KILO_CONFIG_CONTENT", string(data))
	return setEnv(env, "KILO_PERMISSION", string(permission))
}

func kiloFreeStreamError(model, line string) error {
	if openCodeReportedCharge(line) {
		return costError{"Kilo Gateway"}
	}
	var event struct {
		Type  string `json:"type"`
		Error struct {
			Name string `json:"name"`
			Data struct {
				StatusCode int    `json:"statusCode"`
				ProviderID string `json:"providerID"`
				ModelID    string `json:"modelID"`
				Message    string `json:"message"`
			} `json:"data"`
		} `json:"error"`
	}
	line = Clean(line)
	if json.Unmarshal([]byte(line), &event) != nil {
		// Kilo emits this provider diagnostic before it waits to retry.
		diagnostic := strings.ToLower(modelStreamDiagnostic(line, model))
		if strings.HasPrefix(diagnostic, "ai_apicallerror: rate limit exceeded") ||
			strings.HasPrefix(diagnostic, "ai_apicallerror: too many requests") {
			return freeQuotaError{platform: "kilo"}
		}
		return nil
	}
	if event.Type != "error" {
		return nil
	}
	data := event.Error.Data
	switch event.Error.Name {
	case "APIError":
		switch data.StatusCode {
		case 429:
			return freeQuotaError{platform: "kilo"}
		case 401, 402:
			// This request intentionally has no account credential. A model
			// that now requires one leaves the free pool.
			return freeOfferUnavailableError{model}
		case 403:
			return errors.New("Access denied by the provider during the anonymous Kilo request")
		}
	case "ProviderModelNotFoundError":
		if data.ProviderID+"/"+data.ModelID == model {
			return freeOfferUnavailableError{model}
		}
	case "UnknownError":
		missing := "Model not found: " + model
		if data.Message == missing || strings.HasPrefix(data.Message, missing+". Did you mean:") {
			return freeOfferUnavailableError{model}
		}
	}
	return nil
}

func kiloRunSession(raw string) string {
	session := ""
	for _, line := range strings.Split(Clean(raw), "\n") {
		var event struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Type != "step_finish" {
			continue
		}
		if !kiloSessionID.MatchString(event.SessionID) || (session != "" && event.SessionID != session) {
			return ""
		}
		session = event.SessionID
	}
	return session
}

func checkKiloExport(raw, session, model string) error {
	mismatch := freeRouteMismatchError{"Kilo"}
	raw = strings.TrimSpace(Clean(raw))
	// The CLI writes a status line to stderr before emitting the JSON export.
	if !strings.HasPrefix(raw, "{") {
		_, body, found := strings.Cut(raw, "\n{")
		if !found {
			return mismatch
		}
		raw = "{" + body
	}
	var receipt struct {
		Info struct {
			ID string `json:"id"`
		} `json:"info"`
		Messages []struct {
			Info struct {
				Role       string          `json:"role"`
				SessionID  string          `json:"sessionID"`
				ProviderID string          `json:"providerID"`
				ModelID    string          `json:"modelID"`
				Cost       json.RawMessage `json:"cost"`
			} `json:"info"`
		} `json:"messages"`
	}
	if json.NewDecoder(strings.NewReader(raw)).Decode(&receipt) != nil || receipt.Info.ID != session {
		return mismatch
	}
	confirmed := false
	for _, message := range receipt.Messages {
		info := message.Info
		if info.Role != "assistant" {
			continue
		}
		if len(info.Cost) > 0 && !zeroCatalogPrice(info.Cost) {
			return costError{"Kilo Gateway"}
		}
		if info.SessionID != session || info.ProviderID+"/"+info.ModelID != model || len(info.Cost) == 0 {
			return mismatch
		}
		confirmed = true
	}
	if !confirmed {
		return mismatch
	}
	return nil
}

func verifyKiloRun(ctx context.Context, executable, profile string, env []string, model, raw string) error {
	session := kiloRunSession(raw)
	if session == "" {
		return freeRouteMismatchError{"Kilo"}
	}
	result, err := RunProcess(ctx, []string{executable, "--pure", "export", session, "--sanitize"}, profile,
		filepath.Join(profile, "receipt.log"), "", env, 25*time.Second, nil)
	if err != nil {
		return err
	}
	if err := completeAgentOutput(result); err != nil {
		return err
	}
	if result.Code != 0 {
		return freeRouteMismatchError{"Kilo"}
	}
	return checkKiloExport(result.Output, session, model)
}
