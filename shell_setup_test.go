package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedPathRemovalPreservesUserChanges(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "commands")
	content := ManagedPathBlock("user content\n", directory, true, false)
	if next := ManagedPathBlock(content, directory, true, false); next != content {
		t.Fatal("repeat installation changed profile")
	}
	if got := removeOwnPathBlock(content, directory); strings.Contains(got, pathStart) || !strings.Contains(got, "user content") {
		t.Fatal("owned block not removed safely")
	}
	changed := strings.ReplaceAll(content, shellPathLine(directory, false), "export CUSTOM_PATH=keep")
	if removeOwnPathBlock(changed, directory) != changed {
		t.Fatal("removed user-replaced block")
	}
	if err := removeRecordedPathFiles([]string{"relative-profile"}, directory); err == nil {
		t.Fatal("relative receipt path accepted")
	}
	file := filepath.Join(t.TempDir(), "profile")
	if err := os.WriteFile(file, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeRecordedPathFiles([]string{file}, directory); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	if string(got) != changed {
		t.Fatal("changed unrelated content")
	}
}
