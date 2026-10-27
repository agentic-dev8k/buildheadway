package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type PauseRequest struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	CreatedAt string `json:"created_at"`
}

type RunControl struct {
	Version      int           `json:"version"`
	Pause        *PauseRequest `json:"pause,omitempty"`
	Acknowledged string        `json:"acknowledged,omitempty"`
}

type runPauseError struct{ request PauseRequest }

func (e runPauseError) Error() string {
	return "pause requested from another terminal (" + e.request.ID + "); progress is saved, use buildheadway resume to continue"
}
func (e runPauseError) Unwrap() error { return context.Canceled }

func readRunControl(dir, runID string) (RunControl, error) {
	control := RunControl{Version: 1}
	path := filepath.Join(dir, "control.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return control, nil
	}
	if err != nil {
		return control, err
	}
	if !info.Mode().IsRegular() {
		return control, errors.New("run control must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return control, err
	}
	defer file.Close()
	data, err := ReadLimited(file, 4096)
	if err != nil {
		return control, fmt.Errorf("reading run control: %w", err)
	}
	control = RunControl{}
	if err := json.Unmarshal(data, &control); err != nil {
		return control, fmt.Errorf("invalid run control: %w", err)
	}
	if control.Version != 1 {
		return control, errors.New("unsupported run control format")
	}
	if control.Pause == nil {
		if control.Acknowledged != "" {
			return control, errors.New("run control acknowledges a missing pause")
		}
		return control, nil
	}
	request := control.Pause
	if !validRunID.MatchString(request.ID) || request.RunID != runID || !validRunID.MatchString(request.RunID) {
		return control, errors.New("invalid pause request identity")
	}
	if _, err := time.Parse(time.RFC3339Nano, request.CreatedAt); err != nil {
		return control, errors.New("invalid pause request time")
	}
	if control.Acknowledged != "" && control.Acknowledged != request.ID {
		return control, errors.New("run control acknowledges a different pause")
	}
	return control, nil
}

func pendingPause(control RunControl) error {
	if control.Pause != nil && control.Pause.ID != control.Acknowledged {
		return runPauseError{*control.Pause}
	}
	return nil
}

// Completion shares this lock, so a request either stops completion or observes
// the already-complete checkpoint. It never waits for the active run lock.
func RequestPause(repo, id string) (PauseRequest, error) {
	var request PauseRequest
	lock, err := acquireProjectLock(repo, "steering-lock", true)
	if err != nil {
		return request, err
	}
	defer lock.Close()
	state, dir, err := LoadRun(repo, id)
	if err != nil {
		return request, err
	}
	if state.Status == "complete" {
		return request, errors.New("this goal is complete; no active work to pause")
	}
	control, err := readRunControl(dir, state.RunID)
	if err != nil {
		return request, err
	}
	if pendingPause(control) != nil {
		return *control.Pause, nil
	}
	request.ID, err = newID("pause-")
	if err != nil {
		return request, err
	}
	request.RunID, request.CreatedAt = state.RunID, time.Now().UTC().Format(time.RFC3339Nano)
	control.Pause, control.Acknowledged = &request, ""
	return request, AtomicJSON(filepath.Join(dir, "control.json"), control)
}

func (r *Runner) pausePending() error {
	control, err := readRunControl(r.Dir, r.State.RunID)
	if err != nil {
		return err
	}
	return pendingPause(control)
}

// Explicit Run/resume acknowledges an older request before starting work. Only
// immutable directory/identity values are captured by the watcher; it never
// reads or changes Runner.State from a background goroutine.
func (r *Runner) pauseContext(parent context.Context) (context.Context, func(), error) {
	lock, err := acquireProjectLock(r.Repo, "steering-lock", true)
	if err != nil {
		return nil, nil, err
	}
	control, err := readRunControl(r.Dir, r.State.RunID)
	if err == nil && control.Pause != nil && control.Acknowledged != control.Pause.ID {
		control.Acknowledged = control.Pause.ID
		err = AtomicJSON(filepath.Join(r.Dir, "control.json"), control)
	}
	lock.Close()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	dir, runID := r.Dir, r.State.RunID
	go func() {
		defer close(done)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				control, err := readRunControl(dir, runID)
				if err == nil {
					err = pendingPause(control)
				}
				if err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(nil); <-done }, nil
}
