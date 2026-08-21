// Package engine owns the VPN lifecycle: it generates configs, runs the
// unprivileged Xray backend, and drives the privileged sing-box TUN via the
// platform helper.
package engine

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"freesurf/internal/proxy"
	"freesurf/internal/store"
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

// Engine runs the Xray backend and drives the privileged sing-box TUN.
type Engine struct {
	deps deps
	// Poll intervals for the two background watchers, tightened in tests.
	monitorTick time.Duration
	tailTick    time.Duration

	mu   sync.Mutex
	conn ConnState
	xray process       // local Xray process (unprivileged)
	stop chan struct{} // closed to stop the monitor and log tail

	logMu  sync.Mutex
	logBuf []string
}

func New() *Engine {
	return &Engine{
		conn:        ConnState{Status: StatusDisconnected},
		deps:        defaultDeps(),
		monitorTick: 2 * time.Second,
		tailTick:    1 * time.Second,
	}
}

func (e *Engine) State() ConnState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conn
}

func (e *Engine) setState(s ConnState) {
	e.mu.Lock()
	e.conn = s
	e.mu.Unlock()
	e.deps.emit("vpn:state", s)
}

var (
	// ANSI colour/style escape sequences emitted by sing-box.
	ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")
	// Leading sing-box timestamp, e.g. "+0300 2026-06-25 11:46:08 ".
	sbTimeRe = regexp.MustCompile(`^[+-]\d{4} \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\s+`)
	// Per-connection id/duration token, e.g. "[4294882405 3ms] ".
	sbConnRe = regexp.MustCompile(`\[\d+ [\d.]+[a-zµ]*s]\s*`)
)

// sanitize removes ANSI escape sequences and other non-printable characters.
func sanitize(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r != '\t' && r < 0x20 {
			return -1
		}
		return r
	}, s)
}

// cleanCoreLine sanitizes a raw sing-box/xray log line and strips the redundant
// inner timestamp and per-connection id token so identical events collapse.
func cleanCoreLine(s string) string {
	s = sanitize(s)
	s = sbTimeRe.ReplaceAllString(s, "")
	s = sbConnRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// logf appends a timestamped line to the in-memory log buffer and emits it so any
// open logs window updates live.
func (e *Engine) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05") + "  " + sanitize(fmt.Sprintf(format, args...))
	e.logMu.Lock()
	e.logBuf = append(e.logBuf, line)
	if len(e.logBuf) > logBufferMax {
		e.logBuf = e.logBuf[len(e.logBuf)-logBufferMax:]
	}
	e.logMu.Unlock()
	e.deps.emit("log:line", line)
}

// Logf writes a line into the shared log buffer (and live logs window) from outside
// the engine, e.g. ping diagnostics, using the same format as core logging.
func (e *Engine) Logf(format string, args ...any) { e.logf(format, args...) }

// LogText returns the full log buffer as a single string.
func (e *Engine) LogText() string {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	return strings.Join(e.logBuf, "\n")
}

func (e *Engine) ClearLog() {
	e.logMu.Lock()
	e.logBuf = nil
	e.logMu.Unlock()
	e.deps.emit("log:cleared")
}

// ReinstallCores force-reinstalls the embedded core binaries (sing-box, Xray,
// and the Wintun driver on Windows). Refused while the tunnel is up, since the
// binaries may be running.
func (e *Engine) ReinstallCores() error {
	if st := e.State(); st.Status != StatusDisconnected {
		return fmt.Errorf("disconnect the VPN before reinstalling dependencies")
	}
	e.logf("Reinstalling embedded cores (sing-box %s, xray %s)…", proxy.RequiredCoreVersion, proxy.RequiredXrayVersion)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := proxy.ReinstallCores(ctx); err != nil {
		e.logf("Reinstall failed: %v", err)
		return err
	}
	e.logf("Cores reinstalled.")
	return nil
}

// Connect brings up the tunnel to the given node, reporting progress through the
// "vpn:state" event. The returned error (if any) is for the caller to surface;
// the state is already emitted.
func (e *Engine) Connect(node *store.Node) (ConnState, error) {
	e.logf("Connecting to %q…", node.Name)
	e.setState(ConnState{Status: StatusConnecting, NodeID: node.ID, Message: "Preparing core…"})

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	e.logf("Ensuring cores (sing-box %s, xray %s)…", proxy.RequiredCoreVersion, proxy.RequiredXrayVersion)
	bin, err := e.deps.ensureCore(ctx)
	if err != nil {
		return e.fail(node.ID, err)
	}
	xrayBin, err := e.deps.ensureXray(ctx)
	if err != nil {
		return e.fail(node.ID, err)
	}

	e.setState(ConnState{Status: StatusConnecting, NodeID: node.ID, Message: "Building config…"})
	e.logf("Generating configs…")
	xrayCfg, serverIP, err := e.deps.writeXrayConfig(node)
	if err != nil {
		return e.fail(node.ID, err)
	}
	cfg, err := e.deps.writeSingboxConfig(serverIP)
	if err != nil {
		return e.fail(node.ID, err)
	}
	if err := e.deps.checkConfig(bin, cfg); err != nil {
		return e.fail(node.ID, err)
	}
	e.logf("Config OK (sing-box check passed).")

	// Install/update the privileged helper if needed - the only step that may
	// prompt for a password, and only the first time (or after a core bump).
	e.setState(ConnState{Status: StatusConnecting, NodeID: node.ID, Message: "Preparing helper…"})
	if !e.deps.helperInstalled() {
		e.logf("Installing privileged helper (one-time, asks for password)…")
	}
	if err := e.deps.ensureHelper(bin); err != nil {
		return e.fail(node.ID, err)
	}

	// Start Xray (unprivileged) first so its SOCKS port is ready for sing-box.
	xrayLog, err := e.deps.xrayLog()
	if err != nil {
		return e.fail(node.ID, err)
	}
	e.logf("Starting Xray (proxy backend)…")
	xray, err := e.deps.runXray(xrayBin, xrayCfg, xrayLog)
	if err != nil {
		return e.fail(node.ID, err)
	}

	logPath, err := e.deps.coreLog()
	if err != nil {
		stopProcess(xray)
		return e.fail(node.ID, err)
	}
	_ = os.Remove(logPath) // start with a fresh log

	e.setState(ConnState{Status: StatusConnecting, NodeID: node.ID, Message: "Starting tunnel…"})
	e.logf("Starting tunnel…")
	if err := e.deps.startTunnel(); err != nil {
		stopProcess(xray)
		return e.fail(node.ID, err)
	}
	if err := e.deps.waitTunnelUp(logPath, 12*time.Second); err != nil {
		e.deps.stopTunnel()
		stopProcess(xray)
		return e.fail(node.ID, err)
	}

	stop := e.setRunning(xray)

	go e.monitor(stop)
	go e.tailCore(logPath, stop)

	e.logf("Tunnel up.")
	state := ConnState{Status: StatusConnected, NodeID: node.ID}
	e.setState(state)
	return state, nil
}

func (e *Engine) fail(nodeID int64, err error) (ConnState, error) {
	e.logf("ERROR: %v", err)
	e.appendLogTail("sing-box.log", e.deps.coreLog, "core")
	e.appendLogTail("xray.log", e.deps.xrayLog, "xray")
	state := ConnState{Status: StatusDisconnected, NodeID: nodeID, Message: err.Error()}
	e.setState(state)
	return state, err
}

func stopProcess(p process) {
	if p != nil {
		p.Kill()
	}
}

// setRunning installs a new tunnel generation and returns its stop channel. Any
// previous generation is shut down first so its monitor and log tail exit.
func (e *Engine) setRunning(p process) chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stop != nil {
		close(e.stop)
	}
	e.xray = p
	e.stop = make(chan struct{})
	return e.stop
}

// takeRunning clears the running generation and returns the backend process that
// was running, if any.
func (e *Engine) takeRunning() process {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clearRunningLocked()
}

// clearRunningLocked drops the backend process and closes the stop channel, which
// is what shuts the monitor and log tail down. Caller holds e.mu.
func (e *Engine) clearRunningLocked() process {
	p := e.xray
	e.xray = nil
	if e.stop != nil {
		close(e.stop)
		e.stop = nil
	}
	return p
}

// Disconnect tears down the tunnel (user-initiated) and notifies the UI.
func (e *Engine) Disconnect() ConnState { return e.teardown(true) }

// Shutdown tears down the tunnel on app exit without emitting events. It must not
// block - stopping is just removing the sentinel file the helper watches.
func (e *Engine) Shutdown() { e.teardown(false) }

func (e *Engine) teardown(emit bool) ConnState {
	xray := e.takeRunning()
	had := xray != nil

	e.deps.stopTunnel() // helper stops the root core within ~1s; no prompt needed
	stopProcess(xray)
	if had && emit {
		e.logf("Stopping tunnel…")
	}

	state := ConnState{Status: StatusDisconnected}
	e.mu.Lock()
	e.conn = state
	e.mu.Unlock()
	if emit {
		e.deps.emit("vpn:state", state)
	}
	return state
}

// monitor watches the Xray process and tears the tunnel down if it dies. The root
// sing-box is supervised by the helper (auto-restarted while connected), so we
// only watch the unprivileged half here.
func (e *Engine) monitor(stop chan struct{}) {
	ticker := time.NewTicker(e.monitorTick)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Clearing under the same lock that observed the death keeps a
			// concurrent Connect from having its fresh tunnel torn down here.
			e.mu.Lock()
			xray := e.xray
			dead := xray == nil || xray.Exited()
			if dead {
				e.clearRunningLocked()
			}
			e.mu.Unlock()
			if !dead {
				continue
			}
			e.logf("Xray process exited unexpectedly.")
			e.deps.stopTunnel()
			stopProcess(xray)
			e.appendLogTail("sing-box.log", e.deps.coreLog, "core")
			e.appendLogTail("xray.log", e.deps.xrayLog, "xray")
			e.setState(ConnState{Status: StatusDisconnected, Message: "Tunnel stopped - see logs"})
			return
		}
	}
}

// appendLogTail dumps the last lines of a core log into the log buffer.
func (e *Engine) appendLogTail(name string, pathFn func() (string, error), prefix string) {
	path, err := pathFn()
	if err != nil {
		return
	}
	lines := tailLines(path, 40)
	if len(lines) == 0 {
		return
	}
	e.logf("--- %s (tail) ---", name)
	for _, l := range lines {
		if cleaned := cleanCoreLine(l); cleaned != "" {
			e.logf("%s: %s", prefix, cleaned)
		}
	}
	e.logf("--- end %s ---", name)
}

// tailCore follows sing-box.log while the tunnel is up, streaming new complete
// lines into the logs window so connection/routing problems are visible live.
func (e *Engine) tailCore(path string, stop chan struct{}) {
	var offset int64
	ticker := time.NewTicker(e.tailTick)
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
				_ = f.Close()
				continue
			}
			data, _ := io.ReadAll(f)
			_ = f.Close()

			nl := bytes.LastIndexByte(data, '\n')
			if nl < 0 {
				continue
			}
			offset += int64(nl) + 1
			for _, line := range strings.Split(string(data[:nl]), "\n") {
				if cleaned := cleanCoreLine(line); cleaned != "" {
					e.logf("core: %s", cleaned)
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
	defer func() {
		_ = f.Close()
	}()

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
