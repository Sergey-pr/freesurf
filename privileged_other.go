//go:build !darwin

package main

import (
	"fmt"
	"runtime"
)

// TUN auto-start with privilege elevation is implemented for macOS first.
// Linux (pkexec/sudo) and Windows (runas + wintun.dll) are the next platform steps.

func startTunnelPrivileged(_, _, _, _ string, _ int) (scriptPID, singboxPID int, err error) {
	return 0, 0, fmt.Errorf("TUN mode is not yet supported on %s", runtime.GOOS)
}

func processAlive(_ int) bool {
	return false
}

func FreePrivilegedAuthorization() {}
