package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWorkerEvidenceBoundsWholeBatchWithoutLosingFailures(t *testing.T) {
	report := "EARLY_BLOCKER " + strings.Repeat("界", 12000) + " FINAL_ACTION"
	results := make([]WorkerResult, 30)
	for i := range results {
		results[i] = WorkerResult{Task: itoa(i), Model: "opencode/free", OK: i != 0, Report: report, Error: report, Reply: &WorkerReply{Protocol: 1, Status: "complete", Report: report}}
	}
	before, _ := json.Marshal(results)
	brief := briefWorkers(results)
	for i, item := range brief {
		if len(item.Report) > 3000 || len(item.Error) > 1000 || !item.ReportTruncated || !item.ErrorTruncated || !strings.Contains(item.Report, "EARLY_BLOCKER") || !strings.Contains(item.Report, "FINAL_ACTION") || !utf8.ValidString(item.Report) || item.OK != results[i].OK || item.Source != "state.json:worker_results["+itoa(i)+"]" {
			t.Fatalf("lost bounded evidence for task %d", i)
		}
	}
	data, _ := json.Marshal(brief)
	if len(data) > 150000 {
		t.Fatalf("whole batch projection too large: %d", len(data))
	}
	after, _ := json.Marshal(results)
	if string(before) != string(after) {
		t.Fatal("prompt projection rewrote durable reports")
	}
}
func TestSeniorAndConsultationPromptsUseBoundedWorkerEvidence(t *testing.T) {
	r, _ := fixtureRunner(t)
	report := "HEAD " + strings.Repeat("MIDDLE_SENTINEL", 20000) + " TAIL"
	r.State.WorkerResults = []WorkerResult{{Task: "one", OK: false, Report: report, Error: "needs repair"}}
	r.State.WorkerSession = &WorkerSession{TaskKey: "one", Report: report}
	for _, prompt := range []string{r.plannerPrompt(true), r.advicePrompt(DialogueFrame{To: "lead", From: "@worker", Question: "Investigate"}), handoffReport(report)} {
		if len(prompt) > 50000 || !strings.Contains(prompt, "HEAD") || !strings.Contains(prompt, "TAIL") || !strings.Contains(prompt, "state.json") {
			t.Fatalf("unbounded or untraceable prompt: %d", len(prompt))
		}
	}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if err != nil || !strings.Contains(string(saved), report) {
		t.Fatal("full report was not saved")
	}
	if r.State.WorkerSession.Report != report {
		t.Fatal("handoff mutated")
	}
}
func TestEvidenceExcerptExactBoundaryAndUnicode(t *testing.T) {
	for _, limit := range []int{0, 1, 20, 1000, 3000} {
		text := strings.Repeat("é界", 2000)
		got, truncated := evidenceExcerpt(text, limit)
		if !truncated || len(got) > limit || !utf8.ValidString(got) {
			t.Fatalf("invalid boundary %d", limit)
		}
	}
	text := strings.Repeat("x", 3000)
	if got, truncated := evidenceExcerpt(text, 3000); got != text || truncated {
		t.Fatal("exact-size report altered")
	}
	if briefHandoff(nil) != nil {
		t.Fatal("nil handoff changed")
	}
}
func TestWorkerHandoffProjectionRemainsIndependent(t *testing.T) {
	session := &WorkerSession{TaskKey: "one", Round: 3, Status: "consulting", Report: strings.Repeat("r", 12000), DialogueOwner: "owner"}
	copy := briefHandoff(session)
	if len(copy.Report) > 3000 || copy.Round != 3 || copy.Status != "consulting" || copy.DialogueOwner != "owner" || len(session.Report) != 12000 {
		t.Fatal("handoff state lost")
	}
}
