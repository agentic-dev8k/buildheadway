package main

import (
	"errors"
	"fmt"
)

// InspectTask reads the same checkpoint as tasks/status without migrating it on disk.
// Keys are exact ledger identifiers, never filesystem paths or model task IDs.
func InspectTask(repo, runID, key string) (TaskRecord, string, error) {
	state, _, err := LoadRun(repo, runID)
	if err != nil {
		return TaskRecord{}, "", err
	}
	for _, record := range state.TaskHistory {
		if record.Key == key {
			text := fmt.Sprintf("Task %s · %s\n%s\nCycle %d · %d attempts · model %s\nStarted: %s\nFinished: %s\n\nInstructions:\n%s\n\nReport:\n%s\n\nError:\n%s", record.Key, record.Status, record.Task.Title, record.Cycle, record.Attempts, record.Model, record.StartedAt, record.FinishedAt, record.Task.Instructions, record.Report, record.Error)
			return record, text, nil
		}
	}
	return TaskRecord{}, "", errors.New("task key not found; use buildheadway tasks to list saved keys")
}
