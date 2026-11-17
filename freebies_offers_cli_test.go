package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestDiscoveryCLIUntrustedSavedTextCannotControlTerminal(t *testing.T) {
	paths := testPaths(t)
	unsafe := "saved\x1b[2J\u009b31m\u009d52;clipboard\a\rtext"
	if err := mutateFreebies(paths, func(s *FreebieState) error {
		s.Report = FreebieReport{CheckedAt: unsafe, CatalogStatus: unsafe, CatalogError: unsafe, XStatus: unsafe, XError: unsafe, Offers: []FreebieOffer{{ID: unsafe, Source: "x", Title: unsafe, Verification: unsafe, ObservedAt: unsafe, URL: "https://example.invalid/\u009d52;clipboard"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(freebieStatePath(paths))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"freebies", "status"}, {"freebies", "offers"}} {
		cmd := exec.Command(nativeTestBinary(t), args...)
		cmd.Env = testCommandEnv(filepath.Dir(paths.Data))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(out))
		}
		for _, r := range string(out) {
			if unicode.IsControl(r) && r != '\n' {
				t.Fatalf("%v emitted terminal control U+%04X", args, r)
			}
		}
		if !bytes.Contains(out, []byte("https://example.invalid/")) {
			t.Fatal("lost offer URL")
		}
	}
	cmd := exec.Command(nativeTestBinary(t), "freebies", "status", "--json")
	cmd.Env = testCommandEnv(filepath.Dir(paths.Data))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var saved, inspected FreebieState
	if err := json.Unmarshal(before, &saved); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out, &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Report.CheckedAt != unsafe || inspected.Report.Offers[0].URL != saved.Report.Offers[0].URL {
		t.Fatal("JSON inspection rewrote original evidence")
	}
	after, err := os.ReadFile(freebieStatePath(paths))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("inspection changed saved bytes", err)
	}
}

func TestDiscoveryOffersCLIInspectsSavedOffersWithoutChangingEvidence(t *testing.T) {
	binary := nativeTestBinary(t)
	paths := testPaths(t)
	if err := mutateFreebies(paths, func(s *FreebieState) error {
		s.Watch = true
		s.X = FreebieXConfig{AllowedRequests: 4, UsedRequests: 2, Blocked: "HTTP 429"}
		s.Report = FreebieReport{CheckedAt: "2026-11-12T00:00:00Z", CatalogStatus: "public_catalog", XStatus: "blocked", Offers: []FreebieOffer{
			{ID: "opencode/fast", Source: "opencode", Provider: "opencode", Title: "Fast free coding", URL: "https://opencode.ai/", Verification: "listed_zero_price", ObservedAt: "2026-11-12T00:00:00Z"},
			{ID: "other/fast", Source: "models.dev", Provider: "other", Title: "FAST reference", URL: "https://models.dev/", Verification: "catalog_only"},
			{ID: "x/100", Source: "x", Title: "Fast promotion — مفت", URL: "https://x.com/i/status/100", Verification: "unverified_announcement", ObservedAt: "2026-11-01T00:00:00Z"},
			{ID: "opencode/slow", Source: "opencode", Provider: "opencode", Title: "Slow model", URL: "https://opencode.ai/", Verification: "listed_zero_price"},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	config := filepath.Join(project, ".buildheadway.json")
	if err := os.WriteFile(config, []byte("invalid project settings must not affect inspection"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(freebieStatePath(paths))
	if err != nil {
		t.Fatal(err)
	}
	run := func(wantSuccess bool, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, append([]string{"freebies", "offers"}, args...)...)
		cmd.Dir, cmd.Env = project, testCommandEnv(filepath.Dir(paths.Data))
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantSuccess || ctx.Err() != nil {
			t.Fatalf("offers %v: %v %s", args, err, out)
		}
		return out
	}
	var listing struct {
		CheckedAt string         `json:"checked_at"`
		Total     int            `json:"total"`
		Matched   int            `json:"matched"`
		Offers    []FreebieOffer `json:"offers"`
	}
	if err := json.Unmarshal(run(true, "--source", "catalog", "--search", "FaSt", "--limit", "1", "--json"), &listing); err != nil || listing.Total != 4 || listing.Matched != 2 || len(listing.Offers) != 1 || listing.Offers[0].ID != "opencode/fast" || listing.CheckedAt != "2026-11-12T00:00:00Z" {
		t.Fatal("bounded catalog search failed", listing, err)
	}
	if err := json.Unmarshal(run(true, "--source", "x", "--search", "مفت", "--json"), &listing); err != nil || listing.Matched != 1 || len(listing.Offers) != 1 || listing.Offers[0].ID != "x/100" || listing.Offers[0].ObservedAt != "2026-11-01T00:00:00Z" {
		t.Fatal("Unicode announcement search lost its evidence", listing, err)
	}
	if err := json.Unmarshal(run(true, "--provider", "OPENCODE", "--limit", "100", "--json"), &listing); err != nil || listing.Matched != 2 || len(listing.Offers) != 2 {
		t.Fatal("provider selection failed", listing, err)
	}
	text := run(true, "--search", "promotion")
	if !bytes.Contains(text, []byte("unverified_announcement")) || !bytes.Contains(text, []byte("2026-11-01T00:00:00Z")) {
		t.Fatal("terminal inspection omitted verification or observation time", string(text))
	}
	for _, args := range [][]string{{"--source", "unknown"}, {"--limit", "0"}, {"--limit", "101"}, {"--search", "bad\nquery"}, {"--provider", "bad provider"}, {"unexpected"}, {"--requests", "1"}} {
		run(false, args...)
	}
	after, err := os.ReadFile(freebieStatePath(paths))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("offer inspection changed the watcher, attempts, blocker or saved report", err)
	}
	if data, err := os.ReadFile(config); err != nil || string(data) != "invalid project settings must not affect inspection" {
		t.Fatal("offer inspection changed project settings", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".forge")); !os.IsNotExist(err) {
		t.Fatal("offer inspection created goal state")
	}
}

func TestDiscoveryOffersCLIWithNoSavedStateIsReadOnly(t *testing.T) {
	binary := nativeTestBinary(t)
	home := filepath.Join(t.TempDir(), "missing user data")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "freebies", "offers", "--json")
	cmd.Env = testCommandEnv(home)
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte(`"offers": []`)) {
		t.Fatalf("empty saved-offer inspection failed: %v %s", err, out)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("empty offer inspection created user settings")
	}
}

func TestDiscoveryCLIDistinguishesCheckAgeFromRetainedOfferAge(t *testing.T) {
	paths := testPaths(t)
	if err := mutateFreebies(paths, func(s *FreebieState) error {
		s.Report = FreebieReport{CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), CatalogStatus: "error", CatalogError: "provider unavailable", XStatus: "blocked", Offers: []FreebieOffer{{ID: "x/1", Source: "x", Title: "retained announcement", Verification: "unverified_announcement", URL: "https://example.invalid/1", ObservedAt: "2000-01-01T00:00:00Z"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(freebieStatePath(paths))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"freebies", "status"}, {"freebies", "offers"}} {
		cmd := exec.Command(nativeTestBinary(t), args...)
		cmd.Env = testCommandEnv(filepath.Dir(paths.Data))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(out))
		}
		for _, want := range []string{"Last check age: less than 1m ago", "Observed age: over 365d ago", "provider unavailable", "unverified_announcement"} {
			if !strings.Contains(string(out), want) {
				t.Fatalf("%v omitted %q: %s", args, want, out)
			}
		}
	}
	after, err := os.ReadFile(freebieStatePath(paths))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("age inspection changed evidence", err)
	}
}
