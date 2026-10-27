package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpDiagnosticLimit = 1 << 20

type MCPToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
type MCPCheckReport struct {
	Name      string           `json:"name"`
	Connected bool             `json:"connected"`
	Protocol  string           `json:"protocol"`
	Server    string           `json:"server"`
	Tools     []MCPToolSummary `json:"tools"`
}

func mcpCredentials(name string, server MCPServer) error {
	keys := append([]string{}, server.Env...)
	if server.BearerTokenEnv != "" {
		keys = append(keys, server.BearerTokenEnv)
	}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); !ok || value == "" {
			return fmt.Errorf("MCP server %s requires environment variable %s", name, key)
		}
	}
	return nil
}

// The SDK owns framing and protocol negotiation. These wrappers bound diagnostic
// input and preserve BuildHeadway's cross-platform process-tree cancellation.
type mcpCommandTransport struct{ command *exec.Cmd }
type mcpProcessInput struct {
	io.WriteCloser
	command *exec.Cmd
	once    sync.Once
}

func (w *mcpProcessInput) Close() error {
	w.once.Do(func() {
		_ = w.WriteCloser.Close()
		done := make(chan error, 1)
		go func() { done <- w.command.Wait() }()
		select {
		case <-done:
		case <-time.After(250 * time.Millisecond):
			_ = terminateProcess(w.command)
			<-done
		}
	})
	return nil
}
func (t *mcpCommandTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	stdout, err := t.command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := t.command.StdinPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, err
	}
	if err := t.command.Start(); err != nil {
		_ = stdout.Close()
		_ = stdin.Close()
		return nil, err
	}
	transport := &mcp.IOTransport{Reader: stdout, Writer: &mcpProcessInput{WriteCloser: stdin, command: t.command}, MaxLineLength: mcpDiagnosticLimit}
	return transport.Connect(ctx)
}

type mcpLimitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *mcpLimitedBody) Read(data []byte) (int, error) {
	if b.remaining <= 0 {
		var extra [1]byte
		n, err := b.ReadCloser.Read(extra[:])
		if n != 0 {
			return 0, errors.New("MCP diagnostic response exceeds 1 MiB")
		}
		return 0, err
	}
	if int64(len(data)) > b.remaining {
		data = data[:b.remaining]
	}
	n, err := b.ReadCloser.Read(data)
	b.remaining -= int64(n)
	return n, err
}

type mcpHTTPTransport struct {
	base  http.RoundTripper
	token string
}

func (t mcpHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	if t.token != "" {
		request.Header.Set("Authorization", "Bearer "+t.token)
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = &mcpLimitedBody{ReadCloser: response.Body, remaining: mcpDiagnosticLimit}
	return response, nil
}

func mcpProcessEnv(paths Paths, server MCPServer) []string {
	allowed := map[string]bool{}
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SYSTEMROOT", "WINDIR", "COMSPEC", "TMP", "TEMP", "TMPDIR", "LANG", "LC_ALL", "PATHEXT"} {
		allowed[key] = true
	}
	for _, key := range server.Env {
		allowed[strings.ToUpper(key)] = true
	}
	var env []string
	for _, entry := range ToolEnv(paths) {
		key, _, _ := strings.Cut(entry, "=")
		if allowed[strings.ToUpper(key)] {
			env = append(env, entry)
		}
	}
	return env
}

func CheckMCP(parent context.Context, paths Paths, repo, name string, server MCPServer) (MCPCheckReport, error) {
	result := MCPCheckReport{Name: name, Tools: []MCPToolSummary{}}
	if err := validateMCPServers(map[string]MCPServer{name: server}); err != nil {
		return result, err
	}
	if server.Disabled {
		return result, errors.New("MCP server is disabled; enable it before checking")
	}
	if err := mcpCredentials(name, server); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(parent, server.timeout())
	defer cancel()
	var transport mcp.Transport
	if server.Command != "" {
		command, err := PreparedCommand(ctx, append([]string{server.Command}, server.Args...), ToolEnv(paths))
		if err != nil {
			return result, fmt.Errorf("MCP server %s executable is unavailable; check command and PATH", name)
		}
		command.Dir, command.Env, command.Stderr = repo, mcpProcessEnv(paths, server), io.Discard
		command.Env = setEnv(command.Env, "PWD", repo)
		configureProcess(command)
		command.Cancel = func() error { return terminateProcess(command) }
		command.WaitDelay = time.Second
		transport = &mcpCommandTransport{command}
	} else {
		token := ""
		if server.BearerTokenEnv != "" {
			token = os.Getenv(server.BearerTokenEnv)
		}
		client := &http.Client{Transport: mcpHTTPTransport{base: http.DefaultTransport, token: token}, Timeout: server.timeout(), CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("MCP diagnostic redirects are not followed")
		}}
		transport = &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: mcpDiagnosticLimit}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "buildheadway-check", Version: Version}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := client.Connect(ctx, transport, nil)
	// Server/transport errors may contain echoed credentials or credential URLs.
	// Never include their raw text in terminal output, JSON, checkpoints or reports.
	failure := func() error {
		if parent.Err() != nil {
			return parent.Err()
		}
		if ctx.Err() != nil {
			return fmt.Errorf("MCP server %s check timed out: %w", name, ctx.Err())
		}
		return fmt.Errorf("MCP server %s could not initialize or list tools; check its executable/URL, credentials, protocol and response size", name)
	}
	if err != nil {
		return result, failure()
	}
	defer session.Close()
	seenTools, seenCursors := map[string]bool{}, map[string]bool{"": true}
	cursor := ""
	for page := 0; page < 16; page++ {
		listed, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return MCPCheckReport{Name: name}, failure()
		}
		for _, tool := range listed.Tools {
			if len(result.Tools) >= 256 || seenTools[tool.Name] {
				return MCPCheckReport{Name: name}, errors.New("MCP tool listing exceeds 256 tools or contains duplicate names")
			}
			seenTools[tool.Name] = true
			description, _ := evidenceExcerpt(tool.Description, 512)
			result.Tools = append(result.Tools, MCPToolSummary{tool.Name, description})
		}
		if listed.NextCursor == "" {
			initialized := session.InitializeResult()
			if initialized == nil || initialized.ServerInfo == nil {
				return MCPCheckReport{Name: name}, errors.New("MCP server omitted initialization identity")
			}
			result.Connected, result.Protocol = true, initialized.ProtocolVersion
			result.Server, _ = evidenceExcerpt(initialized.ServerInfo.Name, 128)
			sort.Slice(result.Tools, func(i, j int) bool { return result.Tools[i].Name < result.Tools[j].Name })
			return result, nil
		}
		if seenCursors[listed.NextCursor] {
			return MCPCheckReport{Name: name}, errors.New("MCP server repeated a tool-list cursor")
		}
		cursor, seenCursors[listed.NextCursor] = listed.NextCursor, true
	}
	return MCPCheckReport{Name: name}, errors.New("MCP tool listing exceeds 16 pages")
}
