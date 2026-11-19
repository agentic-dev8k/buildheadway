package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPiLocalCatalogsRequireLoadedToolModels(t *testing.T) {
	for _, tc := range []struct {
		platform, origin, path, metadata, served string
		want                                     []localPiModel
	}{
		{
			"lmstudio", lmStudioOrigin, "/api/v1/models",
			`{"models":[
				{"type":"llm","capabilities":{"trained_for_tool_use":true},"loaded_instances":[
					{"id":"fixture/qwen","config":{"context_length":8192}},{"id":"hidden","config":{"context_length":8192}}
				]},
				{"type":"llm","capabilities":{"trained_for_tool_use":true},"loaded_instances":[]},
				{"type":"llm","capabilities":{"trained_for_tool_use":false},"loaded_instances":[{"id":"text","config":{"context_length":8192}}]},
				{"type":"embedding","capabilities":{"trained_for_tool_use":true},"loaded_instances":[{"id":"embedding","config":{"context_length":8192}}]}
			]}`,
			`{"data":[{"id":"fixture/qwen"},{"id":"downloaded-only"},{"id":"text"},{"id":"embedding"}]}`,
			[]localPiModel{{"fixture/qwen", 8192}},
		},
		{
			"llamacpp", llamaCppOrigin, "/props",
			`{"model_alias":"qwen.gguf","model_path":"/fixture/qwen.gguf","is_sleeping":false,
				"default_generation_settings":{"n_ctx":16384},"chat_template_caps":{"supports_tools":true,"supports_tool_calls":true}}`,
			`{"data":[{"id":"qwen.gguf"},{"id":"listed-only"}]}`,
			[]localPiModel{{"qwen.gguf", 16384}},
		},
		{
			"llamacpp", llamaCppOrigin, "/props",
			`{"model_path":"/fixture/qwen.gguf","is_sleeping":true,
				"default_generation_settings":{"n_ctx":16384},"chat_template_caps":{"supports_tools":true,"supports_tool_calls":true}}`,
			"", nil,
		},
		{
			"llamacpp", llamaCppOrigin, "/props",
			`{"model_path":"/fixture/qwen.gguf","default_generation_settings":{"n_ctx":8192},
				"chat_template_caps":{"supports_tools":true,"supports_tool_calls":true}}`,
			`{"data":[{"id":"one"},{"id":"two"}]}`, nil,
		},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			client := &http.Client{Transport: freeCatalogTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Scheme+"://"+req.URL.Host != tc.origin || req.Header.Get("Authorization") != "" {
					t.Errorf("unexpected readiness request: %s %s", req.Method, req.URL)
				}
				var payload string
				switch req.URL.Path {
				case tc.path:
					payload = tc.metadata
				case "/v1/models":
					payload = tc.served
				default:
					t.Errorf("unexpected metadata path: %s", req.URL.Path)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload))}, nil
			})}
			models, err := readLocalPiModels(context.Background(), client, tc.platform)
			if !reflect.DeepEqual(models, tc.want) || (err == nil) != (tc.want != nil) {
				t.Fatalf("unexpected eligible models: %v %v", models, err)
			}
		})
	}
}

func TestPiLocalRelayPinsModelAndPreventsRedirects(t *testing.T) {
	var forwarded atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != localPiCompletionPath || r.Header.Get("Authorization") != "" ||
			r.Header.Get(localPiSessionHeader) != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected local inference request: %s %s", r.Method, r.URL)
		}
		data, _ := io.ReadAll(r.Body)
		if strings.Contains(string(data), "redirect") {
			http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	relay, err := startLocalPiRelay(upstream.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.server.Close()
	for _, tc := range []struct {
		session, body string
		status        int
	}{
		{"other-session", `{"model":"fixture"}`, 401},
		{relay.session, `{"model":"different"}`, 400},
		{relay.session, `{"model":"fixture"}`, 200},
		{relay.session, `{"model":"fixture","marker":"redirect"}`, 502},
	} {
		req, err := http.NewRequest(http.MethodPost, relay.endpoint+"/chat/completions", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(localPiSessionHeader, tc.session)
		req.Header.Set("Authorization", "Bearer fixture")
		req.Header.Set("Cookie", "profile=fixture")
		resp, err := localPiClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status || resp.Header.Get("Location") != "" {
			t.Fatalf("unexpected relay response: %d %s", resp.StatusCode, data)
		}
		if resp.StatusCode == 200 && string(data) != "data: [DONE]\n\n" {
			t.Fatalf("the response stream changed: %q", data)
		}
	}
	if forwarded.Load() != 2 {
		t.Fatalf("unexpected number of forwarded calls: %d", forwarded.Load())
	}
}

func TestPiLocalReceiptRequiresNativeModelAndZeroCost(t *testing.T) {
	const receipt = `{"type":"message_end","message":{"role":"assistant","provider":"buildheadway-local","model":"fixture","usage":{"cost":{"input":0,"output":0,"total":0,"totalFromProvider":false}}}}`
	if err := verifyLocalPiOutput(receipt, "fixture"); err != nil {
		t.Fatal(err)
	}
	var mismatch freeRouteMismatchError
	if err := verifyLocalPiOutput(strings.Replace(receipt, "buildheadway-local", "other", 1), "fixture"); !errors.As(err, &mismatch) {
		t.Fatalf("accepted a different model provider: %v", err)
	}
	var charge costError
	if err := verifyLocalPiOutput(strings.Replace(receipt, `"input":0`, `"input":1e-900`, 1), "fixture"); !errors.As(err, &charge) {
		t.Fatalf("accepted a reported charge: %v", err)
	}
}

func TestPiLocalRoutesUseAnotherProviderAfterModelDisappears(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.EnableFigureOut("full"); err != nil {
		t.Fatal(err)
	}
	r.freePlatforms = []string{"kilo", "lmstudio", "llamacpp"}
	models := []string{"kilo/vendor/promo:free", "lmstudio/fixture/model", "llamacpp/fixture.gguf"}
	r.Discover = func(context.Context) ([]string, error) { return models, nil }
	var used []string
	r.Invoke = func(_ context.Context, connection Connection, _, _ string, _ bool, model string) (string, error) {
		used = append(used, model)
		switch model {
		case models[0]:
			return "", freeQuotaError{platform: "kilo"}
		case models[1], models[2]:
			if connection.Backend != "pi" || connection.Provider != localPiProvider || !connection.RequireFree {
				t.Fatalf("lost the Pi routing profile: %+v", connection)
			}
			if model == models[1] {
				return "", freeOfferUnavailableError{model}
			}
			return "Implemented the requested change.", nil
		default:
			t.Fatalf("unexpected model %s", model)
			return "", nil
		}
	}
	got, err := r.workOnce(context.Background(), Task{"one", "Work", "Implement"}, "")
	if err != nil || !got.OK || got.Model != models[2] || !reflect.DeepEqual(used, models) {
		t.Fatalf("unexpected route: %+v, %v, %v", got, err, used)
	}
}
