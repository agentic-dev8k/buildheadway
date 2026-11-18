package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var agentLogFields = regexp.MustCompile(`(?:^|\s)([A-Za-z_.]+)=("(?:[^"\\]|\\.)*"|[^\s]+)`)
var clineQuotaDiagnostic = regexp.MustCompile(`(?i)\b429\b|rate limit (?:exceeded|reached)|too many requests|free usage limit reached`)

type freeOfferUnavailableError struct{ model string }

func (e freeOfferUnavailableError) Error() string { return e.model + " is unavailable as a free offer" }

type freeQuotaError struct{ platform string }

func (e freeQuotaError) Error() string {
	platform := e.platform
	if platform == "" || platform == "opencode" {
		platform = "OpenCode"
	}
	return platform + " reported a free-tier rate limit. Wait for the provider limit to reset, then use /resume."
}

type freeRouteMismatchError struct{ agent string }

func (e freeRouteMismatchError) Error() string {
	agent := e.agent
	if agent == "" {
		agent = "Cline"
	}
	return agent + " did not confirm the selected free model. Check the native connection before resuming."
}

type clineReportedModel struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

func selectedClineModelReported(raw, platform, model string) bool {
	for _, line := range strings.Split(Clean(raw), "\n") {
		var event struct {
			Type  string             `json:"type"`
			Model clineReportedModel `json:"model"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Type == "run_result" &&
			event.Model.ID == model && event.Model.Provider == platform {
			return true
		}
	}
	return false
}

func clineFreeStreamError(platform, model, line string) error {
	var event struct {
		Type         string             `json:"type"`
		Model        clineReportedModel `json:"model"`
		FinishReason string             `json:"finishReason"`
		Message      string             `json:"message"`
		StatusCode   int                `json:"statusCode"`
		Usage        struct {
			TotalCost json.RawMessage `json:"totalCost"`
		} `json:"usage"`
		Event struct {
			Type       string          `json:"type"`
			ErrorClass string          `json:"errorClass"`
			Cost       json.RawMessage `json:"cost"`
			TotalCost  json.RawMessage `json:"totalCost"`
			Error      struct {
				Message    string `json:"message"`
				StatusCode int    `json:"statusCode"`
			} `json:"error"`
		} `json:"event"`
	}
	if json.Unmarshal([]byte(Clean(line)), &event) != nil {
		return nil
	}
	positive := func(value json.RawMessage) bool {
		return len(value) > 0 && decimalPricing.Match(value) && !zeroCatalogPrice(value)
	}
	switch event.Type {
	case "run_result":
		if positive(event.Usage.TotalCost) {
			return costError{platform}
		}
		if event.FinishReason == "completed" && (event.Model.ID != model || event.Model.Provider != platform) {
			return freeRouteMismatchError{}
		}
	case "agent_event":
		switch event.Event.Type {
		case "usage":
			if positive(event.Event.Cost) || positive(event.Event.TotalCost) {
				return costError{platform}
			}
		case "error":
			// Cline 3 classifies most provider rate limits as "unknown"
			// and keeps the server diagnostic in its structured error event.
			if event.Event.ErrorClass != "auth" && (event.Event.Error.StatusCode == 429 ||
				clineQuotaDiagnostic.MatchString(event.Event.Error.Message)) {
				return freeQuotaError{platform: platform}
			}
		}
	case "error":
		if event.StatusCode == 429 || clineQuotaDiagnostic.MatchString(event.Message) {
			return freeQuotaError{platform: platform}
		}
	}
	return nil
}

func openCodeFreeRouteError(model string, outer, data map[string]any) error {
	id, ok := strings.CutPrefix(model, "opencode/")
	if !ok || id == "" {
		return nil
	}
	switch outer["name"] {
	case "ProviderModelNotFoundError":
		if data["providerID"] == "opencode" && data["modelID"] == id {
			return freeOfferUnavailableError{model}
		}
	case "APIError":
		status, _ := data["statusCode"].(float64)
		body, _ := data["responseBody"].(string)
		var receipt struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(body), &receipt) == nil && receipt.Type == "error" {
			// Console returns 401 for both an ended promotion and real
			// authentication failures. Only its specific model errors may fail over.
			if receipt.Error.Type == "ModelError" && (status == 401 || status == 0) {
				msg := receipt.Error.Message
				promotion, hasPrefix := strings.CutPrefix(msg, "Free promotion has ended for ")
				name, hasSuffix := strings.CutSuffix(promotion, ". You can continue using the model by subscribing to OpenCode Go - https://opencode.ai/go")
				if (hasPrefix && hasSuffix && strings.TrimSpace(name) != "") || msg == "Model "+id+" is not supported" {
					return freeOfferUnavailableError{model}
				}
			}
			switch receipt.Error.Type {
			case "FreeUsageLimitError", "RateLimitError", "GoUsageLimitError", "BlackUsageLimitError":
				return freeQuotaError{}
			}
		}
		if status == 429 {
			return freeQuotaError{}
		}
		if status == 401 || status == 402 {
			return authError{"opencode", fmt.Sprintf("HTTP %.0f · %s", status, freebieTerminalLine(agentErrorMessage(data), 1500))}
		}
		if status == 403 {
			return errors.New("Access denied by the provider · " + freebieTerminalLine(agentErrorMessage(data), 1500))
		}
	}
	return nil
}

// Read authoritative step receipts, not model/tool text. Preserve decimal
// spelling: float64 can round tiny positive reported charges to zero.
func openCodeReportedCharge(line string) bool {
	var event struct {
		Type string `json:"type"`
		Part struct {
			Cost json.RawMessage `json:"cost"`
		} `json:"part"`
	}
	if json.Unmarshal([]byte(Clean(line)), &event) != nil || event.Type != "step_finish" {
		return false
	}
	cost := strings.TrimSpace(string(event.Part.Cost))
	if cost == "" || cost[0] < '0' || cost[0] > '9' {
		return false
	}
	// JSON syntax is already validated. An exponent cannot change whether a
	// nonnegative decimal mantissa is exactly zero.
	if i := strings.IndexAny(cost, "eE"); i >= 0 {
		cost = cost[:i]
	}
	return strings.ContainsAny(cost, "123456789")
}

func modelStreamDiagnostic(line, model string) string {
	fields := map[string]string{}
	for _, match := range agentLogFields.FindAllStringSubmatch(line, -1) {
		value := match[2]
		if strings.HasPrefix(value, `"`) {
			var err error
			value, err = strconv.Unquote(value)
			if err != nil {
				return ""
			}
		}
		fields[match[1]] = value
	}
	provider, id, ok := strings.Cut(model, "/")
	if ok && fields["level"] == "ERROR" && fields["message"] == "stream error" && fields["providerID"] == provider && fields["modelID"] == id {
		return fields["error.error"]
	}
	return ""
}

// Read process diagnostics, keeping model and tool text out of routing decisions.
func agentStreamError(backend, model, line string, freeOnly bool) error {
	line = Clean(line)
	var event map[string]any
	message := ""
	if json.Unmarshal([]byte(line), &event) == nil {
		kind, _ := event["type"].(string)
		switch {
		case kind == "error":
			// OpenCode nests the API message under error.data.message.
			if backend == "opencode" {
				if outer, ok := event["error"].(map[string]any); ok {
					if data, ok := outer["data"].(map[string]any); ok {
						if freeOnly {
							if err := openCodeFreeRouteError(model, outer, data); err != nil {
								return err
							}
						}
						message = agentErrorMessage(data)
					}
				}
			}
			if message == "" {
				message = agentErrorMessage(event)
			}
		case backend == "cline" && kind == "run_result" && event["finishReason"] != "completed":
			message = agentErrorMessage(event)
		case backend == "cline" && kind == "agent_event":
			if inner, ok := event["event"].(map[string]any); ok && inner["type"] == "error" {
				message = agentErrorMessage(inner)
			}
		}
	} else if backend == "opencode" {
		message = modelStreamDiagnostic(line, model)
	}
	if freeOnly && backend == "opencode" && strings.Contains(strings.ToLower(message), "free usage limit reached") {
		return freeQuotaError{}
	}
	if authRE.MatchString(message) {
		excerpt, _ := evidenceExcerpt(message, 2000)
		return authError{backend, excerpt}
	}
	if policyRE.MatchString(message) {
		excerpt, _ := evidenceExcerpt(message, 2000)
		return errors.New(backend + " provider policy: " + excerpt)
	}
	return nil
}
