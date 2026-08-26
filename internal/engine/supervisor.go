package engine

import (
	"log"
	"os"
	"time"

	"freesurf/internal/proxy"
)

// rootFiles are the root-owned paths the supervisor works with. Everything root
// reads or executes lives here, out of reach of the unprivileged side; the request
// file in the user's data directory is the sole exception, and it is validated.
type rootFiles struct {
	exe     string // root-owned copy of this binary, run by launchd / the SCM
	singbox string
	config  string
	log     string
	status  string
}

// supervisorTick is how often the request file is checked; a variable so tests
// don't have to wait out real seconds.
var supervisorTick = time.Second

// coreStopGrace is how long sing-box gets to unwind its routing before it is
// killed. Cutting this short strands the machine's default route in a dead tun.
const coreStopGrace = 5 * time.Second

// flagRunService puts this binary into privileged supervisor mode; flagRequest
// names the user's request file, the one path the supervisor cannot derive itself.
// launchd (macOS) and the SCM (Windows) both launch the root-owned copy this way.
const (
	flagRunService = "--freesurf-tun-service"
	flagRequest    = "--request"
)

// parseRequestPath pulls the request path out of the supervisor's command line.
func parseRequestPath(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flagRequest {
			return args[i+1]
		}
	}
	return ""
}

// superviseTunnel keeps sing-box running while the request file exists and stops it
// when the file goes away, reporting what happened through the status file. It
// regenerates the config from the request on every start, so the document root
// hands to sing-box is one this process built, never one it was given.
func superviseTunnel(files rootFiles, requestPath string, stop <-chan struct{}, lg *log.Logger) {
	var (
		core    *proxy.Process
		running tunnelRequest
	)
	stopCore := func() {
		if core == nil {
			return
		}
		core.Stop(coreStopGrace)
		core = nil
		running = tunnelRequest{}
	}
	defer stopCore()

	ticker := time.NewTicker(supervisorTick)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			lg.Printf("supervisor stopping")
			if core != nil {
				stopCore()
				_ = writeStatus(files.status, tunnelStatus{Nonce: running.Nonce, State: tunnelStopped})
			}
			return
		case <-ticker.C:
		}

		req, want := readRequest(requestPath)
		switch {
		case !want:
			if core != nil {
				lg.Printf("request withdrawn, stopping sing-box")
				nonce := running.Nonce
				stopCore()
				_ = writeStatus(files.status, tunnelStatus{Nonce: nonce, State: tunnelStopped})
			}
		case core != nil && req.Nonce != running.Nonce:
			// A new run was requested without a stop in between; restart on it.
			lg.Printf("new request %s, restarting sing-box", req.Nonce)
			stopCore()
			startCore(files, req, lg, &core, &running)
		case core != nil && core.Exited():
			lg.Printf("sing-box exited, restarting")
			core = nil
			startCore(files, req, lg, &core, &running)
		case core == nil:
			startCore(files, req, lg, &core, &running)
		}
	}
}

// startCore generates the config for req and launches sing-box on it, recording the
// outcome in the status file.
func startCore(files rootFiles, req tunnelRequest, lg *log.Logger, core **proxy.Process, running *tunnelRequest) {
	_ = writeStatus(files.status, tunnelStatus{Nonce: req.Nonce, State: tunnelStarting})

	cfg, err := proxy.SingboxConfig(req.ServerIP)
	if err == nil {
		err = os.WriteFile(files.config, cfg, 0644)
	}
	var p *proxy.Process
	if err == nil {
		p, err = proxy.RunSingbox(files.singbox, files.config, files.log)
	}
	if err != nil {
		lg.Printf("failed to start sing-box: %v", err)
		_ = writeStatus(files.status, tunnelStatus{Nonce: req.Nonce, State: tunnelFailed, Message: err.Error()})
		return
	}

	*core = p
	*running = req
	lg.Printf("sing-box started for %s", req.Nonce)
	_ = writeStatus(files.status, tunnelStatus{Nonce: req.Nonce, State: tunnelRunning})
}

// supervisorLogger writes the supervisor's own diagnostics next to the core log.
func supervisorLogger(path string) (*log.Logger, func()) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return log.New(os.Stderr, "", log.LstdFlags), func() {}
	}
	return log.New(f, "", log.LstdFlags), func() { _ = f.Close() }
}
