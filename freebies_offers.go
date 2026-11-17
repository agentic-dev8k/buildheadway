package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type FreebieOfferQuery struct {
	Search   string `json:"search,omitempty"`
	Source   string `json:"source"`
	Provider string `json:"provider,omitempty"`
	Limit    int    `json:"limit"`
}

type FreebieOfferListing struct {
	CheckedAt     string            `json:"checked_at"`
	CatalogStatus string            `json:"catalog_status"`
	CatalogError  string            `json:"catalog_error,omitempty"`
	XStatus       string            `json:"x_status"`
	XError        string            `json:"x_error,omitempty"`
	Query         FreebieOfferQuery `json:"query"`
	Total         int               `json:"total"`
	Matched       int               `json:"matched"`
	Offers        []FreebieOffer    `json:"offers"`
}

func normalizeFreebieOfferQuery(q FreebieOfferQuery) (FreebieOfferQuery, error) {
	if !utf8.ValidString(q.Search) || len(q.Search) > 256 || strings.IndexFunc(q.Search, unicode.IsControl) >= 0 {
		return q, errors.New("offer search must be valid UTF-8 of at most 256 bytes without control characters")
	}
	q.Search = strings.TrimSpace(q.Search)
	q.Source = strings.ToLower(strings.TrimSpace(q.Source))
	if q.Source == "" {
		q.Source = "all"
	}
	if q.Source != "all" && q.Source != "catalog" && q.Source != "x" {
		return q, errors.New("offer source must be all, catalog or x")
	}
	q.Provider = strings.ToLower(strings.TrimSpace(q.Provider))
	if q.Provider != "" && !freebieID.MatchString(q.Provider) {
		return q, errors.New("offer provider must be an exact catalog provider identifier")
	}
	if q.Limit < 1 || q.Limit > freebieLimit {
		return q, errors.New("offer limit must be between 1 and 100")
	}
	return q, nil
}

// Inspection reads one complete saved report. It never refreshes prices, scans
// executables, reserves X requests or changes a watcher or goal checkpoint.
func inspectFreebieOffers(paths Paths, query FreebieOfferQuery) (FreebieOfferListing, error) {
	q, err := normalizeFreebieOfferQuery(query)
	if err != nil {
		return FreebieOfferListing{}, err
	}
	state, err := readFreebies(paths)
	if err != nil {
		return FreebieOfferListing{}, err
	}
	r := state.Report
	listing := FreebieOfferListing{CheckedAt: r.CheckedAt, CatalogStatus: r.CatalogStatus, CatalogError: r.CatalogError, XStatus: r.XStatus, XError: r.XError, Query: q, Total: len(r.Offers), Offers: []FreebieOffer{}}
	search := strings.ToLower(q.Search)
	for _, offer := range r.Offers {
		catalog := offer.Source == "opencode" || offer.Source == "models.dev"
		if (q.Source == "catalog" && !catalog) || (q.Source == "x" && offer.Source != "x") {
			continue
		}
		if q.Provider != "" && !strings.EqualFold(offer.Provider, q.Provider) {
			continue
		}
		text := strings.Join([]string{offer.ID, offer.Title, offer.Provider, offer.Source, offer.Verification, offer.URL}, "\n")
		if search != "" && !strings.Contains(strings.ToLower(text), search) {
			continue
		}
		listing.Matched++
		if len(listing.Offers) < q.Limit {
			listing.Offers = append(listing.Offers, offer)
		}
	}
	return listing, nil
}

func formatFreebieOffers(listing FreebieOfferListing) string {
	return formatFreebieOffersAt(listing, time.Now())
}
func formatFreebieOffersAt(listing FreebieOfferListing, now time.Time) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Saved offers · showing %d of %d matches · %d saved\nCatalog: %s · X: %s\n", len(listing.Offers), listing.Matched, listing.Total, freebieTitle(listing.CatalogStatus), freebieTitle(listing.XStatus))
	if listing.CheckedAt != "" {
		fmt.Fprintf(&out, "Report checked: %s\n", freebieTitle(listing.CheckedAt))
	}
	fmt.Fprintf(&out, "Last check age: %s (check attempt, not offer freshness)\n", freebieAge(listing.CheckedAt, now))
	if listing.CatalogError != "" {
		fmt.Fprintf(&out, "Catalog: %s\n", freebieTitle(listing.CatalogError))
	}
	if listing.XError != "" {
		fmt.Fprintf(&out, "X: %s\n", freebieTitle(listing.XError))
	}
	if listing.Total == 0 {
		out.WriteString("\nNo saved offers. Use freebies check to refresh discovery.\n")
	} else if listing.Matched == 0 {
		out.WriteString("\nNo saved offers match these filters.\n")
	}
	for _, offer := range listing.Offers {
		fmt.Fprintf(&out, "\n%s · %s · %s\n%s\n%s\n", freebieTitle(offer.Title), freebieTitle(offer.Source), freebieTitle(offer.Verification), freebieTitle(offer.ID), freebieTerminalLine(offer.URL, 2048))
		if offer.ObservedAt != "" {
			fmt.Fprintf(&out, "Observed: %s\n", freebieTitle(offer.ObservedAt))
		}
		fmt.Fprintf(&out, "Observed age: %s\n", freebieAge(offer.ObservedAt, now))
	}
	if listing.Matched > len(listing.Offers) {
		fmt.Fprintf(&out, "\n%d more matches; increase --limit up to 100.\n", listing.Matched-len(listing.Offers))
	}
	out.WriteString("\nThis is a saved report. Inspection does not refresh prices or prove account quota or inference. Announcements remain unverified; offers never install software, change models or resume blocked goals.")
	return out.String()
}

func runFreebieOffersCommand(paths Paths, args []string) int {
	f := flag.NewFlagSet("buildheadway freebies offers", flag.ContinueOnError)
	search := f.String("search", "", "Case-insensitive text search of the saved report")
	source := f.String("source", "all", "Offer source: all, catalog or x")
	provider := f.String("provider", "", "Exact catalog provider identifier")
	limit := f.Int("limit", 20, "Maximum matching offers to display, 1–100")
	asJSON := f.Bool("json", false, "Output JSON")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 {
		return fail(errors.New("use freebies offers with named --search, --source, --provider and --limit filters"))
	}
	listing, err := inspectFreebieOffers(paths, FreebieOfferQuery{Search: *search, Source: *source, Provider: *provider, Limit: *limit})
	if err != nil {
		return fail(err)
	}
	if err := outputValue(listing, formatFreebieOffers(listing), *asJSON); err != nil {
		return fail(err)
	}
	return 0
}
