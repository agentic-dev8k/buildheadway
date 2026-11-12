package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func recoveryCatalog(context.Context, Paths) ([]string, error) {
	return []string{"opencode/fixture"}, nil
}

func TestDiscoveryCursorAlwaysPublishesWithItsOffers(t *testing.T) {
	paths := testPaths(t)
	enableXFixture(t, paths, 16)
	stop := make(chan struct{})
	ready := make(chan struct{}, 4)
	failures := make(chan string, 1)
	var reads atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			for {
				select {
				case <-stop:
					return
				default:
				}
				s, err := readFreebies(paths)
				if err != nil {
					select {
					case failures <- err.Error():
					default:
					}
					return
				}
				reads.Add(1)
				if s.X.SinceID != "" {
					found := false
					for _, o := range s.Report.Offers {
						found = found || o.ID == "x/"+s.X.SinceID
					}
					if !found {
						select {
						case failures <- "saved cursor " + s.X.SinceID + " has no saved offer":
						default:
						}
						return
					}
				}
				runtime.Gosched()
			}
		}()
	}
	defer func() { close(stop); wg.Wait() }()
	for range 4 {
		<-ready
	}
	request := 0
	client := &http.Client{Transport: freebieTransport(func(*http.Request) (*http.Response, error) {
		request++
		return freebieResponse(200, fmt.Sprintf(`{"data":[{"id":"%d","text":"Offer"}],"meta":{"result_count":1,"newest_id":"%d"}}`, 1000+request, 1000+request)), nil
	})}
	for range 16 {
		if _, err := checkFreebiesWith(context.Background(), paths, client, recoveryCatalog); err != nil {
			t.Fatal(err)
		}
		select {
		case message := <-failures:
			t.Fatal(message)
		default:
		}
	}
	if reads.Load() == 0 {
		t.Fatal("no concurrent state observations")
	}
	state, err := readFreebies(paths)
	if err != nil || state.X.UsedRequests != 16 || state.X.SinceID != "1016" {
		t.Fatal(state, err)
	}
}

func TestDiscoveryResponseCannotOverwriteNewXAuthorization(t *testing.T) {
	for _, code := range []int{200, 401, 402, 403, 429} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			paths := testPaths(t)
			enableXFixture(t, paths, 2)
			t.Setenv("BUILDHEADWAY_TEST_X_REPLACEMENT", "NEW_SECRET_NEVER_SAVE")
			calls := 0
			client := &http.Client{Transport: freebieTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					if err := mutateFreebies(paths, func(s *FreebieState) error {
						s.X.TokenEnv = "BUILDHEADWAY_TEST_X_REPLACEMENT"
						s.X.AllowedRequests += 2
						s.X.Blocked = ""
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					return freebieResponse(code, `{"data":[{"id":"100","text":"Offer"}],"meta":{"result_count":1,"newest_id":"100"}}`), nil
				}
				if r.Header.Get("Authorization") != "Bearer NEW_SECRET_NEVER_SAVE" {
					t.Fatal("new request did not use new authorization")
				}
				return freebieResponse(200, `{"data":[{"id":"101","text":"New offer"}],"meta":{"result_count":1,"newest_id":"101"}}`), nil
			})}
			first, err := checkFreebiesWith(context.Background(), paths, client, recoveryCatalog)
			state, readErr := readFreebies(paths)
			if err != nil || readErr != nil || first.XStatus != "superseded" || state.X.SinceID != "" || state.X.Blocked != "" || state.X.TokenEnv != "BUILDHEADWAY_TEST_X_REPLACEMENT" || state.X.UsedRequests != 1 || state.X.AllowedRequests != 4 {
				t.Fatalf("old response overwrote new authorization: %+v %+v %v %v", first, state, err, readErr)
			}
			second, err := checkFreebiesWith(context.Background(), paths, client, recoveryCatalog)
			state, _ = readFreebies(paths)
			if err != nil || second.XStatus != "checked" || calls != 2 || state.X.UsedRequests != 2 || state.X.SinceID != "101" {
				t.Fatal(second, state, err, calls)
			}
		})
	}
}

func TestDiscoveryRejectsRegressedOrIncompleteCursorPages(t *testing.T) {
	for i, data := range []string{
		`{"data":[{"id":"11"},{"id":"12"}],"meta":{"result_count":2,"newest_id":"11"}}`,
		`{"data":[{"id":"11"}],"meta":{"result_count":1}}`,
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, _, err := xFreebieOffers([]byte(data), "now"); err == nil {
				t.Fatalf("accepted ambiguous newest cursor: %s", data)
			}
		})
	}
	paths := testPaths(t)
	enableXFixture(t, paths, 1)
	if err := mutateFreebies(paths, func(s *FreebieState) error { s.X.SinceID = "9007199254740993"; return nil }); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: freebieTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("since_id") != "9007199254740993" {
			t.Fatal(r.URL)
		}
		return freebieResponse(200, `{"data":[{"id":"9007199254740992"}],"meta":{"result_count":1,"newest_id":"9007199254740992"}}`), nil
	})}
	report, err := checkFreebiesWith(context.Background(), paths, client, recoveryCatalog)
	state, _ := readFreebies(paths)
	if err != nil || report.XStatus != "error" || state.X.SinceID != "9007199254740993" || state.X.UsedRequests != 1 {
		t.Fatal(report, state, err)
	}
}

func TestDiscoveryFailedPublicationPreservesEvidenceAndReservations(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			paths := testPaths(t)
			enableXFixture(t, paths, 2)
			if err := mutateFreebies(paths, func(s *FreebieState) error {
				s.X.SinceID = "100"
				s.Report = FreebieReport{XStatus: "checked", Offers: []FreebieOffer{{ID: "x/100", Source: "x", URL: "https://x.com/i/status/100"}}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			config, status, err := reserveXRequest(paths)
			if err != nil || status != "reserved" {
				t.Fatal(config, status, err)
			}
			config.UsedRequests++
			if blocked {
				applied, err := retainXBlock(paths, config, 429)
				if err != nil || !applied {
					t.Fatal(applied, err)
				}
				config.Blocked = "HTTP 429"
			}
			before, err := os.ReadFile(freebieStatePath(paths))
			if err != nil {
				t.Fatal(err)
			}
			invalid := FreebieReport{XStatus: "checked", Offers: []FreebieOffer{{ID: "x/101", Source: "x", URL: "http://invalid.example"}}}
			if _, err := publishFreebieReport(paths, invalid, &config, "101"); err == nil {
				t.Fatal("invalid evidence was published")
			}
			after, err := os.ReadFile(freebieStatePath(paths))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed publication changed the cursor, report, attempt or blocker", err)
			}
			state, err := readFreebies(paths)
			if err != nil || state.X.UsedRequests != 1 || state.X.SinceID != "100" || state.Report.Offers[0].ID != "x/100" || blocked && state.X.Blocked != "HTTP 429" {
				t.Fatal(state, err)
			}
		})
	}
}

func TestDiscoveryPendingResponseRespectsDisableButNotWatchSettings(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(fmt.Sprint(disable), func(t *testing.T) {
			paths := testPaths(t)
			enableXFixture(t, paths, 1)
			client := &http.Client{Transport: freebieTransport(func(*http.Request) (*http.Response, error) {
				if err := mutateFreebies(paths, func(s *FreebieState) error {
					if disable {
						s.X.Enabled = false
					} else {
						s.Watch = true
						s.IntervalSeconds = 900
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				return freebieResponse(200, `{"data":[{"id":"99","text":"Offer"}],"meta":{"result_count":1,"newest_id":"99"}}`), nil
			})}
			report, err := checkFreebiesWith(context.Background(), paths, client, recoveryCatalog)
			state, readErr := readFreebies(paths)
			if err != nil || readErr != nil || state.X.UsedRequests != 1 {
				t.Fatal(report, state, err, readErr)
			}
			if disable {
				if state.X.Enabled || state.X.SinceID != "" || report.XStatus != "superseded" || len(report.Offers) != 2 {
					t.Fatal("disabled request applied a cursor or lost unverified history", state)
				}
			} else if !state.Watch || state.IntervalSeconds != 900 || state.X.SinceID != "99" || report.XStatus != "checked" {
				t.Fatal("unrelated watch settings invalidated a valid response", state)
			}
		})
	}
}

func TestDiscoveryCancellationCannotRefundOrHideReceivedHardBlock(t *testing.T) {
	for _, code := range []int{401, 402, 403, 429} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			paths := testPaths(t)
			enableXFixture(t, paths, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			client := &http.Client{Transport: freebieTransport(func(*http.Request) (*http.Response, error) {
				calls++
				cancel()
				return freebieResponse(code, ""), nil
			})}
			report, err := checkFreebiesWith(ctx, paths, client, recoveryCatalog)
			state, readErr := readFreebies(paths)
			if !errors.Is(err, context.Canceled) || readErr != nil || report.XStatus != "blocked" || state.X.Blocked != fmt.Sprintf("HTTP %d", code) || state.X.UsedRequests != 1 {
				t.Fatal(report, state, err, readErr)
			}
			report, err = checkFreebiesWith(context.Background(), paths, client, recoveryCatalog)
			if err != nil || report.XStatus != "blocked" || calls != 1 {
				t.Fatal("watch retried a retained hard blocker", report, err, calls)
			}
		})
	}
}

func TestDiscoveryCursorOrderingIsExactDecimal(t *testing.T) {
	for _, pair := range [][2]string{{"9", "10"}, {"9007199254740992", "9007199254740993"}, {"9999999999999999999", "10000000000000000000"}} {
		if compareXPostIDs(pair[0], pair[1]) >= 0 || compareXPostIDs(pair[1], pair[0]) <= 0 || compareXPostIDs("00"+pair[0], pair[0]) != 0 {
			t.Fatal("incorrect decimal ordering", pair)
		}
	}
	data := `{"data":[{"id":"9007199254740992"},{"id":"9007199254740993"}],"meta":{"result_count":2,"newest_id":"9007199254740993"}}`
	if offers, cursor, err := xFreebieOffers([]byte(data), "now"); err != nil || len(offers) != 2 || cursor != "9007199254740993" {
		t.Fatal(offers, cursor, err)
	}
}

func TestDiscoveryPreviewShowsDatedAnnouncementsAmongFullCatalog(t *testing.T) {
	s := FreebieState{Report: FreebieReport{CatalogStatus: "public_catalog", XStatus: "checked"}}
	for i := range 100 {
		s.Report.Offers = append(s.Report.Offers, FreebieOffer{ID: fmt.Sprint(i), Source: "models.dev", Title: fmt.Sprint(i), Verification: "catalog_only"})
	}
	s.Report.Offers = append(s.Report.Offers, FreebieOffer{ID: "x/88", Source: "x", Title: "Retained announcement", Verification: "unverified_announcement", ObservedAt: "2026-11-01T00:00:00Z"})
	text := formatFreebies(s)
	if !strings.Contains(text, "Retained announcement") || !strings.Contains(text, "2026-11-01T00:00:00Z") || !strings.Contains(text, "81 more offers") {
		t.Fatal("announcement is hidden or appears newly observed: " + text)
	}
	old := FreebieOffer{ID: "x/88", Source: "x", Verification: "unverified_announcement", ObservedAt: "original"}
	fresh := old
	fresh.ObservedAt = "later"
	if got := mergeXAnnouncements([]FreebieOffer{fresh}, []FreebieOffer{old}); len(got) != 1 || got[0].ObservedAt != "original" {
		t.Fatal("replayed announcement lost its first observation", got)
	}
}
