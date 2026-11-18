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

type freeOfferUnavailableError struct{ model string }

func (e freeOfferUnavailableError) Error() string { return e.model + " is unavailable as a free offer" }

type freeQuotaError struct{}

func (freeQuotaError) Error() string {
	return "OpenCode reported a free-tier rate limit. Wait for the provider limit to reset, then use /resume."
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
		fields := map[string]string{}
		for _, match := range agentLogFields.FindAllStringSubmatch(line, -1) {
			value := match[2]
			if strings.HasPrefix(value, `"`) {
				var err error
				value, err = strconv.Unquote(value)
				if err != nil {
					return nil
				}
			}
			fields[match[1]] = value
		}
		provider, id, ok := strings.Cut(model, "/")
		if ok && fields["level"] == "ERROR" && fields["message"] == "stream error" && fields["providerID"] == provider && fields["modelID"] == id {
			message = fields["error.error"]
		}
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
