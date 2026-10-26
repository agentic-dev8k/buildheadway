package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A team is pinned to a goal. Changing global connections affects new goals,
// never the membership or permissions of an already-running council.
type SeniorAgent struct {
	Name       string     `json:"name"`
	Connection Connection `json:"connection"`
}
type AgentMessage struct {
	Thread   string    `json:"thread,omitempty"`
	ReplyTo  int       `json:"reply_to,omitempty"`
	Sequence int       `json:"sequence"`
	At       string    `json:"at"`
	Cycle    int       `json:"cycle"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Kind     string    `json:"kind"`
	Text     string    `json:"text"`
	Decision *Decision `json:"decision,omitempty"`
}
type CouncilRound struct {
	Cycle       int        `json:"cycle"`
	Review      bool       `json:"review"`
	Fingerprint string     `json:"fingerprint"`
	Next        int        `json:"next"`
	Decisions   []Decision `json:"decisions"`
	Complete    bool       `json:"complete"`
	Consensus   bool       `json:"consensus"`
	Final       Decision   `json:"final"`
}

func validateSeniorConnection(c Connection) error {
	if !SupportedBackend(c.Backend) {
		return fmt.Errorf("unsupported senior agent %q", c.Backend)
	}
	if len(c.Model) > 300 || len(c.Provider) > 100 || strings.ContainsAny(c.Model+c.Provider, "\r\n\x00") {
		return errors.New("invalid model or provider name")
	}
	if c.Reasoning != "" {
		if c.Backend != "codex" {
			return errors.New("--reasoning is supported by the Codex adapter")
		}
		if !contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}, c.Reasoning) {
			return errors.New("unknown reasoning effort; choose low, medium, high, xhigh, max, or ultra supported by your model")
		}
	}
	return nil
}
func ValidateTeam(team []SeniorAgent) error {
	if len(team) > 6 {
		return errors.New("a team can contain at most six senior agents")
	}
	names := map[string]bool{}
	for _, member := range team {
		if !validRunID.MatchString(member.Name) || names[member.Name] {
			return errors.New("senior names must be unique letters, numbers, dashes, or underscores")
		}
		names[member.Name] = true
		if err := validateSeniorConnection(member.Connection); err != nil {
			return err
		}
	}
	return nil
}
func FigureOutTeam(settings Settings) ([]SeniorAgent, error) {
	team := []SeniorAgent{{Name: "lead", Connection: settings.Planner}}
	team = append(team, settings.Seniors...)
	if len(team) == 1 {
		// Separate calls and roles, not a claim that another model is installed.
		team = append(team, SeniorAgent{Name: "reviewer", Connection: settings.Planner})
	}
	return team, ValidateTeam(team)
}
func (r *Runner) EnableFigureOut(access string) error {
	if r.resumeID != "" {
		return errors.New("resume keeps the saved goal's mode, team, and access")
	}
	if access == "" {
		access = "full"
	}
	if access != "full" && access != "workspace" {
		return errors.New("access must be full or workspace")
	}
	team, err := FigureOutTeam(r.Settings)
	if err != nil {
		return err
	}
	r.State.Mode, r.State.Access, r.State.Team = "figureout", access, team
	worker := r.Settings.Worker
	r.State.WorkerConnection = &worker
	if r.State.Config.MaxStalledCycles == nil {
		r.MaxStalledCycles = 0
	}
	r.note("figureout", "Autonomous senior council · "+access+" access")
	return nil
}
func ValidateCouncilState(s State) error {
	if err := ValidateDialogueState(s); err != nil {
		return err
	}
	if s.Mode == "" {
		if s.Access != "" || s.Council != nil || len(s.Team) > 0 {
			return errors.New("council state requires figureout mode")
		}
		return nil
	}
	if s.Mode != "figureout" || (s.Access != "full" && s.Access != "workspace") {
		return errors.New("invalid saved goal mode or access")
	}
	if len(s.Team) < 2 {
		return errors.New("saved figureout team needs a lead and a reviewer")
	}
	if err := ValidateTeam(s.Team); err != nil {
		return err
	}
	if s.WorkerConnection == nil || (s.WorkerConnection.Backend != "opencode" && s.WorkerConnection.Backend != "demo") {
		return errors.New("invalid saved figureout worker")
	}
	if s.MessageSequence < 0 || len(s.Discussion) > 400 {
		return errors.New("invalid saved discussion")
	}
	previous := 0
	for _, message := range s.Discussion {
		if message.Sequence <= previous || message.Sequence > s.MessageSequence {
			return errors.New("invalid discussion sequence")
		}
		previous = message.Sequence
	}
	if round := s.Council; round != nil {
		if round.Cycle != s.Cycle || round.Next != len(round.Decisions) || round.Next < 0 || round.Next > len(s.Team)+1 || len(round.Fingerprint) != 64 {
			return errors.New("invalid saved council round")
		}
		if round.Complete != (round.Next == len(s.Team)+1) {
			return errors.New("incomplete council decision")
		}
		for _, d := range round.Decisions {
			if err := ValidateDecision(d); err != nil {
				return err
			}
		}
		if round.Complete {
			if err := ValidateDecision(round.Final); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *Runner) fullAccess() bool { return r.State.Mode == "figureout" && r.State.Access == "full" }
func (r *Runner) message(from, to, kind, text string, decisions ...Decision) {
	r.recordMessage(from, to, kind, text, 0, decisions...)
}
func (r *Runner) recordMessage(from, to, kind, text string, replyTo int, decisions ...Decision) int {
	if r.State.Mode != "figureout" {
		return 0
	}
	r.State.MessageSequence++
	m := AgentMessage{Sequence: r.State.MessageSequence, At: time.Now().UTC().Format(time.RFC3339), Cycle: r.State.Cycle, From: from, To: to, Kind: kind, Text: Tail(text, 12000)}
	m.ReplyTo = replyTo
	if r.State.Dialogue != nil {
		m.Thread = r.State.Dialogue.Owner
	}
	if len(decisions) > 0 {
		decision := decisions[0]
		m.Decision = &decision
	}
	r.State.Discussion = append(r.State.Discussion, m)
	if len(r.State.Discussion) > 400 {
		r.State.Discussion = r.State.Discussion[len(r.State.Discussion)-400:]
	}
	r.Emit(Event{Kind: "discussion", Text: from + " → " + to + " · " + kind + "\n" + Tail(text, 1600), Value: m})
	return m.Sequence
}
func discussionContext(s State, n int) string {
	data, _ := json.Marshal(briefDiscussion(s.Discussion, n))
	return string(data)
}
func FormatDiscussion(s State) string {
	if len(s.Discussion) == 0 {
		return "No agent discussion recorded. Start with /figureout YOUR GOAL."
	}
	var out strings.Builder
	for _, m := range s.Discussion {
		reply := ""
		if m.ReplyTo != 0 {
			reply = fmt.Sprintf(" · reply to #%d", m.ReplyTo)
		}
		fmt.Fprintf(&out, "#%d · cycle %d · %s · %s → %s · %s%s\n%s\n\n", m.Sequence, m.Cycle, m.At, m.From, m.To, m.Kind, reply, m.Text)
	}
	return strings.TrimSpace(out.String())
}
func FormatTeam(team []SeniorAgent) string {
	var out strings.Builder
	for _, member := range team {
		c := member.Connection
		model := c.Model
		if model == "" {
			model = "account default"
		}
		if c.Backend == "opencode" && (model == "auto" || c.Model == "") {
			model = "verified free model"
		}
		fmt.Fprintf(&out, "%s · %s · %s", member.Name, c.Backend, model)
		if c.Reasoning != "" {
			fmt.Fprintf(&out, " · reasoning %s", c.Reasoning)
		}
		out.WriteString("\n")
	}
	if len(team) == 2 && team[0].Connection == team[1].Connection {
		out.WriteString("Two separate roles use the same connection. Attach another agent for a different reviewer.\n")
	}
	return strings.TrimSpace(out.String())
}
func (r *Runner) councilFingerprint(ctx context.Context, review bool) (string, error) {
	snapshot, err := WorkspaceSnapshot(ctx, r.Repo)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal([]any{r.State.Cycle, review, snapshot, r.State.Steering, r.State.HookResults, r.State.CheckResults, r.State.WorkerResults, r.State.RequiredChecks, r.State.SuggestedChecks, r.State.SuccessCriteria, r.State.Acceptance, r.State.ProjectContext, r.State.ProjectMemory})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
func (r *Runner) councilPrompt(review bool, step int, round *CouncilRound) string {
	member := r.State.Team[0]
	role := "Propose the next ordered implementation batch. Other seniors will critique your proposal."
	if step > 0 && step < len(r.State.Team) {
		member = r.State.Team[step]
		role = "Independently inspect the code and critique the lead's proposal and earlier peer feedback. Identify concrete gaps and give corrective tasks. Do not rubber-stamp a report."
	} else if step == len(r.State.Team) {
		role = "Resolve the proposals and critiques into ONE ordered, non-overlapping worker batch. Address every unresolved issue; merge duplicate tasks. A reviewer veto prevents completion this round, even if you disagree."
	}
	decisions, _ := json.Marshal(round.Decisions)
	return r.plannerPrompt(review) + "\nFIGUREOUT COUNCIL\nYou are senior " + member.Name + ". " + role +
		"\nThe user requested autonomous execution. Make reasonable product and engineering choices, record assumptions, and keep working without routine clarification. " +
		"Workers do implementation; coordinate through your JSON decision and the shared discussion. Never launch a nested coding agent. " +
		"Define concrete acceptance criteria proportional to the goal: working user flows, failure handling, meaningful tests, build/start instructions, and no unfinished placeholders. " +
		"Review actual files and executed checks, not merely worker claims. Avoid unrelated features or endless polish beyond the user's scope. " +
		"If the same checks keep failing, diagnose the root cause and change strategy, split the task, or use another implementation approach. " +
		"A missing login or account policy is a real blocker; never invent credentials or success. " +
		"When reviewing, done=true is your explicit approval of this exact workspace and all acceptance criteria. Otherwise supply useful corrective work and checks. " +
		"Output the required JSON only.\nCURRENT ROUND DECISIONS:\n" + string(decisions) + "\nRECENT AGENT DISCUSSION:\n" + discussionContext(r.State, 8)
}
func (r *Runner) askCouncil(ctx context.Context, review bool) (Decision, error) {
	for {
		fingerprint, err := r.councilFingerprint(ctx, review)
		if err != nil {
			return Decision{}, err
		}
		round := r.State.Council
		if round == nil || round.Cycle != r.State.Cycle || round.Review != review || round.Fingerprint != fingerprint {
			if round != nil {
				r.message("harness", "seniors", "recheck", "Evidence changed; previous votes are invalid. Inspect the current workspace.")
			}
			round = &CouncilRound{Cycle: r.State.Cycle, Review: review, Fingerprint: fingerprint}
			r.State.Council = round
			r.State.Dialogue = nil
			if err := r.checkpoint(); err != nil {
				return Decision{}, err
			}
		}
		if round.Complete {
			return round.Final, nil
		}
		step := round.Next
		member, kind := r.State.Team[0], "proposal"
		if step > 0 && step < len(r.State.Team) {
			member, kind = r.State.Team[step], "critique"
		}
		if step == len(r.State.Team) {
			kind = "decision"
		}
		phase := "plan"
		if review {
			phase = "review"
		}
		answer, err := r.coordinatedDecision(ctx, review, member, r.councilPrompt(review, step, round), "council-"+phase+"-"+member.Name+"-"+kind)
		if err != nil {
			return Decision{}, err
		}
		after, err := r.councilFingerprint(ctx, review)
		if err != nil {
			return Decision{}, err
		}
		if after != fingerprint {
			r.State.Council = nil
			r.message("harness", "seniors", "recheck", "Files changed during council review. Votes discarded; verification must run on the new files.")
			if review {
				// Return a non-approval to the ordinary loop, which reruns checks.
				return Decision{Summary: "Reverify files changed during review.", Checks: answer.Checks, SuccessCriteria: answer.SuccessCriteria}, r.checkpoint()
			}
			if err := r.checkpoint(); err != nil {
				return Decision{}, err
			}
			continue
		}
		round.Decisions = append(round.Decisions, answer)
		round.Next++
		r.message(member.Name, "seniors", phase+"-"+kind, decisionMessage(answer), answer)
		if round.Next == len(r.State.Team)+1 {
			round.Complete, round.Consensus = true, review
			for _, vote := range round.Decisions {
				if !vote.Done {
					round.Consensus = false
				}
			}
			if !round.Consensus && answer.Done {
				answer.Done = false
				answer.Summary += " A senior requested more work; completion requires unanimous review."
				for _, vote := range round.Decisions[:len(r.State.Team)] {
					if !vote.Done {
						answer.Tasks = append(answer.Tasks, vote.Tasks...)
					}
				}
				// Renumber to avoid conflicting IDs from different reviewers.
				if len(answer.Tasks) > 30 {
					answer.Tasks = answer.Tasks[:30]
				}
				for i := range answer.Tasks {
					answer.Tasks[i].ID = fmt.Sprintf("repair-%d", i+1)
				}
			}
			// A senior's requested verification cannot silently disappear in synthesis.
			for _, vote := range round.Decisions {
				answer.Checks = Unique(append(answer.Checks, vote.Checks...))
				answer.SuccessCriteria = Unique(append(answer.SuccessCriteria, vote.SuccessCriteria...))
				answer.Acceptance = mergeAcceptance(answer.Acceptance, vote.Acceptance)
			}
			if len(answer.Checks) > 30 {
				return Decision{}, errors.New("council requested more than 30 checks; narrow the goal")
			}
			if err := ValidateDecision(answer); err != nil {
				return Decision{}, err
			}
			round.Final = answer
			if !round.Consensus && review {
				r.message("harness", "workers", "continue", "Senior review requested another implementation or verification round.")
			}
		}
		if err := r.checkpoint(); err != nil {
			return Decision{}, err
		}
	}
}

func decisionMessage(d Decision) string {
	var out strings.Builder
	out.WriteString(d.Summary)
	if d.Done {
		out.WriteString("\nReview vote: ready.")
	}
	for _, task := range d.Tasks {
		fmt.Fprintf(&out, "\n• %s: %s", task.Title, task.Instructions)
	}
	for _, check := range d.Checks {
		fmt.Fprintf(&out, "\nCheck: %s", check)
	}
	return out.String()
}

// This rejects obvious no-op gates, not a proof that arbitrary tests are good.
// Reviewers must still inspect the test coverage and acceptance criteria.
func usefulCheck(command string) bool {
	s := strings.ToLower(strings.TrimSpace(command))
	if s == "" || s == "true" || s == ":" || s == "exit 0" || s == "exit /b 0" {
		return false
	}
	for _, prefix := range []string{"echo ", "printf ", "write-output ", "write-host "} {
		if strings.HasPrefix(s, prefix) && !strings.ContainsAny(s, "|&;") {
			return false
		}
	}
	return true
}
func councilCompletionAllowed(s State) bool {
	if s.Mode != "figureout" {
		return true
	}
	if s.Dialogue != nil || s.WorkerSession != nil {
		return false
	}
	round := s.Council
	if round == nil || !round.Complete || !round.Review || !round.Consensus || round.Cycle != s.Cycle {
		return false
	}
	if len(round.Decisions) != len(s.Team)+1 {
		return false
	}
	for _, vote := range round.Decisions {
		if !vote.Done {
			return false
		}
	}
	if len(Unique(append(append([]string{}, s.SuccessCriteria...), round.Final.SuccessCriteria...))) == 0 {
		return false
	}
	for _, result := range s.CheckResults {
		if result.ExitCode == 0 && !result.SourceChanged && usefulCheck(result.Command) {
			return true
		}
	}
	return false
}
