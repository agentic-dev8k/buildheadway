package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const queueArchiveLimit = 1 << 20
const queueArchiveDirectory = ".forge/queue-archive"

type queueArchivedGoal struct {
	Version int        `json:"version"`
	Item    QueuedGoal `json:"item"`
}

type QueuePruneReport struct {
	Pruned      int      `json:"pruned"`
	Remaining   int      `json:"remaining"`
	ArchivedIDs []string `json:"archived_ids"`
	Directory   string   `json:"directory,omitempty"`
}

func finishedQueuedGoal(item QueuedGoal) bool {
	return item.Status == "complete" || item.Status == "canceled"
}

// Persist every finished record before publishing the smaller queue. A retry
// reuses byte-identical per-goal archives after an interrupted prune.
func PruneQueue(repo string) (QueuePruneReport, error) {
	report := QueuePruneReport{ArchivedIDs: []string{}}
	lock, err := acquireProjectLock(repo, "queue.lock", true)
	if err != nil {
		return report, err
	}
	defer lock.Close()
	queue, err := ReadQueue(repo)
	if err != nil {
		return report, err
	}
	kept := make([]QueuedGoal, 0, len(queue.Items))
	for _, item := range queue.Items {
		if !finishedQueuedGoal(item) {
			kept = append(kept, item)
		}
	}
	report.Remaining = len(kept)
	if len(kept) == len(queue.Items) {
		return report, nil
	}
	dir := filepath.Join(repo, filepath.FromSlash(queueArchiveDirectory))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return report, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return report, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return report, errors.New("queue archive must be a real directory, not a symlink")
	}
	for _, item := range queue.Items {
		if !finishedQueuedGoal(item) {
			continue
		}
		data, err := json.MarshalIndent(queueArchivedGoal{Version: 1, Item: item}, "", "  ")
		if err != nil {
			return report, err
		}
		data = append(data, '\n')
		if len(data) > queueArchiveLimit {
			return report, errors.New("finished queue record exceeds the 1 MiB archive limit; queue is unchanged")
		}
		path := filepath.Join(dir, item.ID+".json")
		info, err := os.Lstat(path)
		if err == nil {
			if !info.Mode().IsRegular() {
				return report, errors.New("queue archive must be a regular file")
			}
			file, err := os.Open(path)
			if err != nil {
				return report, err
			}
			existing, readErr := ReadLimited(file, queueArchiveLimit)
			closeErr := file.Close()
			if readErr != nil {
				return report, readErr
			}
			if closeErr != nil {
				return report, closeErr
			}
			if !bytes.Equal(existing, data) {
				return report, errors.New("queue archive conflicts with the finished record; queue is unchanged")
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := AtomicJSON(path, queueArchivedGoal{Version: 1, Item: item}); err != nil {
				return report, err
			}
		} else {
			return report, err
		}
		report.ArchivedIDs = append(report.ArchivedIDs, item.ID)
	}
	queue.Items = kept
	if err := saveQueue(repo, queue); err != nil {
		return report, err
	}
	report.Pruned = len(report.ArchivedIDs)
	report.Directory = queueArchiveDirectory
	return report, nil
}

func FormatQueuePrune(report QueuePruneReport) string {
	if report.Pruned == 0 {
		return fmt.Sprintf("No finished queue entries to archive; %d remain.", report.Remaining)
	}
	return fmt.Sprintf("Archived and pruned %d finished queue entries; %d remain.\nRecords: %s\nBrowse with buildheadway queue archives; inspect one with buildheadway queue archive QUEUE_ID.", report.Pruned, report.Remaining, report.Directory)
}

func ReadQueueArchive(repo, id string) (QueuedGoal, error) {
	var item QueuedGoal
	if !validRunID.MatchString(id) {
		return item, errors.New("invalid archived queue ID")
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return item, err
	}
	defer root.Close()
	path := filepath.Join(filepath.FromSlash(queueArchiveDirectory), id+".json")
	info, err := root.Lstat(path)
	if err != nil {
		return item, err
	}
	if !info.Mode().IsRegular() {
		return item, errors.New("queue archive must be a regular file")
	}
	file, err := root.Open(path)
	if err != nil {
		return item, err
	}
	defer file.Close()
	data, err := ReadLimited(file, queueArchiveLimit)
	if err != nil {
		return item, err
	}
	var archive queueArchivedGoal
	if err := json.Unmarshal(data, &archive); err != nil {
		return item, err
	}
	item = archive.Item
	if archive.Version != 1 || item.ID != id || !finishedQueuedGoal(item) {
		return QueuedGoal{}, errors.New("archive identity or finished status is invalid")
	}
	if strings.TrimSpace(item.Goal) == "" || len(item.Goal) > 32000 || (item.Mode != "" && item.Mode != "figureout") || (item.RunID != "" && !validRunID.MatchString(item.RunID)) || len(item.CreatedAt) > 128 || len(item.UpdatedAt) > 128 {
		return QueuedGoal{}, errors.New("archived goal metadata is invalid")
	}
	return item, nil
}
