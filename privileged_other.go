//go:build !darwin

package main

import (
	"fmt"
	"runtime"
)

// The privileged TUN helper is implemented for macOS first (launchd LaunchDaemon).
// Linux (systemd unit / pkexec) and Windows (a Windows service + UAC) are the next
// platform steps — both pure Go, no CGO. The tunnel start/stop (sentinel file) is
// already cross-platform in helper.go; only install/uninstall is platform-specific.

func helperInstalled() bool { return false }

func ensureHelper(_ string) error {
	return fmt.Errorf("TUN mode is not yet supported on %s", runtime.GOOS)
}

func uninstallHelper() error { return nil }
