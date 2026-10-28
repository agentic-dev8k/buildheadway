package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type QueueArchiveSummary struct {
	ID            string `json:"id"`
	Goal          string `json:"goal"`
	GoalTruncated bool   `json:"goal_truncated,omitempty"`
	Status        string `json:"status"`
	Mode          string `json:"mode,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type QueueArchivePage struct {
	Directory string                `json:"directory"`
	Items     []QueueArchiveSummary `json:"items"`
	More      bool                  `json:"more"`
	NextAfter string                `json:"next_after,omitempty"`
}

// Scan in directory batches and retain at most limit+1 matching summaries.
// No queue/run lock is taken and no saved record is rewritten.
func ListQueueArchives(ctx context.Context, repo, search, after string, limit int) (QueueArchivePage, error) {
	page := QueueArchivePage{Directory: queueArchiveDirectory, Items: []QueueArchiveSummary{}}
	if limit < 1 || limit > 100 || len(search) > 256 || (after != "" && !validRunID.MatchString(after)) {
		return page, errors.New("archive limit must be 1–100, search at most 256 bytes, and after a valid queue ID")
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return page, err
	}
	defer root.Close()
	archives, err := root.OpenRoot(filepath.FromSlash(queueArchiveDirectory))
	if errors.Is(err, os.ErrNotExist) {
		return page, nil
	}
	if err != nil {
		return page, err
	}
	defer archives.Close()
	dir, err := archives.Open(".")
	if err != nil {
		return page, err
	}
	defer dir.Close()
	query := strings.ToLower(strings.TrimSpace(search))
	for {
		if err := ctx.Err(); err != nil {
			return QueueArchivePage{}, err
		}
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return QueueArchivePage{}, err
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".json") || !entry.Type().IsRegular() {
				continue
			}
			id := strings.TrimSuffix(name, ".json")
			if !validRunID.MatchString(id) || id <= after {
				continue
			}
			item, err := ReadQueueArchive(repo, id)
			if err != nil {
				return QueueArchivePage{}, fmt.Errorf("queue archive %s: %w", id, err)
			}
			if query != "" && !strings.Contains(strings.ToLower(item.ID+"\n"+item.Status+"\n"+item.Goal), query) {
				continue
			}
			goal, truncated := evidenceExcerpt(item.Goal, 240)
			summary := QueueArchiveSummary{ID: item.ID, Goal: goal, GoalTruncated: truncated, Status: item.Status, Mode: item.Mode, RunID: item.RunID, UpdatedAt: item.UpdatedAt}
			index := sort.Search(len(page.Items), func(i int) bool { return page.Items[i].ID >= id })
			page.Items = append(page.Items, QueueArchiveSummary{})
			copy(page.Items[index+1:], page.Items[index:])
			page.Items[index] = summary
			if len(page.Items) > limit+1 {
				page.Items = page.Items[:limit+1]
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return QueueArchivePage{}, readErr
		}
	}
	page.More = len(page.Items) > limit
	if page.More {
		page.Items = page.Items[:limit]
		page.NextAfter = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func FormatQueueArchives(page QueueArchivePage) string {
	if len(page.Items) == 0 {
		return "No matching archived goals."
	}
	var out strings.Builder
	for _, item := range page.Items {
		fmt.Fprintf(&out, "[%s] %s\n  %s\n", item.Status, item.ID, strings.ReplaceAll(item.Goal, "\n", " "))
	}
	if page.More {
		fmt.Fprintf(&out, "More matches: repeat the same search with --after %s.\n", page.NextAfter)
	}
	out.WriteString("Inspect full evidence with buildheadway queue archive QUEUE_ID.")
	return out.String()
}
