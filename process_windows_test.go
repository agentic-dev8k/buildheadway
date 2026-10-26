//go:build windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestWindowsShellPreservesQuotedChecksAndFailure(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "project with spaces")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "value with spaces.txt")
	command := `findstr /x /c:"good phrase" "value with spaces.txt" >nul && echo quoted-check-passed`
	for _, test := range []struct {
		name, content string
		passed        bool
	}{{"matches", "good phrase\n", true}, {"rejects wrong output", "wrong phrase\n", false}} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := RunProcess(context.Background(), ShellCommand(command), repo, filepath.Join(t.TempDir(), "check.log"), "", nil, 15*time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.passed && (result.Code != 0 || strings.TrimSpace(result.Output) != "quoted-check-passed") {
				t.Fatalf("quoted check failed: code=%d output=%q", result.Code, result.Output)
			}
			if !test.passed && (result.Code == 0 || strings.Contains(result.Output, "quoted-check-passed")) {
				t.Fatalf("failing check was accepted: code=%d output=%q", result.Code, result.Output)
			}
		})
	}
}

func TestWindowsShellArgumentHelper(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_WINDOWS_ARGUMENT_HELPER") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	if index < 0 {
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(os.Args[index+1:]); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestWindowsShellExecutesQuotedPathAndPreservesArguments(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "tools with spaces")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "argument helper.exe")
	if err := os.WriteFile(helper, data, 0700); err != nil {
		t.Fatal(err)
	}
	command := `"` + helper + `" -test.run=^TestWindowsShellArgumentHelper$ -- "two words" "a&b" "" "C:\last\\"`
	env := setEnv(os.Environ(), "BUILDHEADWAY_WINDOWS_ARGUMENT_HELPER", "1")
	result, err := RunProcess(context.Background(), ShellCommand(command), dir, filepath.Join(t.TempDir(), "arguments.log"), "", env, 20*time.Second, nil)
	if err != nil || result.Code != 0 {
		t.Fatalf("quoted executable failed: code=%d output=%q error=%v", result.Code, result.Output, err)
	}
	var got []string
	if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
		t.Fatalf("invalid helper response: %v %q", err, result.Output)
	}
	want := []string{"two words", "a&b", "", `C:\last\`}
	if !slices.Equal(got, want) {
		t.Fatalf("shell changed arguments: got=%q want=%q", got, want)
	}
}

func TestDoctorRejectsShellThatExistsButMasksFailedChecks(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMSPEC", executable)
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", filepath.Join(t.TempDir(), "capture.json"))
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_REPLY", "buildheadway-shell-ok\n")
	result := verificationShellDiagnostic(context.Background())
	if result.Status != "error" || !strings.Contains(result.Detail, "failure probe failed") {
		t.Fatalf("a shell hiding failed checks was accepted: %+v", result)
	}
}
