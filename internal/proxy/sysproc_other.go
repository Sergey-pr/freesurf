//go:build !windows

package proxy

import "syscall"

// hiddenProcAttr is a no-op off Windows: there is no console window to suppress.
func hiddenProcAttr() *syscall.SysProcAttr { return nil }
