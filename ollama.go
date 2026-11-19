package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ollamaOrigin = "http://127.0.0.1:11434"
	ollamaAPI    = ollamaOrigin + "/v1"
)

var (
	ollamaModelID = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]{0,127}/){0,2}[A-Za-z0-9][A-Za-z0-9._-]{0,127}(:[A-Za-z0-9][A-Za-z0-9._-]{0,127})?$`)
	ollamaDigest  = regexp.MustCompile(`^(sha256:)?[a-fA-F0-9]{64}$`)
	ollamaClient  = &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: time.Second}).DialContext,
		},
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("the local Ollama API redirected")
		},
	}
)

type ollamaModel struct {
	Name         string   `json:"name"`
	Digest       string   `json:"digest"`
	Size         int64    `json:"size"`
	Context      int      `json:"context_length"`
	RemoteModel  string   `json:"remote_model"`
	RemoteHost   string   `json:"remote_host"`
	Capabilities []string `json:"capabilities"`
}

func localOllamaModelID(model string) bool {
	if !ollamaModelID.MatchString(model) {
		return false
	}
	name, tag, _ := strings.Cut(strings.ToLower(model), ":")
	return !strings.HasSuffix(name, "-cloud") && tag != "cloud" && !strings.HasSuffix(tag, "-cloud")
}

func readOllamaMetadata(ctx context.Context, client *http.Client, path, model string, out any) error {
	method := http.MethodGet
	var body io.Reader
	if model != "" {
		data, err := json.Marshal(map[string]string{"model": model})
		if err != nil {
			return err
		}
		method, body = http.MethodPost, bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, ollamaOrigin+path, body)
	if err != nil {
		return err
	}
	if model != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("local Ollama is unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("local Ollama %s returned HTTP %d", path, resp.StatusCode)
	}
	data, err := ReadLimited(resp.Body, 4<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("invalid local Ollama metadata: %w", err)
	}
	return nil
}

func readLocalOllamaModels(ctx context.Context, client *http.Client) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	verified, err := readOllamaToolModels(ctx, client, false)
	if err != nil {
		return nil, err
	}
	var models []string
	for _, item := range verified {
		models = append(models, item.Name)
	}
	sort.Strings(models)
	return Unique(models), nil
}

func readOllamaToolModels(ctx context.Context, client *http.Client, runningOnly bool) ([]ollamaModel, error) {
	var list struct {
		Models []ollamaModel `json:"models"`
	}
	path := "/api/tags"
	if runningOnly {
		path = "/api/ps"
	}
	if err := readOllamaMetadata(ctx, client, path, "", &list); err != nil {
		return nil, err
	}
	var models []ollamaModel
	var probeErr error
	for _, item := range list.Models {
		if !localOllamaModelID(item.Name) ||
			item.RemoteModel != "" || item.RemoteHost != "" || item.Size <= 0 || !ollamaDigest.MatchString(item.Digest) ||
			(runningOnly && item.Context <= 0) {
			continue
		}
		var details ollamaModel
		if err := readOllamaMetadata(ctx, client, "/api/show", item.Name, &details); err != nil {
			probeErr = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if details.RemoteModel == "" && details.RemoteHost == "" && contains(details.Capabilities, "tools") {
			models = append(models, item)
		}
	}
	if len(models) == 0 && probeErr != nil {
		return nil, probeErr
	}
	return models, nil
}
