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
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"freesurf/internal/paths"
	"freesurf/internal/proxy"
	"freesurf/internal/store"

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

// Engine runs the Xray backend and drives the privileged sing-box TUN.
type Engine struct {
	mu      sync.Mutex
	conn    ConnState
	xrayCmd *exec.Cmd     // local Xray process (unprivileged)
	stop    chan struct{} // closed to stop the liveness monitor

	logMu  sync.Mutex
	logBuf []string
}

func New() *Engine {
	return &Engine{conn: ConnState{Status: StatusDisconnected}}
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
	application.Get().Event.Emit("vpn:state", s)
}

var (
	// ANSI colour/style escape sequences emitted by sing-box.
	ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")
	// Leading sing-box timestamp, e.g. "+0300 2026-06-25 11:46:08 ".
	sbTimeRe = regexp.MustCompile(`^[+-]\d{4} \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\s+`)
	// Per-connection id/duration token, e.g. "[4294882405 3ms] ".
	sbConnRe = regexp.MustCompile(`\[\d+ [\d.]+[a-zµ]*s\]\s*`)
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
	application.Get().Event.Emit("log:line", line)
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
	application.Get().Event.Emit("log:cleared")
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
	bin, err := proxy.EnsureCore(ctx)
	if err != nil {
		return e.fail(node.ID, err)
	}
	xrayBin, err := proxy.EnsureXray(ctx)
	if err != nil {
		return e.fail(node.ID, err)
	}

	e.setState(ConnState{Status: StatusConnecting, NodeID: node.ID, Message: "Building config…"})
	e.logf("Generating configs…")
	xrayCfg, serverIP, err := proxy.WriteXrayConfig(node)
	if err != nil {
		return e.fail(node.ID, err)
	}
	cfg, err := proxy.WriteSingboxConfig(serverIP)
	if err != nil {
		return e.fail(node.ID, err)
	}
	if err := proxy.CheckConfig(bin, cfg); err != nil {
		return e.fail(node.ID, err)
	}
	e.logf("Config OK (sing-box check passed).")

	// Install/update the privileged helper if needed - the only step that may
	// prompt for a password, and only the first time (or after a core bump).
	e.setState(ConnState{Status: StatusConnecting, NodeID: node.ID, Message: "Preparing helper…"})
	if !HelperInstalled() {
		e.logf("Installing privileged helper (one-time, asks for password)…")
	}
	if err := EnsureHelper(bin); err != nil {
		return e.fail(node.ID, err)
	}

	// Start Xray (unprivileged) first so its SOCKS port is ready for sing-box.
	xrayLog, err := paths.XrayLog()
	if err != nil {
		return e.fail(node.ID, err)
	}
	e.logf("Starting Xray (proxy backend)…")
	xrayCmd, err := proxy.RunXray(xrayBin, xrayCfg, xrayLog)
	if err != nil {
		return e.fail(node.ID, err)
	}

	logPath, err := paths.CoreLog()
	if err != nil {
		e.stopXray(xrayCmd)
		return e.fail(node.ID, err)
	}
	_ = os.Remove(logPath) // start with a fresh log

	e.setState(ConnState{Status: StatusConnecting, NodeID: node.ID, Message: "Starting tunnel…"})
	e.logf("Starting tunnel…")
	if err := startTunnel(); err != nil {
		e.stopXray(xrayCmd)
		return e.fail(node.ID, err)
	}
	if err := waitTunnelUp(logPath, 12*time.Second); err != nil {
		stopTunnel()
		e.stopXray(xrayCmd)
		return e.fail(node.ID, err)
	}

	e.mu.Lock()
	e.xrayCmd = xrayCmd
	e.stop = make(chan struct{})
	stop := e.stop
	e.mu.Unlock()

	go e.monitor(stop)
	go e.tailCore(logPath, stop)

	e.logf("Tunnel up.")
	state := ConnState{Status: StatusConnected, NodeID: node.ID}
	e.setState(state)
	return state, nil
}

func (e *Engine) fail(nodeID int64, err error) (ConnState, error) {
	e.logf("ERROR: %v", err)
	e.appendLogTail("sing-box.log", paths.CoreLog, "core")
	e.appendLogTail("xray.log", paths.XrayLog, "xray")
	state := ConnState{Status: StatusDisconnected, NodeID: nodeID, Message: err.Error()}
	e.setState(state)
	return state, err
}

func (e *Engine) stopXray(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// Disconnect tears down the tunnel (user-initiated) and notifies the UI.
func (e *Engine) Disconnect() ConnState { return e.teardown(true) }

// Shutdown tears down the tunnel on app exit without emitting events. It must not
// block - stopping is just removing the sentinel file the helper watches.
func (e *Engine) Shutdown() { e.teardown(false) }

func (e *Engine) teardown(emit bool) ConnState {
	e.mu.Lock()
	xrayCmd := e.xrayCmd
	had := xrayCmd != nil
	if e.stop != nil {
		close(e.stop)
		e.stop = nil
	}
	e.xrayCmd = nil
	e.mu.Unlock()

	stopTunnel() // helper stops the root core within ~1s; no prompt needed
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

// monitor watches the Xray process and tears the tunnel down if it dies. The root
// sing-box is supervised by the helper (auto-restarted while connected), so we
// only watch the unprivileged half here.
func (e *Engine) monitor(stop chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			e.mu.Lock()
			xrayCmd := e.xrayCmd
			dead := xrayCmd == nil || xrayCmd.ProcessState != nil
			if dead {
				e.xrayCmd = nil
				e.stop = nil
			}
			e.mu.Unlock()
			if !dead {
				continue
			}
			e.logf("Xray process exited unexpectedly.")
			stopTunnel()
			e.stopXray(xrayCmd)
			e.appendLogTail("sing-box.log", paths.CoreLog, "core")
			e.appendLogTail("xray.log", paths.XrayLog, "xray")
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
