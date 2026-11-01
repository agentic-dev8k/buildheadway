package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type browserBlocker struct {
	Version int    `json:"version"`
	Nonce   string `json:"nonce"`
	Error   string `json:"error,omitempty"`
}

func newBrowserHandoff(repo string) (string, string, error) {
	id, e := newID("browser-")
	if e != nil {
		return "", "", e
	}
	nonce, e := newID("nonce-")
	if e != nil {
		return "", "", e
	}
	r, e := os.OpenRoot(repo)
	if e != nil {
		return "", "", e
	}
	defer r.Close()
	if e = r.MkdirAll(filepath.Join(".forge", "browser-handoffs"), 0700); e != nil {
		return "", "", e
	}
	return filepath.Join(".forge", "browser-handoffs", id+".json"), nonce, nil
}
func readBrowserHandoff(repo, path, nonce string) error {
	r, e := os.OpenRoot(repo)
	if e != nil {
		return e
	}
	defer r.Close()
	st, e := r.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || !st.Mode().IsRegular() {
		return mcpSetupError{"browser blocker handoff is not a regular file"}
	}
	f, e := r.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	v, e := ReadLimited(f, 4096)
	var b browserBlocker
	if e != nil || json.Unmarshal(v, &b) != nil || b.Version != 1 || b.Nonce != nonce || b.Error == "" || len(b.Error) > 2000 {
		return mcpSetupError{"browser blocker handoff is invalid or stale"}
	}
	return mcpSetupError{b.Error}
}
func saveBrowserBlocker(repo string, e error) {
	var setup mcpSetupError
	if !errors.As(e, &setup) {
		return
	}
	root, path, nonce := browserHandoffTarget(repo)
	if root == "" {
		return
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer r.Close()
	f, err := r.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	msg, _ := evidenceExcerpt(setup.Error(), 2000)
	data, _ := json.Marshal(browserBlocker{1, nonce, msg})
	_, _ = f.Write(data)
	_ = f.Sync()
}
