package engine

import (
	"fmt"
	"os"
	"time"

	"freesurf/internal/paths"
)

// startTunnel requests the tunnel for the pinned serverIP, returning this run's nonce.
func startTunnel(serverIP string) (string, error) {
	path, err := paths.Sentinel()
	if err != nil {
		return "", err
	}
	nonce, err := newNonce()
	if err != nil {
		return "", err
	}
	if err := writeRequest(path, tunnelRequest{Nonce: nonce, ServerIP: serverIP}); err != nil {
		return "", err
	}
	return nonce, nil
}

// stopTunnel withdraws the request; the supervisor stops the core within ~1s.
func stopTunnel() {
	if path, err := paths.Sentinel(); err == nil {
		_ = os.Remove(path)
	}
}

// ClearSentinel drops a request left by a crash, so startup begins disconnected.
func ClearSentinel() { stopTunnel() }

// waitTunnelUp polls the status for this nonce, ignoring reports from other runs.
func waitTunnelUp(path, nonce string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st, ok := readStatus(path); ok && st.Nonce == nonce {
			switch st.State {
			case tunnelRunning:
				return nil
			case tunnelFailed:
				return st.err()
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for the tunnel to come up (see logs)")
}
