//go:build !windows

package proxy

import (
	"os"
	"syscall"
)

// hiddenProcAttr is a no-op off Windows: there is no console window to suppress.
func hiddenProcAttr() *syscall.SysProcAttr { return nil }

// requestStop asks the process to shut down cleanly, reporting whether it was told.
func requestStop(p *os.Process) bool { return p.Signal(syscall.SIGTERM) == nil }
