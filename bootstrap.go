package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type NodeRuntime struct {
	Node string `json:"node"`
	Npm  string `json:"npm"`
}

func RuntimeNode(paths Paths) NodeRuntime {
	var node NodeRuntime
	if ReadJSON(filepath.Join(paths.Data, "runtime", "current.json"), &node) == nil {
		if _, err := os.Stat(node.Node); err == nil {
			return node
		}
	}
	return NodeRuntime{}
}

var nodeVersion = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)`)

func SuitableNode(version string) bool {
	parts := nodeVersion.FindStringSubmatch(strings.TrimSpace(version))
	if len(parts) != 4 {
		return false
	}
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	return major > 22 || (major == 22 && minor >= 19)
}
func NodeArchive(version, goos, arch string) (string, error) {
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(version) {
		return "", errors.New("invalid Node.js release version")
	}
	platform := goos
	architecture := arch
	if architecture == "amd64" {
		architecture = "x64"
	}
	if architecture != "x64" && architecture != "arm64" {
		return "", fmt.Errorf("automatic agent installation does not support %s", arch)
	}
	if goos == "windows" {
		return "node-" + version + "-win-" + architecture + ".zip", nil
	}
	if platform != "darwin" && platform != "linux" {
		return "", fmt.Errorf("unsupported OS: %s", goos)
	}
	return "node-" + version + "-" + platform + "-" + architecture + ".tar.gz", nil
}
func getHTTPS(ctx context.Context, url string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, errors.New("downloads require HTTPS")
	}
	request, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "BuildHeadway/"+Version)
	client := &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 5 {
			return errors.New("unsafe download redirect")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("download returned HTTP %d", response.StatusCode)
	}
	return ReadLimited(response.Body, limit)
}
func expectedChecksum(manifest, filename string) (string, error) {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == filename {
			if len(fields[0]) != 64 {
				return "", errors.New("invalid SHA-256 manifest")
			}
			if _, err := hex.DecodeString(fields[0]); err != nil {
				return "", err
			}
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", errors.New("the runtime archive is missing from the official SHA-256 manifest")
}
func safeArchivePath(root, name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", errors.New("invalid archive path")
	}
	target := filepath.Join(root, filepath.FromSlash(name))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", errors.New("archive path escapes installation directory")
	}
	return target, nil
}
func unpackNode(archive, destination string) error {
	var total int64
	write := func(name string, reader io.Reader, size int64, mode os.FileMode) error {
		if size < 0 || size > 300<<20 || total+size > 600<<20 {
			return errors.New("runtime archive exceeds extraction limits")
		}
		total += size
		target, err := safeArchivePath(destination, name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
		if err != nil {
			return err
		}
		_, err = io.CopyN(out, reader, size)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	if strings.HasSuffix(archive, ".zip") {
		bundle, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer bundle.Close()
		for _, entry := range bundle.File {
			if entry.FileInfo().IsDir() || entry.Mode()&os.ModeSymlink != 0 {
				continue
			}
			input, err := entry.Open()
			if err != nil {
				return err
			}
			err = write(entry.Name, input, int64(entry.UncompressedSize64), entry.Mode())
			input.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	bundle := tar.NewReader(gz)
	for {
		header, err := bundle.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if err := write(header.Name, bundle, header.Size, os.FileMode(header.Mode)); err != nil {
			return err
		}
	}
}
func EnsureNode(ctx context.Context, paths Paths, emit Emit) (NodeRuntime, error) {
	if node := RuntimeNode(paths); node.Node != "" {
		return node, nil
	}
	if node := FindTool("node", paths); node != "" {
		log := filepath.Join(paths.Data, "setup", "node-version.log")
		result, err := RunProcess(ctx, []string{node, "--version"}, paths.Data, log, "", ToolEnv(paths), 10*time.Second, nil)
		if err == nil && result.Code == 0 && SuitableNode(strings.TrimSpace(result.Output)) {
			npm := FindTool("npm", paths)
			if npm != "" {
				resolved, _ := filepath.EvalSymlinks(npm)
				if strings.HasSuffix(resolved, ".js") {
					return NodeRuntime{node, resolved}, nil
				}
				for _, candidate := range []string{
					filepath.Join(filepath.Dir(node), "node_modules", "npm", "bin", "npm-cli.js"),
					filepath.Join(filepath.Dir(node), "..", "lib", "node_modules", "npm", "bin", "npm-cli.js"),
				} {
					if _, err := os.Stat(candidate); err == nil {
						return NodeRuntime{node, candidate}, nil
					}
				}
			}
		}
	}
	emit(Event{Kind: "status", Text: "Setting up the agent runtime for " + runtime.GOOS + " / " + runtime.GOARCH})
	data, err := getHTTPS(ctx, "https://nodejs.org/dist/index.json", 4<<20)
	if err != nil {
		return NodeRuntime{}, err
	}
	var releases []struct {
		Version string `json:"version"`
		LTS     any    `json:"lts"`
	}
	if err := json.Unmarshal(data, &releases); err != nil {
		return NodeRuntime{}, err
	}
	version := ""
	for _, release := range releases {
		if lts, ok := release.LTS.(string); ok && lts != "" && SuitableNode(release.Version) {
			version = release.Version
			break
		}
	}
	if version == "" {
		return NodeRuntime{}, errors.New("no supported Node.js LTS release was found")
	}
	filename, err := NodeArchive(version, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return NodeRuntime{}, err
	}
	base := "https://nodejs.org/dist/" + version + "/"
	manifest, err := getHTTPS(ctx, base+"SHASUMS256.txt", 2<<20)
	if err != nil {
		return NodeRuntime{}, err
	}
	checksum, err := expectedChecksum(string(manifest), filename)
	if err != nil {
		return NodeRuntime{}, err
	}
	archiveData, err := getHTTPS(ctx, base+filename, 120<<20)
	if err != nil {
		return NodeRuntime{}, err
	}
	actual := sha256.Sum256(archiveData)
	if hex.EncodeToString(actual[:]) != checksum {
		return NodeRuntime{}, errors.New("runtime checksum verification failed")
	}
	runtimeRoot := filepath.Join(paths.Data, "runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return NodeRuntime{}, err
	}
	stage, err := os.MkdirTemp(runtimeRoot, ".node-install-*")
	if err != nil {
		return NodeRuntime{}, err
	}
	defer os.RemoveAll(stage)
	archive := filepath.Join(stage, filename)
	if err := os.WriteFile(archive, archiveData, 0600); err != nil {
		return NodeRuntime{}, err
	}
	if err := unpackNode(archive, stage); err != nil {
		return NodeRuntime{}, err
	}
	folder := strings.TrimSuffix(strings.TrimSuffix(filename, ".tar.gz"), ".zip")
	target := filepath.Join(runtimeRoot, folder)
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(filepath.Join(stage, folder), target); err != nil {
			return NodeRuntime{}, err
		}
	}
	node := NodeRuntime{
		Node: filepath.Join(target, "bin", "node"),
		Npm:  filepath.Join(target, "lib", "node_modules", "npm", "bin", "npm-cli.js"),
	}
	if runtime.GOOS == "windows" {
		node.Node = filepath.Join(target, "node.exe")
		node.Npm = filepath.Join(target, "node_modules", "npm", "bin", "npm-cli.js")
	}
	if err := AtomicJSON(filepath.Join(runtimeRoot, "current.json"), node); err != nil {
		return NodeRuntime{}, err
	}
	return node, nil
}
func InstallAgents(ctx context.Context, paths Paths, ids []string, emit Emit) (map[string]string, error) {
	return installAgents(ctx, paths, ids, emit, false)
}
func installAgents(ctx context.Context, paths Paths, ids []string, emit Emit, force bool) (map[string]string, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	if contains(ids, "opencode") || contains(ids, "claude") {
		if err := EnsureGitBash(ctx, paths, emit); err != nil {
			return nil, fmt.Errorf("Windows coding tools setup: %w", err)
		}
	}
	selected := map[string]string{}
	packages := []string{}
	for _, id := range Unique(ids) {
		if installed := FindTool(id, paths); installed != "" && !force {
			selected[id] = installed
			continue
		}
		for _, definition := range Registry {
			if definition.ID == id && definition.Adapter && definition.Package != "" {
				packages = append(packages, definition.Package)
			}
		}
	}
	if len(packages) == 0 {
		return selected, nil
	}
	node, err := EnsureNode(ctx, paths, emit)
	if err != nil {
		return nil, fmt.Errorf("agent runtime setup: %w", err)
	}
	emit(Event{Kind: "status", Text: "Installing missing connections: " + strings.Join(packages, ", ")})
	env := ToolEnv(paths)
	env = setEnv(env, "PATH", filepath.Dir(node.Node)+string(os.PathListSeparator)+envValue(env, "PATH"))
	argv := append([]string{node.Node, node.Npm, "install", "--no-fund", "--no-audit", "--prefix", filepath.Join(paths.Data, "tools")}, packages...)
	result, err := RunProcess(ctx, argv, paths.Data, filepath.Join(paths.Data, "setup", "install.log"), "", env, 10*time.Minute, nil)
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("connection installation failed: %s", Tail(result.Output, 1800))
	}
	for _, id := range ids {
		if selected[id] != "" {
			continue
		}
		path := FindInDirs(id, []string{filepath.Join(paths.Data, "tools", "node_modules", ".bin")}, runtime.GOOS)
		if path == "" {
			return nil, fmt.Errorf("%s did not provide an executable after installation", id)
		}
		selected[id] = path
	}
	return selected, nil
}
func setEnv(env []string, key, value string) []string {
	out := []string{}
	for _, entry := range env {
		if !strings.HasPrefix(strings.ToUpper(entry), strings.ToUpper(key)+"=") {
			out = append(out, entry)
		}
	}
	return append(out, key+"="+value)
}
func envValue(env []string, key string) string {
	for _, entry := range env {
		if strings.HasPrefix(strings.ToUpper(entry), strings.ToUpper(key)+"=") {
			return entry[len(key)+1:]
		}
	}
	return ""
}
