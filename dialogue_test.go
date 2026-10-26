package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func discussionDecision(summary string, requests ...TeamRequest) string {
	d := Decision{Summary: summary, Tasks: []Task{}, Checks: []string{}, SuccessCriteria: []string{}, CheckReplacements: []CheckReplacement{}, Messages: requests}
	data, _ := json.Marshal(d)
	return string(data)
}
func workerCommunication(status, report string, requests ...TeamRequest) string {
	if requests == nil {
		requests = []TeamRequest{}
	}
	data, _ := json.Marshal(WorkerReply{Protocol: 1, Status: status, Report: report, Messages: requests})
	return string(data)
}
func TestAgentsConsultEachOtherAndWorkerContinuesFromRealAnswers(t *testing.T) {
	r, _ := councilFixture(t)
	proposals, criticAdvice, workerCalls := 0, 0, 0
	order := []string{}
	r.MaxCalls = 30
	r.Invoke = func(_ context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		order = append(order, label)
		if label == "council-plan-lead-proposal" {
			proposals++
			if proposals == 1 {
				return discussionDecision("I need an implementation recommendation", TeamRequest{"critic", "question", "Which operation should the worker implement?"}), nil
			}
			if !strings.Contains(prompt, "USE_ADDITION") {
				t.Fatal("lead did not receive the critic's final answer")
			}
		}
		if label == "dialogue-critic-answer" {
			criticAdvice++
			if criticAdvice == 1 {
				return discussionDecision("Consult a coding peer before deciding", TeamRequest{"@peer", "question", "Inspect the arithmetic contract and recommend an operation."}), nil
			}
			if !strings.Contains(prompt, "PEER_SAYS_ADD") {
				t.Fatal("critic did not receive peer feedback")
			}
			return discussionDecision("USE_ADDITION. Preserve tests and finish with a newline."), nil
		}
		if label == "dialogue-peer-answer" {
			if !c.RequireFree || c.Model != "opencode/free" {
				t.Fatal("coding advice escaped verified free routing")
			}
			return discussionDecision("PEER_SAYS_ADD: the goal requires addition and a good output."), nil
		}
		if label == "dialogue-lead-answer" {
			if !strings.Contains(prompt, "Please resolve the output format") {
				t.Fatal("worker question was not delivered to the lead")
			}
			return discussionDecision("WRITE_GOOD: write good followed by a newline to result.txt, then verify."), nil
		}
		if !planner {
			workerCalls++
			if workerCalls == 1 {
				os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("partial\n"), 0600)
				return workerCommunication("consult", "Inspected the task and wrote partial output.", TeamRequest{"lead", "challenge", "Please resolve the output format before I finish."}), nil
			}
			if !strings.Contains(prompt, "WRITE_GOOD") || !strings.Contains(prompt, "CONTINUE THE SAME ASSIGNMENT") {
				t.Fatal("worker was restarted without the actual senior answer")
			}
			if err := os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("good\n"), 0600); err != nil {
				return "", err
			}
			return workerCommunication("complete", "Implemented the senior's decision and preserved the checks."), nil
		}
		data, _ := os.ReadFile(filepath.Join(r.Repo, "result.txt"))
		if string(data) == "good\n" {
			return decisionJSON(true, nil, nil), nil
		}
		return decisionJSON(false, []Task{{"fix", "Implement the chosen operation", "Write the correct result after following the team decision."}}, nil), nil
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workerCalls != 2 || r.State.WorkerCalls != 2 || proposals != 2 || criticAdvice != 2 || r.State.Status != "complete" {
		t.Fatalf("missing two-way coordination: %v", order)
	}
	if r.State.Dialogue != nil || r.State.WorkerSession != nil {
		t.Fatal("completed goal retained pending conversations")
	}
	questions, answers := 0, 0
	for _, message := range r.State.Discussion {
		if message.Kind == "question" || message.Kind == "challenge" {
			questions++
		}
		if message.Kind == "answer" {
			answers++
			if message.ReplyTo == 0 || message.Thread == "" {
				t.Fatal("reply lost its conversation identity")
			}
		}
	}
	if questions != 3 || answers != 3 {
		t.Fatalf("messages not actually routed: questions=%d answers=%d", questions, answers)
	}
	if _, _, err := LoadRun(r.Repo, r.State.RunID); err != nil {
		t.Fatal(err)
	}
}
func TestSavedWorkerConsultationResumesBeforeFurtherImplementation(t *testing.T) {
	r, _ := councilFixture(t)
	workerCalls, advisorCalls := 0, 0
	r.Invoke = func(_ context.Context, _ Connection, _ string, label string, planner bool, _ string) (string, error) {
		if strings.HasPrefix(label, "dialogue-") {
			advisorCalls++
			return "", context.Canceled
		}
		if planner {
			return decisionJSON(false, []Task{{"fix", "Write output", "Write the correct output"}}, nil), nil
		}
		workerCalls++
		os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("partial\n"), 0600)
		return workerCommunication("consult", "Partial work saved; awaiting senior advice", TeamRequest{"lead", "question", "What final output is required?"}), nil
	}
	if err := r.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("did not save an interrupted consultation: %v", err)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.WorkerSession == nil || saved.WorkerSession.Status != "consulting" || saved.ActiveTask == "" || len(saved.Dialogue.Frames) != 2 {
		t.Fatal("returned handoff did not survive interruption")
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.Discover = r.Discover
	first := true
	resumed.Invoke = func(_ context.Context, _ Connection, prompt, label string, planner bool, _ string) (string, error) {
		if first {
			first = false
			if label != "dialogue-lead-answer" {
				t.Fatalf("replayed the worker before completing advice: %s", label)
			}
		}
		if label == "dialogue-lead-answer" {
			return discussionDecision("RESUME_ADVICE: write good followed by newline."), nil
		}
		if planner {
			return decisionJSON(true, nil, nil), nil
		}
		workerCalls++
		if !strings.Contains(prompt, "RESUME_ADVICE") || !strings.Contains(prompt, "Partial work saved") {
			t.Fatal("resume discarded partial work or answer")
		}
		os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("good\n"), 0600)
		return workerCommunication("complete", "Applied the advice"), nil
	}
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workerCalls != 2 || advisorCalls != 1 || resumed.State.Status != "complete" {
		t.Fatal("resume duplicated implementation or skipped review")
	}
}
func TestInterruptedNestedSeniorDiscussionResumesAtPendingRecipient(t *testing.T) {
	r, _ := councilFixture(t)
	r.PlanOnly = true
	leadCalls := 0
	r.Invoke = func(_ context.Context, _ Connection, _ string, label string, _ bool, _ string) (string, error) {
		switch label {
		case "council-plan-lead-proposal":
			leadCalls++
			return discussionDecision("Consult critic", TeamRequest{"critic", "question", "Choose an approach"}), nil
		case "dialogue-critic-answer":
			return discussionDecision("Ask peer", TeamRequest{"@peer", "question", "Review the approach"}), nil
		case "dialogue-peer-answer":
			return "", context.Canceled
		}
		return "", errors.New("unexpected call")
	}
	if err := r.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.PlanOnly = true
	resumed.Discover = r.Discover
	first := true
	resumed.Invoke = func(_ context.Context, _ Connection, prompt, label string, _ bool, _ string) (string, error) {
		if first {
			first = false
			if label != "dialogue-peer-answer" {
				t.Fatalf("lost pending recipient: %s", label)
			}
		}
		if label == "dialogue-peer-answer" {
			return discussionDecision("PEER_ANSWER"), nil
		}
		if label == "dialogue-critic-answer" {
			if !strings.Contains(prompt, "PEER_ANSWER") {
				t.Fatal("nested response did not reach original asker")
			}
			return discussionDecision("CRITIC_RESOLVED"), nil
		}
		if label == "council-plan-lead-proposal" {
			leadCalls++
			if !strings.Contains(prompt, "CRITIC_RESOLVED") {
				t.Fatal("root lost its answer")
			}
		}
		return decisionJSON(false, []Task{{"one", "Work", "Implement the agreed approach"}}, nil), nil
	}
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if leadCalls != 2 || resumed.State.Status != "planned" || resumed.State.WorkerCalls != 0 {
		t.Fatal("partial conversation was not resumed")
	}
}
func TestWorkerReplanReturnsToTeamBeforeDependentTask(t *testing.T) {
	r, _ := councilFixture(t)
	workerCalls := 0
	r.Invoke = func(_ context.Context, _ Connection, prompt, label string, planner bool, _ string) (string, error) {
		if planner {
			if r.State.Cycle == 1 && !strings.Contains(label, "review") {
				return decisionJSON(false, []Task{{"first", "Investigate", "Implement"}, {"dependent", "Dependent", "Do later work"}}, nil), nil
			}
			if r.State.Cycle == 1 {
				if !strings.Contains(prompt, "wrong assumption") {
					t.Fatal("team did not receive worker challenge")
				}
				return decisionJSON(false, []Task{{"corrected", "Correct approach", "Write good output"}}, nil), nil
			}
			return decisionJSON(true, nil, nil), nil
		}
		workerCalls++
		if workerCalls == 1 {
			return workerCommunication("replan", "The assignment has a wrong assumption. Investigate and replace it before dependent work."), nil
		}
		if r.State.Tasks[r.State.NextTask].ID == "dependent" {
			t.Fatal("dependent assignment ran after the worker challenged the plan")
		}
		os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("good\n"), 0600)
		return workerCommunication("complete", "Implemented the revised plan"), nil
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workerCalls != 2 || r.State.Cycle != 2 {
		t.Fatal("team failed to revise the plan")
	}
}
func TestConversationLimitsProduceAnUnresolvedHandoffInsteadOfEndlessChat(t *testing.T) {
	r, _ := councilFixture(t)
	os.MkdirAll(r.Dir, 0700)
	if err := r.beginDialogue(context.Background(), "bounded", "lead", []TeamRequest{{"critic", "question", "Please resolve this"}}); err != nil {
		t.Fatal(err)
	}
	r.Invoke = func(_ context.Context, _ Connection, _ string, label string, _ bool, _ string) (string, error) {
		recipient := "lead"
		if label == "dialogue-lead-answer" {
			recipient = "critic"
		}
		return discussionDecision("Consult another teammate", TeamRequest{recipient, "question", "Can you decide?"}), nil
	}
	if err := r.routeDialogue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.State.Dialogue.Turns != 8 || !r.State.Dialogue.Complete || !r.State.Dialogue.Exhausted || r.State.PlannerCalls != 8 {
		t.Fatal("discussion did not yield a bounded actionable handoff")
	}
	for _, reply := range r.State.Dialogue.Replies {
		if !reply.Unresolved {
			t.Fatal("invented an answer when nobody resolved the question")
		}
	}
	if !strings.Contains(r.dialogueContext(), "focused investigation") {
		t.Fatal("unresolved questions were silently discarded")
	}
}
func TestMessagesAndWorkerRepliesAreStrictlyValidated(t *testing.T) {
	r, _ := councilFixture(t)
	for _, request := range []TeamRequest{{"human", "question", "Choose for us"}, {"lead", "unknown", "bad"}, {"lead", "question", ""}} {
		if r.validateAddresses("@worker", []TeamRequest{request}) == nil {
			t.Fatalf("accepted invalid route: %v", request)
		}
	}
	for _, raw := range []string{
		`{"buildheadway_worker":1,"status":"complete","report":"done","messages":[{"to":"lead","kind":"question","text":"help"}]}`,
		`{"buildheadway_worker":1,"status":"consult","report":"help","messages":[]}`,
		`{"buildheadway_worker":1,"status":"complete","report":"done","messages":null}`,
		`{"buildheadway_worker":1,"status":"consult","report":"help","messages":[{"to":"lead","kind":"question","text":"help","from":"critic"}]}`,
	} {
		if _, _, err := ParseWorkerReply(raw); err == nil {
			t.Fatalf("accepted malformed handoff %s", raw)
		}
	}
	valid := discussionDecision("Advice needed", TeamRequest{"critic", "question", "Which approach?"})
	if d, err := ParseDecision("```json\n" + valid + "\n```"); err != nil || len(d.Messages) != 1 {
		t.Fatalf("valid routed decision was rejected: %v", err)
	}
	if _, err := ParseDecision(strings.Replace(valid, `"done":false`, `"done":true`, 1)); err == nil {
		t.Fatal("approved completion with unresolved questions")
	}
	if _, structured, err := ParseWorkerReply("Implemented the assigned task."); err != nil || structured {
		t.Fatal("legacy worker report compatibility was lost")
	}
}
func TestNormalTerminalGoalsQueueAutonomousTeamMode(t *testing.T) {
	r, _ := councilFixture(t)
	m := newApp(r.Paths, r.Repo, "", false, false)
	m.busy, m.connected, m.settings = true, true, r.Settings
	m.startGoal("Build the app", false)
	queue, err := ReadQueue(r.Repo)
	if err != nil || len(queue.Items) != 1 || queue.Items[0].Mode != "figureout" {
		t.Fatal("plain terminal goal lost team mode")
	}
}

func TestCorruptDiscussionCannotPopItsRootOnResume(t *testing.T) {
	r, _ := councilFixture(t)
	if err := os.MkdirAll(r.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := r.beginDialogue(context.Background(), "saved-root", "lead", []TeamRequest{{"critic", "question", "Inspect the approach"}}); err != nil {
		t.Fatal(err)
	}
	r.State.Dialogue.Frames[0].NeedsAnswer = true
	if err := r.checkpoint(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadRun(r.Repo, r.State.RunID); err == nil {
		t.Fatal("a corrupt root could be popped and crash the resumed runner")
	}
}

func TestCancellationWhileSavingWorkerHandoffRemainsRecoverable(t *testing.T) {
	r, _ := councilFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if planner {
			return decisionJSON(false, []Task{{"fix", "Write output", "Write good output"}}, nil), nil
		}
		cancel()
		return workerCommunication("consult", "Inspected files", TeamRequest{"lead", "question", "Confirm the expected output"}), nil
	}
	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected interrupted handoff: %v", err)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil {
		t.Fatalf("handoff cancellation corrupted the checkpoint: %v", err)
	}
	if saved.WorkerSession == nil || saved.WorkerSession.Status != "executing" {
		t.Fatal("unsaved handoff was treated as durable")
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.recoverCheckpoint()
	if resumed.State.Phase != "checking" || resumed.State.WorkerSession != nil {
		t.Fatal("uncertain handoff skipped verification")
	}
}

func TestChangedFilesInvalidateFinishedAdviceBeforeWorkerResumes(t *testing.T) {
	r, _ := councilFixture(t)
	if err := os.MkdirAll(r.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	task := Task{"fix", "Write output", "Write good output"}
	r.State.Tasks = []Task{task}
	if err := r.beginTask(task); err != nil {
		t.Fatal(err)
	}
	r.State.WorkerSession = &WorkerSession{TaskKey: r.State.ActiveTask, Status: "consulting", Round: 1, Report: "First inspection done", DialogueOwner: "saved-advice"}
	if err := r.beginDialogue(context.Background(), "saved-advice", "@worker", []TeamRequest{{"lead", "question", "Inspect result.txt and determine the final output"}}); err != nil {
		t.Fatal(err)
	}
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, _ bool, _ string) (string, error) {
		return discussionDecision("STALE_ADVICE"), nil
	}
	if err := r.routeDialogue(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.State.WorkerSession.Status = "ready"
	if err := os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("changed externally\n"), 0600); err != nil {
		t.Fatal(err)
	}
	advice, workers := 0, 0
	r.Invoke = func(_ context.Context, _ Connection, prompt, label string, planner bool, _ string) (string, error) {
		if planner {
			advice++
			return discussionDecision("CURRENT_ADVICE: write good"), nil
		}
		workers++
		// Historical discussion is retained honestly, but the decision-specific
		// answer set must be refreshed before dispatch.
		answers := r.dialogueContext()
		if !strings.Contains(answers, "CURRENT_ADVICE") || strings.Contains(answers, "STALE_ADVICE") || !strings.Contains(prompt, "CURRENT_ADVICE") {
			t.Fatal("worker received stale decision evidence")
		}
		return workerCommunication("complete", "Used the current answer"), nil
	}
	result, err := r.collaborativeWork(context.Background(), task)
	if err != nil || !result.OK || advice != 1 || workers != 1 {
		t.Fatalf("advice was not rechecked: %v %d %d", err, advice, workers)
	}
}

func TestFreeCodingAdviceRejectsPaidCatalogAndReportedCharge(t *testing.T) {
	r, _ := councilFixture(t)
	r.Settings.Worker.Model = "opencode/no-longer-free"
	connection, err := r.dialogueConnection("@peer")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		calls++
		return discussionDecision("Unreachable"), nil
	}
	_, err = r.askDecision(context.Background(), false, connection, "Advise", "dialogue-peer-answer", "@peer")
	if err == nil || calls != 0 {
		t.Fatal("adviser bypassed refreshed zero-price catalog")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", filepath.Join(t.TempDir(), "capture.json"))
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_REPLY", `{"type":"step_finish","part":{"cost":0.01}}`)
	connection = Connection{Backend: "opencode", Model: "opencode/claimed-free", Executable: binary, RequireFree: true}
	_, err = r.invokeCLI(context.Background(), connection, "Advise", "dialogue-peer-answer", true, "")
	var charged costError
	if !errors.As(err, &charged) {
		t.Fatalf("advice accepted a reported charge: %v", err)
	}
}
