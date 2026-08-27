//go:build windows

package proxy

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// hiddenProcAttr stops a spawned core from popping a console window.
func hiddenProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// requestStop cannot signal a console-less child, so callers fall through to Kill.
func requestStop(*os.Process) bool { return false }
