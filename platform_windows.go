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

// Readers must share rename/delete access so a concurrent atomic publication
// can replace the name while the reader finishes its previous complete file.
// This grants no delete access to this handle; it remains read-only.
func openFreebieStateFile(path string) (*os.File, error) {
	name, err := filepath.Abs(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	// Preserve long paths and UNC paths, including Unicode, without depending
	// on a machine-wide long-path policy or changing the working directory.
	if !strings.HasPrefix(name, `\\?\`) {
		if strings.HasPrefix(name, `\\`) {
			name = `\\?\UNC\` + strings.TrimPrefix(name, `\\`)
		} else {
			name = `\\?\` + name
		}
	}
	wide, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(wide, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

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
