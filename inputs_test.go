package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestInputsPageActualSavedPromptsWithoutChangingEvidence(t *testing.T) {
	r, _ := fixtureRunner(t)
	if e := r.State.Save(r.Dir); e != nil {
		t.Fatal(e)
	}
	if e := AtomicJSON(filepath.Join(r.Repo, ".forge/latest.json"), map[string]string{"run_id": r.State.RunID}); e != nil {
		t.Fatal(e)
	}
	name := "120000-plan.prompt.txt"
	data := strings.Repeat("EXACT_USER_INSTRUCTIONS\n", 70000)
	file := filepath.Join(r.Dir, name)
	if e := os.WriteFile(file, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	listing, e := ReadInputs(r.Repo, "latest", "", 16000, -1)
	if e != nil || len(listing.Files) != 1 || listing.Files[0].Name != name {
		t.Fatal("missing actual input file", e, listing)
	}
	var got strings.Builder
	offset := int64(0)
	for {
		page, e := ReadInputs(r.Repo, r.State.RunID, name, 100000, offset)
		if e != nil || len(page.Output) > 100000 || page.Offset != offset {
			t.Fatal("unbounded or incorrect page", e)
		}
		got.WriteString(page.Output)
		offset = page.NextOffset
		if !page.More {
			break
		}
	}
	if got.String() != data {
		t.Fatal("paging lost actual input bytes")
	}
	tail, e := ReadInputs(r.Repo, r.State.RunID, name, 100, -1)
	if e != nil || tail.Output != data[len(data)-100:] || !tail.Truncated {
		t.Fatal("tail inspection changed")
	}
	after, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	actual, _ := os.ReadFile(file)
	if string(before) != string(after) || string(actual) != data {
		t.Fatal("inspection modified evidence")
	}
	for _, bad := range []string{"../state.json", "state.json", "/tmp/x.prompt.txt", ".hidden.prompt.txt", "120000-plan.log"} {
		if _, e = ReadInputs(r.Repo, r.State.RunID, bad, 100, 0); e == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, e = ReadInputs(r.Repo, r.State.RunID, name, 100, int64(len(data)+1)); e == nil {
		t.Fatal("beyond EOF accepted")
	}
	if _, e = ReadInputs(r.Repo, r.State.RunID, "", 100, 0); e == nil {
		t.Fatal("offset without filename accepted")
	}
	if _, e = ReadLogs(r.Repo, r.State.RunID, false, name, 100); e == nil {
		t.Fatal("process logs silently broadened to private prompts")
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	_ = os.WriteFile(outside, []byte("outside"), 0600)
	link := "linked.prompt.txt"
	if os.Symlink(outside, filepath.Join(r.Dir, link)) == nil {
		if _, e = ReadInputs(r.Repo, r.State.RunID, link, 100, 0); e == nil {
			t.Fatal("symlink input accepted")
		}
	}
}
func TestInputsRealNativeCLIIsReadOnly(t *testing.T) {
	binary := nativeTestBinary(t)
	r, _ := fixtureRunner(t)
	_ = r.State.Save(r.Dir)
	name := "120000-worker.prompt.txt"
	text := "Changed / Verification / Remaining\nFull exact prompt 界\n"
	_ = os.WriteFile(filepath.Join(r.Dir, name), []byte(text), 0600)
	before, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	cmd := exec.Command(binary, "inputs", "--project", r.Repo, "--run", r.State.RunID, "--json", "--offset", "0", name)
	data, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(data))
	}
	var report LogReport
	if json.Unmarshal(data, &report) != nil || report.Output != text || report.NextOffset != int64(len(text)) {
		t.Fatal("native inspector did not return exact input", string(data))
	}
	after, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if string(before) != string(after) {
		t.Fatal("native inspector changed checkpoint")
	}
	cmd = exec.Command(binary, "inputs", "--project", r.Repo, "--run", r.State.RunID, name, "--json")
	if cmd.Run() == nil {
		t.Fatal("misplaced flags accepted")
	}
}
func TestRecoveredProfessionalPromptsKeepExactRequirementsAndRetryEvidence(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.State.Goal = "Build a tracker.\nDo not change unrelated data. 界"
	r.State.Steering = []SteeringMessage{{ID: "steer", Text: "Preserve records"}}
	task := Task{ID: "task", Title: "Persist", Instructions: "Inspect the storage boundary; preserve existing fields."}
	r.State.ActiveTask = "1:0"
	before, _ := json.Marshal(r.State)
	prompt := r.workerPrompt(task, "")
	_, data, ok := strings.Cut(prompt, "\nWORK ORDER:\n")
	if !ok {
		t.Fatal("missing work order")
	}
	var order struct {
		Goal           string
		Task           Task
		Platform       map[string]string
		RequiredChecks []string `json:"required_checks"`
	}
	if json.NewDecoder(strings.NewReader(data)).Decode(&order) != nil || order.Goal != r.State.goalContext() || order.Task != task || order.Platform["os"] != runtime.GOOS || order.Platform["verification_shell"] != ShellCommand("")[0] || !reflect.DeepEqual(order.RequiredChecks, r.State.RequiredChecks) {
		t.Fatal("work order lost active requirements or platform")
	}
	after, _ := json.Marshal(r.State)
	if string(before) != string(after) {
		t.Fatal("prompt changed saved evidence")
	}
	diagnostic := "OPENING_DIAGNOSIS " + strings.Repeat("界", 2000) + " FINAL_DETAIL"
	retry := decisionRetryPrompt(r.plannerPrompt(false), errors.New(diagnostic))
	if !utf8.ValidString(retry) || !strings.Contains(retry, "OPENING_DIAGNOSIS") || !strings.Contains(retry, "FINAL_DETAIL") || strings.Contains(retry, strings.Repeat("界", 1000)) {
		t.Fatal("retry diagnosis is missing or unbounded")
	}
	r.State.WorkerSession = &WorkerSession{Round: 1, Report: diagnostic}
	r.State.Dialogue = &DialogueState{Replies: []DialogueReply{{Answer: "Actual answer: preserve fields"}}}
	continuation := r.workerContinuationPrompt(r.State.WorkerSession)
	if !strings.Contains(continuation, "Actual answer: preserve fields") || !strings.Contains(continuation, "state.json:worker_session.report") {
		t.Fatal("continuation lost actual answer or traceability")
	}
	calls := 0
	r.Invoke = func(_ context.Context, _ Connection, p, _ string, _ bool, _ string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("OBSOLETE_DIAGNOSIS")
		}
		if calls == 2 {
			return "", errors.New(diagnostic)
		}
		if strings.Contains(p, "OBSOLETE_DIAGNOSIS") {
			t.Fatal("stale retry retained")
		}
		return decisionJSON(false, nil, nil), nil
	}
	if _, e := r.askDecision(context.Background(), false, r.Settings.Planner, r.plannerPrompt(false), "plan", ""); e != nil || calls != 3 {
		t.Fatal("retry accounting changed", e)
	}
}
