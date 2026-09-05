package proxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"freesurf/internal/paths"
)

// Xray-core handles the node's protocol as a local SOCKS server that the TUN feeds.
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

// Process is a running core, whose exit is reported by a channel, not exec.Cmd fields.
type Process struct {
	cmd  *exec.Cmd
	done chan struct{}
}

// Stop signals the process, killing it after grace; sing-box needs it to unwind routes.
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

// RunSingbox starts sing-box from its own directory, so Windows finds wintun.dll.
func RunSingbox(binPath, cfgPath, logPath string) (*Process, error) {
	// 0644: the unprivileged app tails this out of the root-owned directory.
	return runCore(binPath, filepath.Dir(binPath), cfgPath, logPath, 0644)
}

// RunXray starts the (unprivileged) Xray process writing to logPath, returning the
// running process so the caller can supervise and stop it.
func RunXray(binPath, cfgPath, logPath string) (*Process, error) {
	return runCore(binPath, "", cfgPath, logPath, 0600)
}

// runCore runs a core with `run -c <cfgPath>`, truncating logPath for its output.
func runCore(binPath, dir, cfgPath, logPath string, logPerm os.FileMode) (*Process, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, logPerm)
	if err != nil {
		return nil, err
	}
	// O_CREATE only sets the mode on a file it creates.
	if err := logFile.Chmod(logPerm); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		_ = logFile.Close()
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
