package proxy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"freesurf/internal/paths"
)

// Xray-core provides the proxy protocols sing-box lacks (notably XHTTP/splithttp
// over h2/h3). It runs as a local SOCKS server that sing-box's TUN forwards to.
const (
	// RequiredXrayVersion is the pinned Xray-core release. Bumping it requires
	// re-running cmd/fetchcores (the Taskfile build tasks do this automatically).
	RequiredXrayVersion = "26.3.27"

	socksPort = 10808 // local SOCKS port sing-box forwards to
)

// EnsureXray installs the pinned Xray binary (embedded at build time) if
// missing/out of date, returning its path.
func EnsureXray(ctx context.Context) (string, error) {
	path, err := paths.Xray()
	if err != nil {
		return "", err
	}
	if IsEmbeddedCore(paths.XrayName, path) {
		return path, nil
	}
	if err := installEmbeddedCore(paths.XrayName, path); err != nil {
		return "", err
	}
	if !IsEmbeddedCore(paths.XrayName, path) {
		return "", fmt.Errorf("installed Xray does not match the core embedded in this build")
	}
	return path, nil
}

// Process is a running core process. Exit is reported through a channel closed
// by the wait goroutine, so a supervisor never reads exec.Cmd fields that the
// same goroutine writes.
type Process struct {
	cmd  *exec.Cmd
	done chan struct{}
}

// Stop shuts the process down, asking politely first and waiting up to grace
// before killing it. sing-box only unwinds the routes and DNS settings auto_route
// installed if it gets that chance - killed outright, it leaves the machine routed
// into a tun device that no longer exists, which breaks all networking until the
// system cleans up. Safe to call more than once, or after the process has exited.
func (p *Process) Stop(grace time.Duration) {
	if p == nil || p.cmd.Process == nil {
		return
	}
	if requestStop(p.cmd.Process) {
		select {
		case <-p.done:
			return
		case <-time.After(grace):
		}
	}
	_ = p.cmd.Process.Kill()
	<-p.done
}

// Exited reports whether the process has terminated.
func (p *Process) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// RunSingbox starts the sing-box core writing to logPath, returning the running
// process. Called by the privileged supervisor; cmd.Dir is the binary's own
// directory so a sibling wintun.dll is found on Windows.
func RunSingbox(binPath, cfgPath, logPath string) (*Process, error) {
	return runCore(binPath, filepath.Dir(binPath), cfgPath, logPath)
}

// RunXray starts the (unprivileged) Xray process writing to logPath, returning the
// running process so the caller can supervise and stop it.
func RunXray(binPath, cfgPath, logPath string) (*Process, error) {
	return runCore(binPath, "", cfgPath, logPath)
}

// runCore starts a core binary with `run -c <cfgPath>`, truncating logPath and
// sending both its streams there.
func runCore(binPath, dir, cfgPath, logPath string) (*Process, error) {
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath, "run", "-c", cfgPath)
	cmd.Dir = dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = hiddenProcAttr() // no console window on Windows
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	p := &Process{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		close(p.done)
	}()
	return p, nil
}
