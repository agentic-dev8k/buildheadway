//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func isolateShellHome(t *testing.T, shell string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDHEADWAY_HOME", "")
	t.Setenv("CODENTRAIL_HOME", "")
	t.Setenv("FORGE_HOME", "")
	t.Setenv("SHELL", "/bin/"+shell)
	t.Setenv("ZDOTDIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	return home
}

func TestBashLoginSetupAndCommandResolution(t *testing.T) {
	home := isolateShellHome(t, "bash")
	directory := filepath.Join(home, "bin with ' dollar$")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "buildheadway-fixture"), []byte("#!/bin/sh\nprintf 'resolved'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, ".bash_login")
	os.WriteFile(profile, []byte("# keep\n"), 0640)
	added, files, err := updateUserPath(directory, true)
	if err != nil || !added || len(files) != 2 || files[1] != profile {
		t.Fatal(added, files, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".bash_profile")); !os.IsNotExist(err) {
		t.Fatal("ignored existing login precedence")
	}
	first, _ := os.ReadFile(profile)
	if _, _, err := updateUserPath(directory, true); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(profile)
	if string(first) != string(second) {
		t.Fatal("repeat setup changed bytes")
	}
	info, _ := os.Stat(profile)
	if info.Mode().Perm() != 0640 {
		t.Fatal("lost profile permissions")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "--noprofile", "--norc", "-c", `. "$1"; buildheadway-fixture`, "bash", profile)
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "resolved" {
		t.Fatalf("actual Bash could not resolve safely quoted path: %v %s", err, out)
	}
	// Removal uses recorded paths after the preferred shell has changed.
	t.Setenv("SHELL", "/bin/zsh")
	if err := removeRecordedPathFiles(files, directory); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(profile)
	if strings.Contains(string(got), pathStart) || !strings.Contains(string(got), "# keep") {
		t.Fatal("cleanup lost user content")
	}
}

func TestShellCustomLocationsSymlinksAndInvalidProfiles(t *testing.T) {
	for _, shell := range []string{"zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			home := isolateShellHome(t, shell)
			custom := filepath.Join(home, "custom")
			want := filepath.Join(custom, ".zshrc")
			if shell == "zsh" {
				t.Setenv("ZDOTDIR", custom)
			} else {
				t.Setenv("XDG_CONFIG_HOME", custom)
				want = filepath.Join(custom, "fish", "conf.d", "buildheadway.fish")
			}
			_, files, err := updateUserPath(filepath.Join(home, "bin"), true)
			if err != nil || len(files) != 1 || files[0] != want {
				t.Fatal(files, err)
			}
		})
	}
	home := isolateShellHome(t, "bash")
	target := filepath.Join(home, "actual-profile")
	os.WriteFile(target, []byte("keep\n"), 0644)
	if err := os.Symlink(target, filepath.Join(home, ".bashrc")); err != nil {
		t.Fatal(err)
	}
	_, files, err := updateUserPath(filepath.Join(home, "bin"), true)
	resolvedTarget, resolveErr := filepath.EvalSymlinks(target)
	if err != nil || resolveErr != nil || len(files) != 2 || files[0] != resolvedTarget {
		t.Fatal(files, err)
	}
	info, _ := os.Lstat(filepath.Join(home, ".bashrc"))
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced profile symlink")
	}
	os.Remove(filepath.Join(home, ".bash_profile"))
	if err := os.Mkdir(filepath.Join(home, ".bash_profile"), 0700); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(target)
	if _, _, err := updateUserPath(filepath.Join(home, "other"), true); err == nil {
		t.Fatal("accepted directory profile")
	}
	after, _ := os.ReadFile(target)
	if string(before) != string(after) {
		t.Fatal("edited an earlier profile before validation finished")
	}
	os.Remove(filepath.Join(home, ".bash_profile"))
	os.Symlink(filepath.Join(home, "missing"), filepath.Join(home, ".bash_profile"))
	if _, _, err := updateUserPath(filepath.Join(home, "other"), true); err == nil {
		t.Fatal("accepted dangling profile")
	}
}

func TestNativeInstallRecordsProfilesAcrossShellChange(t *testing.T) {
	home := isolateShellHome(t, "bash")
	binary := nativeTestBinary(t)
	destination := filepath.Join(home, "native commands")
	paths, err := UserPaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"install", "--dir", destination}, {"install", "--dir", destination, "--no-path"}} {
		if out, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	var receipt InstallReceipt
	if err := ReadJSON(filepath.Join(paths.Data, installReceiptName), &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.PathFiles) != 2 || !receipt.PathAdded {
		t.Fatal("lost receipt ownership", receipt)
	}
	t.Setenv("SHELL", "/bin/zsh")
	if out, err := exec.Command(binary, "uninstall").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for _, file := range receipt.PathFiles {
		data, _ := os.ReadFile(file)
		if strings.Contains(string(data), pathStart) {
			t.Fatal("did not clean original shell", file)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatal("touched replacement shell")
	}
}
