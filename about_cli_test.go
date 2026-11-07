package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeAboutCreditsIgnoreBrokenSettingsAndProjectState(t *testing.T) {
	binary, repo := nativeTestBinary(t), t.TempDir()
	blockedHome := filepath.Join(t.TempDir(), "unavailable user home")
	writeFixture(t, blockedHome, "preserve settings path")
	config := filepath.Join(repo, ".buildheadway.json")
	checkpoint := filepath.Join(repo, ".forge", "runs", "saved", "state.json")
	writeFixture(t, config, "invalid project configuration")
	writeFixture(t, checkpoint, "preserve checkpoint bytes")
	invoke := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, binary, args...)
		c.Dir, c.Env = repo, setEnv(testCommandEnv(blockedHome), "PATH", t.TempDir())
		return c.CombinedOutput()
	}
	first, err := invoke("about", "--json")
	if err != nil {
		t.Fatalf("offline about command unavailable: %v %s", err, first)
	}
	var report struct {
		Name          string `json:"name"`
		Version       string `json:"version"`
		AgentProjects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"agent_projects"`
	}
	if err := json.Unmarshal(first, &report); err != nil || report.Name != "BuildHeadway" || report.Version != Version || len(report.AgentProjects) != 6 {
		t.Fatalf("invalid about report: %s", first)
	}
	alias, err := invoke("credits", "--json")
	if err != nil || string(first) != string(alias) {
		t.Fatal("credits alias changed the offline report", err)
	}
	filtered, err := invoke("credits", "--agent", "claude", "--json")
	if err := json.Unmarshal(filtered, &report); err != nil || len(report.AgentProjects) != 1 || report.AgentProjects[0].Name != "Claude Code" {
		t.Fatalf("Claude credit selection unavailable: %v %s", err, filtered)
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"about", "--agent", ""}, {"about", "--agent", "qwen"}, {"credits", "claude"}, {"about", "--typo"}} {
		if out, err := invoke(args...); err == nil {
			t.Fatalf("invalid credits arguments accepted: %v %s", args, out)
		}
	}
	text, err := invoke("about", "--agent", "claude")
	if err != nil || !strings.Contains(string(text), "Claude Code") || !strings.Contains(string(text), "https://github.com/anthropics/claude-code") {
		t.Fatalf("missing visible Claude acknowledgment: %v %s", err, text)
	}
	for path, want := range map[string]string{blockedHome: "preserve settings path", config: "invalid project configuration", checkpoint: "preserve checkpoint bytes"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Fatal("metadata inspection modified a saved file", path, err)
		}
	}
}
