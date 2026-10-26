package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPortableGitSelectionRequiresOfficialVerifiedNativeAsset(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	assets := []GitReleaseAsset{}
	for _, arch := range []string{"64-bit", "arm64"} {
		name := "PortableGit-2.56.0.2-" + arch + ".7z.exe"
		assets = append(assets, GitReleaseAsset{name, "https://github.com/git-for-windows/git/releases/download/v2.56.0.windows.2/" + name, "sha256:" + checksum})
	}
	for _, arch := range []string{"amd64", "arm64"} {
		asset, digest, err := portableGitAsset(assets, arch)
		if err != nil || digest != checksum || !strings.Contains(asset.Name, map[string]string{"amd64": "64-bit", "arm64": "arm64"}[arch]) {
			t.Fatalf("incorrect Windows runtime selection for %s: %#v %v", arch, asset, err)
		}
	}
	if _, _, err := portableGitAsset(assets, "386"); err == nil {
		t.Fatal("unsupported architecture accepted")
	}
	for _, mutate := range []func(*GitReleaseAsset){
		func(a *GitReleaseAsset) { a.Digest = "" },
		func(a *GitReleaseAsset) { a.Digest = "sha256:" + strings.Repeat("g", 64) },
		func(a *GitReleaseAsset) { a.URL = "https://example.org/" + a.Name },
		func(a *GitReleaseAsset) { a.Name = "PortableGit-../../evil-64-bit.7z.exe" },
	} {
		asset := assets[0]
		mutate(&asset)
		if _, _, err := portableGitAsset([]GitReleaseAsset{asset}, "amd64"); err == nil {
			t.Fatalf("unverified or unexpected runtime asset accepted: %#v", asset)
		}
	}
}

func TestPrivateGitDiscoveryHandlesSpacesAndWindowsLayouts(t *testing.T) {
	paths := testPaths(t)
	root := filepath.Join(paths.Data, "Runtime With Spaces", "Git")
	bash := filepath.Join(root, "usr", "bin", "bash.exe")
	os.MkdirAll(filepath.Dir(bash), 0700)
	os.WriteFile(bash, []byte("fixture"), 0600)
	if gitBashInRoot(root) != bash {
		t.Fatal("portable Git layout was not detected")
	}
	if err := AtomicJSON(filepath.Join(paths.Data, "runtime", "git", "current.json"), map[string]string{"root": root}); err != nil {
		t.Fatal(err)
	}
	if privateGitRoot(paths) != root {
		t.Fatal("private runtime receipt was not reused")
	}
	dirs := SearchDirs(paths, "windows", t.TempDir(), "")
	if !contains(dirs, filepath.Join(root, "cmd")) || !contains(dirs, filepath.Join(root, "usr", "bin")) {
		t.Fatal("private Git commands missing from the child environment")
	}
	os.Remove(bash)
	if privateGitRoot(paths) != "" {
		t.Fatal("missing runtime was treated as ready")
	}
}

func TestPrivateWindowsGitBootstrap(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("BUILDHEADWAY_BOOTSTRAP_TESTS") != "1" {
		t.Skip("opt-in native Windows download and extraction test")
	}
	paths := testPaths(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if err := installPrivateGit(ctx, paths, noEmit); err != nil {
		t.Fatal(err)
	}
	bash := gitBashInRoot(privateGitRoot(paths))
	result, err := RunProcess(ctx, []string{bash, "-c", "printf BUILDHEADWAY_BASH_OK"}, paths.Data, filepath.Join(paths.Data, "bash-check.log"), "", ToolEnv(paths), 20*time.Second, nil)
	if err != nil || result.Code != 0 || !strings.Contains(result.Output, "BUILDHEADWAY_BASH_OK") {
		t.Fatalf("private Windows shell did not execute: %v %s", err, result.Output)
	}
}
