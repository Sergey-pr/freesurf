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

// RunXray starts the (unprivileged) Xray process writing to logPath, returning the
// running command so the caller can supervise and stop it.
func RunXray(binPath, cfgPath, logPath string) (*exec.Cmd, error) {
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath, "run", "-c", cfgPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = hiddenProcAttr() // no console window on Windows
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	go func() { _ = cmd.Wait(); logFile.Close() }()
	return cmd, nil
}
