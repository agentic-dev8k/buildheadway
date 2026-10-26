//go:build windows

package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Windows may keep the running executable mapped. Rotate it out of the way
// instead of trying to overwrite it, and restore it if the new move fails.
func replaceInstalledFile(source, target string) error {
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return replaceFile(source, target)
	}
	for _, old := range func() []string {
		values, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".buildheadway-previous-*"))
		return values
	}() {
		if IsBuildHeadwayBinary(old) {
			_ = os.Remove(old)
		}
	}
	backup, err := os.CreateTemp(filepath.Dir(target), ".buildheadway-previous-*")
	if err != nil {
		return err
	}
	name := backup.Name()
	backup.Close()
	os.Remove(name)
	if err := os.Rename(target, name); err != nil {
		return err
	}
	if err := replaceFile(source, target); err != nil {
		restoreErr := os.Rename(name, target)
		if restoreErr != nil {
			return fmt.Errorf("update failed: %v; restore %s to %s: %w", err, name, target, restoreErr)
		}
		return err
	}
	// An in-use backup is cleaned at the next install after that session exits.
	_ = os.Remove(name)
	return nil
}

func UpdateUserPath(directory string, add bool) (bool, error) {
	if root := brandEnv("BUILDHEADWAY_HOME"); root != "" {
		content := ManagedPathBlock("", directory, add, false)
		return add, os.WriteFile(filepath.Join(root, "shell-path.conf"), []byte(content), 0600)
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer key.Close()
	current, valueType, _ := key.GetStringValue("Path")
	parts := []string{}
	present := false
	for _, part := range strings.Split(current, ";") {
		if strings.EqualFold(filepath.Clean(part), filepath.Clean(directory)) {
			present = true
		} else if strings.TrimSpace(part) != "" {
			parts = append(parts, part)
		}
	}
	if add && present {
		return false, nil
	}
	if add {
		parts = append(parts, directory)
	}
	value := strings.Join(parts, ";")
	if valueType == registry.SZ {
		err = key.SetStringValue("Path", value)
	} else {
		err = key.SetExpandStringValue("Path", value)
	}
	if err != nil {
		return false, err
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	broadcast := user32.NewProc("SendMessageTimeoutW")
	name, _ := windows.UTF16PtrFromString("Environment")
	broadcast.Call(0xffff, 0x001A, 0, uintptr(unsafe.Pointer(name)), 0x0002, 1000, 0)
	return add, nil
}
func RemoveInstalledExecutable(path string) error {
	self, _ := os.Executable()
	if !strings.EqualFold(filepath.Clean(path), filepath.Clean(self)) {
		return os.Remove(path)
	}
	// Defer deletion until this executable has exited; keep settings and projects.
	script := "Wait-Process -Id " + itoa(os.Getpid()) + " -ErrorAction SilentlyContinue; Remove-Item -LiteralPath '" + strings.ReplaceAll(path, "'", "''") + "' -Force"
	words := utf16.Encode([]rune(script))
	data := make([]byte, len(words)*2)
	for i, value := range words {
		binary.LittleEndian.PutUint16(data[i*2:], value)
	}
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", base64.StdEncoding.EncodeToString(data))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008}
	return command.Start()
}
