package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLocalOllamaCatalogRequiresInstalledToolModels(t *testing.T) {
	digest := strings.Repeat("a", 64)
	probe := []string{}
	client := &http.Client{Transport: freeCatalogTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme+"://"+req.URL.Host != ollamaOrigin || req.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected local probe: %s %s", req.Method, req.URL)
		}
		var payload string
		switch req.Method + " " + req.URL.Path {
		case "GET /api/tags":
			rows := []ollamaModel{
				{Name: "qwen3:8b", Size: 20, Digest: digest},
				{Name: "library/granite:8b", Size: 20, Digest: digest},
				{Name: "embedding", Size: 20, Digest: digest},
				{Name: "remote", Size: 20, Digest: digest, RemoteHost: "https://fixture.invalid"},
				{Name: "marker:cloud", Size: 20, Digest: digest},
				{Name: "alias-cloud:latest", Size: 20, Digest: digest},
				{Name: "uninstalled", Digest: digest},
				{Name: "missing-digest", Size: 20},
				{Name: "stale", Size: 20, Digest: digest},
			}
			data, err := json.Marshal(map[string]any{"models": rows})
			if err != nil {
				t.Fatal(err)
			}
			payload = string(data)
		case "POST /api/show":
			var body struct{ Model string }
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			probe = append(probe, body.Model)
			switch body.Model {
			case "qwen3:8b", "library/granite:8b":
				payload = `{"capabilities":["completion","tools"]}`
			case "embedding":
				payload = `{"capabilities":["embedding"]}`
			case "stale":
				payload = `{"capabilities":["tools"],"remote_model":"provider-cloud"}`
			default:
				t.Fatalf("unverified model reached the metadata probe: %s", body.Model)
			}
		default:
			t.Fatalf("unexpected local probe: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}
	got, err := readLocalOllamaModels(context.Background(), client)
	want := []string{"library/granite:8b", "qwen3:8b"}
	if err != nil || !reflect.DeepEqual(got, want) || len(probe) != 4 {
		t.Fatalf("unexpected catalog: %v %v (probed %v)", got, err, probe)
	}
}

func TestCommandCodeLocalSetupPreservesOtherProviders(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":{"existing":{"baseURL":"https://fixture.invalid"}},"extra":{"keep":42}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{{"fixture:8b"}, {"fixture:12b"}} {
		if err := writeCommandCodeLocalProvider(home, names); err != nil {
			t.Fatal(err)
		}
	}
	config, providers, key, err := readCommandCodeProviders(home)
	if err != nil || key != "providers" || providers["existing"] == nil || config["extra"] == nil {
		t.Fatalf("setup changed unrelated config: %v %v", key, err)
	}
	provider, err := commandCodeLocalProfile(home)
	if err != nil || provider.BaseURL != ollamaAPI || provider.Models["fixture:12b"] == nil || len(provider.Models) != 1 {
		t.Fatalf("setup left an incorrect local provider: %+v %v", provider, err)
	}
	changed := `{"provider":{"buildheadway-ollama":{"baseURL":"https://fixture.invalid","apiKey":false,"models":{"fixture:12b":{}}}}}`
	if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := commandCodeLocalProfile(home); err == nil {
		t.Fatal("non-loopback provider was eligible")
	}
	if err := writeCommandCodeLocalProvider(home, []string{"fixture:12b"}); err == nil {
		t.Fatal("setup replaced an existing incompatible provider")
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != changed {
		t.Fatalf("setup changed a conflicting configuration: %v", err)
	}
}

func TestCommandCodeLocalSettingsRequireVerifiedFeatureModels(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	path := filepath.Join(repo, ".commandcode", "settings.json")
	for _, tc := range []struct {
		override string
		allowed  bool
	}{
		{"buildheadway-ollama/fixture:8b", true},
		{"buildheadway-ollama/removed:8b", false},
		{"another/remote-model", false},
	} {
		if err := AtomicJSON(path, map[string]any{"featureModels": map[string]string{"subagents": tc.override}}); err != nil {
			t.Fatal(err)
		}
		if err := checkCommandCodeLocalSettings(home, repo, []string{"fixture:8b"}); (err == nil) != tc.allowed {
			t.Fatalf("feature override %s: %v", tc.override, err)
		}
	}
}

func TestCommandCodeNDJSONRequiresCompletedPinnedModel(t *testing.T) {
	const end = `{"type":"event","event":{"type":"model_request_end","model":"buildheadway-ollama/fixture:8b","usage":{"inputTokens":12,"outputTokens":2}}}`
	const result = `{"type":"result","subtype":"success","stopReason":"end_turn","finalText":"verified","usage":{"inputTokens":12,"outputTokens":2}}`
	output, err := decodeCommandCodeOutput("read-only native warning\n"+end+"\n"+result, "fixture:8b")
	if err != nil || output != "verified" {
		t.Fatalf("unexpected result: %q %v", output, err)
	}
	for _, raw := range []string{
		result, strings.Replace(end, "fixture:8b", "alternate:8b", 1) + "\n" + result,
	} {
		_, err := decodeCommandCodeOutput(raw, "fixture:8b")
		var mismatch freeRouteMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("accepted a missing or different model: %v", err)
		}
	}
	_, err = decodeCommandCodeOutput(strings.Replace(end, `"inputTokens":12`, `"cost":0.5`, 1)+"\n"+result, "fixture:8b")
	var charge costError
	if !errors.As(err, &charge) {
		t.Fatalf("accepted a charge: %v", err)
	}
	partial := strings.Replace(result, "end_turn", "max_turns", 1)
	if _, err := decodeCommandCodeOutput(end+"\n"+partial, "fixture:8b"); err == nil {
		t.Fatal("accepted an incomplete result")
	}
}
