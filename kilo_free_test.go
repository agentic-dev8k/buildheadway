package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestKiloFreeRunPinsAnonymousProfile(t *testing.T) {
	profile := t.TempDir()
	env := kiloFreeRunEnv([]string{
		"PATH=fixture", "kilo_api_key=ambient", "KILOCODE_TOKEN=ambient", "KILO_CONFIG_CONTENT=ambient",
		"KILO_ORG_ID=ambient", "XDG_DATA_HOME=ambient", "OPENCODE_CONFIG_CONTENT=ambient",
	}, profile, kiloFreeModel{"vendor/new:free", 16000, 8000}, false)
	for _, name := range []string{"KILO_API_KEY", "KILOCODE_TOKEN", "KILO_ORG_ID", "OPENCODE_CONFIG_CONTENT"} {
		if envValue(env, name) != "" {
			t.Fatalf("unexpected inherited setting %s", name)
		}
	}
	if envValue(env, "PATH") != "fixture" || envValue(env, "XDG_DATA_HOME") != filepath.Join(profile, "data") ||
		envValue(env, "KILO_DISABLE_PROJECT_CONFIG") != "1" || envValue(env, "KILO_DISABLE_DEFAULT_PLUGINS") != "1" {
		t.Fatal("Kilo did not receive its isolated profile")
	}
	var config struct {
		Model     string   `json:"model"`
		Small     string   `json:"small_model"`
		Enabled   []string `json:"enabled_providers"`
		Share     string   `json:"share"`
		Providers map[string]struct {
			Options map[string]string `json:"options"`
			Models  map[string]struct {
				Tools bool           `json:"tool_call"`
				Limit map[string]int `json:"limit"`
			} `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(envValue(env, "KILO_CONFIG_CONTENT")), &config); err != nil {
		t.Fatal(err)
	}
	kilo := config.Providers["kilo"]
	model := kilo.Models["vendor/new:free"]
	if config.Model != "kilo/vendor/new:free" || config.Small != config.Model || config.Share != "disabled" ||
		len(config.Enabled) != 1 || config.Enabled[0] != "kilo" || len(kilo.Models) != 1 ||
		kilo.Options["baseURL"] != kiloGatewayURL || kilo.Options["apiKey"] != "" ||
		!model.Tools || model.Limit["context"] != 16000 || model.Limit["output"] != 8000 {
		t.Fatalf("unexpected Kilo route: %+v", config)
	}
}

func TestKiloNativeEventsDistinguishQuotaAndCost(t *testing.T) {
	const model = "kilo/vendor/test:free"
	for _, tc := range []struct {
		event, kind string
	}{
		{`{"type":"error","error":{"name":"APIError","data":{"statusCode":429}}}`, "quota"},
		{`{"type":"error","error":{"name":"APIError","data":{"statusCode":401}}}`, "unavailable"},
		{`{"type":"error","error":{"name":"APIError","data":{"statusCode":403}}}`, "policy"},
		{`{"type":"error","error":{"name":"UnknownError","data":{"message":"Model not found: kilo/vendor/test:free. Did you mean: another?"}}}`, "unavailable"},
		{`{"type":"step_finish","part":{"cost":1e-400}}`, "cost"},
		{`{"type":"text","part":{"text":"HTTP 429","cost":4}}`, ""},
		{`timestamp=2026-11-18T15:12:35.208Z level=ERROR message="stream error" providerID=kilo modelID=vendor/test:free error.error="AI_APICallError: Rate limit exceeded for free models. Try again later."`, "quota"},
		{`level=ERROR message="stream error" providerID=kilo modelID=vendor/other:free error.error="AI_APICallError: Rate limit exceeded"`, ""},
	} {
		var kind string
		if err := kiloFreeStreamError(model, tc.event); err != nil {
			kind = modelFailureKind(err, false)
		}
		if kind != tc.kind {
			t.Fatalf("unexpected classification %q for %s", kind, tc.event)
		}
	}
}

func TestKiloReceiptChecksTheSelectedSessionAndModel(t *testing.T) {
	const session = "ses_abc"
	const model = "kilo/vendor/test:free"
	const event = `{"type":"step_finish","sessionID":"ses_abc","part":{"cost":0}}`
	if got := kiloRunSession(event); got != session {
		t.Fatalf("got session %q", got)
	}
	if got := kiloRunSession(event + "\n" + strings.Replace(event, "ses_abc", "ses_other", 1)); got != "" {
		t.Fatal("accepted multiple session identities")
	}
	const receipt = `{
		"info":{"id":"ses_abc"},
		"messages":[{"info":{"role":"user"}},{"info":{"role":"assistant","sessionID":"ses_abc","providerID":"kilo","modelID":"vendor/test:free","cost":0}}]
	}`
	if err := checkKiloExport("Exporting session: ses_abc\n"+receipt, session, model); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(receipt, "vendor/test:free", "vendor/other:free", 1),
		strings.Replace(receipt, "ses_abc", "ses_bad", 1),
	} {
		var mismatch freeRouteMismatchError
		if err := checkKiloExport(raw, session, model); !errors.As(err, &mismatch) {
			t.Fatalf("accepted a mismatched native receipt: %v", err)
		}
	}
	var charge costError
	if err := checkKiloExport(strings.Replace(receipt, `"cost":0`, `"cost":0.001`, 1), session, model); !errors.As(err, &charge) {
		t.Fatalf("accepted a receipt with a charge: %v", err)
	}
}
