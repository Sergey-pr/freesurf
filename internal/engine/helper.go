package engine

import (
	"fmt"
	"os"
	"strings"
	"time"

	"freesurf/internal/paths"
)

// startTunnel signals the privileged helper to bring the tunnel up by creating the
// sentinel file it watches. Pure Go, no privileges, no prompt.
func startTunnel() error {
	sp, err := paths.Sentinel()
	if err != nil {
		return err
	}
	return os.WriteFile(sp, []byte("run\n"), 0644)
}

// stopTunnel signals the helper to stop by removing the sentinel file.
func stopTunnel() {
	if sp, err := paths.Sentinel(); err == nil {
		_ = os.Remove(sp)
	}
}

// ClearSentinel removes the run flag so the tunnel is down - used at startup to
// recover from a stale sentinel left by a previous crash.
func ClearSentinel() { stopTunnel() }

// waitTunnelUp polls the core log until sing-box reports it started, a fatal error
// appears, or the timeout elapses.
func waitTunnelUp(logPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(logPath)
		s := string(data)
		if line := firstMatch(s, "FATAL"); line != "" {
			return fmt.Errorf("sing-box failed to start: %s", line)
		}
		if strings.Contains(s, "sing-box started") || strings.Contains(s, "started at utun") || strings.Contains(s, "started at tun") {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for the tunnel to come up (see logs)")
}

func firstMatch(text, sub string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, sub) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
