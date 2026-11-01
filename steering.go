package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var errSteering = errors.New("new user direction is waiting")

type SteeringMessage struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
	Source    string `json:"source,omitempty"`
	EventID   string `json:"event_id,omitempty"`
}
type SteeringInbox struct {
	Version  int               `json:"version"`
	Messages []SteeringMessage `json:"messages"`
}

type SteeringReceipt struct {
	RunID     string          `json:"run_id"`
	Duplicate bool            `json:"duplicate"`
	Status    string          `json:"status"`
	Message   SteeringMessage `json:"message"`
}

type SteeringView struct {
	RunID    string            `json:"run_id"`
	Messages []SteeringReceipt `json:"messages"`
	Status   string            `json:"status"` // Retain the original JSON status text.
}

var steeringSourcePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var steeringEventPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./:@-]{0,127}$`)

func validSteeringOrigin(source, eventID string) bool {
	return (source == "" && eventID == "") || (steeringSourcePattern.MatchString(source) && steeringEventPattern.MatchString(eventID))
}

func validateSteering(messages []SteeringMessage) error {
	seen, events, size := map[string]bool{}, map[string]bool{}, 0
	for _, m := range messages {
		size += len(m.Text)
		if !validRunID.MatchString(m.ID) || seen[m.ID] || strings.TrimSpace(m.Text) == "" || len(m.Text) > 8000 || strings.ContainsRune(m.Text, 0) || !utf8.ValidString(m.Text) || !validSteeringOrigin(m.Source, m.EventID) {
			return errors.New("invalid saved steering message")
		}
		seen[m.ID] = true
		if m.Source != "" {
			key := m.Source + "\x00" + m.EventID
			if events[key] {
				return errors.New("duplicate saved steering event")
			}
			events[key] = true
		}
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

// Retain applied receipts even if an inbox was restored from an older backup.
// Conflicting copies block; they must not silently replace accepted directions.
func steeringHistory(state State, inbox SteeringInbox) ([]SteeringMessage, error) {
	history := append([]SteeringMessage{}, state.Steering...)
	seen := map[string]SteeringMessage{}
	for _, m := range state.Steering {
		seen[m.ID] = m
	}
	for _, m := range inbox.Messages {
		if previous, exists := seen[m.ID]; exists {
			if previous != m {
				return nil, errors.New("steering inbox conflicts with the applied checkpoint")
			}
			continue
		}
		seen[m.ID] = m
		history = append(history, m)
	}
	return history, validateSteering(history)
}

func steeringReceipt(state State, msg SteeringMessage, duplicate bool) SteeringReceipt {
	status := "waiting"
	for _, applied := range state.Steering {
		if applied.ID == msg.ID {
			status = "applied"
			break
		}
	}
	return SteeringReceipt{RunID: state.RunID, Duplicate: duplicate, Status: status, Message: msg}
}

// The inbox lock is independent of the run lock so a second terminal can steer
// a running team. Completion takes this same lock before its final checkpoint.
func SendSteering(repo, id, text string) (SteeringMessage, error) {
	receipt, err := DeliverSteering(repo, id, text, "", "")
	return receipt.Message, err
}

// DeliverSteering accepts an explicitly authorized direction, not an arbitrary
// untrusted event feed. Source/event identify retries within one saved run.
func DeliverSteering(repo, id, text, source, eventID string) (SteeringReceipt, error) {
	var receipt SteeringReceipt
	text = strings.TrimSpace(text)
	if !validSteeringOrigin(source, eventID) {
		return receipt, errors.New("use both --source (1–64 letters/digits/_.-) and --event (1–128 letters/digits/_./:@-), beginning with a letter or digit")
	}
	if source != "" && (id == "" || id == "latest" || !validRunID.MatchString(id)) {
		return receipt, errors.New("event delivery requires an explicit --run ID; latest is not a stable task address")
	}
	if text == "" || len(text) > 8000 || strings.ContainsRune(text, 0) || !utf8.ValidString(text) {
		return receipt, errors.New("a direction must contain 1 to 8000 UTF-8 bytes without NUL")
	}
	lock, err := acquireProjectLock(repo, "steering-lock", true)
	if err != nil {
		return receipt, err
	}
	defer lock.Close()
	state, dir, err := LoadRun(repo, id)
	if err != nil {
		return receipt, err
	}
	inbox, err := readSteering(dir)
	if err != nil {
		return receipt, err
	}
	history, err := steeringHistory(state, inbox)
	if err != nil {
		return receipt, err
	}
	for _, msg := range history {
		if source != "" && msg.Source == source && msg.EventID == eventID {
			if msg.Text != text {
				return receipt, errors.New("this source/event already delivered different text; use a new event ID for a new direction")
			}
			return steeringReceipt(state, msg, true), nil
		}
	}
	if state.Status == "complete" {
		return receipt, errors.New("this goal is complete; start a new goal")
	}
	var msg SteeringMessage
	msg.ID, err = newID("steer-")
	if err != nil {
		return receipt, err
	}
	msg.Text, msg.CreatedAt, msg.Source, msg.EventID = text, time.Now().UTC().Format(time.RFC3339Nano), source, eventID
	inbox.Messages = append(inbox.Messages, msg)
	if err = validateSteering(append(history, msg)); err != nil {
		return receipt, err
	}
	return steeringReceipt(state, msg, false), AtomicJSON(filepath.Join(dir, "steering.json"), inbox)
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
	if _, err := steeringHistory(r.State, inbox); err != nil {
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
	combined, err := steeringHistory(r.State, inbox)
	if err != nil {
		return err
	}
	pending := pendingSteering(r.State, inbox)
	if len(pending) == 0 {
		return nil
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
		origin := ""
		if msg.Source != "" {
			origin = "[source=" + msg.Source + " event=" + msg.EventID + "] "
		}
		fmt.Fprintf(&out, "%d. %s%s\n", i+1, origin, msg.Text)
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
	view, err := InspectSteering(repo, id)
	return view.Status, err
}

func InspectSteering(repo, id string) (SteeringView, error) {
	view := SteeringView{Messages: []SteeringReceipt{}}
	lock, err := acquireProjectLock(repo, "steering-lock", true)
	if err != nil {
		return view, err
	}
	defer lock.Close()
	state, dir, err := LoadRun(repo, id)
	if err != nil {
		return view, err
	}
	inbox, err := readSteering(dir)
	if err != nil {
		return view, err
	}
	history, err := steeringHistory(state, inbox)
	if err != nil {
		return view, err
	}
	view.RunID = state.RunID
	var out strings.Builder
	fmt.Fprintf(&out, "%s · %d user updates\n", state.RunID, len(history))
	for _, m := range history {
		receipt := steeringReceipt(state, m, false)
		view.Messages = append(view.Messages, receipt)
		status := "waiting for the next agent boundary (or /resume)"
		if receipt.Status == "applied" {
			status = "applied"
		}
		fmt.Fprintf(&out, "[%s] %s\n", status, m.ID)
		if m.Source != "" {
			fmt.Fprintf(&out, "source: %s · event: %s\n", m.Source, m.EventID)
		}
		fmt.Fprintf(&out, "%s\n", m.Text)
	}
	view.Status = strings.TrimSpace(out.String())
	return view, nil
}
