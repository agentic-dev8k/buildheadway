package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const queueDeliveryDirectory = ".forge/queue-events"
const queueDeliveryLimit = 4096

type QueueReceipt struct {
	Duplicate bool       `json:"duplicate"`
	Archived  bool       `json:"archived"`
	Item      QueuedGoal `json:"item"`
}

// Immutable lookup metadata, not a second queue or a cached execution status.
// The complete current record remains in queue.json or its per-goal archive.
type queueDelivery struct {
	Version   int    `json:"version"`
	QueueID   string `json:"queue_id"`
	Source    string `json:"source"`
	EventID   string `json:"event_id"`
	Mode      string `json:"mode,omitempty"`
	GoalHash  string `json:"goal_sha256"`
	CreatedAt string `json:"created_at"`
}

func validQueueGoal(goal string) bool {
	return strings.TrimSpace(goal) != "" && len(goal) <= 32000 && utf8.ValidString(goal) && !strings.ContainsRune(goal, 0)
}

func queueDeliveryName(source, eventID string) string {
	key := sha256.Sum256([]byte(source + "\x00" + eventID))
	return fmt.Sprintf("%x.json", key)
}

func queuedDelivery(item QueuedGoal) queueDelivery {
	digest := sha256.Sum256([]byte(item.Mode + "\x00" + item.Goal))
	return queueDelivery{1, item.ID, item.Source, item.EventID, item.Mode, fmt.Sprintf("%x", digest), item.CreatedAt}
}

// Root-relative handles and real-directory/regular-file checks keep metadata
// lookups bounded and refuse symlinked delivery stores, including inside a repo.
func openQueueDeliveries(repo string, create bool) (*os.Root, error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, path := range []string{".forge", filepath.FromSlash(queueDeliveryDirectory)} {
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
			return nil, errors.New("queue delivery metadata must use real directories, not symlinks")
		}
	}
	return root.OpenRoot(filepath.FromSlash(queueDeliveryDirectory))
}

func readQueueDelivery(repo, source, eventID string) (queueDelivery, error) {
	var record queueDelivery
	root, err := openQueueDeliveries(repo, false)
	if err != nil {
		return record, err
	}
	defer root.Close()
	name := queueDeliveryName(source, eventID)
	info, err := root.Lstat(name)
	if err != nil {
		return record, err
	}
	if !info.Mode().IsRegular() {
		return record, errors.New("queue delivery metadata must be a regular file")
	}
	f, err := root.Open(name)
	if err != nil {
		return record, err
	}
	defer f.Close()
	data, err := ReadLimited(f, queueDeliveryLimit)
	if err != nil {
		return record, fmt.Errorf("reading queue delivery metadata: %w", err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, fmt.Errorf("invalid queue delivery metadata: %w", err)
	}
	digest, hashErr := hex.DecodeString(record.GoalHash)
	if record.Version != 1 || !validRunID.MatchString(record.QueueID) || record.Source != source || record.EventID != eventID || !validSteeringOrigin(source, eventID) || source == "" || hashErr != nil || len(digest) != sha256.Size || len(record.CreatedAt) == 0 || len(record.CreatedAt) > 128 || (record.Mode != "" && record.Mode != "figureout") {
		return queueDelivery{}, errors.New("invalid queue delivery identity or metadata")
	}
	return record, nil
}

func writeQueueDelivery(repo string, record queueDelivery) error {
	root, err := openQueueDeliveries(repo, true)
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > queueDeliveryLimit {
		return errors.New("queue delivery metadata exceeds 4096 bytes")
	}
	id, err := newID("delivery-")
	if err != nil {
		return err
	}
	temp := "." + id + ".tmp"
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(temp, queueDeliveryName(record.Source, record.EventID))
}

func checkQueueArchiveCopy(repo string, item QueuedGoal) error {
	archive, err := ReadQueueArchive(repo, item.ID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if archive != item {
		return errors.New("queue item conflicts with its finished archive; restore consistent saved records before continuing")
	}
	return nil
}

// All callers hold queue.lock. Publish the queue first on admission; repair a
// missing lookup record before execution, cancellation, movement or pruning.
// A process interruption can leave one queue entry without its lookup file,
// but can never start work and then forget its accepted event key.
func ensureQueueDelivery(repo string, item QueuedGoal) error {
	if item.Source == "" {
		return nil
	}
	if err := checkQueueArchiveCopy(repo, item); err != nil {
		return err
	}
	want := queuedDelivery(item)
	got, err := readQueueDelivery(repo, item.Source, item.EventID)
	if err == nil {
		if got != want {
			return errors.New("queue delivery conflicts with the saved goal; records were left unchanged")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeQueueDelivery(repo, want)
}

// DeliverQueuedGoal accepts a goal the caller is authorized to submit. Labels
// identify retries; they do not authenticate an event or authorize its content.
func DeliverQueuedGoal(repo, goal, mode, source, eventID string) (QueueReceipt, error) {
	var receipt QueueReceipt
	goal = strings.TrimSpace(goal)
	if !validQueueGoal(goal) {
		return receipt, errors.New("a queued goal must contain 1 to 32000 UTF-8 bytes without NUL")
	}
	if mode != "" && mode != "figureout" {
		return receipt, errors.New("invalid goal mode")
	}
	if !validSteeringOrigin(source, eventID) {
		return receipt, errors.New("queue delivery requires both a valid source and event ID")
	}
	if source != "" {
		// Refuse bad paths before acquiring a lock beneath the metadata folder.
		dir, err := openQueueDeliveries(repo, true)
		if err != nil {
			return receipt, err
		}
		dir.Close()
	}
	lock, err := acquireProjectLock(repo, "queue.lock", true)
	if err != nil {
		return receipt, err
	}
	defer lock.Close()
	queue, err := ReadQueue(repo)
	if err != nil {
		return receipt, err
	}
	if source != "" {
		record, readErr := readQueueDelivery(repo, source, eventID)
		if readErr == nil {
			want := queuedDelivery(QueuedGoal{Goal: goal, Mode: mode})
			if record.GoalHash != want.GoalHash || record.Mode != mode {
				return receipt, errors.New("this queue source/event already delivered a different goal or mode; use a new event ID for a new request")
			}
			for _, item := range queue.Items {
				if item.ID != record.QueueID {
					continue
				}
				if queuedDelivery(item) != record {
					return receipt, errors.New("queue delivery conflicts with the saved goal")
				}
				if err := checkQueueArchiveCopy(repo, item); err != nil {
					return receipt, err
				}
				return QueueReceipt{Duplicate: true, Item: item}, nil
			}
			item, err := ReadQueueArchive(repo, record.QueueID)
			if errors.Is(err, os.ErrNotExist) {
				return receipt, errors.New("accepted queue delivery is missing its goal record; restore the queue or archive before retrying")
			}
			if err != nil {
				return receipt, err
			}
			if queuedDelivery(item) != record {
				return receipt, errors.New("queue delivery conflicts with the archived goal")
			}
			return QueueReceipt{Duplicate: true, Archived: true, Item: item}, nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return receipt, readErr
		}
		for _, item := range queue.Items {
			if item.Source != source || item.EventID != eventID {
				continue
			}
			if item.Goal != goal || item.Mode != mode {
				return receipt, errors.New("this queue source/event already delivered a different goal or mode")
			}
			if err := ensureQueueDelivery(repo, item); err != nil {
				return receipt, err
			}
			return QueueReceipt{Duplicate: true, Item: item}, nil
		}
	}
	if len(queue.Items) >= 1000 {
		return receipt, errors.New("queue history is full (1000 goals)")
	}
	id, err := newID("q-")
	if err != nil {
		return receipt, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	item := QueuedGoal{ID: id, Goal: goal, Mode: mode, Source: source, EventID: eventID, Status: "pending", CreatedAt: now, UpdatedAt: now}
	queue.Items = append(queue.Items, item)
	if err := saveQueue(repo, queue); err != nil {
		return receipt, err
	}
	if err := ensureQueueDelivery(repo, item); err != nil {
		return receipt, fmt.Errorf("goal %s is saved; retry this same event to repair delivery metadata: %w", item.ID, err)
	}
	return QueueReceipt{Item: item}, nil
}

func FormatQueueReceipt(receipt QueueReceipt) string {
	if receipt.Duplicate {
		location := "queue"
		if receipt.Archived {
			location = "archive"
		}
		return fmt.Sprintf("Already accepted %s [%s; %s]\n%s", receipt.Item.ID, receipt.Item.Status, location, receipt.Item.Goal)
	}
	return "Queued " + receipt.Item.ID + "\n" + receipt.Item.Goal
}
