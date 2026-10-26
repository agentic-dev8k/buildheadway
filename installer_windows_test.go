//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
)

func bundledInstallerCommand(bundle, destination string) *exec.Cmd {
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	// The installer must verify SHA-256 even when the utility cmdlet is absent.
	script := "function global:Get-FileHash { throw 'Utility hashing cmdlet unavailable in this fixture' }; & " +
		quote(filepath.Join(bundle, "install.ps1")) + " --no-path --dir " + quote(destination)
	return exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
}
