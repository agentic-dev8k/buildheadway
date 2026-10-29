//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func replaceInstalledFile(source, target string) error { return replaceFile(source, target) }
func updateUserPath(directory string, add bool) (bool, []string, error) {
	if root := brandEnv("BUILDHEADWAY_HOME"); root != "" {
		return updateShellProfiles([]string{filepath.Join(root, "shell-path.conf")}, directory, add, false)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, nil, err
	}
	profiles := []string{filepath.Join(home, ".profile")}
	fish := false
	switch filepath.Base(os.Getenv("SHELL")) {
	case "bash":
		login := filepath.Join(home, ".bash_profile")
		for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
			candidate := filepath.Join(home, name)
			if _, err := os.Lstat(candidate); err == nil {
				login = candidate
				break
			} else if !os.IsNotExist(err) {
				return false, nil, err
			}
		}
		profiles = []string{filepath.Join(home, ".bashrc"), login}
	case "zsh":
		base := os.Getenv("ZDOTDIR")
		if base == "" {
			base = home
		}
		if !filepath.IsAbs(base) {
			return false, nil, fmt.Errorf("ZDOTDIR must be absolute for managed PATH setup; use --no-path")
		}
		profiles = []string{filepath.Join(base, ".zshrc")}
	case "fish":
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		if !filepath.IsAbs(base) {
			return false, nil, fmt.Errorf("XDG_CONFIG_HOME must be absolute for managed PATH setup; use --no-path")
		}
		profiles, fish = []string{filepath.Join(base, "fish", "conf.d", "buildheadway.fish")}, true
	}
	return updateShellProfiles(profiles, directory, add, fish)
}
func RemoveInstalledExecutable(path string) error { return os.Remove(path) }
