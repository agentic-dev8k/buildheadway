//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x00000200
	// cmd.exe does not use the CommandLineToArgvW quoting convention used by
	// os/exec. Preserve the shell program as written, including quoted paths,
	// redirections and conditions. /S removes only our outer quote pair.
	if cmd.SysProcAttr.CmdLine == "" && strings.EqualFold(filepath.Base(cmd.Path), "cmd.exe") && len(cmd.Args) == 5 &&
		strings.EqualFold(cmd.Args[1], "/D") && strings.EqualFold(cmd.Args[2], "/S") && strings.EqualFold(cmd.Args[3], "/C") {
		cmd.SysProcAttr.CmdLine = syscall.EscapeArg(cmd.Path) + ` /D /S /C "` + cmd.Args[4] + `"`
	}
}
func terminateProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	killer := exec.Command("taskkill.exe", "/PID", itoa(cmd.Process.Pid), "/T", "/F")
	killer.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := killer.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
func replaceFile(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func lockFile(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func isWindowsShim(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".cmd") || strings.HasSuffix(strings.ToLower(path), ".bat")
}
