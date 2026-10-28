package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCompletionRespectsCommandPositionAndFlagValues(t *testing.T) {
	for _, tc := range []struct {
		words []string
		want  []string
	}{
		{[]string{"fig"}, []string{"figure-out", "figureout"}},
		{[]string{"mcp", "ad"}, []string{"add"}},
		{[]string{"mcp", "add", "--to"}, []string{"--token-env"}},
		{[]string{"mcp", "list", "--to"}, []string{}},
		{[]string{"extensions", "init", "--kind", "p"}, []string{"prompt"}},
		{[]string{"figureout", "--access", "wo"}, []string{"workspace"}},
		{[]string{"figureout", "--access=wo"}, []string{"--access=workspace"}},
		{[]string{"figureout", "--project", "folder with spaces", "--max-p"}, []string{"--max-planner-calls", "--max-prompt-bytes"}},
		{[]string{"figureout", "--project", "--max-"}, []string{}},
		{[]string{"figureout", "Build app", "--max-"}, []string{}},
		{[]string{"figureout", "--", "--max-"}, []string{}},
		{[]string{"attach", "claude", "--mo"}, []string{"--model"}},
		{[]string{"queue", "--search", "needles", "arc"}, []string{"archive", "archives"}},
		{[]string{"queue", "archives", "--se"}, []string{}},
		{[]string{"mcp", "--json", "ad"}, []string{}},
		{[]string{"tasks", "--ta"}, []string{"--task"}},
		{[]string{"history", "--ta"}, []string{}},
		{[]string{"completion", "power"}, []string{"powershell"}},
	} {
		t.Run(strings.Join(tc.words, " "), func(t *testing.T) {
			if got := completionCandidates(tc.words); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	for _, words := range [][]string{nil, {strings.Repeat("x", 65537)}, {"bad\nword"}, make([]string, 257)} {
		if len(completionCandidates(words)) != 0 {
			t.Fatal("unbounded/invalid input accepted")
		}
	}
}

func TestCompletionBackendDoesNotLoadProjectOrCreateSettings(t *testing.T) {
	binary := nativeTestBinary(t)
	project := t.TempDir()
	home := filepath.Join(t.TempDir(), "must not exist")
	os.WriteFile(filepath.Join(project, ".buildheadway.json"), []byte("broken config"), 0600)
	cmd := exec.Command(binary, "__complete", "mcp", "--current=ad")
	cmd.Dir = project
	cmd.Env = testCommandEnv(home)
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "add\n" {
		t.Fatalf("completion read project state: %v %s", err, out)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("completion created settings")
	}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		cmd = exec.Command(binary, "completion", shell)
		cmd.Dir = project
		cmd.Env = testCommandEnv(home)
		out, err = cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "__complete") {
			t.Fatal(shell, err, string(out))
		}
	}
	cmd = exec.Command(binary, "completion", "unknown")
	if err := cmd.Run(); err == nil {
		t.Fatal("unsupported shell succeeded")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("script generation created settings")
	}
}

func TestGeneratedCompletionWithAvailableShells(t *testing.T) {
	binary := nativeTestBinary(t)
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			name := shell
			if shell == "powershell" {
				name = "pwsh"
				if runtime.GOOS == "windows" {
					name = "powershell.exe"
				}
			}
			executable, err := exec.LookPath(name)
			if err != nil {
				t.Skip("shell is not installed on this host")
			}
			script, err := completionScript(shell)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "completion."+shell)
			if err := os.WriteFile(file, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			var args []string
			switch shell {
			case "bash":
				args = []string{"--noprofile", "--norc", "-c", `. "$1"; COMP_WORDS=(buildheadway mcp ad); COMP_CWORD=2; _buildheadway_complete; printf '%s\n' "${COMPREPLY[@]}"`, "bash", file}
			case "zsh":
				args = []string{"-f", "-c", `autoload -Uz compinit; compinit -D; source "$1"; compadd() { shift 2; print -rl -- "$@"; }; words=(buildheadway mcp ad); CURRENT=3; _buildheadway_complete`, "zsh", file}
			case "fish":
				args = []string{"--no-config", "-c", `source "$argv[1]"; complete -C 'buildheadway mcp ad'`, file}
			case "powershell":
				args = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `. $env:BUILDHEADWAY_COMPLETION_FILE; $line='buildheadway mcp ad'; (TabExpansion2 $line $line.Length).CompletionMatches | ForEach-Object { $_.CompletionText }`}
			}
			cmd := exec.Command(executable, args...)
			cmd.Env = setEnv(os.Environ(), "PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
			cmd.Env = setEnv(cmd.Env, "BUILDHEADWAY_COMPLETION_FILE", file)
			out, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != "add" {
				t.Fatalf("generated %s completion failed: %v\n%s", shell, err, out)
			}
		})
	}
}
