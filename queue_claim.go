package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// New queued runs have a stable, project-local address even if publication of
// queue.json is interrupted after the initial checkpoint is saved. Existing
// explicit run references remain authoritative, including older random IDs.
func queuedRunID(queueID string) string {
	return fmt.Sprintf("queued-%x", sha256.Sum256([]byte(queueID)))
}

func openQueuedRuns(repo string, create bool) (*os.Root, error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, path := range []string{".forge", filepath.Join(".forge", "runs")} {
		info, err := root.Lstat(path)
		if errors.Is(err, os.ErrNotExist) && create {
			if err = root.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return nil, err
			}
			info, err = root.Lstat(path)
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("queued run storage must use real directories, not symlinks")
		}
	}
	return root.OpenRoot(filepath.Join(".forge", "runs"))
}

func validateQueuedCheckpoint(item QueuedGoal, state State) error {
	if state.QueueID != item.ID || (item.RunID != "" && state.RunID != item.RunID) {
		return errors.New("queue and checkpoint identities do not match; restore consistent saved records")
	}
	if state.Goal != item.Goal || state.Mode != item.Mode {
		return errors.New("queue and checkpoint goal text or mode do not match; restore consistent saved records")
	}
	return nil
}

// Called with the project and queue locks held. Never search unrelated runs,
// replace an existing directory, or treat a damaged/missing checkpoint as a
// reason to create another goal. Reservation without a readable checkpoint is
// ambiguous and requires restoration, rather than guessing that no work ran.
func prepareQueuedRunner(paths Paths, settings Settings, repo string, item QueuedGoal, emit Emit) (*Runner, error) {
	id := item.RunID
	if id == "" {
		id = queuedRunID(item.ID)
	}
	runs, err := openQueuedRuns(repo, item.RunID == "")
	if err != nil {
		return nil, fmt.Errorf("queued goal %s: opening saved runs: %w", item.ID, err)
	}
	defer runs.Close()
	info, err := runs.Lstat(id)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("queued run must be a real directory, not a symlink")
		}
		checkpoint, err := runs.Lstat(filepath.Join(id, "state.json"))
		if err != nil {
			return nil, fmt.Errorf("queued goal %s: run %s has no readable checkpoint; restore its saved state before retrying: %w", item.ID, id, err)
		}
		if !checkpoint.Mode().IsRegular() {
			return nil, errors.New("queued run checkpoint must be a regular file")
		}
		runner, err := ResumeRunner(paths, settings, repo, id, emit)
		if err != nil {
			return nil, fmt.Errorf("queued goal %s: restoring run %s: %w", item.ID, id, err)
		}
		if err := validateQueuedCheckpoint(item, runner.State); err != nil {
			return nil, err
		}
		return runner, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if item.RunID != "" {
		return nil, fmt.Errorf("queued goal %s: saved run %s is missing; restore its checkpoint before retrying: %w", item.ID, id, err)
	}
	runner, err := NewRunner(paths, settings, repo, item.Goal, false, emit)
	if err != nil {
		return nil, err
	}
	if item.Mode == "figureout" {
		if err := runner.EnableFigureOut("full"); err != nil {
			return nil, err
		}
	}
	runner.State.RunID, runner.State.QueueID = id, item.ID
	runner.Dir = filepath.Join(repo, ".forge", "runs", id)
	// An exclusive reservation prevents an existing saved run from being
	// overwritten. Keep it on failure so a retry cannot hide missing history.
	if err := runs.Mkdir(id, 0700); err != nil {
		return nil, fmt.Errorf("reserving queued run %s: %w", id, err)
	}
	if err := runner.State.Save(runner.Dir); err != nil {
		return nil, fmt.Errorf("initializing queued run %s; its directory is preserved for recovery: %w", id, err)
	}
	return runner, nil
}
