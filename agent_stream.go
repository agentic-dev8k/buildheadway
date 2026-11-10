package main

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var agentLogFields = regexp.MustCompile(`(?:^|\s)([A-Za-z_.]+)=("(?:[^"\\]|\\.)*"|[^\s]+)`)

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

// Only authoritative provider events may interrupt a process. Text returned by
// a model/tool is not a provider event, even if it mentions a usage limit.
func agentStreamBlocker(backend, model, line string) error {
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
