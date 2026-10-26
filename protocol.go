package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const decisionSchema = `{
 "type":"object","additionalProperties":false,
 "required":["done","summary","tasks","checks","success_criteria","check_replacements","acceptance"],
 "properties":{
  "done":{"type":"boolean"},"summary":{"type":"string"},
  "tasks":{"type":"array","maxItems":30,"items":{
   "type":"object","additionalProperties":false,"required":["id","title","instructions"],
   "properties":{"id":{"type":"string"},"title":{"type":"string"},"instructions":{"type":"string"}}
  }},
  "checks":{"type":"array","items":{"type":"string"}},
  "success_criteria":{"type":"array","items":{"type":"string"}},
  "acceptance":{"type":"array","maxItems":100,"items":{"type":"object","additionalProperties":false,"required":["criterion","checks"],"properties":{"criterion":{"type":"string"},"checks":{"type":"array","minItems":1,"maxItems":30,"items":{"type":"string"}}}}},
  "check_replacements":{"type":"array","maxItems":30,"items":{
   "type":"object","additionalProperties":false,"required":["old","new","reason"],
   "properties":{"old":{"type":"string"},"new":{"type":"string"},"reason":{"type":"string"}}
  }}
 }
}`

const teamRequestSchema = `{"type":"array","maxItems":4,"items":{"type":"object","additionalProperties":false,"required":["to","kind","text"],"properties":{"to":{"type":"string"},"kind":{"type":"string","enum":["question","challenge","suggestion"]},"text":{"type":"string"}}}}`

var teamDecisionSchema = func() string {
	var schema map[string]any
	var messages any
	json.Unmarshal([]byte(decisionSchema), &schema)
	json.Unmarshal([]byte(teamRequestSchema), &messages)
	schema["properties"].(map[string]any)["messages"] = messages
	schema["required"] = append(schema["required"].([]any), "messages")
	data, _ := json.Marshal(schema)
	return string(data)
}()

func (r *Runner) decisionOutputSchema() string {
	if r.State.Mode == "figureout" {
		return teamDecisionSchema
	}
	return decisionSchema
}

var transientRE = regexp.MustCompile(`(?i)\b(?:429|503|502|504)\b|rate.?limit|too many requests|overloaded|capacity|temporarily|timed out|timeout|connection reset`)
var authRE = regexp.MustCompile(`(?i)\b401\b|unauthori[sz]ed|invalid.?api.?key|not logged in|login required|sign.in required|insufficient.?quota|insufficient.?credit|payment required|authentication failed`)
var policyRE = regexp.MustCompile(`(?i)free tier can only be used|account (?:has been |is )?suspended|access denied by (?:the )?provider|not available in your (?:country|region)`)

func ResponseTexts(raw string) ([]string, error) {
	raw = strings.TrimSpace(Clean(raw))
	var top map[string]any
	if json.Unmarshal([]byte(raw), &top) == nil {
		if failure, exists := top["error"]; exists && failure != nil {
			return nil, fmt.Errorf("agent error: %v", failure)
		}
		if response, ok := top["response"].(string); ok {
			return []string{response}, nil
		}
		if bad, _ := top["is_error"].(bool); bad {
			return nil, fmt.Errorf("agent error: %v", top["result"])
		}
		if value, ok := top["structured_output"]; ok {
			data, _ := json.Marshal(value)
			return []string{string(data)}, nil
		}
		if _, ok := top["tasks"]; ok {
			return []string{raw}, nil
		}
		if result, ok := top["result"].(string); ok {
			return []string{result}, nil
		}
	}
	texts := []string{}
	for _, line := range strings.Split(raw, "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		typeName, _ := event["type"].(string)
		if typeName == "error" {
			return nil, fmt.Errorf("agent error: %v", event["error"])
		}
		if bad, _ := event["is_error"].(bool); bad {
			return nil, fmt.Errorf("agent error: %v", event["result"])
		}
		if response, ok := event["response"].(string); ok {
			if failure, exists := event["error"]; exists && failure != nil {
				return nil, fmt.Errorf("agent error: %v", failure)
			}
			texts = append(texts, response)
		}
		if value, ok := event["structured_output"]; ok {
			data, _ := json.Marshal(value)
			texts = append(texts, string(data))
			continue
		}
		if typeName == "result" {
			if result, ok := event["result"].(string); ok {
				texts = append(texts, result)
			}
		}
		if typeName == "text" {
			if part, ok := event["part"].(map[string]any); ok {
				if text, ok := part["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
		if typeName == "message_end" {
			if message, ok := event["message"].(map[string]any); ok {
				if role, _ := message["role"].(string); role != "assistant" {
					continue
				}
				if reason, _ := message["stopReason"].(string); reason == "error" {
					return nil, fmt.Errorf("Pi error: %v", message["errorMessage"])
				}
				texts = append(texts, messageContent(message))
			}
		}
		if typeName == "agent_event" {
			if inner, ok := event["event"].(map[string]any); ok {
				if text, ok := inner["text"].(string); ok {
					texts = append(texts, text)
				}
				if innerType, _ := inner["type"].(string); innerType == "error" {
					return nil, fmt.Errorf("Cline error: %v", inner)
				}
			}
		}
		if typeName == "item.completed" {
			if item, ok := event["item"].(map[string]any); ok {
				if kind, _ := item["type"].(string); kind == "agent_message" {
					if text, ok := item["text"].(string); ok {
						texts = append(texts, text)
					}
				}
			}
		}
	}
	if len(texts) == 0 {
		return []string{raw}, nil
	}
	return texts, nil
}
func messageContent(message map[string]any) string {
	var out []string
	if contents, ok := message["content"].([]any); ok {
		for _, part := range contents {
			if object, ok := part.(map[string]any); ok {
				if kind, _ := object["type"].(string); kind == "text" {
					if text, ok := object["text"].(string); ok {
						out = append(out, text)
					}
				}
			}
		}
	}
	return strings.Join(out, "\n")
}
func jsonCandidates(raw string) ([]string, error) {
	texts, err := ResponseTexts(raw)
	if err != nil {
		return nil, err
	}
	// Some streaming protocols split the final JSON across text events.
	texts = append(texts, strings.Join(texts, ""))
	candidates := []string{}
	for _, text := range texts {
		// Agents sometimes wrap an otherwise correct decision in an explanation
		// or Markdown. Decode complete objects rather than matching braces with
		// a regex, which breaks on nested tasks and braces inside instructions.
		for offset := 0; offset < len(text); {
			start := strings.IndexByte(text[offset:], '{')
			if start < 0 {
				break
			}
			start += offset
			decoder := json.NewDecoder(strings.NewReader(text[start:]))
			var object json.RawMessage
			if err := decoder.Decode(&object); err != nil {
				offset = start + 1
				continue
			}
			candidates = append(candidates, string(object))
			offset = start + int(decoder.InputOffset())
		}
	}
	return candidates, nil
}
func ParseDecision(raw string) (Decision, error) {
	candidates, err := jsonCandidates(raw)
	if err != nil {
		return Decision{}, err
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		text := strings.TrimSpace(candidates[i])
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(text), &fields) != nil || len(fields) < 5 || len(fields) > 8 {
			continue
		}
		missing := false
		for _, key := range []string{"done", "summary", "tasks", "checks", "success_criteria"} {
			if _, ok := fields[key]; !ok {
				missing = true
			}
		}
		if missing {
			continue
		}
		invalid := false
		for _, key := range []string{"check_replacements", "messages", "acceptance"} {
			if value, ok := fields[key]; ok && (len(value) == 0 || value[0] != '[') {
				invalid = true
			}
		}
		if invalid {
			continue
		}
		if string(fields["done"]) != "true" && string(fields["done"]) != "false" {
			continue
		}
		validArrays := true
		for _, key := range []string{"tasks", "checks", "success_criteria"} {
			var array []any
			if len(fields[key]) == 0 || fields[key][0] != '[' || json.Unmarshal(fields[key], &array) != nil {
				validArrays = false
			}
		}
		if !validArrays {
			continue
		}
		var summary string
		if string(fields["summary"]) == "null" || json.Unmarshal(fields["summary"], &summary) != nil {
			continue
		}
		var decision Decision
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&decision) != nil {
			continue
		}
		if err := ValidateDecision(decision); err != nil {
			return decision, err
		}
		return decision, nil
	}
	return Decision{}, errors.New("the planner did not return a valid task decision")
}
func ValidateDecision(decision Decision) error {
	if err := validateAcceptance(decision.Acceptance); err != nil {
		return err
	}
	if len(decision.SuccessCriteria) > 100 {
		return errors.New("too many acceptance criteria")
	}
	for _, criterion := range decision.SuccessCriteria {
		if strings.TrimSpace(criterion) == "" || len(criterion) > 2000 {
			return errors.New("invalid acceptance criterion")
		}
	}
	if err := ValidateTeamRequests(decision.Messages); err != nil {
		return err
	}
	if decision.Done && len(decision.Messages) > 0 {
		return errors.New("resolve team messages before approving completion")
	}
	if len(decision.CheckReplacements) > 30 || (decision.Done && len(decision.CheckReplacements) > 0) {
		return errors.New("check replacements must execute before completion")
	}
	seenReplacements := map[string]bool{}
	for _, replacement := range decision.CheckReplacements {
		if strings.TrimSpace(replacement.Old) == "" || strings.TrimSpace(replacement.New) == "" || replacement.Old == replacement.New || strings.TrimSpace(replacement.Reason) == "" || seenReplacements[replacement.Old] || len(replacement.Old) > 16000 || len(replacement.New) > 16000 || len(replacement.Reason) > 4000 {
			return errors.New("invalid check replacement")
		}
		seenReplacements[replacement.Old] = true
	}
	if len(decision.Tasks) > 30 {
		return errors.New("planner assigned too many tasks in one batch")
	}
	if decision.Done && len(decision.Tasks) > 0 {
		return errors.New("planner declared completion with unfinished tasks")
	}
	if len(decision.Checks) > 30 {
		return errors.New("planner assigned too many verification commands")
	}
	ids := map[string]bool{}
	for _, task := range decision.Tasks {
		if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.Title) == "" || strings.TrimSpace(task.Instructions) == "" || ids[task.ID] {
			return errors.New("invalid or duplicate planner task")
		}
		if len(task.Instructions) > 30000 {
			return errors.New("planner task is too large")
		}
		ids[task.ID] = true
	}
	for _, command := range decision.Checks {
		if strings.TrimSpace(command) == "" || len(command) > 16000 {
			return errors.New("invalid verification command")
		}
	}
	return nil
}

func allZero(value any) bool {
	switch value := value.(type) {
	case float64:
		return value == 0
	case map[string]any:
		for _, part := range value {
			if !allZero(part) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

var modelHeader = regexp.MustCompile(`(?m)^(opencode/[^\s]+)[ \t]*\r?$`)

func ParseFreeModels(raw string) []string {
	raw = Clean(raw)
	models := []string{}
	for _, index := range modelHeader.FindAllStringSubmatchIndex(raw, -1) {
		name := raw[index[2]:index[3]]
		rest := strings.TrimSpace(raw[index[1]:])
		var meta map[string]any
		if json.NewDecoder(strings.NewReader(rest)).Decode(&meta) != nil {
			continue
		}
		cost, ok := meta["cost"].(map[string]any)
		if !ok {
			continue
		}
		input, inOK := cost["input"].(float64)
		output, outOK := cost["output"].(float64)
		if !inOK || !outOK || input != 0 || output != 0 || !allZero(cost) {
			continue
		}
		if caps, ok := meta["capabilities"].(map[string]any); ok {
			if tool, ok := caps["toolcall"].(bool); ok && !tool {
				continue
			}
		}
		models = append(models, name)
	}
	return Unique(models)
}
