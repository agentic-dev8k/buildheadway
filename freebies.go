package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const freebieCatalogURL = "https://models.dev/api.json"
const freebieXURL = "https://api.x.com/2/tweets/search/recent"
const freebieQuery = `(free OR "free model") (coding OR "AI model") has:links -is:retweet`
const freebieLimit = 100

type FreebieOffer struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Provider     string `json:"provider,omitempty"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	Verification string `json:"verification"`
	ObservedAt   string `json:"observed_at"`
}
type FreebieReport struct {
	CheckedAt     string         `json:"checked_at"`
	CatalogStatus string         `json:"catalog_status"`
	CatalogError  string         `json:"catalog_error,omitempty"`
	XStatus       string         `json:"x_status"`
	XError        string         `json:"x_error,omitempty"`
	Offers        []FreebieOffer `json:"offers"`
}
type FreebieXConfig struct {
	Enabled         bool   `json:"enabled"`
	TokenEnv        string `json:"token_env,omitempty"`
	AllowedRequests int    `json:"allowed_requests"`
	UsedRequests    int    `json:"used_requests"`
	Blocked         string `json:"blocked,omitempty"`
	SinceID         string `json:"since_id,omitempty"`
}
type FreebieState struct {
	Version         int            `json:"version"`
	Watch           bool           `json:"watch"`
	IntervalSeconds int            `json:"interval_seconds"`
	X               FreebieXConfig `json:"x"`
	Report          FreebieReport  `json:"report"`
}

var freebieID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,255}$`)
var freebiePostID = regexp.MustCompile(`^[0-9]{1,30}$`)
var freebieEnv = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func freebieStatePath(paths Paths) string { return filepath.Join(paths.Data, "freebies.json") }
func readFreebies(paths Paths) (FreebieState, error) {
	s := FreebieState{Version: 1, IntervalSeconds: 1800, Report: FreebieReport{Offers: []FreebieOffer{}, XStatus: "disabled", CatalogStatus: "not_checked"}}
	p := freebieStatePath(paths)
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !info.Mode().IsRegular() || info.Size() > 512<<10 {
		return s, errors.New("freebie state must be a regular file of at most 512 KiB")
	}
	file, err := openFreebieStateFile(p)
	if err != nil {
		return s, err
	}
	defer file.Close()
	data, err := ReadLimited(file, 512<<10)
	if err != nil {
		return s, err
	}
	s = FreebieState{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return s, errors.New("invalid freebie state JSON")
	}
	if dec.Decode(new(any)) != io.EOF {
		return s, errors.New("freebie state must contain one JSON object")
	}
	return s, validateFreebies(s)
}
func validateFreebies(s FreebieState) error {
	if s.Version != 1 || s.IntervalSeconds < 900 || s.IntervalSeconds > 86400 || s.X.AllowedRequests < 0 || s.X.AllowedRequests > 1000000 || s.X.UsedRequests < 0 || s.X.UsedRequests > s.X.AllowedRequests || len(s.Report.Offers) > freebieLimit {
		return errors.New("invalid freebie state limits")
	}
	if (s.X.TokenEnv != "" && !freebieEnv.MatchString(s.X.TokenEnv)) || (s.X.Enabled && s.X.TokenEnv == "") || (s.X.SinceID != "" && !freebiePostID.MatchString(s.X.SinceID)) || len(s.X.Blocked) > 64 {
		return errors.New("invalid X discovery configuration")
	}
	for _, o := range s.Report.Offers {
		u, e := url.Parse(o.URL)
		if len(o.ID) > 512 || len(o.Title) > 512 || len(o.Provider) > 256 || len(o.URL) > 2048 || e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return errors.New("invalid saved freebie offer")
		}
	}
	return nil
}
func mutateFreebies(paths Paths, change func(*FreebieState) error) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	lock, err := acquireProjectLock(paths.Data, "freebies-state", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	s, err := readFreebies(paths)
	if err != nil {
		return err
	}
	if err := change(&s); err != nil {
		return err
	}
	if err := validateFreebies(s); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil || len(data)+1 > 512<<10 {
		return errors.New("freebie state exceeds its encoded bound")
	}
	return AtomicJSON(freebieStatePath(paths), s)
}
func freebieHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("discovery redirects are not allowed") }}
}
func freebieGet(ctx context.Context, client *http.Client, endpoint, token string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, errors.New("invalid discovery endpoint")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "BuildHeadway/"+Version)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// Never forward an X bearer credential to a redirect or another origin,
	// including with a caller-supplied client in an integration test.
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("discovery redirects are not allowed") }
	response, err := copy.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, errors.New("discovery request failed; check network access")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, fmt.Errorf("discovery endpoint returned HTTP %d", response.StatusCode)
	}
	data, err := ReadLimited(response.Body, 8<<20)
	if err != nil {
		return nil, response.StatusCode, errors.New("discovery response exceeded its bound or could not be read")
	}
	return data, response.StatusCode, nil
}

func exactZeroCost(v any) bool {
	switch n := v.(type) {
	case json.Number:
		s := strings.TrimPrefix(n.String(), "-")
		if i := strings.IndexAny(s, "eE"); i >= 0 {
			s = s[:i]
		}
		return !strings.ContainsAny(s, "123456789")
	case map[string]any:
		for _, part := range n {
			if !exactZeroCost(part) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
func freebieTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, Clean(s))
	s, _ = evidenceExcerpt(strings.TrimSpace(s), 512)
	return s
}
func catalogFreebies(data []byte, at string) ([]FreebieOffer, error) {
	var providers map[string]struct {
		Models map[string]struct {
			ID       string         `json:"id"`
			Name     string         `json:"name"`
			Status   string         `json:"status"`
			ToolCall bool           `json:"tool_call"`
			Cost     map[string]any `json:"cost"`
		} `json:"models"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&providers); err != nil || providers == nil {
		return nil, errors.New("invalid public model catalog")
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, errors.New("public model catalog contains trailing data")
	}
	offers := []FreebieOffer{}
	for provider, p := range providers {
		if !freebieID.MatchString(provider) {
			continue
		}
		for key, m := range p.Models {
			_, in := m.Cost["input"].(json.Number)
			_, out := m.Cost["output"].(json.Number)
			if !in || !out || !m.ToolCall || !exactZeroCost(m.Cost) || !freebieID.MatchString(key) || (m.ID != "" && m.ID != key) || m.Status == "deprecated" {
				continue
			}
			title := m.Name
			if title == "" {
				title = key
			}
			offers = append(offers, FreebieOffer{ID: provider + "/" + key, Source: "models.dev", Provider: provider, Title: freebieTitle(title), URL: "https://models.dev/providers/" + url.PathEscape(provider) + "/", Verification: "catalog_only", ObservedAt: at})
		}
	}
	sort.Slice(offers, func(i, j int) bool {
		if (offers[i].Provider == "opencode") != (offers[j].Provider == "opencode") {
			return offers[i].Provider == "opencode"
		}
		return offers[i].ID < offers[j].ID
	})
	if len(offers) > freebieLimit {
		offers = offers[:freebieLimit]
	}
	return offers, nil
}

func reserveXRequest(paths Paths) (FreebieXConfig, string, error) {
	var config FreebieXConfig
	status := "disabled"
	err := mutateFreebies(paths, func(s *FreebieState) error {
		config = s.X
		if !config.Enabled {
			return nil
		}
		if config.Blocked != "" {
			status = "blocked"
			return nil
		}
		if config.UsedRequests >= config.AllowedRequests {
			status = "budget_exhausted"
			return nil
		}
		if os.Getenv(config.TokenEnv) == "" {
			status = "missing_credential"
			return nil
		}
		// Persist before the network boundary. Ambiguous/failed attempts are not
		// refunded: they might have incurred charges. Restarts never reset usage.
		s.X.UsedRequests++
		status = "reserved"
		return nil
	})
	return config, status, err
}
func xFreebieOffers(data []byte, at string) ([]FreebieOffer, string, error) {
	var response struct {
		Data []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
		Meta   struct {
			NewestID    string `json:"newest_id"`
			ResultCount *int   `json:"result_count"`
		} `json:"meta"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Errors) > 0 || len(response.Data) > 10 || response.Meta.ResultCount == nil || *response.Meta.ResultCount != len(response.Data) {
		return nil, "", errors.New("invalid or partial X search response")
	}
	offers := []FreebieOffer{}
	seen := map[string]bool{}
	foundCursor := response.Meta.NewestID == ""
	for _, post := range response.Data {
		if !freebiePostID.MatchString(post.ID) || seen[post.ID] {
			return nil, "", errors.New("invalid X post identifier")
		}
		seen[post.ID] = true
		foundCursor = foundCursor || post.ID == response.Meta.NewestID
		if response.Meta.NewestID == "" || compareXPostIDs(post.ID, response.Meta.NewestID) > 0 {
			return nil, "", errors.New("X search cursor does not cover the received posts")
		}
		offers = append(offers, FreebieOffer{ID: "x/" + post.ID, Source: "x", Title: freebieTitle(post.Text), URL: "https://x.com/i/status/" + post.ID, Verification: "unverified_announcement", ObservedAt: at})
	}
	if !foundCursor || (response.Meta.NewestID != "" && !freebiePostID.MatchString(response.Meta.NewestID)) {
		return nil, "", errors.New("invalid X search cursor")
	}
	return offers, response.Meta.NewestID, nil
}

// Post identifiers are decimal strings. Float conversion loses ordering above
// 2^53, and lexicographic comparison alone misorders different digit lengths.
func compareXPostIDs(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

// A response belongs to one durable reservation. User reconfiguration may run
// while HTTP is pending; it must not inherit a stale cursor or old-account block.
func retainXBlock(paths Paths, reservation FreebieXConfig, code int) (bool, error) {
	applied := false
	err := mutateFreebies(paths, func(s *FreebieState) error {
		if s.X != reservation {
			return nil
		}
		s.X.Blocked = fmt.Sprintf("HTTP %d", code)
		applied = true
		return nil
	})
	return applied, err
}

func publishFreebieReport(paths Paths, report FreebieReport, reservation *FreebieXConfig, cursor string) (FreebieReport, error) {
	err := mutateFreebies(paths, func(s *FreebieState) error {
		if reservation != nil && s.X != *reservation {
			report.XStatus = "superseded"
			report.XError = "X settings changed during this request. Its attempt remains counted; received announcements are retained as unverified history and its cursor is not applied."
			cursor = ""
		}
		// The cursor and the received offers have one publication boundary.
		// Failed validation/storage leaves both old values intact; pre-request
		// usage and any already retained hard blocker are never refunded.
		if report.XStatus == "checked" && cursor != "" {
			s.X.SinceID = cursor
		}
		s.Report = report
		return nil
	})
	return report, err
}

func checkFreebies(ctx context.Context, paths Paths, client *http.Client) (FreebieReport, error) {
	return checkFreebiesWith(ctx, paths, client, nativeFreebieCatalog)
}

func nativeFreebieCatalog(ctx context.Context, paths Paths) ([]string, error) {
	executable := FindTool("opencode", paths)
	if executable == "" {
		return nil, errors.New("OpenCode is not installed")
	}
	a := DetectedAgent{AgentDefinition: Registry[2], Installed: true, Path: executable}
	checkDetectedAgent(ctx, &a, paths, true)
	if a.FreeModels.Status != "verified" {
		return nil, errors.New("OpenCode catalog is unavailable: " + a.FreeModels.Status)
	}
	return a.FreeModels.Models, nil
}

func checkFreebiesWith(ctx context.Context, paths Paths, client *http.Client, native func(context.Context, Paths) ([]string, error)) (FreebieReport, error) {
	if client == nil {
		client = freebieHTTPClient()
	}
	// Only one check may publish at a time. Never hold the state lock during
	// HTTP; enable/disable and read-only status remain responsive.
	if err := paths.Ensure(); err != nil {
		return FreebieReport{}, err
	}
	lock, err := acquireProjectLock(paths.Data, "freebies-check", false)
	if err != nil {
		return FreebieReport{}, err
	}
	defer lock.Close()
	previous, err := readFreebies(paths)
	if err != nil {
		return FreebieReport{}, err
	}
	report := FreebieReport{CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), CatalogStatus: "error", XStatus: "disabled", Offers: []FreebieOffer{}}
	// Prefer the installed adapter's real refreshed catalog. Without OpenCode,
	// public metadata is useful discovery but never authorizes dispatch.
	if native != nil {
		models, nativeErr := native(ctx, paths)
		if nativeErr == nil && len(models) > 0 {
			report.CatalogStatus = "native_catalog"
			for _, model := range models {
				if freebieID.MatchString(model) {
					report.Offers = append(report.Offers, FreebieOffer{ID: model, Source: "opencode", Provider: "opencode", Title: model, URL: "https://opencode.ai/docs/console/models", Verification: "listed_zero_price", ObservedAt: report.CheckedAt})
					if len(report.Offers) == freebieLimit {
						break
					}
				}
			}
		}
	}
	if report.CatalogStatus != "native_catalog" {
		data, _, err := freebieGet(ctx, client, freebieCatalogURL, "")
		if err == nil {
			report.Offers, err = catalogFreebies(data, report.CheckedAt)
		}
		if err != nil {
			report.CatalogError = err.Error()
		} else {
			report.CatalogStatus = "public_catalog"
			report.CatalogError = ""
		}
	}
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	config, status, err := reserveXRequest(paths)
	if err != nil {
		return report, err
	}
	report.XStatus = status
	xOffers := []FreebieOffer{}
	var reservation *FreebieXConfig
	cursor := ""
	if status == "reserved" {
		config.UsedRequests++ // reserveXRequest returned the pre-reservation copy.
		reservation = &config
		query := url.Values{"query": {freebieQuery}, "max_results": {"10"}, "tweet.fields": {"created_at"}}
		if config.SinceID != "" {
			query.Set("since_id", config.SinceID)
		}
		data, code, xerr := freebieGet(ctx, client, freebieXURL+"?"+query.Encode(), os.Getenv(config.TokenEnv))
		var offers []FreebieOffer
		if xerr == nil {
			offers, cursor, xerr = xFreebieOffers(data, report.CheckedAt)
			if xerr == nil && config.SinceID != "" {
				for _, o := range offers {
					if compareXPostIDs(strings.TrimPrefix(o.ID, "x/"), config.SinceID) <= 0 {
						xerr = errors.New("X search returned posts at or before the requested cursor")
						break
					}
				}
			}
		}
		if xerr != nil {
			report.XStatus = "error"
			report.XError = xerr.Error()
			if code == 401 || code == 402 || code == 403 || code == 429 {
				report.XStatus = "blocked"
				applied, err := retainXBlock(paths, config, code)
				if err != nil {
					return report, fmt.Errorf("X returned HTTP %d; could not retain its blocker: %w", code, err)
				}
				if applied {
					config.Blocked = fmt.Sprintf("HTTP %d", code)
				}
			}
		} else {
			report.XStatus = "checked"
			xOffers = offers
		}
	}
	// Keep a bounded announcement inbox, with original observation times. Never
	// retain stale catalog prices as current authorization for model dispatch.
	xOffers = mergeXAnnouncements(xOffers, previous.Report.Offers)
	if len(report.Offers) > freebieLimit-len(xOffers) {
		report.Offers = report.Offers[:freebieLimit-len(xOffers)]
	}
	report.Offers = append(report.Offers, xOffers...)
	report, err = publishFreebieReport(paths, report, reservation, cursor)
	if err != nil {
		return report, err
	}
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	return report, nil
}

func mergeXAnnouncements(current, previous []FreebieOffer) []FreebieOffer {
	result := []FreebieOffer{}
	seen := map[string]bool{}
	firstObserved := map[string]string{}
	for _, o := range previous {
		if o.Source == "x" {
			firstObserved[o.ID] = o.ObservedAt
		}
	}
	for _, list := range [][]FreebieOffer{current, previous} {
		for _, o := range list {
			if o.Source != "x" || o.Verification != "unverified_announcement" || seen[o.ID] {
				continue
			}
			seen[o.ID] = true
			if firstObserved[o.ID] != "" {
				o.ObservedAt = firstObserved[o.ID]
			}
			result = append(result, o)
			if len(result) == 20 {
				return result
			}
		}
	}
	return result
}

func formatFreebies(s FreebieState) string {
	r := s.Report
	var out strings.Builder
	fmt.Fprintf(&out, "Free-offer discovery · watch on app launch: %t · every %s\nCatalog: %s · X: %s\n", s.Watch, time.Duration(s.IntervalSeconds)*time.Second, r.CatalogStatus, r.XStatus)
	if r.CheckedAt != "" {
		fmt.Fprintf(&out, "Checked: %s\n", r.CheckedAt)
	}
	if r.CatalogError != "" {
		fmt.Fprintf(&out, "Catalog: %s\n", r.CatalogError)
	}
	if r.XError != "" {
		fmt.Fprintf(&out, "X: %s\n", r.XError)
	}
	if s.X.Enabled {
		fmt.Fprintf(&out, "X request attempts: %d/%d (persistent, not a dollar spending cap)\n", s.X.UsedRequests, s.X.AllowedRequests)
	}
	preview := freebiePreview(r.Offers)
	for _, o := range preview {
		fmt.Fprintf(&out, "\n%s · %s\n%s\n%s\n", o.Title, o.Verification, o.ID, o.URL)
		if o.ObservedAt != "" {
			fmt.Fprintf(&out, "Observed: %s\n", o.ObservedAt)
		}
	}
	if len(r.Offers) > len(preview) {
		fmt.Fprintf(&out, "\n%d more offers; use freebies status --json for the complete bounded report.\n", len(r.Offers)-len(preview))
	}
	out.WriteString("\nAnnouncements and catalog prices do not prove account quota or successful inference. No offer installs software, changes selected models or resumes a blocked goal. X search is metered and disabled by default. Use /connect or buildheadway setup --install AGENT to approve a supported installation; dispatch independently refreshes prices.")
	return out.String()
}

func freebiePreview(offers []FreebieOffer) []FreebieOffer {
	preview := []FreebieOffer{}
	for _, o := range offers {
		if o.Source == "x" && len(preview) < 10 {
			preview = append(preview, o)
		}
	}
	for _, o := range offers {
		if o.Source != "x" && len(preview) < 20 {
			preview = append(preview, o)
		}
	}
	seenX := 0
	for _, o := range offers {
		if o.Source != "x" {
			continue
		}
		seenX++
		if seenX > 10 && len(preview) < 20 {
			preview = append(preview, o)
		}
	}
	return preview
}
