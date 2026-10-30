package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const maxDialogueTurns = 8

type TeamRequest struct {
	To   string `json:"to"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}
type DialogueFrame struct {
	From             string        `json:"from"`
	To               string        `json:"to"`
	Kind             string        `json:"kind,omitempty"`
	Question         string        `json:"question,omitempty"`
	QuestionSequence int           `json:"question_sequence,omitempty"`
	Requests         []TeamRequest `json:"requests,omitempty"`
	Next             int           `json:"next"`
	NeedsAnswer      bool          `json:"needs_answer"`
}
type DialogueReply struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Question   string `json:"question"`
	Answer     string `json:"answer"`
	Unresolved bool   `json:"unresolved,omitempty"`
}
type DialogueState struct {
	Owner     string          `json:"owner"`
	Source    string          `json:"source"`
	Cycle     int             `json:"cycle"`
	Snapshot  string          `json:"snapshot"`
	Turns     int             `json:"turns"`
	Frames    []DialogueFrame `json:"frames"`
	Replies   []DialogueReply `json:"replies"`
	Complete  bool            `json:"complete"`
	Exhausted bool            `json:"exhausted,omitempty"`
}
type WorkerSession struct {
	TaskKey       string `json:"task_key"`
	Model         string `json:"model"`
	Status        string `json:"status"`
	Round         int    `json:"round"`
	Report        string `json:"report,omitempty"`
	DialogueOwner string `json:"dialogue_owner,omitempty"`
}
type WorkerReply struct {
	Protocol int           `json:"buildheadway_worker"`
	Status   string        `json:"status"`
	Report   string        `json:"report"`
	Messages []TeamRequest `json:"messages"`
}

func ValidateTeamRequests(requests []TeamRequest) error {
	if len(requests) > 4 {
		return errors.New("send at most four team messages per reply")
	}
	for _, request := range requests {
		if !validRunID.MatchString(request.To) && request.To != "@team" && request.To != "@peer" && request.To != "@worker" {
			return errors.New("team recipient must be an exact teammate name, @team, @peer, or @worker")
		}
		if !contains([]string{"question", "challenge", "suggestion"}, request.Kind) {
			return errors.New("team message kind must be question, challenge, or suggestion")
		}
		if strings.TrimSpace(request.Text) == "" || len(request.Text) > 6000 {
			return errors.New("team message text must contain 1 to 6000 bytes")
		}
	}
	return nil
}
func (r *Runner) validateAddresses(sender string, requests []TeamRequest) error {
	if err := ValidateTeamRequests(requests); err != nil {
		return err
	}
	if len(requests) > 0 && r.State.Mode != "figureout" {
		return errors.New("team messages require figureout mode")
	}
	for _, request := range requests {
		if request.To == sender {
			return errors.New("send your question to another teammate")
		}
		if request.To == "@team" || request.To == "@worker" || request.To == "@peer" {
			continue
		}
		found := false
		for _, member := range r.State.Team {
			if member.Name == request.To {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unknown teammate %q; use a name from TEAM DIRECTORY", request.To)
		}
	}
	return nil
}
func (r *Runner) teamDirectory() string {
	return "TEAM DIRECTORY:\n" + FormatTeam(r.State.Team) +
		"\n@team: ask all senior agents, excluding yourself.\n@peer: a coding adviser selected from the refreshed free catalog.\n@worker: ask the current implementation model for technical advice (or a verified free coder before implementation).\n" +
		"These are internal teammates, not the user. BuildHeadway delivers their actual responses back to you. Use messages to ask questions, challenge assumptions, or propose an alternative. " +
		"Do not ask the user routine implementation questions. Do not launch other agent CLIs yourself."
}
func (r *Runner) communicationPrompt() string {
	return r.teamDirectory() + "\nBefore committing to a consequential uncertain choice, you may consult a teammate. Return a decision with done=false and messages=[{\"to\":\"NAME\",\"kind\":\"question\",\"text\":\"Your concrete question and context\"}]. " +
		"You will receive answers and then continue your own decision. Peer suggestions are evidence to assess, not orders that override the user's goal. " +
		"Use kind=challenge for a disagreement or kind=suggestion for a proposed alternative. Recipients may consult another teammate before answering you. " +
		"When ready to make your decision, set messages=[]. Avoid unnecessary chatter when the answer is already established."
}
func (r *Runner) expandRequests(sender string, requests []TeamRequest) []TeamRequest {
	var expanded []TeamRequest
	for _, request := range requests {
		if request.To == "@team" {
			for _, member := range r.State.Team {
				if member.Name == sender {
					continue
				}
				copy := request
				copy.To = member.Name
				expanded = append(expanded, copy)
			}
		} else {
			expanded = append(expanded, request)
		}
	}
	return expanded
}
func (r *Runner) beginDialogue(ctx context.Context, owner, sender string, requests []TeamRequest) error {
	if err := r.validateAddresses(sender, requests); err != nil {
		return err
	}
	if len(requests) == 0 {
		return errors.New("a discussion needs a message")
	}
	d := r.State.Dialogue
	snapshot, err := WorkspaceSnapshot(ctx, r.Repo)
	if err != nil {
		return err
	}
	if d == nil || d.Owner != owner || d.Snapshot != snapshot {
		d = &DialogueState{Owner: owner, Source: sender, Cycle: r.State.Cycle, Snapshot: snapshot, Frames: []DialogueFrame{{From: sender, To: sender}}}
		r.State.Dialogue = d
	}
	if d.Turns >= maxDialogueTurns {
		return errors.New("discussion is ready for a concrete decision; do not send more messages")
	}
	root := &d.Frames[0]
	root.Requests = append(root.Requests, r.expandRequests(sender, requests)...)
	d.Complete = false
	return r.checkpoint()
}
func (r *Runner) dialogueConnection(name string) (Connection, error) {
	for _, member := range r.State.Team {
		if member.Name == name {
			return member.Connection, nil
		}
	}
	if name == "@peer" || name == "@worker" {
		c := r.Settings.Worker
		c.RequireFree = true
		if c.Backend != "opencode" {
			return c, errors.New("coding advice requires the verified free OpenCode connection")
		}
		if name == "@worker" && r.State.WorkerSession != nil && r.State.WorkerSession.Model != "" {
			c.Model = r.State.WorkerSession.Model
		}
		return c, nil
	}
	return Connection{}, errors.New("unknown dialogue recipient")
}
func (r *Runner) dialogueContext() string {
	if r.State.Dialogue == nil {
		return ""
	}
	d := r.State.Dialogue
	data, _ := json.Marshal(d.Replies)
	text := "\nTEAM ANSWERS FOR THIS DECISION:\n" + string(data)
	if d.Exhausted || d.Turns >= maxDialogueTurns {
		text += "\nThe discussion has used its eight routed advice calls. Make a concrete decision now with messages=[]. Convert unresolved issues into a focused investigation or implementation task; do not claim they were answered. Continue the goal without asking the user."
	}
	return text
}
func (r *Runner) finishDialogueFrame(answer string, unresolved bool) {
	d := r.State.Dialogue
	f := d.Frames[len(d.Frames)-1]
	kind := "answer"
	if unresolved {
		kind = "unresolved"
		d.Exhausted = true
	}
	r.recordMessage(f.To, f.From, kind, answer, f.QuestionSequence)
	d.Replies = append(d.Replies, DialogueReply{From: f.To, To: f.From, Question: f.Question, Answer: answer, Unresolved: unresolved})
	d.Frames = d.Frames[:len(d.Frames)-1]
}
func (r *Runner) routeDialogue(ctx context.Context) error {
	d := r.State.Dialogue
	if d == nil {
		return nil
	}
	snapshot, err := WorkspaceSnapshot(ctx, r.Repo)
	if err != nil {
		return err
	}
	if snapshot != d.Snapshot {
		root := d.Frames[0]
		root.Next = 0
		d.Frames = []DialogueFrame{root}
		d.Replies = nil
		d.Turns = 0
		d.Snapshot = snapshot
		d.Complete = false
		d.Exhausted = false
		r.recordMessage("harness", d.Source, "recheck", "Files changed while the discussion was saved. Rechecking the questions against the current project.", 0)
		if err := r.checkpoint(); err != nil {
			return err
		}
	}
	for !d.Complete {
		if err := ctx.Err(); err != nil {
			return err
		}
		index := len(d.Frames) - 1
		frame := &d.Frames[index]
		if frame.NeedsAnswer {
			if d.Turns >= maxDialogueTurns {
				r.finishDialogueFrame("This question remains unresolved after the discussion turn budget. Choose a focused investigation or repair task using the evidence already gathered.", true)
				if err := r.checkpoint(); err != nil {
					return err
				}
				continue
			}
			connection, err := r.dialogueConnection(frame.To)
			if err != nil {
				return err
			}
			prompt := r.advicePrompt(*frame)
			answer, err := r.askDecision(ctx, false, connection, prompt, "dialogue-"+strings.TrimPrefix(frame.To, "@")+"-answer", frame.To)
			if err != nil {
				return err
			}
			d.Turns++
			if len(answer.Messages) == 0 {
				r.finishDialogueFrame(decisionMessage(answer), false)
			} else {
				r.recordMessage(frame.To, frame.From, "consulting", answer.Summary, frame.QuestionSequence)
				frame.Requests = r.expandRequests(frame.To, answer.Messages)
				frame.Next, frame.NeedsAnswer = 0, false
			}
		} else if frame.Next < len(frame.Requests) {
			request := frame.Requests[frame.Next]
			frame.Next++
			sequence := r.recordMessage(frame.To, request.To, request.Kind, request.Text, 0)
			d.Frames = append(d.Frames, DialogueFrame{From: frame.To, To: request.To, Kind: request.Kind, Question: request.Text, QuestionSequence: sequence, NeedsAnswer: true})
		} else if index == 0 {
			d.Complete = true
		} else {
			// A recipient that consulted others now receives their replies and
			// answers its original sender. No question is sent to the user.
			frame.NeedsAnswer = true
			frame.Requests, frame.Next = nil, 0
		}
		if err := r.checkpoint(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) advicePrompt(frame DialogueFrame) string {
	evidence, _ := json.Marshal(map[string]any{
		"platform": promptPlatform(), "required_checks": r.State.RequiredChecks, "suggested_checks": r.State.SuggestedChecks,
		"goal": r.State.goalContext(), "project_guidance": r.State.ProjectContext, "project_memory": r.State.ProjectMemory,
		"tasks": r.State.Tasks, "worker_handoff": briefHandoff(r.State.WorkerSession),
		"worker_results": briefWorkers(r.State.WorkerResults), "check_results": briefChecks(r.State.CheckResults), "hook_results": briefHooks(r.State.HookResults), "log_directory": r.Dir, "full_worker_evidence": filepath.Join(r.Dir, "state.json"),
		"success_criteria": r.State.SuccessCriteria,
	})
	return "INTERNAL TEAM CONSULTATION\nYou are " + frame.To + ", replying to " + frame.From + ".\n" + agentWorkingAgreement + promptJSONSection("CONSULTATION REQUEST", map[string]string{"from": frame.From, "to": frame.To, "kind": frame.Kind, "question": frame.Question}) +
		"\nGive advice for this exact question, using the evidence and file-reading tools. You are not executing the goal or reviewing completion in this invocation. " +
		"Do not run shell commands, tests, hashes, edits, or implementation tools. Use read/glob/grep for source inspection. A teammate's request to run a check is a proposed future action, not permission to execute it in this advice call. " +
		"If the requested evidence does not exist yet, explain that and recommend the next concrete action; do not stall or invent results. The worker and verification loop perform those actions. " +
		"Return your answer in summary, recommended follow-up work in tasks, and done=false. Return checks=[] and check_replacements=[]; this is advice, not a completion vote. " +
		r.communicationPrompt() + r.dialogueContext() + "\nReturn only JSON matching this schema:\n" + r.decisionOutputSchema() +
		"\nPROJECT EVIDENCE:\n" + string(evidence) + "\nFILES:\n" + ProjectEvidence(r.Repo, r.Paths) + "\nRECENT TEAM DISCUSSION:\n" + discussionContext(r.State, 12)
}
func (r *Runner) coordinatedDecision(ctx context.Context, review bool, member SeniorAgent, prompt, label string) (Decision, error) {
	owner := fmt.Sprintf("%d-%s", r.State.Cycle, label)
	if d := r.State.Dialogue; d != nil && d.Owner != owner {
		r.State.Dialogue = nil
	}
	for {
		if err := r.routeDialogue(ctx); err != nil {
			return Decision{}, err
		}
		answer, err := r.askDecision(ctx, review, member.Connection, prompt+r.communicationPrompt()+r.dialogueContext(), label, member.Name)
		if err != nil {
			return Decision{}, err
		}
		if len(answer.Messages) == 0 {
			r.State.Dialogue = nil
			return answer, nil
		}
		if err := r.beginDialogue(ctx, owner, member.Name, answer.Messages); err != nil {
			return Decision{}, err
		}
	}
}
func ValidateDialogueState(s State) error {
	d := s.Dialogue
	if d != nil {
		if s.Mode != "figureout" || d.Cycle != s.Cycle || d.Owner == "" || len(d.Owner) > 300 || len(d.Snapshot) != 64 || len(d.Frames) < 1 || len(d.Frames) > maxDialogueTurns+2 || d.Turns < 0 || d.Turns > maxDialogueTurns || len(d.Replies) > 240 {
			return errors.New("invalid saved team discussion")
		}
		r := Runner{State: s}
		root := d.Frames[0]
		if root.NeedsAnswer || root.From != d.Source || root.To != d.Source {
			return errors.New("invalid discussion root")
		}
		if _, err := r.dialogueConnection(d.Source); err != nil && d.Source != "@worker" && d.Source != "@peer" {
			return errors.New("invalid discussion source")
		}
		for index, frame := range d.Frames {
			if frame.Next < 0 || frame.Next > len(frame.Requests) || len(frame.Requests) > 192 || len(frame.Question) > 6000 {
				return errors.New("invalid saved discussion frame")
			}
			if index > 0 && (frame.From != d.Frames[index-1].To || frame.QuestionSequence < 1 || frame.QuestionSequence > s.MessageSequence || strings.TrimSpace(frame.Question) == "" || !contains([]string{"question", "challenge", "suggestion"}, frame.Kind)) {
				return errors.New("invalid saved discussion route")
			}
			for _, request := range frame.Requests {
				if err := r.validateAddresses(frame.To, []TeamRequest{request}); err != nil {
					return err
				}
			}
			if frame.To != d.Source {
				if _, err := r.dialogueConnection(frame.To); err != nil && frame.To != "@peer" && frame.To != "@worker" {
					return err
				}
			}
		}
		if d.Complete && len(d.Frames) != 1 {
			return errors.New("completed discussion still has pending replies")
		}
	}
	if worker := s.WorkerSession; worker != nil {
		if s.Mode != "figureout" || worker.TaskKey == "" || worker.TaskKey != s.ActiveTask || worker.Round < 0 || worker.Round > 8 || !contains([]string{"executing", "consulting", "ready"}, worker.Status) {
			return errors.New("invalid worker conversation checkpoint")
		}
		if worker.Status == "consulting" && (d == nil || d.Owner != worker.DialogueOwner) {
			return errors.New("worker is missing its saved consultation")
		}
		if d != nil && (d.Owner != worker.DialogueOwner || d.Source != "@worker") {
			return errors.New("saved discussion belongs to a different assignment")
		}
	}
	return nil
}
func ParseWorkerReply(raw string) (WorkerReply, bool, error) {
	candidates, err := jsonCandidates(raw)
	if err != nil {
		return WorkerReply{}, false, err
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(candidates[i]), &fields) != nil {
			continue
		}
		if _, ok := fields["buildheadway_worker"]; !ok {
			continue
		}
		var reply WorkerReply
		decoder := json.NewDecoder(strings.NewReader(candidates[i]))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&reply) != nil || reply.Protocol != 1 || len(fields) != 4 || strings.TrimSpace(reply.Report) == "" || len(reply.Report) > 12000 || len(fields["messages"]) == 0 || fields["messages"][0] != '[' {
			return reply, true, errors.New("invalid worker communication reply")
		}
		if err := ValidateTeamRequests(reply.Messages); err != nil {
			return reply, true, err
		}
		if !contains([]string{"complete", "consult", "replan"}, reply.Status) || (reply.Status == "complete" && len(reply.Messages) > 0) || (reply.Status == "consult" && len(reply.Messages) == 0) {
			return reply, true, errors.New("worker status must be complete, consult with messages, or replan")
		}
		return reply, true, nil
	}
	return WorkerReply{}, false, nil
}

const workerCommunicationPrompt = `
INTERNAL TEAM COMMUNICATION
You can ask a senior or a free coding peer for help while implementing, and then continue from their actual answer. If the assignment is flawed, challenge it rather than silently making a different product. End this invocation with exactly one JSON object:
{"buildheadway_worker":1,"status":"consult","report":"What you inspected or changed, and what you need next","messages":[{"to":"lead","kind":"question","text":"Your concrete question"}]}
BuildHeadway routes these messages, returns the answers, and invokes you again to continue this same task. Other recipients may also consult teammates. Do not ask the human routine questions. Do not launch another agent yourself.
Use status="complete", messages=[] when this assignment is implemented; put concise Changed, Verification and Remaining sections in report as a string. Keep the same JSON fields. Use status="replan" when the team should change or split the assignment; report the concrete blocker and proposed next step. Replan is not completion of the goal.
`

func (r *Runner) collaborativeWork(ctx context.Context, task Task) (result WorkerResult, runErr error) {
	if r.State.WorkerSession == nil || r.State.WorkerSession.TaskKey != r.State.ActiveTask {
		r.State.WorkerSession = &WorkerSession{TaskKey: r.State.ActiveTask, Status: "ready"}
	}
	session := r.State.WorkerSession
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if session.Status == "consulting" || (session.Status == "ready" && r.State.Dialogue != nil) {
			if err := r.routeDialogue(ctx); err != nil {
				return result, err
			}
			session.Status = "ready"
			if err := r.checkpoint(); err != nil {
				return result, err
			}
		}
		continuation := r.workerContinuationPrompt(session)
		var err error
		result, err = r.workOnce(ctx, task, continuation)
		if err != nil {
			return result, err
		}
		if result.Reply == nil || result.Reply.Status == "complete" {
			r.State.Dialogue = nil
			return result, nil
		}
		reply := result.Reply
		if reply.Status == "replan" || session.Round >= 8 || (r.State.Dialogue != nil && r.State.Dialogue.Turns >= maxDialogueTurns) {
			result.OK = false
			result.Error = "Worker requested team replanning: " + reply.Report
			r.message("@worker", "@team", "challenge", result.Error)
			r.State.Dialogue = nil
			return result, nil
		}
		session.Round++
		session.Report = reply.Report
		session.DialogueOwner = fmt.Sprintf("worker-%d-%d", r.State.Cycle, r.State.NextTask)
		// Save the returned handoff and the pending recipients atomically before
		// invoking a teammate. On restart no implementation call is replayed.
		session.Status = "consulting"
		r.message("@worker", "@team", "handoff", reply.Report)
		if err := r.beginDialogue(ctx, session.DialogueOwner, "@worker", reply.Messages); err != nil {
			// Snapshotting or persisting the handoff may fail before it is
			// durable. Keep a recoverable uncertain-worker checkpoint in that
			// case; never save "consulting" without its matching conversation.
			session.Status = "executing"
			return result, err
		}
	}
}
