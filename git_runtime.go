package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type GitReleaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

func portableGitAsset(assets []GitReleaseAsset, arch string) (GitReleaseAsset, string, error) {
	suffix := "-64-bit.7z.exe"
	if arch == "arm64" {
		suffix = "-arm64.7z.exe"
	} else if arch != "amd64" {
		return GitReleaseAsset{}, "", errors.New("Git Bash requires Windows amd64 or arm64")
	}
	for _, asset := range assets {
		if !strings.HasPrefix(asset.Name, "PortableGit-") || !strings.HasSuffix(asset.Name, suffix) {
			continue
		}
		if filepath.Base(asset.Name) != asset.Name || strings.ContainsAny(asset.Name, `/\`) || !strings.HasPrefix(asset.URL, "https://github.com/git-for-windows/git/releases/download/") || !strings.HasSuffix(asset.URL, "/"+asset.Name) {
			return GitReleaseAsset{}, "", errors.New("unexpected Git for Windows release asset")
		}
		checksum := strings.TrimPrefix(asset.Digest, "sha256:")
		if checksum == asset.Digest || len(checksum) != 64 {
			return GitReleaseAsset{}, "", errors.New("the official Git download has no SHA-256 digest")
		}
		if _, err := hex.DecodeString(checksum); err != nil {
			return GitReleaseAsset{}, "", err
		}
		return asset, strings.ToLower(checksum), nil
	}
	return GitReleaseAsset{}, "", errors.New("the official Git release has no compatible portable build")
}

func gitBashInRoot(root string) string {
	if root == "" {
		return ""
	}
	for _, relative := range []string{filepath.Join("bin", "bash.exe"), filepath.Join("usr", "bin", "bash.exe")} {
		candidate := filepath.Join(root, relative)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func privateGitRoot(paths Paths) string {
	var record struct {
		Root string `json:"root"`
	}
	if ReadJSON(filepath.Join(paths.Data, "runtime", "git", "current.json"), &record) == nil && gitBashInRoot(record.Root) != "" {
		return record.Root
	}
	return ""
}

func FindGitBash(paths Paths) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, name := range []string{"OPENCODE_GIT_BASH_PATH", "CLAUDE_CODE_GIT_BASH_PATH"} {
		candidate := os.Getenv(name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	roots := []string{privateGitRoot(paths)}
	if git := FindTool("git", paths); git != "" {
		root := filepath.Dir(git)
		for i := 0; i < 3; i++ {
			roots = append(roots, root)
			root = filepath.Dir(root)
		}
	}
	home, _ := os.UserHomeDir()
	parents := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		parents = append(parents, filepath.Join(local, "Programs"))
	}
	for _, parent := range parents {
		if parent != "" {
			roots = append(roots, filepath.Join(parent, "Git"))
		}
	}
	roots = append(roots, filepath.Join(home, "scoop", "apps", "git", "current"))
	for _, root := range roots {
		if bash := gitBashInRoot(root); bash != "" {
			return bash
		}
	}
	return ""
}

func EnsureGitBash(ctx context.Context, paths Paths, emit Emit) error {
	if runtime.GOOS != "windows" || FindGitBash(paths) != "" {
		return nil
	}
	return installPrivateGit(ctx, paths, emit)
}

func installPrivateGit(ctx context.Context, paths Paths, emit Emit) error {
	if runtime.GOOS != "windows" {
		return errors.New("the portable Git bootstrap runs only on Windows")
	}
	emit(Event{Kind: "status", Text: "Setting up Git Bash for the Windows coding agents"})
	data, err := getHTTPS(ctx, "https://api.github.com/repos/git-for-windows/git/releases/latest", 4<<20)
	if err != nil {
		return err
	}
	var release struct {
		Assets []GitReleaseAsset `json:"assets"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return err
	}
	asset, checksum, err := portableGitAsset(release.Assets, runtime.GOARCH)
	if err != nil {
		return err
	}
	data, err = getHTTPS(ctx, asset.URL, 160<<20)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != checksum {
		return errors.New("Git for Windows checksum verification failed")
	}
	cache := filepath.Join(paths.Data, "runtime", "git")
	if err := os.MkdirAll(cache, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(cache, ".install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	archive := filepath.Join(stage, asset.Name)
	if err := os.WriteFile(archive, data, 0600); err != nil {
		return err
	}
	target := filepath.Join(stage, "git")
	// Official PortableGit is a self-contained extractor; no admin or 7-Zip install.
	result, err := RunProcess(ctx, []string{archive, "-o" + target, "-y"}, stage, filepath.Join(paths.Data, "setup", "git.log"), "", os.Environ(), 5*time.Minute, nil)
	if err != nil {
		return err
	}
	if result.Code != 0 || gitBashInRoot(target) == "" {
		return fmt.Errorf("Git Bash setup did not finish: %s", Tail(result.Output, 1400))
	}
	final := filepath.Join(cache, strings.TrimSuffix(asset.Name, ".7z.exe"))
	if gitBashInRoot(final) == "" {
		if err := os.Rename(target, final); err != nil {
			return err
		}
	}
	return AtomicJSON(filepath.Join(cache, "current.json"), map[string]string{"root": final, "archive": asset.Name, "sha256": checksum})
}
