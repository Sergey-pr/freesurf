package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// startTunnel signals the privileged helper to bring the tunnel up by creating the
// sentinel file the LaunchDaemon watches. Pure Go, no privileges, no prompt.
func startTunnel() error {
	sp, err := sentinelPath()
	if err != nil {
		return err
	}
	return os.WriteFile(sp, []byte("run\n"), 0644)
}

// stopTunnel signals the helper to stop by removing the sentinel file.
func stopTunnel() {
	if sp, err := sentinelPath(); err == nil {
		_ = os.Remove(sp)
	}
}

// waitTunnelUp polls the core log until sing-box reports it started, or a fatal
// error appears, or the timeout elapses.
func waitTunnelUp(logPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(logPath)
		s := string(data)
		if line := firstMatch(s, "FATAL"); line != "" {
			return fmt.Errorf("sing-box failed to start: %s", line)
		}
		// Either the startup banner or the TUN coming up means we're live.
		if strings.Contains(s, "sing-box started") || strings.Contains(s, "started at utun") || strings.Contains(s, "started at tun") {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for the tunnel to come up (see logs)")
}

// firstMatch returns the first line containing sub, trimmed, or "".
func firstMatch(text, sub string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, sub) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
