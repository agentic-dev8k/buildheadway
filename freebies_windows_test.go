//go:build windows

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveryWindowsReaderPreservesLongUnicodePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("long-segment-", 7), strings.Repeat("another-segment-", 7), strings.Repeat("unicode-模型-", 7))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "freebies.json")
	if len(path) <= 260 {
		t.Fatal("fixture did not exercise an extended path")
	}
	if err := os.WriteFile(path, []byte("old snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := openFreebieStateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	old, err := io.ReadAll(reader)
	if err != nil || string(old) != "old snapshot" {
		t.Fatal("previous handle did not retain its snapshot", string(old), err)
	}
	if _, err := openFreebieStateFile(filepath.Join(dir, "absent.json")); !os.IsNotExist(err) {
		t.Fatal("missing file error lost its identity", err)
	}
}
