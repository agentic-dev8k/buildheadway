package main

import (
	"context"
	"fmt"
)

// Resume can clear a saved review round while recovering verification or
// applying steering. Import older requests first, without preserving stale
// votes. An unchanged partial plan keeps its cached position after intake.
func (r *Runner) restoreCouncilRequirements(ctx context.Context) error {
	round := r.State.Council
	if round == nil {
		return nil
	}
	before, snapshot, err := r.councilFingerprint(ctx, round.Review)
	if err != nil {
		return err
	}
	if err := r.retainCouncilRequirements(round.Decisions...); err != nil {
		return err
	}
	if round.Fingerprint == before {
		round.Fingerprint, err = r.councilFingerprintAt(snapshot, round.Review)
	}
	return err
}

// Completed senior decisions can lose their votes when evidence changes. Their
// verification requests remain obligations, never proof of passing checks or
// authorization to execute the proposed tasks or check replacements.
func (r *Runner) retainCouncilRequirements(decisions ...Decision) error {
	checks := append([]string{}, r.State.SuggestedChecks...)
	criteria := append([]string{}, r.State.SuccessCriteria...)
	acceptance := mergeAcceptance(r.State.Acceptance)
	for _, decision := range decisions {
		checks = append(checks, decision.Checks...)
		criteria = append(criteria, decision.SuccessCriteria...)
		acceptance = mergeAcceptance(acceptance, decision.Acceptance)
	}
	if err := validateAcceptance(acceptance); err != nil {
		return fmt.Errorf("retaining senior verification requests: %w", err)
	}
	checks = Unique(append(checks, acceptanceCommands(acceptance)...))
	criteria = Unique(append(criteria, acceptanceCriteria(acceptance)...))
	r.State.SuggestedChecks, r.State.SuccessCriteria, r.State.Acceptance = checks, criteria, acceptance
	return nil
}
