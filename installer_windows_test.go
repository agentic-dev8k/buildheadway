//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
)

func bundledInstallerCommand(bundle, destination string) *exec.Cmd {
	return exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-File", filepath.Join(bundle, "install.ps1"), "--no-path", "--dir", destination)
}
