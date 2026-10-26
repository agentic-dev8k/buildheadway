package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Workflow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	Body        string `json:"body"`
}

var workflowName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var workflowArg = regexp.MustCompile(`\$ARGUMENTS\b|\$@|\$[1-9][0-9]*|\$\{([1-9][0-9]*|@):-([^}]*)\}`)

func builtinWorkflows() []Workflow {
	return []Workflow{
		{"review", "Review changes, repair defects, and verify the fixes", "builtin", "built-in", "Review this project's current changes. Focus: ${@:-correctness, security, compatibility, and missing tests}. Inspect the actual diff and relevant code, ask the senior team to challenge findings, fix supported defects, and verify the affected behavior. Preserve unrelated work. Report findings with file references and remaining risks."},
		{"debug", "Reproduce a bug, find its cause, and fix it", "builtin", "built-in", "Diagnose and repair: $ARGUMENTS\nFirst reproduce the problem or gather concrete evidence. Have the team compare plausible causes. Implement a focused fix, add a meaningful regression check where appropriate, and run affected tests. Do not conceal a failure by weakening checks. Report the cause and verification."},
		{"test", "Find important coverage gaps and add meaningful checks", "builtin", "built-in", "Improve verification for ${@:-the project's most important behavior}. Inspect the existing test framework and public behavior. Prioritize realistic failure cases and regressions, add deterministic checks that can fail for a real defect, and run them. Fix genuine defects exposed by those checks. Avoid tests that merely duplicate the implementation."},
		{"docs", "Bring setup, usage, and examples in line with the code", "builtin", "built-in", "Improve documentation for ${@:-this project}. Inspect the actual implementation, commands and configuration. Make setup and ordinary usage straightforward, update concrete examples and troubleshooting, and verify the documented commands where practical. Report what was checked; never invent successful execution."},
	}
}

// Discovery reads text only. It never executes frontmatter or expands shell
// substitutions. Project resources are checked against their discovery root.
func DiscoverWorkflows(paths Paths, repo string) ([]Workflow, error) {
	items := map[string]Workflow{}
	for _, w := range builtinWorkflows() {
		items[w.Name] = w
	}
	for _, scope := range []struct{ root, prompts, skills string }{
		{paths.Config, "prompts", "skills"}, {repo, ".buildheadway-prompts", ".buildheadway-skills"},
	} {
		if scope.root == "" {
			continue
		}
		absolute, err := filepath.Abs(scope.root)
		if err != nil {
			return nil, err
		}
		scope.root = absolute
		level := map[string]bool{}
		for _, kind := range []string{"prompt", "skill"} {
			subdir := scope.prompts
			if kind == "skill" {
				subdir = scope.skills
			}
			dir := filepath.Join(scope.root, subdir)
			entries, err := os.ReadDir(dir)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if len(entries) > 200 {
				return nil, fmt.Errorf("too many workflow entries in %s", dir)
			}
			for _, entry := range entries {
				name, path := strings.TrimSuffix(entry.Name(), ".md"), filepath.Join(dir, entry.Name())
				if kind == "skill" {
					if !entry.IsDir() {
						continue
					}
					name, path = entry.Name(), filepath.Join(path, "SKILL.md")
				} else if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
					continue
				}
				if !workflowName.MatchString(name) {
					return nil, fmt.Errorf("workflow name %q must use lowercase letters, digits and hyphens", name)
				}
				if level[name] {
					return nil, fmt.Errorf("duplicate workflow %q in %s; use distinct skill and prompt names", name, scope.root)
				}
				body, err := readWorkflowFile(scope.root, path)
				if err != nil {
					return nil, err
				}
				w, err := parseWorkflow(name, kind, path, body)
				if err != nil {
					return nil, err
				}
				level[name], items[name] = true, w
			}
		}
	}
	out := make([]Workflow, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func readWorkflowFile(root, path string) (string, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("workflow escapes its configuration folder: %s", path)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("workflow must be a regular file: %s", path)
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("workflow must be a regular file: %s", path)
	}
	data, err := ReadLimited(file, 64<<10)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return "", fmt.Errorf("workflow must be UTF-8 text: %s", path)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}
func parseWorkflow(name, kind, path, body string) (Workflow, error) {
	w := Workflow{Name: name, Kind: kind, Source: path}
	if strings.HasPrefix(body, "---\n") {
		header, rest, ok := strings.Cut(body[4:], "\n---\n")
		if !ok {
			return w, fmt.Errorf("unclosed workflow frontmatter: %s", path)
		}
		for _, line := range strings.Split(header, "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), "\"'")
			switch strings.TrimSpace(key) {
			case "name":
				if value != name {
					return w, fmt.Errorf("workflow name must match its file or folder: %s", path)
				}
			case "description":
				if value == "|" || value == ">" {
					return w, fmt.Errorf("use a single-line description: %s", path)
				}
				w.Description = value
			}
		}
		body = rest
	}
	w.Body = strings.TrimSpace(body)
	if w.Body == "" {
		return w, fmt.Errorf("empty workflow: %s", path)
	}
	if w.Description == "" {
		w.Description, _, _ = strings.Cut(w.Body, "\n")
	}
	if len(w.Description) > 300 {
		w.Description = string([]rune(w.Description)[:min(150, len([]rune(w.Description)))])
	}
	return w, nil
}

// Quotes group positional arguments; backslashes remain literal for Windows
// paths. No environment expansion, command substitution or shell execution.
func workflowArguments(text string) ([]string, error) {
	var args []string
	var word strings.Builder
	quote, started := rune(0), false
	for _, c := range text {
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote, started = c, true
			continue
		}
		if c == ' ' || c == '\n' || c == '\t' || c == '\r' {
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		started = true
		word.WriteRune(c)
	}
	if quote != 0 {
		return nil, errors.New("close the quote in your workflow arguments")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}
func ExpandWorkflow(paths Paths, repo, name string, args []string) (string, error) {
	items, err := DiscoverWorkflows(paths, repo)
	if err != nil {
		return "", err
	}
	for _, w := range items {
		if w.Name != name {
			continue
		}
		hasArgs := workflowArg.MatchString(w.Body)
		body := workflowArg.ReplaceAllStringFunc(w.Body, func(token string) string {
			key, fallback := strings.TrimPrefix(token, "$"), ""
			if strings.HasPrefix(key, "{") {
				key, fallback, _ = strings.Cut(strings.TrimSuffix(key[1:], "}"), ":-")
			}
			value := ""
			if key == "@" || key == "ARGUMENTS" {
				value = strings.Join(args, " ")
			} else if n, err := strconv.Atoi(key); err == nil && n > 0 && n <= len(args) {
				value = args[n-1]
			}
			if value == "" {
				return fallback
			}
			return value
		})
		if !hasArgs && len(args) > 0 {
			body += "\n\nUser request: " + strings.Join(args, " ")
		}
		if len(body) > 80<<10 {
			return "", errors.New("expanded workflow exceeds 80 KiB")
		}
		prefix := fmt.Sprintf("Workflow: %s\nSource: %s\n", w.Name, w.Source)
		if w.Kind == "skill" {
			prefix += "Supporting files are in: " + filepath.Dir(w.Source) + ". Read them as needed for this goal.\n"
		}
		return prefix + "\n" + body, nil
	}
	return "", fmt.Errorf("unknown workflow %q; use buildheadway workflows to list available names", name)
}
func FormatWorkflows(items []Workflow) string {
	var out strings.Builder
	out.WriteString("Run /workflow NAME [arguments] or buildheadway workflow NAME [arguments].\nProject resources override personal resources and built-ins.\n\n")
	for _, w := range items {
		fmt.Fprintf(&out, "%s · %s\n  %s\n  %s\n", w.Name, w.Kind, w.Description, w.Source)
	}
	return strings.TrimSpace(out.String())
}
