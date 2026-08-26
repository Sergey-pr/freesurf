package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
)

// The app and the privileged supervisor talk through exactly two files. The app
// writes a request into its own data directory; the supervisor writes a status
// into the root-owned directory. Nothing else crosses the boundary - in
// particular the supervisor generates the core's config itself, so the request
// below is the whole of what an unprivileged process can influence.
//
// A local attacker who rewrites the request can pick the pinned server IP, which
// costs one direct-route rule (traffic to that address leaves the tunnel). That
// is the price of letting the unprivileged side say which server it dialled; it
// is bounded, unlike handing root a config document.

// Connection states reported by the supervisor.
const (
	tunnelStarting = "starting"
	tunnelRunning  = "running"
	tunnelStopped  = "stopped"
	tunnelFailed   = "failed"
)

// nonceRe is deliberately strict: the nonce is echoed back into a root-owned file.
var nonceRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// tunnelRequest is the run flag the supervisor watches. Its presence means "run";
// nonce names this particular run so the app can tell whose status it is reading.
type tunnelRequest struct {
	Nonce    string `json:"nonce"`
	ServerIP string `json:"serverIP,omitempty"`
}

// tunnelStatus is the supervisor's report on the run named by Nonce.
type tunnelStatus struct {
	Nonce   string `json:"nonce"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

func newNonce() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func writeRequest(path string, req tunnelRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

// readRequest parses and validates the request. Anything malformed is treated as
// absent: the supervisor runs as root and must not act on input it cannot vouch
// for.
func readRequest(path string) (tunnelRequest, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tunnelRequest{}, false
	}
	var req tunnelRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return tunnelRequest{}, false
	}
	if !nonceRe.MatchString(req.Nonce) {
		return tunnelRequest{}, false
	}
	if req.ServerIP != "" && net.ParseIP(req.ServerIP) == nil {
		return tunnelRequest{}, false
	}
	return req, true
}

// writeStatus replaces the status file atomically, so the app never reads a
// half-written report.
func writeStatus(path string, st tunnelStatus) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".status-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0644); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

func readStatus(path string) (tunnelStatus, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tunnelStatus{}, false
	}
	var st tunnelStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return tunnelStatus{}, false
	}
	return st, true
}

func (st tunnelStatus) err() error {
	if st.Message != "" {
		return fmt.Errorf("%s", st.Message)
	}
	return fmt.Errorf("the tunnel failed to start (see logs)")
}
