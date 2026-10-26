package main

import (
	"context"
	"testing"
)

func TestVerificationShellDiagnosticExecutesQuotedPassingAndFailingChecks(t *testing.T) {
	result := verificationShellDiagnostic(context.Background())
	if result.Status != "ok" {
		t.Fatalf("native verification shell failed its real execution probes: %+v", result)
	}
}

func TestVerificationShellDiagnosticHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := verificationShellDiagnostic(ctx)
	if result.Status != "info" || result.Fix != "" {
		t.Fatalf("cancelled probe was treated as shell success/failure: %+v", result)
	}
}
