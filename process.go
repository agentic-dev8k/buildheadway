package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type ProcessResult struct {
	Code            int
	Output          string
	OutputTruncated bool
}

type commandTimeoutError struct{ timeout time.Duration }

func (e commandTimeoutError) Error() string {
	return fmt.Sprintf("command timed out after %s", e.timeout)
}
func (e commandTimeoutError) Unwrap() error { return context.DeadlineExceeded }

// ExecutionEvidence is additive checkpoint metadata. Empty failure_kind/error in
// older checkpoints does not invent a duration or alter their recorded exit code.
type ExecutionEvidence struct {
	DurationMillis int64  `json:"duration_ms,omitempty"`
	FailureKind    string `json:"failure_kind,omitempty"`
	Error          string `json:"error,omitempty"`
}

func verificationEvidence(ctx context.Context, result ProcessResult, err error, started time.Time) (ProcessResult, ExecutionEvidence) {
	evidence := ExecutionEvidence{DurationMillis: max(1, time.Since(started).Milliseconds())}
	switch {
	case ctx.Err() != nil:
		result.Code, evidence.FailureKind, err = 130, "cancelled", ctx.Err()
	case errors.Is(err, context.DeadlineExceeded):
		result.Code, evidence.FailureKind = 124, "timeout"
	case err != nil:
		// No reliable child exit status exists (start, log or wait failure).
		result.Code, evidence.FailureKind = -1, "execution"
	case result.Code != 0:
		evidence.FailureKind = "exit"
	}
	if err != nil {
		evidence.Error, _ = evidenceExcerpt(err.Error(), 2000)
		result.Output += "\n" + evidence.Error
	}
	return result, evidence
}

func executionNotice(e ExecutionEvidence) string {
	if e.FailureKind == "" {
		return ""
	}
	text := fmt.Sprintf("[%s; elapsed %s", e.FailureKind, time.Duration(e.DurationMillis)*time.Millisecond)
	if e.Error != "" {
		text += ": " + e.Error
	}
	return text + "]\n"
}

const processOutputLimit int64 = 8 << 20

type outputLimitError struct{}

func (outputLimitError) Error() string {
	return "agent output exceeded the 8 MiB capture limit; the complete log is saved, but this response cannot establish completion or verified zero cost; inspect the log and use a smaller assignment before resuming"
}

// Keep complete logs on disk, but never read an unbounded log into memory.
func processOutput(file *os.File) (string, bool, error) {
	info, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	offset := max(int64(0), info.Size()-processOutputLimit)
	data := make([]byte, info.Size()-offset)
	if _, err := file.ReadAt(data, offset); err != nil && err != io.EOF {
		return "", false, err
	}
	return string(data), offset > 0, nil
}

func completeAgentOutput(result ProcessResult) error {
	if result.OutputTruncated {
		return outputLimitError{}
	}
	return nil
}

type lineLog struct {
	mu      sync.Mutex
	file    *os.File
	pending string
	line    func(string)
}

func (l *lineLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n, err := l.file.Write(data)
	l.pending += string(data)
	for {
		index := strings.IndexByte(l.pending, '\n')
		if index < 0 {
			break
		}
		line := l.pending[:index]
		l.pending = l.pending[index+1:]
		if l.line != nil {
			l.line(line)
		}
	}
	if len(l.pending) > 1<<20 {
		l.pending = Tail(l.pending, 1<<20)
	}
	return n, err
}

func RunProcess(ctx context.Context, argv []string, cwd, logPath, input string, env []string, timeout time.Duration, onLine func(string)) (ProcessResult, error) {
	if len(argv) == 0 {
		return ProcessResult{}, errors.New("empty command")
	}
	if err := ctx.Err(); err != nil {
		return ProcessResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return ProcessResult{}, err
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return ProcessResult{}, err
	}
	defer file.Close()
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if cwd != "" {
		absolute, err := filepath.Abs(cwd)
		if err != nil {
			return ProcessResult{}, err
		}
		cwd = absolute
		if env == nil {
			env = os.Environ()
		}
		// Some agent runtimes use PWD in preference to the native process cwd.
		// Cmd.Dir alone does not update PWD when an explicit Env is supplied.
		env = setEnv(env, "PWD", cwd)
	}
	command, err := PreparedCommand(runCtx, argv, env)
	if err != nil {
		return ProcessResult{}, err
	}
	command.Dir = cwd
	if env != nil {
		command.Env = env
	}
	command.Stdin = strings.NewReader(input)
	writer := &lineLog{file: file, line: onLine}
	command.Stdout = writer
	command.Stderr = writer
	configureProcess(command)
	command.Cancel = func() error { return terminateProcess(command) }
	command.WaitDelay = 3 * time.Second
	err = command.Run()
	writer.mu.Lock()
	if writer.pending != "" && onLine != nil {
		onLine(writer.pending)
	}
	writer.mu.Unlock()
	output, truncated, readErr := processOutput(file)
	result := ProcessResult{Output: output, OutputTruncated: truncated}
	if runCtx.Err() != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Code = 124
		return result, commandTimeoutError{timeout}
	}
	if readErr != nil {
		return result, fmt.Errorf("reading process log: %w", readErr)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.Code = exit.ExitCode()
			return result, nil
		}
		return result, err
	}
	return result, nil
}

var shimTarget = regexp.MustCompile(`(?i)"(?:%~dp0|%dp0%)[\\/]+([^"\r\n]+?\.(?:cjs|mjs|js|exe))"`)

func ShimTarget(data, dir string) (string, error) {
	matches := shimTarget.FindAllStringSubmatch(data, -1)
	targetName := ""
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		extension := strings.ToLower(filepath.Ext(match[1]))
		if extension == ".js" || extension == ".mjs" || extension == ".cjs" {
			targetName = match[1]
			break
		}
		if !strings.EqualFold(filepath.Base(strings.ReplaceAll(match[1], `\`, "/")), "node.exe") {
			targetName = match[1]
		}
	}
	if targetName == "" {
		return "", errors.New("this Windows command wrapper does not expose a supported executable; install the agent's current native or npm release")
	}
	target := filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(targetName, `\`, "/")))
	return filepath.Clean(target), nil
}
func PreparedCommand(ctx context.Context, argv, env []string) (*exec.Cmd, error) {
	if !isWindowsShim(argv[0]) {
		return exec.CommandContext(ctx, argv[0], argv[1:]...), nil
	}
	data, err := os.ReadFile(argv[0])
	if err != nil {
		return nil, err
	}
	target, err := ShimTarget(string(data), filepath.Dir(argv[0]))
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(target), ".exe") {
		return exec.CommandContext(ctx, target, argv[1:]...), nil
	}
	node := findNodeForShim(argv[0], env)
	if node == "" {
		return nil, errors.New("the installed agent needs Node.js; reconnect it in BuildHeadway to repair the runtime")
	}
	args := append([]string{target}, argv[1:]...)
	return exec.CommandContext(ctx, node, args...), nil
}
func ReadLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, errors.New("response exceeded its allowed size")
	}
	return data, err
}
