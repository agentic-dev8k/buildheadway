//go:build !windows

package main

import (
	"os/exec"
	"path/filepath"
)

func bundledInstallerCommand(bundle, destination string) *exec.Cmd {
	return exec.Command("/bin/sh", filepath.Join(bundle, "install.sh"), "--launch=false", "--no-path", "--dir", destination)
}
