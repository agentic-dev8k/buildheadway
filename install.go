package main

import (
	"archive/zip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type InstallReceipt struct {
	Repository  string   `json:"repository,omitempty"`
	Application string   `json:"application"`
	Version     string   `json:"version"`
	Executable  string   `json:"executable"`
	SHA256      string   `json:"sha256"`
	OS          string   `json:"os"`
	Arch        string   `json:"arch"`
	InstalledAt string   `json:"installed_at"`
	PathAdded   bool     `json:"path_added"`
	PathFiles   []string `json:"path_files,omitempty"`
}

func IsBuildHeadwayBinary(path string) bool {
	if info, err := buildinfo.ReadFile(path); err == nil && info.Main.Path == "github.com/agentic-dev8k/buildheadway" {
		return true
	}
	if bundle, err := zip.OpenReader(path); err == nil {
		defer bundle.Close()
		for _, file := range bundle.File {
			if file.Name == "buildheadway_app.py" {
				return true
			}
		}
	}
	return false
}

const installReceiptName = "buildheadway-installation.json"

func InstallSelf(paths Paths, directory string, updatePath bool) (string, error) {
	repository := brandEnv("BUILDHEADWAY_RELEASE_REPOSITORY")
	if repository == "" {
		repository = ReleaseRepository
	}
	if repository != "" && !releaseRepoRE.MatchString(repository) {
		return "", errors.New("invalid release repository")
	}
	if err := paths.Ensure(); err != nil {
		return "", err
	}
	source, err := os.Executable()
	if err != nil {
		return "", err
	}
	if directory == "" {
		directory = paths.Bin
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	target := filepath.Join(directory, ExecutableName("buildheadway", runtime.GOOS))
	if _, err := os.Stat(target); err == nil && !IsBuildHeadwayBinary(target) {
		return "", fmt.Errorf("another application already owns %s; choose --dir with another folder", target)
	}
	if source != target {
		input, err := os.Open(source)
		if err != nil {
			return "", err
		}
		defer input.Close()
		output, err := os.CreateTemp(directory, ".buildheadway-install-*")
		if err != nil {
			return "", err
		}
		temp := output.Name()
		defer os.Remove(temp)
		_, copyErr := io.Copy(output, input)
		syncErr := output.Sync()
		closeErr := output.Close()
		for _, err := range []error{copyErr, syncErr, closeErr} {
			if err != nil {
				return "", err
			}
		}
		if err := os.Chmod(temp, 0755); err != nil {
			return "", err
		}
		if err := replaceInstalledFile(temp, target); err != nil {
			return "", fmt.Errorf("close other BuildHeadway sessions and retry installation: %w", err)
		}
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	receiptPath := filepath.Join(paths.Data, installReceiptName)
	var previous InstallReceipt
	ReadJSON(receiptPath, &previous)
	receipt := InstallReceipt{Repository: repository, Application: "BuildHeadway", Version: Version, Executable: target, SHA256: fmt.Sprintf("%x", hash), OS: runtime.GOOS, Arch: runtime.GOARCH, InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	if previous.Application == "BuildHeadway" && filepath.Clean(previous.Executable) == filepath.Clean(target) {
		receipt.PathAdded = previous.PathAdded
		receipt.PathFiles = append([]string(nil), previous.PathFiles...)
		if receipt.Repository == "" {
			receipt.Repository = previous.Repository
		}
	}
	var pathErr error
	if updatePath {
		added, files, err := updateUserPath(directory, true)
		receipt.PathAdded = receipt.PathAdded || added
		receipt.PathFiles = Unique(append(receipt.PathFiles, files...))
		pathErr = err
	}
	if err := AtomicJSON(receiptPath, receipt); err != nil {
		return target, err
	}
	if pathErr != nil {
		return target, fmt.Errorf("BuildHeadway was installed, but PATH could not be updated: %w", pathErr)
	}
	return target, nil
}
func UninstallSelf(paths Paths) error {
	var receipt InstallReceipt
	if err := ReadJSON(filepath.Join(paths.Data, installReceiptName), &receipt); err != nil {
		return errors.New("no installed BuildHeadway receipt was found")
	}
	if receipt.Application != "BuildHeadway" || receipt.Executable == "" || !IsBuildHeadwayBinary(receipt.Executable) {
		return errors.New("the installation receipt does not point to a BuildHeadway executable")
	}
	if receipt.PathAdded {
		if len(receipt.PathFiles) > 0 {
			if err := removeRecordedPathFiles(receipt.PathFiles, filepath.Dir(receipt.Executable)); err != nil {
				return err
			}
		} else if _, err := UpdateUserPath(filepath.Dir(receipt.Executable), false); err != nil {
			return err
		}
	}
	if err := RemoveInstalledExecutable(receipt.Executable); err != nil {
		return err
	}
	return os.Remove(filepath.Join(paths.Data, installReceiptName))
}

const pathStart = "# >>> buildheadway terminal app >>>"
const pathEnd = "# <<< buildheadway terminal app <<<"

func ManagedPathBlock(content, directory string, add bool, fish bool) string {
	if add && strings.Contains(content, pathStart+"\n"+shellPathLine(directory, fish)+"\n"+pathEnd) {
		return content
	}
	start := strings.Index(content, pathStart)
	if start >= 0 {
		if end := strings.Index(content[start:], pathEnd); end >= 0 {
			end = start + end + len(pathEnd)
			if end < len(content) && content[end] == '\n' {
				end++
			}
			content = content[:start] + content[end:]
		}
	}
	if !add {
		return content
	}
	line := shellPathLine(directory, fish)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + "\n" + pathStart + "\n" + line + "\n" + pathEnd + "\n"
}
func ShellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func validReceipt(data []byte) bool {
	var r InstallReceipt
	return json.Unmarshal(data, &r) == nil && r.Application == "BuildHeadway"
}
