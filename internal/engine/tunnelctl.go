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

	"freesurf/internal/paths"
)

// The app requests, the supervisor answers with a status; nothing else crosses over.

// Connection states reported by the supervisor.
const (
	tunnelStarting = "starting"
	tunnelRunning  = "running"
	tunnelStopped  = "stopped"
	tunnelFailed   = "failed"
)

// Strict: the nonce is echoed back into a root-owned file.
var nonceRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// tunnelRequest is the run flag the supervisor watches, named by a nonce.
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
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return err
	}
	return paths.RestrictFile(path)
}

// readRequest validates the request; root must not act on anything malformed.
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

// writeStatus replaces the status atomically, so the app never reads half a report.
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
