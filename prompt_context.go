package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultMaxPromptBytes = 512 << 10
const minMaxPromptBytes = 16 << 10
const maxMaxPromptBytes = 8 << 20

type promptLimitError struct {
	label        string
	bytes, limit int
}

func (e promptLimitError) Error() string {
	return fmt.Sprintf("%s prompt has %d bytes; limit is %d; inspect buildheadway context, reduce guidance or split the goal, or resume with a larger --max-prompt-bytes", e.label, e.bytes, e.limit)
}
func (e promptLimitError) Unwrap() error { return limitError{e.Error()} }

type PromptUsage struct {
	At         string `json:"at"`
	Label      string `json:"label"`
	Bytes      int    `json:"bytes"`
	LimitBytes int    `json:"limit_bytes"`
	Fits       bool   `json:"fits"`
}
type ContextReport struct {
	RunID                string        `json:"run_id"`
	ConfiguredLimitBytes int           `json:"configured_limit_bytes"`
	Recent               []PromptUsage `json:"recent"`
}

func validPromptLimit(value int) bool {
	return value == 0 || value >= minMaxPromptBytes && value <= maxMaxPromptBytes
}
func effectivePromptLimit(value int) int {
	if value == 0 {
		return defaultMaxPromptBytes
	}
	return value
}

func validatePromptUsage(items []PromptUsage) error {
	if len(items) > 20 {
		return errors.New("too many saved prompt measurements")
	}
	for _, p := range items {
		if _, err := time.Parse(time.RFC3339Nano, p.At); err != nil {
			return errors.New("invalid saved prompt measurement time")
		}
		if p.Label == "" || len(p.Label) > 300 || p.Bytes < 0 || p.LimitBytes == 0 || !validPromptLimit(p.LimitBytes) || p.Fits != (p.Bytes <= p.LimitBytes) {
			return errors.New("invalid saved prompt measurement")
		}
	}
	return nil
}

// Runs before catalog discovery, model selection, health and call accounting.
// Full active context is retained; rejection cannot choose a different model.
func (r *Runner) admitPrompt(prompt, label string) error {
	if label == "" {
		label = "plan"
	}
	if !validPromptLimit(r.MaxPromptBytes) {
		return errors.New("max_prompt_bytes must be zero or between 16384 and 8388608")
	}
	limit := effectivePromptLimit(r.MaxPromptBytes)
	item := PromptUsage{At: r.routeNow().UTC().Format(time.RFC3339Nano), Label: label, Bytes: len(prompt), LimitBytes: limit, Fits: len(prompt) <= limit}
	r.State.PromptUsage = append(r.State.PromptUsage, item)
	if len(r.State.PromptUsage) > 20 {
		r.State.PromptUsage = append([]PromptUsage(nil), r.State.PromptUsage[len(r.State.PromptUsage)-20:]...)
	}
	var admission error
	if !item.Fits {
		admission = promptLimitError{label, len(prompt), limit}
	}
	if r.Dir != "" {
		if err := r.checkpoint(); err != nil {
			return errors.Join(admission, err)
		}
	}
	return admission
}

func contextReport(state State) ContextReport {
	return ContextReport{RunID: state.RunID, ConfiguredLimitBytes: effectivePromptLimit(state.Config.MaxPromptBytes), Recent: append([]PromptUsage{}, state.PromptUsage...)}
}
func FormatContext(report ContextReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s · configured prompt allowance: %d bytes\n", report.RunID, report.ConfiguredLimitBytes)
	if len(report.Recent) == 0 {
		out.WriteString("No prompt measurements recorded.\n")
	}
	for _, p := range report.Recent {
		status := "fits"
		if !p.Fits {
			status = "over limit"
		}
		fmt.Fprintf(&out, "%s · %s · %d/%d bytes · %s\n", p.At, p.Label, p.Bytes, p.LimitBytes, status)
	}
	out.WriteString("Measurements do not prove model dispatch, passing checks or approval.")
	return out.String()
}

type AcceptanceEvidence struct {
	Criterion       string   `json:"criterion"`
	Status          string   `json:"status"`
	Checks          []string `json:"checks"`
	EvidenceSources []string `json:"evidence_sources"`
}

func briefAcceptance(state State) []AcceptanceEvidence {
	rows := AcceptanceResults(state, Decision{})
	out := make([]AcceptanceEvidence, 0, len(rows))
	for _, row := range rows {
		item := AcceptanceEvidence{Criterion: row.Criterion, Status: row.Status, Checks: row.Checks, EvidenceSources: []string{}}
		for i, check := range state.CheckResults {
			if contains(row.Checks, check.Command) {
				item.EvidenceSources = append(item.EvidenceSources, fmt.Sprintf("state.json:check_results[%d]", i))
			}
		}
		out = append(out, item)
	}
	return out
}
