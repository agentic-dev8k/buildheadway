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
	"time"
)

type freeCatalogTransport func(*http.Request) (*http.Response, error)

func (f freeCatalogTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestFreePlatformChecksRunTogetherAndKeepDirectoryOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	clineStarted, routerReady := make(chan struct{}), make(chan struct{})
	client := &http.Client{Transport: freeCatalogTransport(func(req *http.Request) (*http.Response, error) {
		var ready <-chan struct{}
		var payload string
		switch req.URL.String() {
		case clineFreeCatalog:
			close(clineStarted)
			ready, payload = routerReady, `{"free":[{"id":"cline-free/fixture","supportsTools":true}]}`
		case openRouterCatalog:
			defer close(routerReady)
			ready, payload = clineStarted, `{"data":[{"id":"vendor/fixture:free","pricing":{"prompt":0,"completion":0},"supported_parameters":["tools"]}]}`
		default:
			return nil, errors.New("unexpected catalog")
		}
		select {
		case <-ready:
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload))}, nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})}
	rows := []FreePlatformStatus{{Platform: "cline"}, {Platform: "openrouter"}}
	probeFreePlatformRows(ctx, testPaths(t), rows, client)
	for i, want := range []string{"cline/cline-free/fixture", "openrouter/vendor/fixture:free"} {
		if rows[i].Status != "catalog_checked" || !reflect.DeepEqual(rows[i].Models, []string{want}) {
			t.Fatalf("unexpected row at index %d: %+v", i, rows[i])
		}
	}
}

func TestFreePlatformCatalogsAdmitOnlyRecognizedOffers(t *testing.T) {
	for _, tc := range []struct {
		platform, url, payload string
		want                   []string
	}{
		{"cline", clineFreeCatalog, `{"free":[{"id":"cline-free/trial"},{"id":"vendor/free-text","supportsTools":false},{"id":"vendor/paid"},{"id":"vendor/code-free","supportsTools":true},{"id":"cline-free/trial"}],"recommended":[{"id":"vendor/other:free"}]}`,
			[]string{"cline/cline-free/trial", "cline/vendor/code-free"}},
		{"openrouter", openRouterCatalog, `{"data":[
			{"id":"vendor/good:free","pricing":{"prompt":"0","completion":"0","request":"0.0e-9"},"supported_parameters":["tools"]},
			{"id":"vendor/paid:free","pricing":{"prompt":"0","completion":"0","request":"1e-900"},"supported_parameters":["tools"]},
			{"id":"vendor/repriced:free","pricing":{"prompt":"0","completion":"0.01"},"supported_parameters":["tools"]},
			{"id":"vendor/missing:free","pricing":{"prompt":"0"},"supported_parameters":["tools"]},
			{"id":"vendor/no-tools:free","pricing":{"prompt":"0","completion":"0"},"supported_parameters":["temperature"]},
			{"id":"vendor/mixed","pricing":{"prompt":"0","completion":"0"},"supported_parameters":["tools"]},
			{"id":"vendor/invalid:free","pricing":{"prompt":0,"completion":0,"web_search":null},"supported_parameters":["tools"]}
		]}`, []string{"openrouter/vendor/good:free"}},
		{"kilo", kiloFreeCatalog, `{"data":[
			{"id":"kilo-auto/free","context_length":256000,"pricing":{"prompt":"0","completion":"0"},"supported_parameters":["tools"]},
			{"id":"vendor/fresh:free","context_length":8192,"pricing":{"prompt":0,"completion":0,"discount":0},"supported_parameters":["tools"]},
			{"id":"vendor/paid:free","context_length":8192,"pricing":{"prompt":0,"completion":0,"request":"1e-500"},"supported_parameters":["tools"]},
			{"id":"vendor/text:free","context_length":8192,"pricing":{"prompt":0,"completion":0},"supported_parameters":["temperature"]},
			{"id":"vendor/unpriced:free","context_length":8192,"pricing":{"prompt":0},"supported_parameters":["tools"]},
			{"id":"vendor/unknown:free","context_length":"unknown","pricing":{"prompt":0,"completion":0},"supported_parameters":["tools"]},
			{"id":"vendor/undesignated","context_length":8192,"pricing":{"prompt":0,"completion":0},"supported_parameters":["tools"]}
		]}`, []string{"kilo/kilo-auto/free", "kilo/vendor/fresh:free"}},
		{"commandcode", commandCodeCatalog, `{"data":[
			{"id":"vendor/offer:free","owned_by":"command-code","supported_endpoints":["/chat/completions"]},
			{"id":"vendor/paid","owned_by":"command-code","supported_endpoints":["/chat/completions"]},
			{"id":"vendor/decision:free","owned_by":"command-code","supported_endpoints":["/systemone"]},
			{"id":"vendor/alien:free","owned_by":"someone-else","supported_endpoints":["/chat/completions"]}
		]}`, []string{"vendor/offer:free"}},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			client := &http.Client{Transport: freeCatalogTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.String() != tc.url || req.Header.Get("Authorization") != "" ||
					req.Header.Get("Cache-Control") != "no-cache" || req.Body != nil {
					t.Fatalf("unexpected catalog request: %s %s", req.Method, req.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.payload))}, nil
			})}
			got, err := readAdditionalFreeCatalog(context.Background(), client, tc.platform)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wrong offers: %v %v", got, err)
			}
		})
	}
	if _, err := selectedFreePlatforms("commandcode"); err == nil {
		t.Fatal("discovery data could authorize a dispatch")
	}
}

func TestFreeWorkerSwitchesIndependentPlatformsOnQuota(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.EnableFigureOut("full"); err != nil {
		t.Fatal(err)
	}
	r.freePlatforms = []string{"cline", "openrouter", "kilo", "ollama"}
	calls, refreshes := []string{}, 0
	r.Discover = func(context.Context) ([]string, error) {
		refreshes++
		return []string{"opencode/first", "opencode/another", "cline/cline-free/trial", "openrouter/vendor/backup:free", "kilo/vendor/fast:free", "kilo/kilo-auto/free", "ollama/fixture:8b"}, nil
	}
	r.Invoke = func(_ context.Context, c Connection, _, _ string, planner bool, model string) (string, error) {
		calls = append(calls, c.Provider+"|"+model)
		switch len(calls) {
		case 1:
			return "", freeQuotaError{}
		case 2:
			if c.Backend != "cline" || c.Provider != "cline" || !c.RequireFree {
				t.Fatalf("Cline route lost its connection: %#v", c)
			}
			return "", freeQuotaError{platform: "cline"}
		case 3:
			if c.Backend != "cline" || c.Provider != "openrouter" || !c.RequireFree {
				t.Fatalf("OpenRouter route lost its connection: %#v", c)
			}
			return "", freeQuotaError{platform: "openrouter"}
		case 4:
			if c.Backend != "kilo" || c.Provider != "kilo" || !c.RequireFree || c.Model != "kilo/vendor/fast:free" {
				t.Fatalf("Kilo route lost its connection: %#v", c)
			}
			return "", freeQuotaError{platform: "kilo"}
		case 5:
			if c.Backend != "commandcode" || c.Provider != commandCodeOllamaProvider || !c.RequireFree {
				t.Fatalf("Ollama route lost its local Command Code connection: %#v", c)
			}
			return "Implemented the requested change.", nil
		}
		t.Fatalf("unexpected call %v", calls)
		return "", nil
	}
	result, err := r.workOnce(context.Background(), Task{"one", "Work", "Implement"}, "")
	want := []string{"|opencode/first", "cline|cline-free/trial", "openrouter|vendor/backup:free", "kilo|kilo/vendor/fast:free", "buildheadway-ollama|fixture:8b"}
	if err != nil || !result.OK || result.Model != "ollama/fixture:8b" || !reflect.DeepEqual(calls, want) || refreshes != 5 {
		t.Fatalf("incorrect failover: %+v %v calls=%v refreshes=%d", result, err, calls, refreshes)
	}
	if r.State.modelHealth("worker", "opencode/another") != nil || r.State.modelHealth("worker", "kilo/kilo-auto/free") != nil || r.State.WorkerCalls != 5 {
		t.Fatal("rotation reused a limited platform or lost a counted call")
	}
}

func TestFreePlatformRestrictionsAndPartialWork(t *testing.T) {
	for _, tc := range []struct {
		name, access string
		pin, edit    bool
	}{
		{name: "restricted workspace", access: "workspace"},
		{name: "pinned worker", access: "full", pin: true},
		{name: "partial edit", access: "full", edit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			if err := r.EnableFigureOut(tc.access); err != nil {
				t.Fatal(err)
			}
			r.freePlatforms = []string{"cline"}
			if tc.pin {
				r.Settings.Worker.Model = "opencode/first"
			}
			r.Discover = func(context.Context) ([]string, error) {
				return []string{"opencode/first", "cline/cline-free/trial"}, nil
			}
			calls := 0
			r.Invoke = func(_ context.Context, c Connection, _, _ string, _ bool, model string) (string, error) {
				calls++
				if calls != 1 || c.Backend != "opencode" || model != "opencode/first" {
					t.Fatalf("unadmitted fallback or repeated dispatch: %+v %s", c, model)
				}
				if tc.edit {
					if err := os.WriteFile(filepath.Join(r.Repo, "partial.txt"), []byte("partial"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return "", freeQuotaError{}
			}
			result, err := r.workOnce(context.Background(), Task{"one", "Work", "Implement"}, "")
			var quota freeQuotaError
			if tc.edit {
				if err != nil || !strings.Contains(result.Report, "Partial edits") {
					t.Fatalf("partial work wasn't sent through verification: %+v %v", result, err)
				}
			} else if !errors.As(err, &quota) {
				t.Fatalf("the selected platform did not pause: %v", err)
			}
		})
	}
}

func TestFreePlannerRoutingPreservesMCPRequirements(t *testing.T) {
	for _, activeMCP := range []bool{false, true} {
		r, _ := fixtureRunner(t)
		r.Settings.Planner = Connection{Backend: "opencode", Model: "auto"}
		if err := r.EnableFigureOut("full"); err != nil {
			t.Fatal(err)
		}
		if activeMCP {
			r.State.Config.MCPServers = map[string]MCPServer{"needed": {Command: "required-agent-tool"}}
		}
		r.freePlatforms = []string{"cline"}
		r.Discover = func(context.Context) ([]string, error) {
			return []string{"opencode/first", "cline/cline-free/trial"}, nil
		}
		calls := 0
		r.Invoke = func(_ context.Context, c Connection, _, _ string, planner bool, _ string) (string, error) {
			calls++
			if calls == 1 && planner && c.Backend == "opencode" {
				return "", freeQuotaError{}
			}
			if !planner || activeMCP || c.Backend != "cline" || c.Model != "cline-free/trial" || c.Provider != "cline" {
				t.Fatalf("unexpected planner route: %+v", c)
			}
			return decisionJSON(true, nil, nil), nil
		}
		_, err := r.askDecision(context.Background(), false, r.Settings.Planner, "Plan the goal", "council-lead-plan", "lead")
		if activeMCP {
			var quota freeQuotaError
			if calls != 1 || !errors.As(err, &quota) {
				t.Fatalf("MCP requirement was lost: calls=%d err=%v", calls, err)
			}
		} else if calls != 2 || err != nil || r.State.PlannerModel != "cline/cline-free/trial" {
			t.Fatalf("planner route was not switched: calls=%d model=%s err=%v", calls, r.State.PlannerModel, err)
		}
	}
}

func TestManagedClineProfileRejectsEndpointOverrides(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "data", "settings", "providers.json")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		url string
		ok  bool
	}{
		{"", true},
		{"https://api.cline.bot/api/v1/", true},
		{"https://api.cline.bot.example.org/api/v1", false},
		{"http://127.0.0.1:9000", false},
	} {
		url, _ := json.Marshal(tc.url)
		settings := `{"providers":{"cline":{"settings":{"baseUrl":` + string(url) + `,"auth":{"accessToken":"fixture"}}}}}`
		if err := os.WriteFile(file, []byte(settings), 0600); err != nil {
			t.Fatal(err)
		}
		if err := checkClineProfileURL(dir); (err == nil) != tc.ok || (err != nil && strings.Contains(err.Error(), "fixture")) {
			t.Fatalf("unexpected endpoint gate for %q: %v", tc.url, err)
		}
	}
}

func TestNativeFreeClinePinsModelAndRejectsUnexpectedCosts(t *testing.T) {
	for _, tc := range []struct{ name, payload, failure string }{
		{"complete", `{"type":"run_result","finishReason":"completed","model":{"id":"vendor/code:free","provider":"openrouter"},"usage":{"totalCost":0},"text":"Done"}`, ""},
		{"mismatch", `{"type":"run_result","finishReason":"completed","model":{"id":"vendor/costly","provider":"openrouter"},"usage":{"totalCost":0},"text":"Done"}`, "policy"},
		{"unexpected provider", `{"type":"run_result","finishReason":"completed","model":{"id":"vendor/code:free","provider":"cline"},"usage":{"totalCost":0},"text":"Done"}`, "policy"},
		{"cost", `{"type":"run_result","finishReason":"completed","model":{"id":"vendor/code:free","provider":"openrouter"},"usage":{"totalCost":1e-999},"text":"Done"}`, "cost"},
		{"quota", `{"type":"agent_event","event":{"type":"error","errorClass":"unknown","error":{"message":"Rate limit exceeded"}}}`, "quota"},
		{"auth", `{"type":"agent_event","event":{"type":"error","errorClass":"auth","error":{"message":"Unauthorized with API key"}}}`, "auth"},
		{"no receipt", `{"type":"agent_event","event":{"type":"text","text":"Done"}}`, "policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			if err := r.EnableFigureOut("full"); err != nil {
				t.Fatal(err)
			}
			c := costStreamFixture(t, r, tc.payload+"\n", false)
			c.Backend, c.Provider, c.Model, c.RequireFree = "cline", "openrouter", "vendor/code:free", true
			t.Setenv("OPENROUTER_API_KEY", "test-token-only")
			t.Setenv("CLINE_DIR", "ignore-original-profile")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := r.invokeCLI(ctx, c, "Implement", "free-worker", false, c.Model)
			if (tc.failure == "" && err != nil) || (tc.failure != "" && (err == nil || modelFailureKind(err, false) != tc.failure)) {
				t.Fatalf("incorrect Cline receipt handling: %v", err)
			}
			var capture struct {
				Args []string `json:"args"`
			}
			if err := ReadJSON(os.Getenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE"), &capture); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(capture.Args)
			for _, expected := range []string{`"--provider","openrouter"`, `"--model","vendor/code:free"`, `"--config"`} {
				if !strings.Contains(string(args), expected) {
					t.Fatalf("missing native route option %s in %s", expected, args)
				}
			}
			if strings.Contains(string(args), "test-token-only") || strings.Contains(string(args), "ignore-original-profile") {
				t.Fatalf("native CLI inherited unintended credentials/profile in argv: %s", args)
			}
		})
	}
}
