package proxy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	if xrayVersionOK(path) {
		return path, nil
	}
	if err := installEmbeddedCore(paths.XrayName, path); err != nil {
		return "", err
	}
	if !xrayVersionOK(path) {
		return "", fmt.Errorf("embedded Xray did not report version %s", RequiredXrayVersion)
	}
	return path, nil
}

func xrayVersionOK(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	return err == nil && strings.Contains(string(out), RequiredXrayVersion)
}

// Process is a running core process. Exit is reported through a channel closed
// by the wait goroutine, so a supervisor never reads exec.Cmd fields that the
// same goroutine writes.
type Process struct {
	cmd  *exec.Cmd
	done chan struct{}
}

// Kill stops the process. Safe to call more than once, or after it has exited.
func (p *Process) Kill() {
	if p != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
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

// RunXray starts the (unprivileged) Xray process writing to logPath, returning the
// running process so the caller can supervise and stop it.
func RunXray(binPath, cfgPath, logPath string) (*Process, error) {
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath, "run", "-c", cfgPath)
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
