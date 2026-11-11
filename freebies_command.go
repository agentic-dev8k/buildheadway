package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func configureFreebieWatch(paths Paths, enabled bool, interval time.Duration) error {
	if interval < 15*time.Minute || interval > 24*time.Hour {
		return errors.New("discovery interval must be between 15m and 24h")
	}
	return mutateFreebies(paths, func(s *FreebieState) error {
		s.Watch = enabled
		s.IntervalSeconds = int(interval / time.Second)
		return nil
	})
}

func watchFreebies(ctx context.Context, paths Paths, onReport func(FreebieState)) error {
	return watchFreebiesWith(ctx, paths, onReport, func(ctx context.Context, p Paths) (FreebieReport, error) { return checkFreebies(ctx, p, nil) })
}

func watchFreebiesWith(ctx context.Context, paths Paths, onReport func(FreebieState), check func(context.Context, Paths) (FreebieReport, error)) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	lock, err := acquireProjectLock(paths.Data, "freebies-watch", false)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s, err := readFreebies(paths)
		if err != nil {
			return err
		}
		if !s.Watch {
			return nil
		}
		_, err = check(ctx, paths)
		if err != nil {
			return err
		}
		s, err = readFreebies(paths)
		if err != nil {
			return err
		}
		onReport(s)
		s, err = readFreebies(paths)
		if err != nil {
			return err
		}
		if !s.Watch {
			return nil
		}
		next := time.Now().Add(time.Duration(s.IntervalSeconds) * time.Second)
		info, err := os.Stat(freebieStatePath(paths))
		if err != nil {
			return err
		}
		modified := info.ModTime()
		for time.Now().Before(next) {
			if err := waitContext(ctx, min(time.Second, time.Until(next))); err != nil {
				return err
			}
			info, err := os.Stat(freebieStatePath(paths))
			if err != nil {
				return err
			}
			if !info.ModTime().Equal(modified) {
				s, err = readFreebies(paths)
				if err != nil {
					return err
				}
				if !s.Watch {
					return nil
				}
				modified = info.ModTime()
			}
		}
	}
}

func runFreebiesCommand(paths Paths, args []string) int {
	action := "status"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		action, args = args[0], args[1:]
	}
	f := flag.NewFlagSet("buildheadway freebies "+action, flag.ContinueOnError)
	asJSON := f.Bool("json", false, "Output JSON")
	interval := f.Duration("interval", 30*time.Minute, "Discovery interval, 15m–24h (enable/watch only)")
	tokenEnv := f.String("token-env", "", "Environment variable containing your X bearer token (x-enable only)")
	requests := f.Int("requests", 0, "Add this many explicitly authorized X request attempts (x-enable only)")
	metered := f.Bool("acknowledge-metered", false, "Acknowledge X search may bill your account; this is not free API access")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 {
		return fail(errors.New("freebies accepts one action and named flags"))
	}
	invalid := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == "interval" && action != "enable" && action != "watch" {
			invalid = true
		}
		if (v.Name == "token-env" || v.Name == "requests" || v.Name == "acknowledge-metered") && action != "x-enable" {
			invalid = true
		}
	})
	if invalid {
		return fail(errors.New("these flags do not apply to the chosen freebie action"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var err error
	switch action {
	case "status":
	case "check":
		_, err = checkFreebies(ctx, paths, nil)
	case "enable", "watch":
		err = configureFreebieWatch(paths, true, *interval)
		if err == nil && action == "watch" {
			defer func() { _ = mutateFreebies(paths, func(s *FreebieState) error { s.Watch = false; return nil }) }()
			err = watchFreebies(ctx, paths, func(s FreebieState) { _ = outputValue(s, formatFreebies(s), *asJSON) })
			if errors.Is(err, context.Canceled) {
				return 0
			}
			if err != nil {
				return fail(err)
			}
			return 0
		}
	case "disable":
		err = mutateFreebies(paths, func(s *FreebieState) error { s.Watch = false; return nil })
	case "x-enable":
		if !*metered || !freebieEnv.MatchString(*tokenEnv) || *requests < 1 || *requests > 1000 {
			return fail(errors.New("X search is metered: use x-enable --token-env NAME --requests 1..1000 --acknowledge-metered; this caps attempts, not dollars; set spending limits with X"))
		}
		err = mutateFreebies(paths, func(s *FreebieState) error {
			if s.X.AllowedRequests > 1000000-*requests {
				return errors.New("X request budget exceeds its bound")
			}
			s.X.Enabled = true
			s.X.TokenEnv = *tokenEnv
			s.X.AllowedRequests += *requests
			s.X.Blocked = ""
			return nil
		})
	case "x-disable":
		err = mutateFreebies(paths, func(s *FreebieState) error { s.X.Enabled = false; return nil })
	default:
		return fail(errors.New("use freebies status, check, watch, enable, disable, x-enable or x-disable"))
	}
	if err != nil {
		return fail(err)
	}
	s, err := readFreebies(paths)
	if err != nil {
		return fail(err)
	}
	if err := outputValue(s, formatFreebies(s), *asJSON); err != nil {
		return fail(err)
	}
	if action == "check" && (s.Report.CatalogStatus == "error" || s.Report.XStatus == "blocked" || s.Report.XStatus == "error" || s.Report.XStatus == "missing_credential") {
		return 1
	}
	return 0
}

type freebieInspectionMsg struct {
	State FreebieState
	Err   error
}
type freebieWatchStoppedMsg struct {
	Generation int
	Err        error
}

func (m *appModel) checkFreebieOffers() tea.Cmd {
	paths := m.paths
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, err := checkFreebies(ctx, paths, nil)
		s, readErr := readFreebies(paths)
		return freebieInspectionMsg{s, errors.Join(err, readErr)}
	}
}
func (m *appModel) startFreebieWatch() tea.Cmd {
	if m.freebieCancel != nil {
		m.add("Discovery", "The free-offer watcher is already running.")
		return nil
	}
	state, err := readFreebies(m.paths)
	if err != nil {
		m.add("Discovery", err.Error())
		return nil
	}
	if err := configureFreebieWatch(m.paths, true, time.Duration(state.IntervalSeconds)*time.Second); err != nil {
		m.add("Discovery", err.Error())
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.freebieCancel = cancel
	m.freebieDone = make(chan struct{})
	m.freebieGeneration++
	generation, paths, events, done := m.freebieGeneration, m.paths, m.events, m.freebieDone
	m.add("Discovery", "Watching official free-model catalogs while BuildHeadway is open. X search stays disabled unless explicitly configured with a metered request budget. /freebies stop disables this watcher; coding goals keep their own budgets and blockers.")
	go func() {
		defer close(done)
		err := watchFreebies(ctx, paths, func(s FreebieState) {
			select {
			case events <- Event{Kind: "freebies", Value: freebieInspectionMsg{s, nil}}:
			case <-ctx.Done():
			}
		})
		select {
		case events <- Event{Kind: "freebies-stopped", Value: freebieWatchStoppedMsg{generation, err}}:
		case <-ctx.Done():
		}
	}()
	return nil
}
func (m *appModel) stopFreebieWatch() {
	if err := mutateFreebies(m.paths, func(s *FreebieState) error { s.Watch = false; return nil }); err != nil {
		m.add("Discovery", err.Error())
		return
	}
	if m.freebieCancel != nil {
		m.freebieCancel()
		m.freebieCancel = nil
	}
	m.add("Discovery", "Free-offer watching is disabled. Active coding goals and their checkpoints are unchanged.")
}
