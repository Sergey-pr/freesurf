//go:build !windows

package proxy

import (
	"os"
	"syscall"
)

// hiddenProcAttr is a no-op off Windows: there is no console window to suppress.
func hiddenProcAttr() *syscall.SysProcAttr { return nil }

// requestStop asks the process to shut down cleanly and reports whether the signal
// was delivered. This matters for sing-box: SIGTERM is what makes it undo the
// routes and DNS settings auto_route installed.
func requestStop(p *os.Process) bool { return p.Signal(syscall.SIGTERM) == nil }
