package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryCLIRejectedWatcherPreservesOwner(t *testing.T) {
	binary := nativeTestBinary(t)
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			paths := testPaths(t)
			if err := configureFreebieWatch(paths, enabled, 15*time.Minute); err != nil {
				t.Fatal(err)
			}
			owner, err := acquireProjectLock(paths.Data, "freebies-watch", false)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			before, err := os.ReadFile(freebieStatePath(paths))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "freebies", "watch", "--interval", "1h", "--json")
			cmd.Dir = t.TempDir()
			cmd.Env = testCommandEnv(filepath.Dir(paths.Data))
			out, err := cmd.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "already using") {
				t.Fatalf("competing watcher did not refuse promptly: %v %s", err, out)
			}
			after, err := os.ReadFile(freebieStatePath(paths))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("a rejected watcher changed its owner's saved settings: %v\nbefore: %s\nafter: %s", err, before, after)
			}
		})
	}
}

func TestDiscoveryTerminalRejectedWatcherPreservesOwner(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			paths := testPaths(t)
			if err := configureFreebieWatch(paths, enabled, 15*time.Minute); err != nil {
				t.Fatal(err)
			}
			owner, err := acquireProjectLock(paths.Data, "freebies-watch", false)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			before, err := os.ReadFile(freebieStatePath(paths))
			if err != nil {
				t.Fatal(err)
			}
			m := newApp(paths, t.TempDir(), "", false, false)
			m.busy, m.stage = true, "Implementing goal"
			m.editor.SetValue("Keep this draft")
			m.startFreebieWatch()
			if m.freebieDone != nil {
				select {
				case <-m.freebieDone:
				case <-time.After(3 * time.Second):
					t.Fatal("competing watcher did not settle")
				}
			}
			after, err := os.ReadFile(freebieStatePath(paths))
			if err != nil || !bytes.Equal(before, after) || m.freebieCancel != nil || m.freebieGeneration != 0 {
				t.Fatalf("a rejected terminal watcher changed ownership/settings: %v, generation=%d\nbefore: %s\nafter: %s", err, m.freebieGeneration, before, after)
			}
			if !m.busy || m.stage != "Implementing goal" || m.editor.Value() != "Keep this draft" {
				t.Fatal("rejecting discovery interrupted the active goal or its draft")
			}
		})
	}
}

func TestDiscoveryCLIWatcherSurvivesCompetingStartAndStops(t *testing.T) {
	binary := nativeTestBinary(t)
	agent, _ := scanFixture(t)
	tools := t.TempDir()
	data, err := os.ReadFile(agent.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, ExecutableName("opencode", runtime.GOOS)), data, 0755); err != nil {
		t.Fatal(err)
	}
	paths := testPaths(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	env := setEnv(testCommandEnv(filepath.Dir(paths.Data)), "BUILDHEADWAY_AGENT_PATH", tools)
	owner := exec.CommandContext(ctx, binary, "freebies", "watch", "--interval", "15m", "--json")
	owner.Dir, owner.Env, owner.Stdout, owner.Stderr = t.TempDir(), env, io.Discard, io.Discard
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer owner.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- owner.Wait() }()
	for {
		state, err := readFreebies(paths)
		if err != nil {
			t.Fatal(err)
		}
		if state.Watch && state.Report.CatalogStatus == "native_catalog" {
			break
		}
		select {
		case err := <-done:
			t.Fatal("owner exited before publishing its first catalog", err)
		default:
		}
		if err := waitContext(ctx, 10*time.Millisecond); err != nil {
			t.Fatal("owner did not publish a catalog", err)
		}
	}
	before, err := os.ReadFile(freebieStatePath(paths))
	if err != nil {
		t.Fatal(err)
	}
	competitor := exec.CommandContext(ctx, binary, "freebies", "watch", "--interval", "1h", "--json")
	competitor.Dir, competitor.Env = owner.Dir, env
	if out, err := competitor.CombinedOutput(); err == nil || !strings.Contains(string(out), "already using") {
		t.Fatalf("competing process was not refused: %v %s", err, out)
	}
	after, err := os.ReadFile(freebieStatePath(paths))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected process changed the live owner's state", err)
	}
	select {
	case err := <-done:
		t.Fatal("rejected process stopped the live owner", err)
	default:
	}
	stop := exec.CommandContext(ctx, binary, "freebies", "disable", "--json")
	stop.Dir, stop.Env = owner.Dir, env
	if out, err := stop.CombinedOutput(); err != nil {
		t.Fatalf("external disable failed: %v %s", err, out)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("owner did not stop cleanly", err)
		}
	case <-ctx.Done():
		t.Fatal("owner ignored external disable")
	}
	state, err := readFreebies(paths)
	if err != nil || state.Watch || state.IntervalSeconds != 900 || state.Report.CatalogStatus != "native_catalog" || state.X.UsedRequests != 0 {
		t.Fatal("shutdown lost saved discovery evidence", state, err)
	}
	lock, err := acquireProjectLock(paths.Data, "freebies-watch", false)
	if err != nil {
		t.Fatal("stopped owner did not release discovery ownership", err)
	}
	lock.Close()
}
