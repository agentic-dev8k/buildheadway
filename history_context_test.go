package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDiscussionContextDoesNotDuplicateHistoricalDecisions(t *testing.T) {
	r, _ := fixtureRunner(t)
	huge := "EARLY_FAILURE " + strings.Repeat("界", 40000) + " FINAL_DIAGNOSIS"
	decision := Decision{Summary: huge, Done: false, Tasks: []Task{{ID: "repair", Title: "repair", Instructions: strings.Repeat("fix ", 5000)}}}
	if err := ValidateDecision(decision); err != nil {
		t.Fatal(err)
	}
	text := "EARLY_FAILURE " + strings.Repeat("界", 3900) + " FINAL_DIAGNOSIS"
	for i := 0; i < 20; i++ {
		r.State.Discussion = append(r.State.Discussion, AgentMessage{Sequence: i + 1, ReplyTo: i, Thread: "thread", From: "lead", To: "reviewer", Kind: "answer", Text: text, Decision: &decision})
	}
	before, _ := json.Marshal(r.State.Discussion)
	prompt := discussionContext(r.State, 12)
	if len(prompt) > 60000 || strings.Contains(prompt, `"decision":`) {
		t.Fatalf("historical decisions duplicated or oversized: %d", len(prompt))
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(prompt), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 12 {
		t.Fatalf("lost recent discussion rows: %d", len(rows))
	}
	for i, row := range rows {
		if row["sequence"] != float64(i+9) || row["reply_to"] != float64(i+8) || row["thread"] != "thread" || row["from"] != "lead" || row["source"] != "state.json:discussion["+itoa(i+8)+"]" || row["decision_done"] != false || row["text_truncated"] != true || row["summary_truncated"] != true {
			t.Fatalf("missing route, veto or reference: %#v", row)
		}
		for _, key := range []string{"text", "decision_summary"} {
			s, _ := row[key].(string)
			if !utf8.ValidString(s) || !strings.Contains(s, "EARLY_FAILURE") || !strings.Contains(s, "FINAL_DIAGNOSIS") {
				t.Fatalf("lost diagnostics: %s", key)
			}
		}
	}
	after, _ := json.Marshal(r.State.Discussion)
	if string(before) != string(after) {
		t.Fatal("changed durable discussion")
	}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if err != nil || !strings.Contains(string(saved), huge) {
		t.Fatal("full decision lost from saved evidence")
	}
	if discussionContext(r.State, 0) != "[]" || discussionContext(r.State, -1) != "[]" {
		t.Fatal("nonpositive window must be empty")
	}
}

func TestPlanningHistoryBoundedWhileCurrentCouncilAndAnswersRemainComplete(t *testing.T) {
	r, _ := fixtureRunner(t)
	huge := "EARLY_FAILURE " + strings.Repeat("MIDDLE_SENTINEL", 20000) + " FINAL_DIAGNOSIS"
	for i := 0; i < 30; i++ {
		r.State.History = append(r.State.History, huge)
	}
	before, _ := json.Marshal(r.State.History)
	prompt := r.plannerPrompt(true)
	if strings.Contains(prompt, strings.Repeat("MIDDLE_SENTINEL", 200)) || len(prompt) > 50000 || !strings.Contains(prompt, "state.json:history[29]") {
		t.Fatalf("history unbounded or missing evidence reference: %d", len(prompt))
	}
	after, _ := json.Marshal(r.State.History)
	if string(before) != string(after) {
		t.Fatal("history changed")
	}
	r.State.Dialogue = &DialogueState{Replies: []DialogueReply{{From: "reviewer", To: "lead", Question: "inspect", Answer: huge}}}
	r.State.Team = []SeniorAgent{{Name: "lead"}}
	round := &CouncilRound{Decisions: []Decision{{Summary: huge, Done: false, Tasks: []Task{{ID: "repair", Title: "repair", Instructions: "inspect files and repair"}}}}}
	raw, _ := json.Marshal(round.Decisions)
	if !strings.Contains(r.councilPrompt(true, 0, round), string(raw)) {
		t.Fatal("current council decisions shortened")
	}
	if !strings.Contains(r.dialogueContext(), huge) {
		t.Fatal("active answers shortened")
	}
}

func TestHistoricalContextExactSmallEvidenceAndEmptyWindows(t *testing.T) {
	d := Decision{Done: true, Summary: "approved"}
	messages := []AgentMessage{{Sequence: 1, Text: "question"}, {Sequence: 2, Text: "answer", Decision: &d}}
	rows := briefDiscussion(messages, 5)
	if len(rows) != 2 || rows[0].DecisionDone != nil || rows[1].DecisionDone == nil || !*rows[1].DecisionDone || rows[1].DecisionSummary != "approved" || rows[1].TextTruncated {
		t.Fatal("small message or approval changed")
	}
	history := briefHistory([]string{"old", "new"}, 1)
	if len(history) != 1 || history[0].Summary != "new" || history[0].Truncated || history[0].Source != "state.json:history[1]" {
		t.Fatal("wrong history window")
	}
	for _, n := range []int{-1, 0, 3} {
		if len(briefHistory(nil, n)) != 0 || len(briefDiscussion(nil, n)) != 0 {
			t.Fatal("nonempty nil projection")
		}
	}
	if len(briefHistory([]string{"one"}, 0)) != 0 || len(briefHistory([]string{"one"}, -1)) != 0 {
		t.Fatal("invalid history window")
	}
}
