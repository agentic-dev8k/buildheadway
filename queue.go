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

type QueuedGoal struct {
	Source    string `json:"source,omitempty"`
	EventID   string `json:"event_id,omitempty"`
	Mode      string `json:"mode,omitempty"`
	ID        string `json:"id"`
	Goal      string `json:"goal"`
	Status    string `json:"status"`
	RunID     string `json:"run_id,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Error     string `json:"error,omitempty"`
}
type GoalQueue struct {
	Version int          `json:"version"`
	Items   []QueuedGoal `json:"items"`
}

var errQueueEmpty = errors.New("no waiting goals; add one with /queue add YOUR GOAL")

func ReadQueue(repo string) (GoalQueue, error) {
	queue := GoalQueue{Version: 1, Items: []QueuedGoal{}}
	err := ReadJSON(filepath.Join(repo, ".forge", "queue.json"), &queue)
	if errors.Is(err, os.ErrNotExist) {
		return queue, nil
	}
	if err != nil {
		return queue, err
	}
	if queue.Version != 1 || len(queue.Items) > 1000 {
		return queue, errors.New("invalid goal queue")
	}
	ids, deliveries := map[string]bool{}, map[string]bool{}
	for _, item := range queue.Items {
		if item.Mode != "" && item.Mode != "figureout" {
			return queue, errors.New("invalid queued goal mode")
		}
		if !validRunID.MatchString(item.ID) || ids[item.ID] || !validQueueGoal(item.Goal) || !validSteeringOrigin(item.Source, item.EventID) {
			return queue, errors.New("invalid queued goal")
		}
		if item.Source != "" {
			key := item.Source + "\x00" + item.EventID
			if deliveries[key] {
				return queue, errors.New("duplicate saved queue source/event")
			}
			if item.CreatedAt == "" || len(item.CreatedAt) > 128 {
				return queue, errors.New("invalid queued delivery creation time")
			}
			deliveries[key] = true
		}
		if item.RunID != "" && !validRunID.MatchString(item.RunID) {
			return queue, errors.New("invalid queued run ID")
		}
		switch item.Status {
		case "pending", "running", "blocked", "complete", "canceled":
		default:
			return queue, errors.New("invalid queue status")
		}
		if (item.Status == "running" || item.Status == "blocked") && item.RunID == "" {
			return queue, errors.New("queued run is missing its checkpoint ID")
		}
		ids[item.ID] = true
	}
	return queue, nil
}
func saveQueue(repo string, queue GoalQueue) error {
	return AtomicJSON(filepath.Join(repo, ".forge", "queue.json"), queue)
}
func QueueAdd(repo, goal string) (QueuedGoal, error) {
	return QueueAddMode(repo, goal, "")
}
func QueueAddMode(repo, goal, mode string) (QueuedGoal, error) {
	receipt, err := DeliverQueuedGoal(repo, goal, mode, "", "")
	return receipt.Item, err
}
func QueueRemove(repo, id string) error {
	lock, err := acquireProjectLock(repo, "queue.lock", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	queue, err := ReadQueue(repo)
	if err != nil {
		return err
	}
	for i := range queue.Items {
		item := &queue.Items[i]
		if item.ID != id {
			continue
		}
		if item.Status == "complete" || item.Status == "canceled" {
			return errors.New("this queue item is already finished")
		}
		if item.Status == "running" {
			// A nonblocking repository lock distinguishes an abandoned claim from
			// an active consumer. Never wait here: consumers acquire locks in the
			// opposite order while saving their result.
			repoLock, err := acquireProjectLock(repo, "lock", false)
			if err != nil {
				return errors.New("pause the active goal before removing it from the queue")
			}
			defer repoLock.Close()
		}
		if err := ensureQueueDelivery(repo, *item); err != nil {
			return err
		}
		item.Status, item.UpdatedAt = "canceled", time.Now().UTC().Format(time.RFC3339)
		return saveQueue(repo, queue)
	}
	return errors.New("queue item not found")
}
func QueueMove(repo, id string, position int) error {
	lock, err := acquireProjectLock(repo, "queue.lock", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	queue, err := ReadQueue(repo)
	if err != nil {
		return err
	}
	if position < 1 || position > len(queue.Items) {
		return errors.New("queue position is outside the list")
	}
	index := -1
	for i, item := range queue.Items {
		if item.ID == id {
			index = i
			if item.Status != "pending" && item.Status != "blocked" {
				return errors.New("only pending or blocked goals can be moved")
			}
			break
		}
	}
	if index < 0 {
		return errors.New("queue item not found")
	}
	item := queue.Items[index]
	if err := ensureQueueDelivery(repo, item); err != nil {
		return err
	}
	queue.Items = append(queue.Items[:index], queue.Items[index+1:]...)
	queue.Items = append(queue.Items, QueuedGoal{})
	copy(queue.Items[position:], queue.Items[position-1:])
	queue.Items[position-1] = item
	return saveQueue(repo, queue)
}
func FormatQueue(queue GoalQueue) string {
	if len(queue.Items) == 0 {
		return "No queued goals. Use /queue add YOUR GOAL."
	}
	var out strings.Builder
	for i, item := range queue.Items {
		fmt.Fprintf(&out, "%d. [%s] %s\n  %s\n", i+1, item.Status, item.ID, item.Goal)
		if item.Mode != "" {
			fmt.Fprintf(&out, "  mode: %s\n", item.Mode)
		}
		if item.Source != "" {
			fmt.Fprintf(&out, "  source: %s · event: %s\n", item.Source, item.EventID)
		}
		if item.RunID != "" {
			fmt.Fprintf(&out, "  run: %s\n", item.RunID)
		}
		if item.Error != "" {
			fmt.Fprintf(&out, "  %s\n", Tail(item.Error, 350))
		}
	}
	return strings.TrimSpace(out.String())
}

// Called with the repository lock held, so claims and worker dispatch cannot
// overlap a second BuildHeadway controller. Stable run addresses recover the
// checkpoint even if the queue's run reference was not published before a crash.
func claimQueuedRunner(paths Paths, settings Settings, repo string, emit Emit) (*Runner, error) {
	lock, err := acquireProjectLock(repo, "queue.lock", true)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	queue, err := ReadQueue(repo)
	if err != nil {
		return nil, err
	}
	for i := range queue.Items {
		item := &queue.Items[i]
		if item.Status == "complete" || item.Status == "canceled" {
			continue
		}
		if err := ensureQueueDelivery(repo, *item); err != nil {
			return nil, err
		}
		runner, err := prepareQueuedRunner(paths, settings, repo, *item, emit)
		if err != nil {
			return nil, err
		}
		item.RunID = runner.State.RunID
		if runner.State.Status == "complete" {
			item.Status, item.Error = "complete", ""
			item.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if err := saveQueue(repo, queue); err != nil {
				return nil, err
			}
			continue
		}
		item.Status, item.Error = "running", ""
		item.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := saveQueue(repo, queue); err != nil {
			return nil, err
		}
		runner.lockHeld = true
		return runner, nil
	}
	return nil, errQueueEmpty
}
func (r *Runner) syncQueue() error {
	if r.State.QueueID == "" {
		return nil
	}
	lock, err := acquireProjectLock(r.Repo, "queue.lock", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	queue, err := ReadQueue(r.Repo)
	if err != nil {
		return err
	}
	for i := range queue.Items {
		item := &queue.Items[i]
		if item.ID != r.State.QueueID {
			continue
		}
		if item.Status == "canceled" {
			return nil
		}
		if item.RunID != r.State.RunID {
			return errors.New("queued goal points to another run")
		}
		if err := validateQueuedCheckpoint(*item, r.State); err != nil {
			return err
		}
		if err := ensureQueueDelivery(r.Repo, *item); err != nil {
			return err
		}
		item.Status, item.Error = "blocked", r.State.Error
		if r.State.Status == "complete" {
			item.Status = "complete"
		}
		if r.State.Status == "running" {
			item.Status = "running"
		}
		item.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return saveQueue(r.Repo, queue)
	}
	return errors.New("queued goal is missing; the run checkpoint is preserved")
}
func RunQueued(ctx context.Context, paths Paths, settings Settings, repo string, all bool, emit Emit) error {
	return runQueuedWith(ctx, paths, settings, repo, all, emit, nil)
}
func runQueuedWith(ctx context.Context, paths Paths, settings Settings, repo string, all bool, emit Emit, configure func(*Runner)) error {
	repo, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	if info, err := os.Stat(repo); err != nil || !info.IsDir() {
		return errors.New("choose an existing project folder")
	}
	lock, err := acquireProjectLock(repo, "lock", false)
	if err != nil {
		return errors.New("BuildHeadway is already working in this folder; pause that session first")
	}
	defer lock.Close()
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		runner, err := claimQueuedRunner(paths, settings, repo, emit)
		if errors.Is(err, errQueueEmpty) && count > 0 {
			return nil
		}
		if err != nil {
			return err
		}
		if configure != nil {
			configure(runner)
		}
		if emit != nil {
			emit(Event{Kind: "assistant", Text: "Queued goal: " + runner.State.Goal})
		}
		if err := runner.Run(ctx); err != nil {
			return err
		}
		count++
		if !all {
			return nil
		}
	}
}
func QueueReady(repo string) bool {
	queue, err := ReadQueue(repo)
	if err != nil {
		return false
	}
	for _, item := range queue.Items {
		if item.Status == "complete" || item.Status == "canceled" {
			continue
		}
		return item.Status == "pending"
	}
	return false
}
