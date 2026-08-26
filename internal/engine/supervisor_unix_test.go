//go:build !windows

package engine

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeCore stands in for sing-box: it records that it was started, with which
// config, and then sits still until killed.
func fakeCore(t *testing.T, dir string) (bin, marker string) {
	t.Helper()
	bin = filepath.Join(dir, "fake-core")
	marker = filepath.Join(dir, "started")
	// The trap is the point of TestSupervisorTerminatesCoreGently: a core that is
	// killed outright never gets to run it.
	script := "#!/bin/sh\n" +
		"trap 'printf terminated > " + marker + "; exit 0' TERM\n" +
		"printf '%s' \"$3\" > " + marker + "\n" +
		"while :; do sleep 0.05; done\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return bin, marker
}

func supervisorHarness(t *testing.T) (files rootFiles, request string, stop chan struct{}, done chan struct{}) {
	t.Helper()
	dir := t.TempDir()
	bin, _ := fakeCore(t, dir)

	files = rootFiles{
		singbox: bin,
		config:  filepath.Join(dir, "config.json"),
		log:     filepath.Join(dir, "sing-box.log"),
		status:  filepath.Join(dir, "status.json"),
	}
	request = filepath.Join(dir, "tunnel.run")

	old := supervisorTick
	supervisorTick = 5 * time.Millisecond
	t.Cleanup(func() { supervisorTick = old })

	stop = make(chan struct{})
	done = make(chan struct{})
	go func() {
		superviseTunnel(files, request, stop, log.New(io.Discard, "", 0))
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		<-done
	})
	return files, request, stop, done
}

func waitForState(t *testing.T, statusPath, want string) tunnelStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := readStatus(statusPath); ok && st.State == want {
			return st
		}
		time.Sleep(2 * time.Millisecond)
	}
	st, _ := readStatus(statusPath)
	t.Fatalf("timed out waiting for state %q, last status %+v", want, st)
	return tunnelStatus{}
}

func TestSupervisorRunsWhileRequested(t *testing.T) {
	files, request, _, _ := supervisorHarness(t)
	const nonce = "0123456789abcdef"

	if err := writeRequest(request, tunnelRequest{Nonce: nonce, ServerIP: "198.51.100.7"}); err != nil {
		t.Fatal(err)
	}
	st := waitForState(t, files.status, tunnelRunning)
	if st.Nonce != nonce {
		t.Fatalf("status names run %q, want %q", st.Nonce, nonce)
	}

	// The config root runs is the one this process generated, and it carries the
	// requested pin.
	data, err := os.ReadFile(files.config)
	if err != nil {
		t.Fatalf("supervisor did not generate a config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("generated config is not JSON: %v", err)
	}
	if !containsIP(t, data, "198.51.100.7") {
		t.Error("the generated config does not pin the requested server IP")
	}

	if err := os.Remove(request); err != nil {
		t.Fatal(err)
	}
	waitForState(t, files.status, tunnelStopped)
}

func containsIP(t *testing.T, cfg []byte, ip string) bool {
	t.Helper()
	var doc struct {
		Route struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(cfg, &doc); err != nil {
		t.Fatal(err)
	}
	for _, rule := range doc.Route.Rules {
		cidrs, ok := rule["ip_cidr"].([]any)
		if !ok {
			continue
		}
		for _, c := range cidrs {
			if s, ok := c.(string); ok && s == ip+"/32" {
				return true
			}
		}
	}
	return false
}

// A request the supervisor cannot vouch for must leave the tunnel down.
func TestSupervisorIgnoresInvalidRequest(t *testing.T) {
	files, request, _, _ := supervisorHarness(t)

	if err := os.WriteFile(request, []byte(`{"nonce":"nope","serverIP":"1.2.3.4"}`), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	if _, ok := readStatus(files.status); ok {
		t.Error("the supervisor acted on a request it should have rejected")
	}
	if _, err := os.Stat(files.config); err == nil {
		t.Error("the supervisor generated a config for a rejected request")
	}
}

// Reconnecting without a stop in between must restart the core on the new run.
func TestSupervisorRestartsOnNewNonce(t *testing.T) {
	files, request, _, _ := supervisorHarness(t)

	if err := writeRequest(request, tunnelRequest{Nonce: "0123456789abcdef", ServerIP: "198.51.100.7"}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, files.status, tunnelRunning)

	if err := writeRequest(request, tunnelRequest{Nonce: "fedcba9876543210", ServerIP: "203.0.113.9"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st, ok := readStatus(files.status)
		if ok && st.Nonce == "fedcba9876543210" && st.State == tunnelRunning {
			data, err := os.ReadFile(files.config)
			if err != nil {
				t.Fatal(err)
			}
			if !containsIP(t, data, "203.0.113.9") {
				t.Fatal("the config was not regenerated for the new run")
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the supervisor did not pick up the new run")
}

func TestSupervisorReportsAFailedStart(t *testing.T) {
	files, request, _, _ := supervisorHarness(t)
	files.singbox = filepath.Join(t.TempDir(), "does-not-exist")

	// Restart the loop against the broken core path.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		superviseTunnel(files, request, stop, log.New(io.Discard, "", 0))
		close(done)
	}()
	defer func() { close(stop); <-done }()

	if err := writeRequest(request, tunnelRequest{Nonce: "0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	st := waitForState(t, files.status, tunnelFailed)
	if st.Message == "" {
		t.Error("a failed start was reported without a reason")
	}
}

// sing-box only unwinds the routes auto_route installed if it is asked to stop
// rather than killed - getting this wrong strands the machine's default route in a
// dead tun device and breaks networking well beyond this app.
func TestSupervisorTerminatesCoreGently(t *testing.T) {
	dir := t.TempDir()
	bin, marker := fakeCore(t, dir)
	files := rootFiles{
		singbox: bin,
		config:  filepath.Join(dir, "config.json"),
		log:     filepath.Join(dir, "sing-box.log"),
		status:  filepath.Join(dir, "status.json"),
	}
	request := filepath.Join(dir, "tunnel.run")

	old := supervisorTick
	supervisorTick = 5 * time.Millisecond
	defer func() { supervisorTick = old }()

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		superviseTunnel(files, request, stop, log.New(io.Discard, "", 0))
		close(done)
	}()
	defer func() { <-done }()

	if err := writeRequest(request, tunnelRequest{Nonce: "0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, files.status, tunnelRunning)
	// The marker appearing means the shell got far enough to install its trap;
	// without waiting, the stop below can outrun the process's own startup.
	waitForFile(t, marker)

	if err := os.Remove(request); err != nil {
		t.Fatal(err)
	}
	waitForState(t, files.status, tunnelStopped)
	close(stop)

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "terminated" {
		t.Fatalf("core recorded %q; it was killed rather than asked to stop", got)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
