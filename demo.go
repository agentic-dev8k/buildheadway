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

type calculator struct {
	Operator string `json:"operator"`
}

func DemoCheck(repo string) error {
	var config calculator
	if err := ReadJSON(filepath.Join(repo, "calculator.json"), &config); err != nil {
		return err
	}
	calculate := func(a, b int) (int, error) {
		switch config.Operator {
		case "+":
			return a + b, nil
		case "-":
			return a - b, nil
		}
		return 0, errors.New("unsupported calculator operator")
	}
	for _, example := range [][3]int{{2, 3, 5}, {-2, 3, 1}, {-2, -3, -5}} {
		got, err := calculate(example[0], example[1])
		if err != nil || got != example[2] {
			return fmt.Errorf("addition check failed for %d and %d: got %d", example[0], example[1], got)
		}
	}
	return nil
}
func DemoRunner(paths Paths, emit Emit) (*Runner, error) {
	repo, err := os.MkdirTemp("", "buildheadway-demo-*")
	if err != nil {
		return nil, err
	}
	settings := Settings{Version: 2, Planner: Connection{Backend: "demo"}, Worker: Connection{Backend: "demo"}}
	runner, err := NewRunner(paths, settings, repo, "Create a calculator configuration that adds positive and negative numbers correctly.", false, emit)
	if err != nil {
		return nil, err
	}
	runner.State.RequiredChecks = []string{"@buildheadway-demo-check"}
	runner.MaxCycles = 4
	return runner, nil
}
func (r *Runner) invokeDemo(ctx context.Context, _ string, planner bool) (string, error) {
	if err := waitContext(ctx, 250*time.Millisecond); err != nil {
		return "", err
	}
	path := filepath.Join(r.Repo, "calculator.json")
	var config calculator
	exists := ReadJSON(path, &config) == nil
	if !planner {
		operator := "-"
		if exists {
			operator = "+"
		}
		if err := AtomicJSON(path, calculator{operator}); err != nil {
			return "", err
		}
		return "Calculator configuration updated. Independent checks will verify it.", nil
	}
	correct := exists && config.Operator == "+"
	answer := Decision{
		Done: correct, Summary: "Configure the calculator, then verify its behavior.",
		Tasks: []Task{}, Checks: []string{}, SuccessCriteria: []string{"Addition handles positive and negative operands"},
		Acceptance: []AcceptanceCheck{{Criterion: "Addition handles positive and negative operands", Checks: []string{"@buildheadway-demo-check"}}},
	}
	if correct {
		answer.Summary = "All addition checks pass. The calculator configuration is complete."
	} else {
		title := "Configure addition"
		if exists {
			title = "Fix the addition configuration"
			answer.Summary = "The check found a defect. Assign a repair and verify again."
		}
		answer.Tasks = []Task{{ID: "calculator", Title: title, Instructions: "Configure calculator.json to use addition."}}
	}
	data, _ := json.Marshal(answer)
	return string(data), nil
}
