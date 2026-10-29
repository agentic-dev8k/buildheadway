package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Completion only describes fixed CLI syntax. It never loads project settings,
// scans agents, runs a model, reads credentials or rewrites shell profiles.
type completionFlag struct {
	name    string
	value   bool
	choices []string
}
type completionNode struct {
	flags            []completionFlag
	choices          []string
	children         map[string]completionNode
	flagsAfterChoice bool
	childFirst       bool
	childFlags       bool
}

func completionTree() completionNode {
	f := func(name string, value bool) completionFlag { return completionFlag{name: name, value: value} }
	project := []completionFlag{f("--project", true), f("--json", false)}
	inspection := append(append([]completionFlag{}, project...), f("--task", true), f("--search", true), f("--limit", true))
	run := append(append([]completionFlag{}, project...), f("--max-cycles", true), f("--max-planner-calls", true), f("--max-worker-calls", true), f("--max-stalled-cycles", true), f("--max-runtime", true), f("--max-prompt-bytes", true))
	root := completionNode{childFlags: true, flags: []completionFlag{f("--help", false), f("--version", false), f("--demo", false), f("--plain", false), f("--project", true), f("--choose-project", false), f("--self-test", false)}, children: map[string]completionNode{}}
	add := func(names string, node completionNode) {
		for _, name := range strings.Fields(names) {
			root.children[name] = node
		}
	}
	add("run plan resume", completionNode{flags: run})
	figure := append(append([]completionFlag{}, run...), completionFlag{"--access", true, []string{"full", "workspace"}})
	add("figureout figure-out", completionNode{flags: figure})
	add("status events discussion acceptance routing context", completionNode{flags: project})
	add("history", completionNode{flags: append(append([]completionFlag{}, project...), f("--search", true), f("--limit", true))})
	add("tasks", completionNode{flags: inspection[:len(inspection)-2]})
	add("doctor scan", completionNode{flags: []completionFlag{f("--json", false)}})
	add("install", completionNode{flags: []completionFlag{f("--dir", true), f("--no-path", false), f("--launch", false)}})
	add("update", completionNode{flags: []completionFlag{f("--from", true), f("--version", true)}})
	add("uninstall version", completionNode{})
	add("attach", completionNode{flags: []completionFlag{f("--model", true), f("--provider", true), f("--reasoning", true), f("--name", true), f("--lead", false), f("--json", false)}, choices: []string{"codex", "claude", "opencode", "pi", "cline", "gemini"}, flagsAfterChoice: true})
	add("detach team", completionNode{flags: []completionFlag{f("--json", false)}})
	add("pause init config memory remember buildheadwayt check diff report next workflows skills prompts hooks steering", completionNode{flags: project})
	add("export", completionNode{flags: append(append([]completionFlag{}, project...), f("--output", true), f("--force", false))})
	add("steer", completionNode{flags: []completionFlag{f("--project", true), f("--run", true)}})
	add("logs", completionNode{flags: append(append([]completionFlag{}, project...), f("--run", true), f("--last-check", false), f("--bytes", true), f("--offset", true))})
	add("workflow skill prompt", completionNode{flags: append(append([]completionFlag{}, project...), f("--preview", false))})
	queue := completionNode{flags: project, children: map[string]completionNode{}}
	queue.flags = append(append([]completionFlag{}, project...), f("--search", true), f("--after", true), f("--limit", true))
	for _, action := range strings.Fields("list add figureout remove move run next prune archive archives") {
		queue.children[action] = completionNode{}
	}
	root.children["queue"] = queue
	mcp := completionNode{flags: project, children: map[string]completionNode{}, childFirst: true, childFlags: true}
	for _, action := range strings.Fields("list add check enable disable remove") {
		mcp.children[action] = completionNode{flags: project}
	}
	mcp.children["add"] = completionNode{flags: append(append([]completionFlag{}, project...), f("--url", true), f("--token-env", true), f("--timeout", true), f("--env", true))}
	mcp.children["check"] = completionNode{flags: append(append([]completionFlag{}, project...), f("--timeout", true))}
	root.children["mcp"] = mcp
	ext := completionNode{flags: project, children: map[string]completionNode{}, childFirst: true, childFlags: true}
	ext.children["list"] = completionNode{flags: project}
	ext.children["init"] = completionNode{flags: append(append([]completionFlag{}, project...), f("--user", false), completionFlag{"--kind", true, []string{"skill", "prompt"}})}
	root.children["extensions"] = ext
	root.children["completion"] = completionNode{choices: []string{"bash", "zsh", "fish", "powershell"}}
	return root
}

func completionCandidates(words []string) []string {
	empty := []string{}
	if len(words) == 0 || len(words) > 256 {
		return empty
	}
	bytes := 0
	for _, w := range words {
		bytes += len(w)
		if bytes > 65536 || strings.ContainsAny(w, "\x00\n\r") {
			return empty
		}
	}
	node := completionTree()
	options := true
	position := 0
	children := true
	var pending *completionFlag
	for _, word := range words[:len(words)-1] {
		if pending != nil {
			pending = nil
			continue
		}
		if word == "--" {
			return empty
		}
		if strings.HasPrefix(word, "-") {
			if !options {
				return empty
			}
			name, _, assigned := strings.Cut(word, "=")
			found := false
			for _, flag := range node.flags {
				if flag.name == name {
					found = true
					if flag.value && !assigned {
						copy := flag
						pending = &copy
					}
					break
				}
			}
			if name == "--help" || name == "-h" {
				return empty
			}
			if !found {
				return empty
			}
			if node.childFirst {
				children = false
			}
			continue
		}
		if child, ok := node.children[word]; ok && children && position == 0 {
			// queue's flags must precede its action; MCP/extension flags follow it.
			if !node.childFlags {
				options = false
			} else {
				options = true
			}
			node = child
			position = 0
			children = true
			continue
		}
		position++
		children = false
		if !node.flagsAfterChoice {
			options = false
		}
	}
	current := words[len(words)-1]
	values := []string{}
	if pending != nil {
		values = append(values, pending.choices...)
	} else if options {
		if name, prefix, assigned := strings.Cut(current, "="); assigned {
			for _, flag := range node.flags {
				if flag.name == name {
					for _, v := range flag.choices {
						if strings.HasPrefix(v, prefix) {
							values = append(values, name+"="+v)
						}
					}
					sort.Strings(values)
					return values
				}
			}
			return empty
		}
		for _, flag := range node.flags {
			values = append(values, flag.name)
		}
		values = append(values, "--help")
	}
	if pending == nil && position == 0 && !strings.HasPrefix(current, "-") {
		values = append(values, node.choices...)
		if children {
			for name := range node.children {
				values = append(values, name)
			}
		}
	}
	out := []string{}
	for _, value := range Unique(values) {
		if strings.HasPrefix(value, current) {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func completionScript(shell string) (string, error) {
	switch shell {
	case "bash":
		return `# BuildHeadway completion for Bash 3.2+; generated without changing profiles.
_buildheadway_complete() {
  local candidate
  COMPREPLY=()
  while IFS= read -r candidate; do
    [ -n "$candidate" ] && COMPREPLY+=("$candidate")
  done < <(command buildheadway __complete "${COMP_WORDS[@]:1:$((COMP_CWORD - 1))}" "--current=${COMP_WORDS[COMP_CWORD]}")
  return 0
}
complete -o default -F _buildheadway_complete buildheadway
`, nil
	case "zsh":
		return `# BuildHeadway completion; load after compinit.
_buildheadway_complete() {
  local -a candidates
  candidates=("${(@f)$(command buildheadway __complete "${words[@]:1:$((CURRENT - 2))}" "--current=${words[CURRENT]}")}")
  compadd -Q -- "${candidates[@]}"
}
compdef _buildheadway_complete buildheadway
`, nil
	case "fish":
		return `# BuildHeadway completion; no project or model access.
function __buildheadway_complete
  set -l words (commandline -opc)
  set -l current (commandline -ct)
  command buildheadway __complete $words[2..-1] "--current=$current"
end
complete -c buildheadway -f -a '(__buildheadway_complete)'
`, nil
	case "powershell":
		return `# BuildHeadway completion for PowerShell 5.1+ and pwsh.
Register-ArgumentCompleter -Native -CommandName buildheadway -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $words = @()
  foreach ($element in $commandAst.CommandElements | Select-Object -Skip 1) {
    if ($element.Extent.StartOffset -ge $cursorPosition) { break }
    if ($element.Extent.EndOffset -ge $cursorPosition) { break }
    if ($element -isnot [System.Management.Automation.Language.StringConstantExpressionAst]) { return }
    $words += $element.Value
  }
  $words += "--current=$wordToComplete"
  & buildheadway __complete @words | ForEach-Object {
    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
  }
}
`, nil
	}
	return "", errors.New("usage: buildheadway completion bash|zsh|fish|powershell")
}

func runCompletion(args []string) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println("usage: buildheadway completion bash|zsh|fish|powershell")
		return 0
	}
	if len(args) != 1 {
		return fail(errors.New("usage: buildheadway completion bash|zsh|fish|powershell"))
	}
	script, err := completionScript(args[0])
	if err != nil {
		return fail(err)
	}
	fmt.Print(script)
	return 0
}
