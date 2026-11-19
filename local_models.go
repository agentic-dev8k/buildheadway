package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"time"
)

const (
	lmStudioOrigin = "http://127.0.0.1:1234"
	llamaCppOrigin = "http://127.0.0.1:8080"
)

var (
	localPiModelID   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}(/[A-Za-z0-9][A-Za-z0-9._:-]{0,127}){0,3}$`)
	localPiTransport = &http.Transport{
		DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
	}
	localPiClient = &http.Client{
		Transport: localPiTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

type localPiModel struct {
	id      string
	context int
}

func localPiOrigin(platform string) string {
	switch platform {
	case "lmstudio":
		return lmStudioOrigin
	case "llamacpp":
		return llamaCppOrigin
	default:
		return ""
	}
}

func readLocalPiMetadata(ctx context.Context, client *http.Client, origin, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", path, resp.StatusCode)
	}
	data, err := ReadLimited(resp.Body, 4<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

func loadedLMStudioModels(ctx context.Context, client *http.Client) ([]localPiModel, error) {
	var result struct {
		Models []struct {
			Type         string `json:"type"`
			Capabilities struct {
				Tools bool `json:"trained_for_tool_use"`
			} `json:"capabilities"`
			Loaded []struct {
				ID     string `json:"id"`
				Config struct {
					Context int `json:"context_length"`
				} `json:"config"`
			} `json:"loaded_instances"`
		} `json:"models"`
	}
	if err := readLocalPiMetadata(ctx, client, lmStudioOrigin, "/api/v1/models", &result); err != nil {
		return nil, err
	}
	var models []localPiModel
	for _, entry := range result.Models {
		if entry.Type != "llm" || !entry.Capabilities.Tools {
			continue
		}
		for _, loaded := range entry.Loaded {
			if localPiModelID.MatchString(loaded.ID) && loaded.Config.Context > 0 {
				models = append(models, localPiModel{loaded.ID, loaded.Config.Context})
			}
		}
	}
	return models, nil
}

func loadedLlamaCppModel(ctx context.Context, client *http.Client) ([]localPiModel, error) {
	var props struct {
		Alias      string `json:"model_alias"`
		ModelPath  string `json:"model_path"`
		Sleeping   bool   `json:"is_sleeping"`
		Generation struct {
			Context int `json:"n_ctx"`
		} `json:"default_generation_settings"`
		Template struct {
			Tools     bool `json:"supports_tools"`
			ToolCalls bool `json:"supports_tool_calls"`
		} `json:"chat_template_caps"`
	}
	if err := readLocalPiMetadata(ctx, client, llamaCppOrigin, "/props", &props); err != nil {
		return nil, err
	}
	if props.Sleeping || props.ModelPath == "" || !props.Template.Tools || !props.Template.ToolCalls ||
		(props.Alias != "" && !localPiModelID.MatchString(props.Alias)) || props.Generation.Context <= 0 {
		return nil, nil
	}
	return []localPiModel{{props.Alias, props.Generation.Context}}, nil
}

func readLocalPiModels(ctx context.Context, client *http.Client, platform string) ([]localPiModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var models []localPiModel
	var err error
	switch platform {
	case "lmstudio":
		models, err = loadedLMStudioModels(ctx, client)
	case "llamacpp":
		models, err = loadedLlamaCppModel(ctx, client)
	default:
		return nil, errors.New("unknown Pi local platform")
	}
	if err != nil {
		return nil, fmt.Errorf("%s local server is unavailable: %w", platform, err)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("load a tool-capable model on the %s loopback server", platform)
	}
	var served struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := readLocalPiMetadata(ctx, client, localPiOrigin(platform), "/v1/models", &served); err != nil {
		return nil, fmt.Errorf("%s OpenAI-compatible model list is unavailable: %w", platform, err)
	}
	if platform == "llamacpp" && models[0].id == "" && len(served.Models) == 1 && localPiModelID.MatchString(served.Models[0].ID) {
		models[0].id = served.Models[0].ID
	}
	loaded := make(map[string]int, len(models))
	for _, model := range models {
		if model.id == "" {
			continue
		}
		if existing, ok := loaded[model.id]; !ok || model.context < existing {
			loaded[model.id] = model.context
		}
	}
	var eligible []localPiModel
	for _, model := range served.Models {
		if n, ok := loaded[model.ID]; ok {
			eligible = append(eligible, localPiModel{model.ID, n})
			delete(loaded, model.ID)
		}
	}
	if len(eligible) == 0 {
		return nil, fmt.Errorf("%s has no loaded tool-capable model exposed on /v1/models", platform)
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].id < eligible[j].id })
	return eligible, nil
}
