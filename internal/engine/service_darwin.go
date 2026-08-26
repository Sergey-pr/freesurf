//go:build darwin

package engine

import (
	"os"
	"os/signal"
	"syscall"
)

// MaybeRunService handles the privileged supervisor mode: launchd starts the
// root-owned copy of this binary with flagRunService, and it must never reach the
// Wails GUI. It returns true if the process was started that way and the caller
// (main) should exit afterwards.
func MaybeRunService() bool {
	if len(os.Args) < 2 || os.Args[1] != flagRunService {
		return false
	}

	files := darwinRootFiles()
	lg, closeLog := supervisorLogger(rootSupervisorLg)
	defer closeLog()

	// launchd stops the daemon with SIGTERM; bring the tunnel down with us rather
	// than leaving a root core behind holding the routing table.
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		close(stop)
	}()

	superviseTunnel(files, parseRequestPath(os.Args[2:]), stop, lg)
	return true
}
