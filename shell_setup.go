package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UpdateUserPath retains the public installer API; receipts use the internal
// form to record exactly which profiles this installation owns.
func UpdateUserPath(directory string, add bool) (bool, error) {
	added, _, err := updateUserPath(directory, add)
	return added, err
}

func shellPathLine(directory string, fish bool) string {
	if fish {
		return "fish_add_path --path " + ShellQuote(directory)
	}
	return "export PATH=" + ShellQuote(directory) + ":\"$PATH\""
}

func removeOwnPathBlock(content, directory string) string {
	start := strings.Index(content, pathStart)
	if start < 0 {
		return content
	}
	relative := strings.Index(content[start:], pathEnd)
	if relative < 0 {
		return content
	}
	end := start + relative + len(pathEnd)
	block := strings.TrimSpace(content[start:end])
	for _, fish := range []bool{false, true} {
		if block == pathStart+"\n"+shellPathLine(directory, fish)+"\n"+pathEnd {
			if end < len(content) && content[end] == '\n' {
				end++
			}
			return content[:start] + content[end:]
		}
	}
	return content
}

func readShellProfile(path string) (string, string, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, "", 0600, nil
	}
	if err != nil {
		return "", "", 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", "", 0, err
		}
		info, err = os.Stat(path)
		if err != nil {
			return "", "", 0, err
		}
	}
	if !info.Mode().IsRegular() {
		return "", "", 0, fmt.Errorf("shell profile %s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	return path, string(data), info.Mode().Perm(), err
}

func writeShellProfile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".buildheadway-profile-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.WriteString(content)
	chmodErr := f.Chmod(mode)
	syncErr := f.Sync()
	closeErr := f.Close()
	for _, err := range []error{writeErr, chmodErr, syncErr, closeErr} {
		if err != nil {
			return err
		}
	}
	return replaceFile(f.Name(), path)
}

func updateShellProfiles(profiles []string, directory string, add, fish bool) (bool, []string, error) {
	type profile struct {
		path, before, after string
		mode                os.FileMode
	}
	prepared := []profile{}
	for _, name := range profiles {
		path, before, mode, err := readShellProfile(name)
		if err != nil {
			return false, nil, err
		}
		after := removeOwnPathBlock(before, directory)
		if add {
			unmanaged := false
			for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
				if filepath.Clean(entry) == filepath.Clean(directory) && !strings.Contains(before, pathStart) {
					unmanaged = true
				}
			}
			if unmanaged {
				continue
			}
			after = ManagedPathBlock(before, directory, true, fish)
		}
		prepared = append(prepared, profile{path, before, after, mode})
	}
	owned := []string{}
	for _, p := range prepared {
		if p.before != p.after {
			if err := writeShellProfile(p.path, p.after, p.mode); err != nil {
				return len(owned) > 0, owned, err
			}
		}
		if add && strings.Contains(p.after, pathStart+"\n"+shellPathLine(directory, fish)+"\n"+pathEnd) {
			owned = Unique(append(owned, p.path))
		}
	}
	return len(owned) > 0, owned, nil
}

func removeRecordedPathFiles(files []string, directory string) error {
	for _, name := range files {
		if !filepath.IsAbs(name) {
			return errors.New("installation receipt contains a relative shell profile")
		}
		path, content, mode, err := readShellProfile(name)
		if err != nil {
			return err
		}
		if next := removeOwnPathBlock(content, directory); next != content {
			if err := writeShellProfile(path, next, mode); err != nil {
				return err
			}
		}
	}
	return nil
}
