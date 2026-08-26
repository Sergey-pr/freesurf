package engine

import (
	"fmt"
	"os"
	"time"

	"freesurf/internal/paths"
)

// startTunnel asks the privileged supervisor to bring the tunnel up by writing the
// request file it watches, and returns the nonce naming this run. serverIP is the
// address Xray dials, pinned so the supervisor can route Xray's own traffic out
// directly. Pure Go, no privileges, no prompt.
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

// stopTunnel withdraws the request, which the supervisor answers by stopping the
// core within ~1s.
func stopTunnel() {
	if path, err := paths.Sentinel(); err == nil {
		_ = os.Remove(path)
	}
}

// ClearSentinel withdraws any leftover request so the tunnel is down - used at
// startup to recover from one left by a previous crash.
func ClearSentinel() { stopTunnel() }

// waitTunnelUp polls the supervisor's status file until it reports on this run.
// Reports from earlier runs are ignored, so a stale success can't be mistaken for
// this one.
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
