package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogPagingFindsEarlyFailureAndReconstructsBoundedLog(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(r.Dir, "state.json")
	before, _ := os.ReadFile(checkpoint)
	data := "EARLY_ROOT_CAUSE\n" + strings.Repeat("noise\n", 300000) + "FINAL_FAILURE\n"
	path := filepath.Join(r.Dir, "large.log")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	tail, err := ReadLogs(r.Repo, r.State.RunID, false, "large.log", 64)
	if err != nil || strings.Contains(tail.Output, "EARLY_ROOT_CAUSE") || tail.More {
		t.Fatal("legacy tail changed", err)
	}
	var result strings.Builder
	for offset := int64(0); ; {
		page, err := ReadLogsAt(r.Repo, r.State.RunID, false, "large.log", maxLogRead, offset)
		if err != nil || len(page.Output) > maxLogRead || page.Offset != offset || page.NextOffset != offset+int64(len(page.Output)) || page.Bytes != int64(len(data)) {
			t.Fatalf("invalid bounded page: %v", err)
		}
		if offset == 0 && (!strings.Contains(page.Output, "EARLY_ROOT_CAUSE") || !page.More || !page.Truncated || !strings.Contains(FormatLogs(page), "continue with --offset")) {
			t.Fatal("early failure not discoverable")
		}
		result.WriteString(page.Output)
		if !page.More {
			break
		}
		if page.NextOffset <= offset {
			t.Fatal("pagination did not progress")
		}
		offset = page.NextOffset
	}
	if result.String() != data {
		t.Fatal("paging lost bytes")
	}
	end, err := ReadLogsAt(r.Repo, r.State.RunID, false, "large.log", 16, int64(len(data)))
	if err != nil || end.Output != "" || end.More || end.NextOffset != int64(len(data)) {
		t.Fatal("incorrect EOF page", err)
	}
	for _, offset := range []int64{-1, int64(len(data) + 1)} {
		if _, err := ReadLogsAt(r.Repo, r.State.RunID, false, "large.log", 16, offset); err == nil {
			t.Fatal("invalid offset accepted")
		}
	}
	for _, name := range []string{"", "../state.json"} {
		if _, err := ReadLogsAt(r.Repo, r.State.RunID, false, name, 16, 0); err == nil {
			t.Fatal("invalid file accepted")
		}
	}
	after, _ := os.ReadFile(checkpoint)
	saved, _ := os.ReadFile(path)
	if string(before) != string(after) || string(saved) != data {
		t.Fatal("inspection changed evidence")
	}
}

func TestLogPagingRetainsContainmentAndEmptyFiles(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "empty.log"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	page, err := ReadLogsAt(r.Repo, r.State.RunID, false, "empty.log", 1, 0)
	if err != nil || page.Truncated || page.More || page.NextOffset != 0 {
		t.Fatal("empty log incorrect", err)
	}
	if _, err := ReadLogsAt(r.Repo, r.State.RunID, false, "empty.log", maxLogRead+1, 0); err == nil {
		t.Fatal("read cap bypassed")
	}
	if err := os.Symlink(filepath.Join(r.Dir, "state.json"), filepath.Join(r.Dir, "link.log")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ReadLogsAt(r.Repo, r.State.RunID, false, "link.log", 1, 0); err == nil {
		t.Fatal("paged read followed symlink")
	}
}
