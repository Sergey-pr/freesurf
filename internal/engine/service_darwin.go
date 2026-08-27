//go:build darwin

package engine

import (
	"os"
	"os/signal"
	"syscall"
)

// MaybeRunService runs the launchd supervisor, returning true so main skips the GUI.
func MaybeRunService() bool {
	if len(os.Args) < 2 || os.Args[1] != flagRunService {
		return false
	}

	files := darwinRootFiles()
	lg, closeLog := supervisorLogger(rootSupervisorLg)
	defer closeLog()

	// Take the tunnel down with us instead of orphaning a core that owns the routes.
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
