package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// This is a child browser outcome, independent of the wrapper's actual exit,
// timeout or cancellation. It cannot supply passing application evidence.
type BrowserFailureEvidence struct {
	Error  string `json:"error"`
	Report string `json:"report,omitempty"`
}

func (b BrowserFailureEvidence) valid() bool {
	report := filepath.FromSlash(strings.ReplaceAll(b.Report, `\`, "/"))
	return b.Error != "" && len(b.Error) <= 2000 && utf8.ValidString(b.Error) &&
		(b.Report == "" || len(b.Report) <= 512 && !strings.ContainsRune(b.Report, 0) && filepath.IsLocal(report) && strings.HasSuffix(b.Report, "/report.json"))
}

type browserFailureHandoff struct {
	Version int    `json:"version"`
	Nonce   string `json:"nonce"`
	BrowserFailureEvidence
}

func browserHandoffTarget(repo string) (root, path, nonce string) {
	path, nonce = os.Getenv("BUILDHEADWAY_BROWSER_CHECK_HANDOFF"), os.Getenv("BUILDHEADWAY_BROWSER_CHECK_NONCE")
	if path == "" || nonce == "" || len(nonce) > 128 || filepath.IsAbs(path) || filepath.Dir(path) != filepath.Join(".forge", "browser-handoffs") {
		return "", "", ""
	}
	root = repo // Older callers supplied only the relative handoff and nonce.
	if configured := os.Getenv("BUILDHEADWAY_BROWSER_CHECK_ROOT"); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", "", ""
		}
		root = configured
	}
	return root, path, nonce
}

func browserCommandEnv(paths Paths, repo, handoff, nonce string) []string {
	if absolute, err := filepath.Abs(repo); err == nil {
		repo = absolute
	}
	env := setEnv(ToolEnv(paths), "CI", "1")
	env = setEnv(env, "BUILDHEADWAY_BROWSER_CHECK_HANDOFF", handoff)
	env = setEnv(env, "BUILDHEADWAY_BROWSER_CHECK_NONCE", nonce)
	return setEnv(env, "BUILDHEADWAY_BROWSER_CHECK_ROOT", repo)
}

func saveBrowserFailure(repo string, cause error, report string) {
	var setup mcpSetupError
	if cause == nil || errors.As(cause, &setup) {
		return
	}
	root, path, nonce := browserHandoffTarget(repo)
	if root == "" {
		return
	}
	evidence := BrowserFailureEvidence{}
	evidence.Error, _ = evidenceExcerpt(cause.Error(), 2000)
	if report != "" {
		relative, err := filepath.Rel(root, report)
		if err == nil && filepath.IsLocal(relative) && len(relative) <= 512 {
			evidence.Report = filepath.ToSlash(relative)
		}
	}
	if !evidence.valid() {
		return
	}
	message := browserFailureHandoff{Version: 1, Nonce: nonce, BrowserFailureEvidence: evidence}
	data, err := boundedBrowserFailure(message)
	if err != nil {
		return
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer r.Close()
	// A separate bounded file means a repairable failure cannot overwrite or
	// prevent a later hard setup blocker. Concurrent failures retain one outcome.
	f, err := r.OpenFile(path+".failure", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(data)
	_ = f.Sync()
}

func boundedBrowserFailure(message browserFailureHandoff) ([]byte, error) {
	for {
		data, err := json.Marshal(message)
		if err != nil || len(data) <= 4096 {
			return data, err
		}
		// JSON escaping can expand a bounded diagnostic. Keep both ends and
		// honor the actual encoded-byte limit used by the reader.
		if len(message.Error) > 256 {
			message.Error, _ = evidenceExcerpt(message.Error, max(256, len(message.Error)/2))
		} else if message.Report != "" {
			message.Report = ""
		} else {
			return nil, errors.New("browser failure handoff exceeds 4096 bytes")
		}
	}
}

func readBrowserFailure(repo, path, nonce string) (*BrowserFailureEvidence, error) {
	r, err := os.OpenRoot(repo)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var message browserFailureHandoff
	err = readEvidenceJSON(r, path+".failure", 4096, &message)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || message.Version != 1 || message.Nonce != nonce || !message.BrowserFailureEvidence.valid() {
		return nil, mcpSetupError{"browser verification handoff is invalid, stale or unavailable; inspect the check before retrying"}
	}
	return &message.BrowserFailureEvidence, nil
}

func browserProcessEvidence(repo, handoff, nonce string, evidence ExecutionEvidence) (ExecutionEvidence, error) {
	failure, err := readBrowserFailure(repo, handoff, nonce)
	evidence.BrowserFailure = failure
	hard := errors.Join(readBrowserHandoff(repo, handoff, nonce), err)
	if hard != nil {
		evidence.FailureKind, evidence.Error = "execution", hard.Error()
	}
	return evidence, hard
}

func browserFailureNotice(b *BrowserFailureEvidence) string {
	if b == nil {
		return ""
	}
	text := "Nested browser check failed: " + b.Error + "\n"
	if b.Report != "" {
		text += "Browser report (project-relative): " + b.Report + "\n"
	}
	return text
}
