//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func configureProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func terminateProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}
func replaceFile(source, target string) error { return os.Rename(source, target) }
func lockFile(file *os.File) error            { return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func isWindowsShim(string) bool               { return false }
