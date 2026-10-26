package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Prompt-only projections never rewrite the durable worker result or handoff.
type WorkerEvidence struct {
	Task                string `json:"task"`
	Model               string `json:"model,omitempty"`
	OK                  bool   `json:"ok"`
	Report              string `json:"report,omitempty"`
	Error               string `json:"error,omitempty"`
	ReportTruncated     bool   `json:"report_truncated,omitempty"`
	ErrorTruncated      bool   `json:"error_truncated,omitempty"`
	CommunicationStatus string `json:"communication_status,omitempty"`
	Source              string `json:"source"`
}

func evidenceExcerpt(text string, limit int) (string, bool) {
	text = strings.ToValidUTF8(text, "\uFFFD")
	if len(text) <= limit {
		return text, false
	}
	const marker = "\n[... earlier/later context omitted; read saved evidence ...]\n"
	if limit < len(marker) {
		end := max(0, limit)
		for end > 0 && !utf8.ValidString(text[:end]) {
			end--
		}
		return text[:end], true
	}
	available := limit - len(marker)
	head, tail := available/2, available-available/2
	for head > 0 && !utf8.ValidString(text[:head]) {
		head--
	}
	start := len(text) - tail
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[:head] + marker + text[start:], true
}
func briefWorkers(results []WorkerResult) []WorkerEvidence {
	out := make([]WorkerEvidence, 0, len(results))
	for i, result := range results {
		item := WorkerEvidence{Task: result.Task, Model: result.Model, OK: result.OK, Source: fmt.Sprintf("state.json:worker_results[%d]", i)}
		item.Report, item.ReportTruncated = evidenceExcerpt(result.Report, 3000)
		item.Error, item.ErrorTruncated = evidenceExcerpt(result.Error, 1000)
		if result.Reply != nil {
			item.CommunicationStatus = result.Reply.Status
		}
		out = append(out, item)
	}
	return out
}
func briefHandoff(session *WorkerSession) *WorkerSession {
	if session == nil {
		return nil
	}
	copy := *session
	copy.Report, _ = evidenceExcerpt(copy.Report, 3000)
	return &copy
}

func handoffReport(report string) string {
	excerpt, _ := evidenceExcerpt(report, 3000)
	return excerpt + "\nFull handoff: state.json:worker_session.report in the saved run directory."
}

// Historical messages are navigation evidence, not a replacement for the complete
// current council decisions or active routed questions and answers.
type DiscussionEvidence struct {
	Sequence         int    `json:"sequence"`
	ReplyTo          int    `json:"reply_to,omitempty"`
	Thread           string `json:"thread,omitempty"`
	At               string `json:"at"`
	Cycle            int    `json:"cycle"`
	From             string `json:"from"`
	To               string `json:"to"`
	Kind             string `json:"kind"`
	Text             string `json:"text"`
	TextTruncated    bool   `json:"text_truncated,omitempty"`
	DecisionDone     *bool  `json:"decision_done,omitempty"`
	DecisionSummary  string `json:"decision_summary,omitempty"`
	SummaryTruncated bool   `json:"summary_truncated,omitempty"`
	Source           string `json:"source"`
}

func briefDiscussion(messages []AgentMessage, count int) []DiscussionEvidence {
	out := make([]DiscussionEvidence, 0)
	if count <= 0 {
		return out
	}
	for i := max(0, len(messages)-count); i < len(messages); i++ {
		m := messages[i]
		item := DiscussionEvidence{Sequence: m.Sequence, ReplyTo: m.ReplyTo, Thread: m.Thread, At: m.At, Cycle: m.Cycle, From: m.From, To: m.To, Kind: m.Kind, Source: fmt.Sprintf("state.json:discussion[%d]", i)}
		item.Text, item.TextTruncated = evidenceExcerpt(m.Text, 3000)
		if m.Decision != nil {
			done := m.Decision.Done
			item.DecisionDone = &done
			item.DecisionSummary, item.SummaryTruncated = evidenceExcerpt(m.Decision.Summary, 1000)
		}
		out = append(out, item)
	}
	return out
}

type HistoryEvidence struct {
	Summary   string `json:"summary"`
	Truncated bool   `json:"truncated,omitempty"`
	Source    string `json:"source"`
}

func briefHistory(history []string, count int) []HistoryEvidence {
	out := make([]HistoryEvidence, 0)
	if count <= 0 {
		return out
	}
	for i := max(0, len(history)-count); i < len(history); i++ {
		summary, truncated := evidenceExcerpt(history[i], 1500)
		out = append(out, HistoryEvidence{Summary: summary, Truncated: truncated, Source: fmt.Sprintf("state.json:history[%d]", i)})
	}
	return out
}
