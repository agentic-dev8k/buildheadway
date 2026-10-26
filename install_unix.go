//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func replaceInstalledFile(source, target string) error { return replaceFile(source, target) }

func UpdateUserPath(directory string, add bool) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	shell := filepath.Base(os.Getenv("SHELL"))
	profile := filepath.Join(home, ".profile")
	fish := false
	switch shell {
	case "zsh":
		profile = filepath.Join(home, ".zshrc")
	case "bash":
		profile = filepath.Join(home, ".bashrc")
	case "fish":
		profile = filepath.Join(home, ".config", "fish", "conf.d", "buildheadway.fish")
		fish = true
	}
	// BUILDHEADWAY_HOME makes portable installs and tests self-contained.
	if root := brandEnv("BUILDHEADWAY_HOME"); root != "" {
		profile = filepath.Join(root, "shell-path.conf")
	}
	data, err := os.ReadFile(profile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if add {
		for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
			if filepath.Clean(entry) == filepath.Clean(directory) && !strings.Contains(string(data), pathStart) {
				return false, nil
			}
		}
	}
	content := ManagedPathBlock(string(data), directory, add, fish)
	if content == string(data) {
		return add, nil
	}
	if err := os.MkdirAll(filepath.Dir(profile), 0700); err != nil {
		return false, err
	}
	return add, os.WriteFile(profile, []byte(content), 0600)
}
func RemoveInstalledExecutable(path string) error { return os.Remove(path) }
