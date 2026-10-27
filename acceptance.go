package main

import (
	"errors"
	"fmt"
	"strings"
)

// AcceptanceCheck connects a requirement to commands that the harness executes.
// Agents describe coverage; only real check results can supply passing evidence.
type AcceptanceCheck struct {
	Criterion string   `json:"criterion"`
	Checks    []string `json:"checks"`
}

type AcceptanceResult struct {
	Criterion string        `json:"criterion"`
	Status    string        `json:"status"`
	Checks    []string      `json:"checks"`
	Evidence  []CheckResult `json:"evidence"`
}

func validateAcceptance(items []AcceptanceCheck) error {
	if len(items) > 100 {
		return errors.New("at most 100 acceptance requirements are supported")
	}
	for _, item := range items {
		if strings.TrimSpace(item.Criterion) == "" || len(item.Criterion) > 2000 || len(item.Checks) == 0 || len(item.Checks) > 30 {
			return errors.New("each acceptance requirement needs a description and 1–30 executable checks")
		}
		for _, check := range item.Checks {
			if len(check) > 16000 || strings.ContainsRune(check, 0) || !usefulCheck(check) {
				return fmt.Errorf("acceptance requirement %q needs a meaningful check", item.Criterion)
			}
		}
	}
	return nil
}

func mergeAcceptance(groups ...[]AcceptanceCheck) []AcceptanceCheck {
	var result []AcceptanceCheck
	indices := map[string]int{}
	for _, items := range groups {
		for _, item := range items {
			index, exists := indices[item.Criterion]
			if !exists {
				index = len(result)
				indices[item.Criterion] = index
				result = append(result, AcceptanceCheck{Criterion: item.Criterion, Checks: []string{}})
			}
			result[index].Checks = Unique(append(result[index].Checks, item.Checks...))
		}
	}
	return result
}

func acceptanceCommands(items []AcceptanceCheck) []string {
	var commands []string
	for _, item := range items {
		commands = append(commands, item.Checks...)
	}
	return Unique(commands)
}

func acceptanceCriteria(items []AcceptanceCheck) []string {
	var criteria []string
	for _, item := range items {
		criteria = append(criteria, item.Criterion)
	}
	return Unique(criteria)
}

func AcceptanceResults(state State, answer Decision) []AcceptanceResult {
	items := mergeAcceptance(state.Acceptance, answer.Acceptance)
	criteria := Unique(append(append(append([]string{}, state.SuccessCriteria...), answer.SuccessCriteria...), acceptanceCriteria(items)...))
	results := make([]AcceptanceResult, 0, len(criteria))
	for _, criterion := range criteria {
		row := AcceptanceResult{Criterion: criterion, Status: "unmapped", Checks: []string{}, Evidence: []CheckResult{}}
		for _, item := range items {
			if item.Criterion == criterion {
				row.Checks = item.Checks
			}
		}
		if len(row.Checks) > 0 {
			row.Status = "passed"
			for _, command := range row.Checks {
				found := false
				for _, result := range state.CheckResults {
					if result.Command == command {
						found = true
						evidence := result
						evidence.OutputTruncated = result.OutputTruncated || len(result.Output) > 1200
						evidence.Output = Tail(result.Output, 1200)
						row.Evidence = append(row.Evidence, evidence)
						if result.ExitCode != 0 || result.FailureKind != "" || result.Error != "" {
							row.Status = "failed"
						} else if result.SourceChanged && row.Status != "failed" {
							row.Status = "unverified"
						}
					}
				}
				if (!found || !usefulCheck(command)) && row.Status != "failed" {
					row.Status = "pending"
				}
			}
			if state.CheckedSnapshot == "" {
				row.Status = "unverified"
			}
		}
		results = append(results, row)
	}
	return results
}

func acceptancePassed(state State, answer Decision) bool {
	// Legacy structures used by readers remain readable. Resumed goals are
	// migrated to v8 and must obtain fresh evidence before they can complete.
	if state.Version < 8 {
		return true
	}
	rows := AcceptanceResults(state, answer)
	if len(rows) == 0 {
		return false
	}
	for _, row := range rows {
		if row.Status != "passed" {
			return false
		}
	}
	return true
}

func FormatAcceptance(state State) string {
	rows := AcceptanceResults(state, Decision{})
	if len(rows) == 0 {
		return "The team has not defined acceptance requirements yet."
	}
	var out strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&out, "[%s] %s\n", row.Status, row.Criterion)
		for _, check := range row.Checks {
			fmt.Fprintf(&out, "  %s\n", check)
		}
		if row.Status == "unmapped" {
			out.WriteString("  The team must connect this requirement to an executable check.\n")
		}
	}
	return strings.TrimSpace(out.String())
}
