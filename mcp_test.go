package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func diagnosticMCPServer(calls *atomic.Int32) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "regression-server", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "echo", Description: "inspect available tools", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{}, nil
	})
	return server
}

func TestMCPHTTPCheckNegotiatesListsToolsWithoutInvokingThem(t *testing.T) {
	var calls atomic.Int32
	server := diagnosticMCPServer(&calls)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	t.Setenv("BUILDHEADWAY_MCP_TOKEN", "private-fixture-token")
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-fixture-token" {
			http.Error(w, "private-fixture-token", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer endpoint.Close()
	result, err := CheckMCP(context.Background(), testPaths(t), t.TempDir(), "docs", MCPServer{URL: endpoint.URL, BearerTokenEnv: "BUILDHEADWAY_MCP_TOKEN"})
	if err != nil || !result.Connected || result.Server != "regression-server" || len(result.Tools) != 1 || result.Tools[0].Name != "echo" || calls.Load() != 0 {
		t.Fatalf("MCP handshake/listing failed or called a tool: %+v %v", result, err)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "private-fixture-token") {
		t.Fatal("diagnostic contains credentials")
	}
}

func TestMCPHTTPCheckBoundsResponsesAndDoesNotLeakErrors(t *testing.T) {
	for _, mode := range []string{"auth", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "auth" {
					http.Error(w, "secret-echo-from-server", http.StatusUnauthorized)
					return
				}
				var rpc struct {
					ID any `json:"id"`
				}
				_ = json.NewDecoder(r.Body).Decode(&rpc)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "large", "version": "1"}, "instructions": strings.Repeat("x", mcpDiagnosticLimit+1)}})
			}))
			defer endpoint.Close()
			result, err := CheckMCP(context.Background(), testPaths(t), t.TempDir(), "broken", MCPServer{URL: endpoint.URL, TimeoutSeconds: 2})
			if err == nil || result.Connected || strings.Contains(err.Error(), "secret-echo") || strings.Contains(err.Error(), endpoint.URL) {
				t.Fatalf("unbounded or unsafe diagnostic: %+v %v", result, err)
			}
		})
	}
}

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_TEST_MCP_SERVER") != "1" {
		return
	}
	if os.Getenv("BUILDHEADWAY_TEST_MCP_HANG") == "1" {
		time.Sleep(time.Minute)
		os.Exit(1)
	}
	var calls atomic.Int32
	if err := diagnosticMCPServer(&calls).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestMCPStdioCheckAndTimeoutUseRealProcesses(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDHEADWAY_TEST_MCP_SERVER", "1")
	server := MCPServer{Command: binary, Args: []string{"-test.run=^TestMCPStdioHelper$"}, Env: []string{"BUILDHEADWAY_TEST_MCP_SERVER"}, TimeoutSeconds: 2}
	result, err := CheckMCP(context.Background(), testPaths(t), t.TempDir(), "local", server)
	if err != nil || !result.Connected || len(result.Tools) != 1 {
		t.Fatalf("stdio did not connect: %+v %v", result, err)
	}
	t.Setenv("BUILDHEADWAY_TEST_MCP_HANG", "1")
	server.Env = append(server.Env, "BUILDHEADWAY_TEST_MCP_HANG")
	server.TimeoutSeconds = 1
	start := time.Now()
	result, err = CheckMCP(context.Background(), testPaths(t), t.TempDir(), "local", server)
	if err == nil || result.Connected || time.Since(start) > 5*time.Second {
		t.Fatalf("MCP timeout did not terminate child: %+v %v", result, err)
	}
}

func TestMCPDiagnosticBodyAcceptsExactLimitAndRejectsOverflow(t *testing.T) {
	for _, extra := range []int{0, 1} {
		body := &mcpLimitedBody{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("x", mcpDiagnosticLimit+extra))), remaining: mcpDiagnosticLimit}
		data, err := io.ReadAll(body)
		if (err == nil) != (extra == 0) || len(data) != mcpDiagnosticLimit {
			t.Fatalf("incorrect diagnostic boundary: extra=%d bytes=%d err=%v", extra, len(data), err)
		}
	}
}

func TestMCPAdapterProfilesRetainIsolationAndCredentialReferences(t *testing.T) {
	t.Setenv("BUILDHEADWAY_MCP_TOKEN", "do-not-save-me")
	for _, backend := range []string{"codex", "claude", "opencode"} {
		for _, mode := range []string{"full", "consultation", "worker"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				r, _ := fixtureRunner(t)
				if err := os.MkdirAll(r.Dir, 0700); err != nil {
					t.Fatal(err)
				}
				r.State.Config.MCPServers = map[string]MCPServer{"docs": {URL: "https://example.invalid/mcp", BearerTokenEnv: "BUILDHEADWAY_MCP_TOKEN"}}
				binary, _ := os.Executable()
				t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", filepath.Join(t.TempDir(), "capture.json"))
				connection := Connection{Backend: backend, Executable: binary}
				env := opencodeAccessEnv(r.Paths, "opencode/free", mode != "worker", mode == "full")
				args, env, err := r.prepareMCP(context.Background(), connection, mode != "worker", mode == "full", []string{binary}, env)
				if err != nil {
					t.Fatal(err)
				}
				profile := strings.Join(args, " ")
				switch backend {
				case "claude":
					data, err := os.ReadFile(filepath.Join(r.Dir, "mcp-session.json"))
					if err != nil {
						t.Fatal(err)
					}
					profile += string(data)
					if !contains(args, "--strict-mcp-config") {
						t.Fatal("inherited unrelated MCP config")
					}
				case "opencode":
					profile += envValue(env, "OPENCODE_CONFIG_CONTENT")
					if envValue(env, "OPENCODE_DISABLE_DEFAULT_PLUGINS") != "true" {
						t.Fatal("enabled worker plugins")
					}
				}
				if strings.Contains(profile, "do-not-save-me") || strings.Contains(profile, "https://example.invalid/mcp") != (mode == "full") {
					t.Fatalf("wrong MCP role/secret boundary: %s", profile)
				}
				if mode == "full" && !strings.Contains(profile, "BUILDHEADWAY_MCP_TOKEN") {
					t.Fatal("lost credential reference")
				}
			})
		}
	}
}

func TestMCPSetupBlockerDoesNotRetryOrChangeModel(t *testing.T) {
	r, _ := fixtureRunner(t)
	calls := 0
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		calls++
		return "", mcpSetupError{"MCP configuration unavailable"}
	}
	err := r.Run(context.Background())
	var setup mcpSetupError
	if !errors.As(err, &setup) || calls != 1 || r.State.Status != "blocked" {
		t.Fatalf("retried MCP setup: calls=%d status=%s err=%v", calls, r.State.Status, err)
	}
	if _, _, err := r.prepareMCP(context.Background(), Connection{Backend: "pi"}, true, true, nil, nil); err != nil {
		t.Fatal("unconfigured MCP changed old adapters")
	}
	r.State.Config.MCPServers = map[string]MCPServer{"docs": {URL: "https://example.invalid"}}
	if _, _, err := r.prepareMCP(context.Background(), Connection{Backend: "pi"}, true, true, nil, nil); !errors.As(err, &setup) {
		t.Fatal(fmt.Sprintf("silently ignored unsupported MCP adapter: %v", err))
	}
}

func TestMCPProfilesReachActualAgentSubprocesses(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"codex", "claude", "opencode"} {
		t.Run(backend, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			if err := r.EnableFigureOut("full"); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(r.Dir, 0700); err != nil {
				t.Fatal(err)
			}
			r.State.Config.MCPServers = map[string]MCPServer{"fixture": {Command: "node", Args: []string{"mcp server.cjs"}}}
			capture := filepath.Join(t.TempDir(), "capture.json")
			t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", capture)
			t.Setenv("BUILDHEADWAY_TEST_ADAPTER_REPLY", decisionJSON(true, nil, nil))
			_, err := r.invokeCLI(context.Background(), Connection{Backend: backend, Model: "opencode/free", Executable: binary}, "Inspect tools", "plan", true, "")
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Args   []string `json:"args"`
				Config string   `json:"opencode_config"`
			}
			if err := ReadJSON(capture, &got); err != nil {
				t.Fatal(err)
			}
			payload := strings.Join(got.Args, " ") + got.Config
			if backend == "claude" {
				data, err := os.ReadFile(filepath.Join(r.Dir, "mcp-session.json"))
				if err != nil {
					t.Fatal(err)
				}
				payload += string(data)
			}
			if !strings.Contains(payload, "fixture") || !strings.Contains(payload, "mcp server.cjs") {
				t.Fatalf("MCP profile never reached the child: %s", payload)
			}
		})
	}
}

func TestMCPClaudeStdioWithoutArgumentsUsesAnArray(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := os.MkdirAll(r.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	r.State.Config.MCPServers = map[string]MCPServer{"local": {Command: "my-server"}}
	binary, _ := os.Executable()
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", filepath.Join(t.TempDir(), "capture.json"))
	if _, _, err := r.prepareMCP(context.Background(), Connection{Backend: "claude", Executable: binary}, true, true, []string{binary}, nil); err != nil {
		t.Fatal(err)
	}
	var profile map[string]map[string]map[string]any
	if err := ReadJSON(filepath.Join(r.Dir, "mcp-session.json"), &profile); err != nil {
		t.Fatal(err)
	}
	args, ok := profile["mcpServers"]["local"]["args"].([]any)
	if !ok || len(args) != 0 {
		t.Fatal("stdio args must be an empty array, not null")
	}
}
