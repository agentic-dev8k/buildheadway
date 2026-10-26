package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Set by release.py when packaging for a real GitHub repository.
var ReleaseRepository = "agentic-dev8k/buildheadway"

var releaseRepoRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var releaseTagRE = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`)

func nativeAssetName() string {
	return ExecutableName("buildheadway-"+runtime.GOOS+"-"+runtime.GOARCH, runtime.GOOS)
}

func releaseChecksum(manifest []byte, name string) (string, error) {
	matches := 0
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == "bin/"+name {
			matches++
		}
	}
	if matches != 1 {
		return "", errors.New("release manifest must contain exactly one checksum for this platform")
	}
	return expectedChecksum(string(manifest), "bin/"+name)
}

func verifyReleaseBinary(manifest, binary []byte, name string) error {
	expected, err := releaseChecksum(manifest, name)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(binary)) != expected {
		return errors.New("release failed integrity verification; download it again")
	}
	return nil
}

// Resolving latest once keeps the manifest and binary on the same release if a
// newer version is published while the download is in flight.
func resolveReleaseTag(ctx context.Context, repository, version string, fetch func(context.Context, string, int64) ([]byte, error)) (string, error) {
	if !releaseRepoRE.MatchString(repository) {
		return "", errors.New("release repository must be OWNER/REPOSITORY")
	}
	if version != "" && version != "latest" {
		if !releaseTagRE.MatchString(version) {
			return "", errors.New("release version must be a version such as 0.11.0")
		}
		return "v" + strings.TrimPrefix(version, "v"), nil
	}
	data, err := fetch(ctx, "https://api.github.com/repos/"+repository+"/releases/latest", 1<<20)
	if err != nil {
		return "", err
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if json.Unmarshal(data, &release) != nil || !releaseTagRE.MatchString(release.Tag) || release.Draft || release.Prerelease {
		return "", errors.New("the release service did not return a stable version")
	}
	return release.Tag, nil
}

func stageReleaseUpdate(ctx context.Context, directory, repository, version string) (string, func(), error) {
	var manifest, data []byte
	var err error
	name := nativeAssetName()
	if directory != "" {
		manifest, err = os.ReadFile(filepath.Join(directory, "SHA256SUMS"))
		if err == nil {
			data, err = os.ReadFile(filepath.Join(directory, "bin", name))
		}
	} else {
		var tag string
		tag, err = resolveReleaseTag(ctx, repository, version, getHTTPS)
		if err == nil {
			base := "https://github.com/" + repository + "/releases/download/" + tag + "/"
			manifest, err = getHTTPS(ctx, base+"SHA256SUMS", 1<<20)
			if err == nil {
				data, err = getHTTPS(ctx, base+name, 100<<20)
			}
		}
	}
	if err != nil {
		return "", func() {}, err
	}
	if err := verifyReleaseBinary(manifest, data, name); err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp("", "buildheadway-update-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0700); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if !IsBuildHeadwayBinary(path) {
		cleanup()
		return "", func() {}, errors.New("the release asset is not a BuildHeadway application")
	}
	return path, cleanup, nil
}

func runUpdate(paths Paths, args []string) int {
	flags := flag.NewFlagSet("buildheadway update", flag.ContinueOnError)
	from := flags.String("from", "", "Update from an extracted release folder (offline)")
	version := flags.String("version", "latest", "Release version; defaults to the latest stable release")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(flags.Args()) != 0 {
		return fail(errors.New("use buildheadway update or buildheadway update --from RELEASE_FOLDER"))
	}
	if brandEnv("BUILDHEADWAY_INSTALL_METHOD") == "npm" {
		fmt.Println("This installation is managed by npm. Update using the downloaded release:\nnpm install --global ./buildheadway-VERSION.tgz\nAfter registry publication, use: npm install --global buildheadway@latest")
		return 0
	}
	var receipt InstallReceipt
	if err := ReadJSON(filepath.Join(paths.Data, installReceiptName), &receipt); err != nil {
		return fail(errors.New("install BuildHeadway first, or run install.sh / install.ps1 from the new release"))
	}
	if receipt.Application != "BuildHeadway" || !IsBuildHeadwayBinary(receipt.Executable) {
		return fail(errors.New("the installation receipt does not point to a BuildHeadway executable"))
	}
	if *from == "" && receipt.Repository == "" {
		return fail(errors.New("this build has no published release repository yet; use buildheadway update --from RELEASE_FOLDER"))
	}
	if *from != "" && *version != "latest" {
		return fail(errors.New("--version cannot be combined with --from"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	asset, cleanup, err := stageReleaseUpdate(ctx, *from, receipt.Repository, *version)
	if err != nil {
		return fail(err)
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, asset, "install", "--dir", filepath.Dir(receipt.Executable), "--no-path")
	cmd.Env = setEnv(os.Environ(), "BUILDHEADWAY_RELEASE_REPOSITORY", receipt.Repository)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fail(err)
	}
	fmt.Println("Update installed. Start a new BuildHeadway session to use it.")
	return 0
}
