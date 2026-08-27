package engine

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"freesurf/internal/store"
)

// fakeProcess stands in for the Xray backend, so no real process is spawned.
type fakeProcess struct {
	mu     sync.Mutex
	killed int
	exited bool
}

func (p *fakeProcess) Stop(time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killed++
	p.exited = true
}

func (p *fakeProcess) Exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

// die makes the process look like it crashed on its own.
func (p *fakeProcess) die() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exited = true
}

func (p *fakeProcess) kills() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

// harness wires an Engine to fake deps and records what they were asked to do.
type harness struct {
	t   *testing.T
	e   *Engine
	dir string

	mu         sync.Mutex
	procs      []*fakeProcess
	states     []ConnState
	logEvents  [][]string
	tunnelUp   int
	tunnelDown int
	serverIP   string
}

func (h *harness) setServerIP(ip string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.serverIP = ip
}

func (h *harness) pinnedIP() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.serverIP
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir()}
	h.e = &Engine{
		conn: ConnState{Status: StatusDisconnected},
		// Fast enough that the watchers react within a test's patience.
		monitorTick: 2 * time.Millisecond,
		tailTick:    2 * time.Millisecond,
		deps: deps{
			ensureCore:      func(context.Context) (string, error) { return "sing-box", nil },
			ensureXray:      func(context.Context) (string, error) { return "xray", nil },
			writeXrayConfig: func(*store.Node) (string, string, error) { return h.path("xray.json"), "192.0.2.1", nil },
			singboxConfig:   func(string) ([]byte, error) { return []byte("{}"), nil },
			checkConfig:     func(string, []byte) error { return nil },
			helperInstalled: func() bool { return true },
			ensureHelper:    func(string) error { return nil },
			coreLog:         func() (string, error) { return h.path("sing-box.log"), nil },
			xrayLog:         func() (string, error) { return h.path("xray.log"), nil },
			runXray:         func(string, string, string) (process, error) { return h.newProc(), nil },
			startTunnel: func(serverIP string) (string, error) {
				h.bump(&h.tunnelUp)
				h.setServerIP(serverIP)
				return "0123456789abcdef", nil
			},
			waitTunnelUp: func(string, time.Duration) error { return nil },
			stopTunnel:   func() { h.bump(&h.tunnelDown) },
			emit:         h.emit,
		},
	}
	return h
}

func (h *harness) path(name string) string { return filepath.Join(h.dir, name) }

func (h *harness) bump(n *int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	*n++
}

func (h *harness) counts() (up, down int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tunnelUp, h.tunnelDown
}

func (h *harness) newProc() *fakeProcess {
	p := &fakeProcess{}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.procs = append(h.procs, p)
	return p
}

func (h *harness) lastProc() *fakeProcess {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.procs) == 0 {
		h.t.Fatal("no backend process was started")
	}
	return h.procs[len(h.procs)-1]
}

func (h *harness) emit(name string, data ...any) {
	if len(data) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if st, ok := data[0].(ConnState); ok && name == "vpn:state" {
		h.states = append(h.states, st)
	}
	if lines, ok := data[0].([]string); ok && name == "log:line" {
		h.logEvents = append(h.logEvents, lines)
	}
}

func (h *harness) logs() [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]string(nil), h.logEvents...)
}

func (h *harness) statuses() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.states))
	for i, s := range h.states {
		out[i] = s.Status
	}
	return out
}

// stopChan returns the current generation's stop channel.
func (h *harness) stopChan() chan struct{} {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	return h.e.stop
}

func (h *harness) waitStatus(status string) ConnState {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := h.e.State(); st.Status == status {
			return st
		}
		time.Sleep(time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for status %q, engine is %+v", status, h.e.State())
	return ConnState{}
}

func testNode() *store.Node {
	return &store.Node{ID: 7, Name: "node-7", URI: "vless://uuid@example.com:443"}
}

func TestConnectBringsTunnelUp(t *testing.T) {
	h := newHarness(t)

	state, err := h.e.Connect(testNode())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if state.Status != StatusConnected || state.NodeID != 7 {
		t.Fatalf("got %+v, want connected node 7", state)
	}
	if up, down := h.counts(); up != 1 || down != 0 {
		t.Fatalf("tunnel start/stop = %d/%d, want 1/0", up, down)
	}
	// The supervisor needs the pinned IP to route Xray's own traffic out directly.
	if ip := h.pinnedIP(); ip != "192.0.2.1" {
		t.Fatalf("server IP handed to the supervisor = %q, want 192.0.2.1", ip)
	}
	last := h.statuses()[len(h.statuses())-1]
	if last != StatusConnected {
		t.Fatalf("last emitted status = %q, want connected", last)
	}
	h.e.Disconnect()
}

func TestConnectFailureCleansUp(t *testing.T) {
	tests := []struct {
		name       string
		breakDep   func(*deps, error)
		wantKilled bool // the backend was started, so it must be killed
		wantStop   bool // the tunnel was started, so it must be stopped
	}{
		{
			name:     "core install fails",
			breakDep: func(d *deps, err error) { d.ensureCore = func(context.Context) (string, error) { return "", err } },
		},
		{
			name:     "config check fails",
			breakDep: func(d *deps, err error) { d.checkConfig = func(string, []byte) error { return err } },
		},
		{
			name:     "helper install fails",
			breakDep: func(d *deps, err error) { d.ensureHelper = func(string) error { return err } },
		},
		{
			name:       "tunnel start fails",
			breakDep:   func(d *deps, err error) { d.startTunnel = func(string) (string, error) { return "", err } },
			wantKilled: true,
		},
		{
			name:       "tunnel never comes up",
			breakDep:   func(d *deps, err error) { d.waitTunnelUp = func(string, time.Duration) error { return err } },
			wantKilled: true,
			wantStop:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			boom := errors.New("boom")
			tc.breakDep(&h.e.deps, boom)

			state, err := h.e.Connect(testNode())
			if !errors.Is(err, boom) {
				t.Fatalf("Connect error = %v, want boom", err)
			}
			if state.Status != StatusDisconnected || state.Message != "boom" {
				t.Fatalf("got %+v, want disconnected with the error message", state)
			}
			if h.e.State().Status != StatusDisconnected {
				t.Fatalf("engine state = %+v, want disconnected", h.e.State())
			}
			if h.stopChan() != nil {
				t.Fatal("a failed connect left a live tunnel generation behind")
			}
			if tc.wantKilled && h.lastProc().kills() != 1 {
				t.Fatalf("backend kills = %d, want 1", h.lastProc().kills())
			}
			if _, down := h.counts(); tc.wantStop != (down > 0) {
				t.Fatalf("tunnel stops = %d, want stopped=%v", down, tc.wantStop)
			}
		})
	}
}

func TestDisconnectStopsBackendAndWatchers(t *testing.T) {
	h := newHarness(t)
	if _, err := h.e.Connect(testNode()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	stop := h.stopChan()
	xray := h.lastProc()

	state := h.e.Disconnect()
	if state.Status != StatusDisconnected {
		t.Fatalf("got %+v, want disconnected", state)
	}
	if xray.kills() != 1 {
		t.Fatalf("backend kills = %d, want 1", xray.kills())
	}
	if _, down := h.counts(); down != 1 {
		t.Fatalf("tunnel stops = %d, want 1", down)
	}
	select {
	case <-stop:
	default:
		t.Fatal("stop channel still open: monitor and log tail would leak")
	}
	if h.stopChan() != nil {
		t.Fatal("stop channel not cleared")
	}
}

func TestMonitorReportsBackendDeath(t *testing.T) {
	h := newHarness(t)
	if _, err := h.e.Connect(testNode()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	stop := h.stopChan()

	h.lastProc().die()

	state := h.waitStatus(StatusDisconnected)
	if !strings.Contains(state.Message, "see logs") {
		t.Fatalf("message = %q, want it to point at the logs", state.Message)
	}
	if _, down := h.counts(); down != 1 {
		t.Fatalf("tunnel stops = %d, want 1", down)
	}
	select {
	case <-stop:
	default:
		t.Fatal("stop channel still open: the log tail would keep running")
	}
}

// A second Connect must not strand the previous generation's watchers.
func TestReconnectClosesPreviousGeneration(t *testing.T) {
	h := newHarness(t)
	if _, err := h.e.Connect(testNode()); err != nil {
		t.Fatalf("first Connect: %v", err)
	}
	first := h.stopChan()

	if _, err := h.e.Connect(testNode()); err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	select {
	case <-first:
	default:
		t.Fatal("previous stop channel still open")
	}
	if h.stopChan() == first {
		t.Fatal("second connect reused the closed stop channel")
	}
	h.e.Disconnect()
}

// Connect and Disconnect racing must stay race-free and end in a consistent state.
func TestConcurrentConnectDisconnect(t *testing.T) {
	h := newHarness(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.e.Connect(testNode())
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.e.Disconnect()
		}()
	}
	wg.Wait()

	if state := h.e.Disconnect(); state.Status != StatusDisconnected {
		t.Fatalf("got %+v, want disconnected", state)
	}
	if h.stopChan() != nil {
		t.Fatal("a tunnel generation survived the final Disconnect")
	}
}

func TestLogBufferIsCapped(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < logBufferMax+50; i++ {
		h.e.logf("line %d", i)
	}
	lines := strings.Split(h.e.LogText(), "\n")
	if len(lines) != logBufferMax {
		t.Fatalf("buffer holds %d lines, want %d", len(lines), logBufferMax)
	}
	if !strings.HasSuffix(lines[len(lines)-1], "line 849") {
		t.Fatalf("last line = %q, want the newest one", lines[len(lines)-1])
	}
	h.e.ClearLog()
	if h.e.LogText() != "" {
		t.Fatal("ClearLog left the buffer non-empty")
	}
}

func TestLogStreamingIsGatedAndBatched(t *testing.T) {
	h := newHarness(t)

	h.e.logLines("a", "b", "c")
	if n := len(h.logs()); n != 0 {
		t.Fatalf("emitted %d events while streaming is off, want 0", n)
	}
	if !strings.Contains(h.e.LogText(), "a") {
		t.Fatal("lines skipped the buffer while streaming is off")
	}

	h.e.SetLogStreaming(true)
	h.e.logLines("d", "e", "f")
	h.e.logLines()
	events := h.logs()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 per batch (and none for an empty one)", len(events))
	}
	if len(events[0]) != 3 {
		t.Fatalf("batch carried %d lines, want 3", len(events[0]))
	}
}

func TestWaitTunnelUp(t *testing.T) {
	const nonce = "0123456789abcdef"
	tests := []struct {
		name    string
		status  *tunnelStatus
		wantErr string
	}{
		{name: "running", status: &tunnelStatus{Nonce: nonce, State: tunnelRunning}},
		{
			name:    "failed",
			status:  &tunnelStatus{Nonce: nonce, State: tunnelFailed, Message: "bind: permission denied"},
			wantErr: "permission denied",
		},
		{name: "no status at all", wantErr: "timed out"},
		{
			// A success left by an earlier run must not be read as this one's.
			name:    "status from another run",
			status:  &tunnelStatus{Nonce: "fedcba9876543210", State: tunnelRunning},
			wantErr: "timed out",
		},
		{name: "still starting", status: &tunnelStatus{Nonce: nonce, State: tunnelStarting}, wantErr: "timed out"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "status.json")
			if tc.status != nil {
				if err := writeStatus(path, *tc.status); err != nil {
					t.Fatal(err)
				}
			}
			err := waitTunnelUp(path, nonce, 400*time.Millisecond)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("waitTunnelUp: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCleanCoreLine(t *testing.T) {
	tests := []struct{ in, want string }{
		{"\x1b[36m+0300 2026-06-25 11:46:08 \x1b[0mINFO router: started", "INFO router: started"},
		{"+0300 2026-06-25 11:46:08 INFO [4294882405 3ms] outbound/direct", "INFO outbound/direct"},
		{"  plain line\r", "plain line"},
	}
	for _, tc := range tests {
		if got := cleanCoreLine(tc.in); got != tc.want {
			t.Errorf("cleanCoreLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
