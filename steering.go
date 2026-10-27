package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var errSteering = errors.New("new user direction is waiting")

type SteeringMessage struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}
type SteeringInbox struct {
	Version  int               `json:"version"`
	Messages []SteeringMessage `json:"messages"`
}

func validateSteering(messages []SteeringMessage) error {
	seen, size := map[string]bool{}, 0
	for _, m := range messages {
		size += len(m.Text)
		if !validRunID.MatchString(m.ID) || seen[m.ID] || strings.TrimSpace(m.Text) == "" || len(m.Text) > 8000 || strings.ContainsRune(m.Text, 0) {
			return errors.New("invalid saved steering message")
		}
		seen[m.ID] = true
	}
	if len(messages) > 64 || size > 128<<10 {
		return errors.New("steering history is full; start a new goal")
	}
	return nil
}
func readSteering(dir string) (SteeringInbox, error) {
	inbox := SteeringInbox{Version: 1, Messages: []SteeringMessage{}}
	err := ReadJSON(filepath.Join(dir, "steering.json"), &inbox)
	if errors.Is(err, os.ErrNotExist) {
		return inbox, nil
	}
	if err != nil {
		return inbox, err
	}
	if inbox.Version != 1 {
		return inbox, errors.New("unsupported steering inbox")
	}
	return inbox, validateSteering(inbox.Messages)
}

// The inbox lock is independent of the run lock so a second terminal can steer
// a running team. Completion takes this same lock before its final checkpoint.
func SendSteering(repo, id, text string) (SteeringMessage, error) {
	var msg SteeringMessage
	lock, err := acquireProjectLock(repo, "steering-lock", true)
	if err != nil {
		return msg, err
	}
	defer lock.Close()
	state, dir, err := LoadRun(repo, id)
	if err != nil {
		return msg, err
	}
	if state.Status == "complete" {
		return msg, errors.New("this goal is complete; start a new goal")
	}
	inbox, err := readSteering(dir)
	if err != nil {
		return msg, err
	}
	msg.ID, err = newID("steer-")
	if err != nil {
		return msg, err
	}
	msg.Text, msg.CreatedAt = strings.TrimSpace(text), time.Now().UTC().Format(time.RFC3339Nano)
	inbox.Messages = append(inbox.Messages, msg)
	if err = validateSteering(inbox.Messages); err != nil {
		return msg, err
	}
	return msg, AtomicJSON(filepath.Join(dir, "steering.json"), inbox)
}
func pendingSteering(state State, inbox SteeringInbox) []SteeringMessage {
	applied := map[string]bool{}
	for _, m := range state.Steering {
		applied[m.ID] = true
	}
	var pending []SteeringMessage
	for _, m := range inbox.Messages {
		if !applied[m.ID] {
			pending = append(pending, m)
		}
	}
	return pending
}
func (r *Runner) steeringPending() error {
	inbox, err := readSteering(r.Dir)
	if err != nil {
		return err
	}
	if len(pendingSteering(r.State, inbox)) > 0 {
		return errSteering
	}
	return nil
}
func (r *Runner) applySteering() error {
	lock, err := acquireProjectLock(r.Repo, "steering-lock", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	inbox, err := readSteering(r.Dir)
	if err != nil {
		return err
	}
	pending := pendingSteering(r.State, inbox)
	if len(pending) == 0 {
		return nil
	}
	combined := append(append([]SteeringMessage{}, r.State.Steering...), pending...)
	if err := validateSteering(combined); err != nil {
		return err
	}
	r.State.Steering = combined
	for i := range r.State.TaskHistory {
		record := &r.State.TaskHistory[i]
		if record.Status == "pending" || record.Key == r.State.ActiveTask {
			record.Status = "superseded"
			record.Error = "User redirected the goal; inspect any partial work before continuing."
		}
	}
	r.State.ActiveTask = ""
	r.State.Tasks, r.State.NextTask = nil, 0
	r.State.Council, r.State.Dialogue, r.State.WorkerSession = nil, nil, nil
	r.State.CheckedSnapshot, r.State.VerificationSignature = "", ""
	r.State.StalledCycles, r.State.HookRechecks = 0, 0
	r.State.RecoveryNote = "The user updated the goal. Prior plans and approvals are invalid. Inspect partial work and plan against all user updates; later updates take precedence if they conflict. Existing required verification remains in force."
	r.State.Phase = "planning"
	if r.State.WorkerCalls > 0 {
		r.State.Phase = "checking"
	}
	r.State.Cycle++
	for _, msg := range pending {
		r.note("steering-applied", msg.Text)
		r.message("user", "seniors", "direction", msg.Text)
	}
	if err := r.checkpoint(); err != nil {
		return err
	}
	r.Emit(Event{Kind: "assistant", Text: "Applied your direction. The team is reassessing the goal before more implementation."})
	return nil
}
func (s State) goalContext() string {
	if len(s.Steering) == 0 {
		return s.Goal
	}
	var out strings.Builder
	out.WriteString(s.Goal + "\n\nUSER UPDATES (later updates take precedence):\n")
	for i, msg := range s.Steering {
		fmt.Fprintf(&out, "%d. %s\n", i+1, msg.Text)
	}
	return out.String()
}
func (r *Runner) invokeAgent(ctx context.Context, connection Connection, prompt, label string, planner bool, model string) (string, error) {
	if err := r.pausePending(); err != nil {
		return "", err
	}
	if err := r.steeringPending(); err != nil {
		return "", err
	}
	output, err := r.Invoke(ctx, connection, prompt, label, planner, model)
	// Preserve failures such as quota, cancellation or nonzero free-model cost.
	if err != nil {
		return output, err
	}
	if err := r.steeringPending(); err != nil {
		return output, err
	}
	return output, nil
}
func (r *Runner) finishRun(status, summary string) error {
	lock, err := acquireProjectLock(r.Repo, "steering-lock", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := r.pausePending(); err != nil {
		return err
	}
	if err := r.steeringPending(); err != nil {
		return err
	}
	r.State.Status, r.State.Summary = status, summary
	if status == "complete" {
		for i := range r.State.TaskHistory {
			if r.State.TaskHistory[i].Status == "pending" {
				r.State.TaskHistory[i].Status = "superseded"
			}
		}
	}
	r.note(status, summary)
	return r.checkpoint()
}
func SteeringStatus(repo, id string) (string, error) {
	state, dir, err := LoadRun(repo, id)
	if err != nil {
		return "", err
	}
	inbox, err := readSteering(dir)
	if err != nil {
		return "", err
	}
	applied := map[string]bool{}
	for _, m := range state.Steering {
		applied[m.ID] = true
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s · %d user updates\n", state.RunID, len(inbox.Messages))
	for _, m := range inbox.Messages {
		status := "waiting for the next agent boundary (or /resume)"
		if applied[m.ID] {
			status = "applied"
		}
		fmt.Fprintf(&out, "[%s] %s\n%s\n", status, m.ID, m.Text)
	}
	return strings.TrimSpace(out.String()), nil
}
