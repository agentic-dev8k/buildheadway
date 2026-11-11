package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDiscoveryCLIConfiguresWithoutNetworkModelsOrCredentialStorage(t *testing.T) {
	binary := nativeTestBinary(t)
	home := filepath.Join(t.TempDir(), "user data")
	project := t.TempDir()
	run := func(want bool, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Dir = project
		cmd.Env = append(testCommandEnv(home), "TEST_X_DISCOVERY_TOKEN=NEVER_SAVE_THIS_VALUE")
		out, err := cmd.CombinedOutput()
		if (err == nil) != want {
			t.Fatalf("%v: %v %s", args, err, out)
		}
		return out
	}
	var state FreebieState
	if err := json.Unmarshal(run(true, "freebies", "--json"), &state); err != nil || state.X.Enabled || state.Watch {
		t.Fatal(state, err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("read-only status created settings")
	}
	for _, args := range [][]string{{"setup", "--install="}, {"setup", "--install", "not-an-agent"}, {"freebies", "x-enable", "--token-env", "TEST_X_DISCOVERY_TOKEN", "--requests", "1"}, {"freebies", "status", "--requests", "1"}, {"freebies", "enable", "--interval", "1s"}} {
		run(false, args...)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("rejected input created state")
	}
	json.Unmarshal(run(true, "freebies", "x-enable", "--token-env", "TEST_X_DISCOVERY_TOKEN", "--requests", "2", "--acknowledge-metered", "--json"), &state)
	if !state.X.Enabled || state.X.UsedRequests != 0 || state.X.AllowedRequests != 2 || state.X.TokenEnv != "TEST_X_DISCOVERY_TOKEN" {
		t.Fatal(state)
	}
	run(true, "freebies", "enable", "--interval", "15m", "--json")
	run(true, "freebies", "disable", "--json")
	json.Unmarshal(run(true, "freebies", "x-disable", "--json"), &state)
	if state.Watch || state.X.Enabled || state.X.AllowedRequests != 2 {
		t.Fatal(state)
	}
	run(true, "setup", "--decline-install", "--json")
	path := filepath.Join(home, "data", "freebies.json")
	before, err := os.ReadFile(path)
	if err != nil || bytes.Contains(before, []byte("NEVER_SAVE_THIS_VALUE")) {
		t.Fatal(err, string(before))
	}
	run(true, "freebies", "status", "--json")
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("status changed state")
	}
	if _, err := os.Stat(filepath.Join(home, "config", "connection.json")); !os.IsNotExist(err) {
		t.Fatal("discovery installed or connected agents")
	}
}
