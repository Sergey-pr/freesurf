//go:build windows

package proxy

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// hiddenProcAttr stops a spawned core from popping a console window. The GUI app
// has no console of its own, so without this Windows allocates and shows a new
// one for the child (xray) - and closing that window would kill the tunnel.
func hiddenProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
