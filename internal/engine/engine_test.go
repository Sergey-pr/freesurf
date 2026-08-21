package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"freesurf/internal/store"
)

// fakeProcess stands in for the Xray backend so the lifecycle can be driven
// without spawning a real process.
type fakeProcess struct {
	mu     sync.Mutex
	killed int
	exited bool
}

func (p *fakeProcess) Kill() {
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
	tunnelUp   int
	tunnelDown int
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
			ensureCore:         func(context.Context) (string, error) { return "sing-box", nil },
			ensureXray:         func(context.Context) (string, error) { return "xray", nil },
			writeXrayConfig:    func(*store.Node) (string, string, error) { return h.path("xray.json"), "192.0.2.1", nil },
			writeSingboxConfig: func(string) (string, error) { return h.path("config.json"), nil },
			checkConfig:        func(string, string) error { return nil },
			helperInstalled:    func() bool { return true },
			ensureHelper:       func(string) error { return nil },
			coreLog:            func() (string, error) { return h.path("sing-box.log"), nil },
			xrayLog:            func() (string, error) { return h.path("xray.log"), nil },
			runXray:            func(string, string, string) (process, error) { return h.newProc(), nil },
			startTunnel:        func() error { h.bump(&h.tunnelUp); return nil },
			waitTunnelUp:       func(string, time.Duration) error { return nil },
			stopTunnel:         func() { h.bump(&h.tunnelDown) },
			emit:               h.emit,
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
	if name != "vpn:state" || len(data) == 0 {
		return
	}
	st, ok := data[0].(ConnState)
	if !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.states = append(h.states, st)
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
			breakDep: func(d *deps, err error) { d.checkConfig = func(string, string) error { return err } },
		},
		{
			name:     "helper install fails",
			breakDep: func(d *deps, err error) { d.ensureHelper = func(string) error { return err } },
		},
		{
			name:       "tunnel start fails",
			breakDep:   func(d *deps, err error) { d.startTunnel = func() error { return err } },
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

// Connect and Disconnect racing each other must stay race-free and end in a
// consistent state. Rejecting a concurrent Connect outright is M1-T4.
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

func TestWaitTunnelUp(t *testing.T) {
	tests := []struct {
		name    string
		log     string
		wantErr string
	}{
		{name: "started", log: "+0300 2026-06-25 11:46:08 INFO sing-box started at utun4\n"},
		{name: "fatal", log: "FATAL start service: bind: permission denied\n", wantErr: "sing-box failed to start"},
		{name: "silent", log: "nothing interesting\n", wantErr: "timed out"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sing-box.log")
			if err := os.WriteFile(path, []byte(tc.log), 0600); err != nil {
				t.Fatal(err)
			}
			err := waitTunnelUp(path, 400*time.Millisecond)
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
