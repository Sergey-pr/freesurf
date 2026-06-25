//go:build !darwin && !windows

package engine

import (
	"fmt"
	"runtime"
)

// The privileged TUN helper is implemented for macOS (launchd LaunchDaemon) and
// Windows (a native Go Windows service - see privileged_windows.go). Linux
// (systemd unit / pkexec) is next - pure Go, no CGO. Tunnel start/stop (the
// sentinel file) is already cross-platform in helper.go; only install/uninstall
// is platform-specific.

func HelperInstalled() bool { return false }

func EnsureHelper(_ string) error {
	return fmt.Errorf("TUN mode is not yet supported on %s", runtime.GOOS)
}

func UninstallHelper() error { return nil }
