package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryOfferQueriesRejectAmbiguityAndPreserveLimits(t *testing.T) {
	for _, q := range []FreebieOfferQuery{
		{Limit: 0}, {Limit: 101}, {Limit: 20, Source: "unknown"},
		{Limit: 20, Provider: "not a provider"}, {Limit: 20, Search: strings.Repeat("a", 257)},
		{Limit: 20, Search: "bad\x00query"}, {Limit: 20, Search: "bad\nquery"}, {Limit: 20, Search: "\xff"},
	} {
		if _, err := normalizeFreebieOfferQuery(q); err == nil {
			t.Fatalf("accepted invalid query: %+v", q)
		}
	}
	q, err := normalizeFreebieOfferQuery(FreebieOfferQuery{Limit: 100, Source: " CATALOG ", Provider: " OPENCODE ", Search: " مفت "})
	if err != nil || q.Source != "catalog" || q.Provider != "opencode" || q.Search != "مفت" || q.Limit != 100 {
		t.Fatal(q, err)
	}
	listing := FreebieOfferListing{CatalogStatus: "checked", XStatus: "disabled", Query: q, Total: 1, Matched: 1, Offers: []FreebieOffer{{ID: "safe\x1b[2Jid", Source: "x", Title: "Free\n\x1b[31mpromo", URL: "https://x.com/i/status/1", Verification: "unverified_announcement", ObservedAt: "date\x1b[0m"}}}
	if text := formatFreebieOffers(listing); strings.ContainsRune(text, '\x1b') || !strings.Contains(text, "unverified_announcement") {
		t.Fatal("terminal listing emitted control sequences or lost verification", text)
	}
}

func TestDiscoveryOfferInspectionPreservesActiveTerminalWork(t *testing.T) {
	paths := testPaths(t)
	if err := mutateFreebies(paths, func(s *FreebieState) error {
		s.Watch = true
		s.X = FreebieXConfig{AllowedRequests: 3, UsedRequests: 2, Blocked: "HTTP 429"}
		s.Report = FreebieReport{CatalogStatus: "not_checked", XStatus: "blocked", Offers: []FreebieOffer{{ID: "x/1", Source: "x", Title: "مفت discovery", URL: "https://x.com/i/status/1", Verification: "unverified_announcement", ObservedAt: "2026-11-01T00:00:00Z"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(freebieStatePath(paths))
	if err != nil {
		t.Fatal(err)
	}
	m := newApp(paths, t.TempDir(), "", false, false)
	m.mode, m.busy, m.stage = "chat", true, "Implementing goal"
	m.editor.SetValue("Retain the unsent draft")
	goalCanceled, watchCanceled := false, false
	m.cancel = func() { goalCanceled = true }
	m.freebieCancel = func() { watchCanceled = true }
	m.freebieGeneration = 7
	m.progress = Progress{RunID: "saved-goal", PlannerCalls: 4, WorkerCalls: 2, ActiveMillis: 1234}
	progress := m.progress
	if cmd := m.handle("/freebies offers مفت"); cmd != nil {
		t.Fatal("saved-offer inspection scheduled asynchronous work")
	}
	if !m.busy || m.stage != "Implementing goal" || m.editor.Value() != "Retain the unsent draft" || goalCanceled || watchCanceled || m.freebieGeneration != 7 || m.progress != progress {
		t.Fatal("saved-offer inspection changed an active goal, watcher or draft")
	}
	last := m.entries[len(m.entries)-1].Text
	if !strings.Contains(last, "مفت discovery") || !strings.Contains(last, "unverified_announcement") || !strings.Contains(last, "2026-11-01T00:00:00Z") || !strings.Contains(last, "Observed age:") {
		t.Fatal("terminal search omitted saved evidence", last)
	}
	after, err := os.ReadFile(freebieStatePath(paths))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("terminal search rewrote discovery evidence", err)
	}
	choices := completionCandidates([]string{"freebies", "offers", "--source", ""})
	if len(choices) != 3 || !contains(choices, "all") || !contains(choices, "catalog") || !contains(choices, "x") {
		t.Fatal("source completion lost the supported choices", choices)
	}
}

func TestDiscoveryAgeBoundariesAndClockSkew(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ stamp, want string }{
		{"", "unknown (timestamp missing)"}, {"bad\x1b[2J", "unknown (invalid timestamp)"},
		{now.Add(time.Nanosecond).Format(time.RFC3339Nano), "unknown (future timestamp; check system clock)"},
		{now.Format(time.RFC3339Nano), "less than 1m ago"},
		{now.Add(-59 * time.Second).Format(time.RFC3339Nano), "less than 1m ago"},
		{now.Add(-time.Minute).Format(time.RFC3339Nano), "1m ago"},
		{now.Add(-time.Hour).Format(time.RFC3339Nano), "1h 0m ago"},
		{now.Add(-24 * time.Hour).Format(time.RFC3339Nano), "1d 0h ago"},
		{now.Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano), "365d 0h ago"},
		{"0001-01-01T00:00:00Z", "over 365d ago"},
		{"2026-11-17T14:00:00+02:00", "less than 1m ago"},
	} {
		if got := freebieAge(tc.stamp, now); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.stamp, got, tc.want)
		}
	}
	listing := FreebieOfferListing{CheckedAt: now.Format(time.RFC3339Nano), Offers: []FreebieOffer{{ObservedAt: "2000-01-01T00:00:00Z"}, {ObservedAt: "2999-01-01T00:00:00Z"}, {}}}
	text := formatFreebieOffersAt(listing, now)
	for _, want := range []string{"Last check age: less than 1m ago (check attempt, not offer freshness)", "Observed age: over 365d ago", "Observed age: unknown (future timestamp; check system clock)", "Observed age: unknown (timestamp missing)"} {
		if !strings.Contains(text, want) {
			t.Error("missing", want, text)
		}
	}
}
