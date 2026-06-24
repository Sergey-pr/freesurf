package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Connection status values surfaced to the UI.
const (
	StatusDisconnected = "disconnected"
	StatusConnecting   = "connecting"
	StatusConnected    = "connected"
)

// ConnState is the VPN connection state surfaced to the UI.
type ConnState struct {
	Status  string `json:"status"`
	NodeID  int64  `json:"nodeId"`
	Message string `json:"message,omitempty"`
}

const logBufferMax = 800

// Engine owns the sing-box lifecycle: downloading the core, generating the
// config, and supervising the privileged TUN process.
type Engine struct {
	mu        sync.Mutex
	conn      ConnState
	scriptPID int
	corePID   int
	xrayCmd   *exec.Cmd     // local Xray process (unprivileged)
	stop      chan struct{} // closed to stop the liveness monitor

	logMu  sync.Mutex
	logBuf []string
}

func newEngine() *Engine {
	return &Engine{conn: ConnState{Status: StatusDisconnected}}
}

func (e *Engine) state() ConnState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conn
}

func (e *Engine) setState(s ConnState) {
	e.mu.Lock()
	e.conn = s
	e.mu.Unlock()
	application.Get().Event.Emit("vpn:state", s)
}

// logf appends a timestamped line to the in-memory log buffer and emits it so any
// open logs window updates live.
func (e *Engine) logf(format string, args ...any) {
	line := fmt.Sprintf("%s  %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	e.logMu.Lock()
	e.logBuf = append(e.logBuf, line)
	if len(e.logBuf) > logBufferMax {
		e.logBuf = e.logBuf[len(e.logBuf)-logBufferMax:]
	}
	e.logMu.Unlock()
	application.Get().Event.Emit("log:line", line)
}

// logText returns the full log buffer as a single string.
func (e *Engine) logText() string {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	return strings.Join(e.logBuf, "\n")
}

func (e *Engine) clearLog() {
	e.logMu.Lock()
	e.logBuf = nil
	e.logMu.Unlock()
	application.Get().Event.Emit("log:cleared")
}

// connect brings up the tunnel to the given node. It reports progress through the
// "vpn:state" event and returns the final state. The returned error (if any) is
// for the caller to surface; the state is already emitted.
func (e *Engine) connect(node *Node) (ConnState, error) {
	e.logf("Connecting to %q…", node.Name)
	e.setState(ConnState{Status: StatusConnecting, NodeID: node.ID, Message: "Preparing core…"})

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	e.logf("Ensuring cores (sing-box %s, xray %s)…", requiredCoreVersion, requiredXrayVersion)
	bin, err := ensureCore(ctx)
	if err != nil {
		return e.fail(node.ID, err)
	}
	xrayBin, err := ensureXray(ctx)
	if err != nil {
		return e.fail(node.ID, err)
	}

	e.setState(ConnState{Status: StatusConnecting, NodeID: node.ID, Message: "Building config…"})
	e.logf("Generating configs…")
	xrayCfg, err := writeXrayConfig(node)
	if err != nil {
		return e.fail(node.ID, err)
	}
	cfg, err := writeSingboxConfig(xraySocksPort)
	if err != nil {
		return e.fail(node.ID, err)
	}
	if err := checkConfig(bin, cfg); err != nil {
		return e.fail(node.ID, err)
	}
	e.logf("Config OK (sing-box check passed).")

	// Start Xray (unprivileged) first so its SOCKS port is ready for sing-box.
	xrayLog, err := xrayLogPath()
	if err != nil {
		return e.fail(node.ID, err)
	}
	e.logf("Starting Xray (proxy backend)…")
	xrayCmd, err := runXray(xrayBin, xrayCfg, xrayLog)
	if err != nil {
		return e.fail(node.ID, err)
	}

	logPath, err := coreLogPath()
	if err != nil {
		e.stopXray(xrayCmd)
		return e.fail(node.ID, err)
	}
	// (The privileged start script truncates the root-owned log for a fresh run.)

	sentinel, err := sentinelPath()
	if err != nil {
		e.stopXray(xrayCmd)
		return e.fail(node.ID, err)
	}
	if err := os.WriteFile(sentinel, []byte("run\n"), 0644); err != nil {
		e.stopXray(xrayCmd)
		return e.fail(node.ID, err)
	}

	e.setState(ConnState{Status: StatusConnecting, NodeID: node.ID, Message: "Starting tunnel (admin)…"})
	e.logf("Starting tunnel (requesting privileges if needed)…")
	scriptPID, corePID, err := startTunnelPrivileged(bin, cfg, logPath, sentinel, os.Getpid())
	if err != nil {
		_ = os.Remove(sentinel)
		e.stopXray(xrayCmd)
		return e.fail(node.ID, err)
	}

	e.mu.Lock()
	e.scriptPID = scriptPID
	e.corePID = corePID
	e.xrayCmd = xrayCmd
	e.stop = make(chan struct{})
	stop := e.stop
	e.mu.Unlock()

	go e.monitor(corePID, stop)
	go e.tailCore(logPath, stop)

	e.logf("Tunnel up (core PID %d).", corePID)
	state := ConnState{Status: StatusConnected, NodeID: node.ID}
	e.setState(state)
	return state, nil
}

func (e *Engine) fail(nodeID int64, err error) (ConnState, error) {
	e.logf("ERROR: %v", err)
	e.appendCoreLogTail()
	e.appendXrayLogTail()
	state := ConnState{Status: StatusDisconnected, NodeID: nodeID, Message: err.Error()}
	e.setState(state)
	return state, err
}

// stopXray terminates the (unprivileged) Xray process if running.
func (e *Engine) stopXray(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// disconnect tears down the tunnel (user-initiated) and notifies the UI.
func (e *Engine) disconnect() ConnState {
	return e.teardown(true)
}

// shutdown tears down the tunnel on app exit without emitting events (the UI is
// already going away). It must not block — stopping is just removing the
// sentinel file, which the privileged core watches.
func (e *Engine) shutdown() {
	e.teardown(false)
}

func (e *Engine) teardown(emit bool) ConnState {
	e.mu.Lock()
	had := e.scriptPID > 0 || e.corePID > 0
	xrayCmd := e.xrayCmd
	if e.stop != nil {
		close(e.stop)
		e.stop = nil
	}
	e.scriptPID, e.corePID = 0, 0
	e.xrayCmd = nil
	e.mu.Unlock()

	// Removing the sentinel makes the privileged babysitter kill the core within
	// ~1s — no privileged call (and therefore no password prompt) needed here.
	if sp, err := sentinelPath(); err == nil {
		_ = os.Remove(sp)
	}
	e.stopXray(xrayCmd)
	if had && emit {
		e.logf("Stopping tunnel…")
	}

	state := ConnState{Status: StatusDisconnected}
	e.mu.Lock()
	e.conn = state
	e.mu.Unlock()
	if emit {
		application.Get().Event.Emit("vpn:state", state)
	}
	return state
}

// monitor watches the core process and flips the UI to disconnected if it dies.
func (e *Engine) monitor(corePID int, stop chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			e.mu.Lock()
			xrayDead := e.xrayCmd != nil && e.xrayCmd.ProcessState != nil
			e.mu.Unlock()
			if processAlive(corePID) && !xrayDead {
				continue
			}
			e.mu.Lock()
			active := e.corePID == corePID
			if active {
				xrayCmd := e.xrayCmd
				e.scriptPID, e.corePID = 0, 0
				e.xrayCmd = nil
				e.stop = nil
				e.mu.Unlock()
				if !processAlive(corePID) {
					e.logf("sing-box process %d exited unexpectedly.", corePID)
				} else {
					e.logf("Xray process exited unexpectedly.")
				}
				// Remove sentinel so the privileged babysitter stops the core too.
				if sp, err := sentinelPath(); err == nil {
					_ = os.Remove(sp)
				}
				e.stopXray(xrayCmd)
				e.appendCoreLogTail()
				e.appendXrayLogTail()
				e.setState(ConnState{Status: StatusDisconnected, Message: "Tunnel stopped — see logs"})
			} else {
				e.mu.Unlock()
			}
			return
		}
	}
}

// appendCoreLogTail copies the last lines of sing-box.log into the log buffer so
// the user can see why the core failed without opening a file.
func (e *Engine) appendCoreLogTail() {
	path, err := coreLogPath()
	if err != nil {
		return
	}
	lines := tailLines(path, 40)
	if len(lines) == 0 {
		return
	}
	e.logf("--- sing-box.log (tail) ---")
	for _, l := range lines {
		e.logf("core: %s", l)
	}
	e.logf("--- end sing-box.log ---")
}

// appendXrayLogTail copies the last lines of xray.log into the log buffer.
func (e *Engine) appendXrayLogTail() {
	path, err := xrayLogPath()
	if err != nil {
		return
	}
	lines := tailLines(path, 30)
	if len(lines) == 0 {
		return
	}
	e.logf("--- xray.log (tail) ---")
	for _, l := range lines {
		e.logf("xray: %s", l)
	}
	e.logf("--- end xray.log ---")
}

// tailCore follows sing-box.log while the tunnel is up, streaming new complete
// lines into the logs window so connection/routing problems are visible live.
func (e *Engine) tailCore(path string, stop chan struct{}) {
	var offset int64
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				f.Close()
				continue
			}
			data, _ := io.ReadAll(f)
			f.Close()

			nl := bytes.LastIndexByte(data, '\n')
			if nl < 0 {
				continue // no complete line yet
			}
			offset += int64(nl) + 1
			for _, line := range strings.Split(string(data[:nl]), "\n") {
				if line = strings.TrimRight(line, "\r"); line != "" {
					e.logf("core: %s", line)
				}
			}
		}
	}
}

func tailLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var all []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimRight(sc.Text(), "\r"); line != "" {
			all = append(all, line)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}
