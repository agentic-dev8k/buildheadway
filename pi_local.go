package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const (
	localPiProvider       = "buildheadway-local"
	localPiSessionHeader  = "X-BuildHeadway-Session"
	localPiCompletionPath = "/v1/chat/completions"
)

func localPiEnv(env []string, profile string) []string {
	clean := make([]string, 0, len(env))
	for _, item := range env {
		name, _, _ := strings.Cut(item, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "PI_") {
			clean = append(clean, item)
		}
	}
	clean = setEnv(clean, "PI_CODING_AGENT_DIR", profile)
	clean = setEnv(clean, "PI_SKIP_VERSION_CHECK", "1")
	noProxy := "127.0.0.1,localhost"
	if previous := envValue(env, "NO_PROXY"); previous != "" {
		noProxy += "," + previous
	}
	return setEnv(clean, "NO_PROXY", noProxy)
}

func compatibleLocalPi(ctx context.Context, paths Paths) error {
	help, err := AgentHelp(ctx, Connection{Backend: "pi", Provider: localPiProvider, RequireFree: true}, paths)
	if err != nil {
		return err
	}
	for _, flag := range []string{"--print", "--mode", "--model", "--models", "--provider", "--no-session",
		"--no-extensions", "--no-skills", "--no-mcp", "--no-context-files", "--no-prompt-templates"} {
		if !strings.Contains(help.Global, flag) {
			return fmt.Errorf("the installed Pi needs %s for local routing", flag)
		}
	}
	return nil
}

func configuredLocalPiOffers(ctx context.Context, platform string, compatible func() error) ([]string, error) {
	models, err := readLocalPiModels(ctx, localPiClient, platform)
	if err != nil {
		return nil, err
	}
	if err := compatible(); err != nil {
		return nil, err
	}
	var offers []string
	for _, model := range models {
		offers = append(offers, platform+"/"+model.id)
	}
	return offers, nil
}

type localPiRelay struct {
	endpoint string
	session  string
	server   *http.Server
	failure  atomic.Pointer[localPiRequestError]
}

type localPiRequestError struct{ status int }

func (e *localPiRequestError) Error() string {
	switch {
	case e.status == 0:
		return "the local inference connection failed"
	case e.status >= 300 && e.status < 400:
		return fmt.Sprintf("the local inference server attempted an HTTP %d redirect", e.status)
	case e.status < 200:
		return fmt.Sprintf("the local inference server returned unexpected HTTP %d", e.status)
	case e.failureKind() == "policy":
		return fmt.Sprintf("the local inference server refused keyless access (HTTP %d)", e.status)
	default:
		return fmt.Sprintf("the local inference server returned HTTP %d", e.status)
	}
}

func (e *localPiRequestError) failureKind() string {
	switch {
	case e.status == 0 || e.status >= 500:
		return "transient"
	case e.status < 400 || e.status == 401 || e.status == 402 || e.status == 403:
		return "policy"
	case e.status == 404 || e.status == 408 || e.status == 410 || e.status == 429:
		return "transient"
	default:
		return "execution"
	}
}

// The native client only sees this per-run endpoint. The relay forwards the
// selected model to a fixed loopback server and never follows its redirects.
func startLocalPiRelay(origin, model string) (*localPiRelay, error) {
	target, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	if target.Scheme != "http" || target.Hostname() != "127.0.0.1" || target.User != nil {
		return nil, errors.New("the local Pi provider must bind to 127.0.0.1")
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	session := hex.EncodeToString(nonce[:])
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	relay := &localPiRelay{endpoint: "http://" + listener.Addr().String() + "/v1", session: session}
	reject := func(response http.ResponseWriter) {
		// Send Pi a non-retryable response. BuildHeadway owns recovery and
		// retains the original status even when project settings enable retries.
		http.Error(response, "Local inference request failed", http.StatusBadRequest)
	}
	proxy := &httputil.ReverseProxy{
		Transport:     localPiTransport,
		FlushInterval: -1,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Header = http.Header{
				"Content-Type": {"application/json"},
				"Accept":       {"text/event-stream"},
			}
		},
		ModifyResponse: func(response *http.Response) error {
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return &localPiRequestError{status: response.StatusCode}
			}
			response.Header = http.Header{"Content-Type": {response.Header.Get("Content-Type")}}
			return nil
		},
		ErrorHandler: func(response http.ResponseWriter, request *http.Request, err error) {
			var failure *localPiRequestError
			if !errors.As(err, &failure) {
				if request.Context().Err() != nil {
					return
				}
				failure = &localPiRequestError{}
			}
			// Preserve the upstream status separately from the native agent's
			// stderr. Its response body may contain sensitive diagnostics.
			relay.failure.CompareAndSwap(nil, failure)
			reject(response)
		},
	}
	relay.server = &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		MaxHeaderBytes:    16 << 10,
		Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodPost || request.URL.Path != localPiCompletionPath || request.URL.RawQuery != "" {
				http.NotFound(response, request)
				return
			}
			if request.Header.Get(localPiSessionHeader) != session {
				http.Error(response, "Invalid local Pi session", http.StatusUnauthorized)
				return
			}
			data, err := ReadLimited(request.Body, 8<<20)
			if err != nil {
				http.Error(response, "Local model request exceeded its size limit", http.StatusRequestEntityTooLarge)
				return
			}
			var body struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(data, &body) != nil || body.Model != model {
				http.Error(response, "The local Pi request must use the selected model", http.StatusBadRequest)
				return
			}
			if failure := relay.failure.Load(); failure != nil {
				reject(response)
				return
			}
			request.Body = io.NopCloser(bytes.NewReader(data))
			request.ContentLength, request.TransferEncoding = int64(len(data)), nil
			proxy.ServeHTTP(response, request)
		}),
	}
	go func() { _ = relay.server.Serve(listener) }()
	return relay, nil
}

func writeLocalPiProfile(profile string, model localPiModel, relay *localPiRelay) error {
	settings := map[string]any{
		"retry": map[string]any{"enabled": false, "provider": map[string]int{"maxRetries": 0}},
	}
	if err := AtomicJSON(filepath.Join(profile, "settings.json"), settings); err != nil {
		return err
	}
	provider := map[string]any{
		"baseUrl": relay.endpoint, "api": "openai-completions", "apiKey": "local",
		"headers": map[string]string{localPiSessionHeader: relay.session},
		"compat": map[string]any{
			"supportsDeveloperRole": false, "supportsStore": false,
			"supportsReasoningEffort": false, "maxTokensField": "max_tokens",
		},
		"models": []map[string]any{{
			"id": model.id, "name": model.id, "input": []string{"text"}, "reasoning": false,
			"contextWindow": model.context, "maxTokens": min(4096, model.context),
			"cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
		}},
	}
	return AtomicJSON(filepath.Join(profile, "models.json"), map[string]any{
		"providers": map[string]any{localPiProvider: provider},
	})
}

type localPiAssistantMessage struct {
	Role       string          `json:"role"`
	Provider   string          `json:"provider"`
	Model      string          `json:"model"`
	StopReason json.RawMessage `json:"stopReason"`
	Content    json.RawMessage `json:"content"`
	Usage      struct {
		Cost map[string]json.RawMessage `json:"cost"`
	} `json:"usage"`
}

func localPiReceipt(line, model string) (*localPiAssistantMessage, error) {
	var event struct {
		Type    string                  `json:"type"`
		Message localPiAssistantMessage `json:"message"`
	}
	if json.Unmarshal([]byte(line), &event) != nil || event.Type != "message_end" || event.Message.Role != "assistant" {
		return nil, nil
	}
	message := event.Message
	for name, cost := range message.Usage.Cost {
		if name != "totalFromProvider" && !zeroCatalogPrice(cost) {
			return nil, costError{"Pi"}
		}
	}
	if message.Provider != localPiProvider || message.Model != model || len(message.Usage.Cost["total"]) == 0 {
		return nil, freeRouteMismatchError{"Pi"}
	}
	return &message, nil
}

func verifyLocalPiOutput(raw, model string) error {
	var final *localPiAssistantMessage
	for _, line := range strings.Split(raw, "\n") {
		message, err := localPiReceipt(line, model)
		if err != nil {
			return err
		}
		if message != nil {
			final = message
		}
	}
	if final == nil {
		return freeRouteMismatchError{"Pi"}
	}
	var reason string
	if json.Unmarshal(final.StopReason, &reason) != nil || reason != "stop" {
		return errors.New("Pi did not finish the local response")
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(final.Content, &blocks); err != nil {
		return errors.New("Pi returned an invalid final local response")
	}
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return nil
		}
	}
	return errors.New("Pi returned an empty final local response")
}
