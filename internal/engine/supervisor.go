package engine

import (
	"log"
	"os"
	"time"

	"freesurf/internal/paths"
	"freesurf/internal/proxy"
)

// rootFiles are the root-owned paths the supervisor reads, writes and executes.
type rootFiles struct {
	exe     string // root-owned copy of this binary, run by launchd / the SCM
	singbox string
	config  string
	log     string
	status  string
}

// supervisorTick is how often the request file is checked.
var supervisorTick = time.Second

// coreStopGrace is how long sing-box gets to unwind its routing before it is killed.
const coreStopGrace = 5 * time.Second

// Supervisor mode, and the one path the supervisor cannot derive for itself.
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

// superviseTunnel runs sing-box while the request file exists, reporting through the status.
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
			// A new run without a stop in between.
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

// startCore generates req's config and launches sing-box on it.
func startCore(files rootFiles, req tunnelRequest, lg *log.Logger, core **proxy.Process, running *tunnelRequest) {
	_ = writeStatus(files.status, tunnelStatus{Nonce: req.Nonce, State: tunnelStarting})

	cfg, err := proxy.SingboxConfig(req.ServerIP)
	if err == nil {
		// Only root reads this config; the chmod also narrows one already on disk.
		err = os.WriteFile(files.config, cfg, 0600)
		if err == nil {
			err = paths.RestrictFile(files.config)
		}
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

// supervisorLogger writes the supervisor's own diagnostics.
func supervisorLogger(path string) (*log.Logger, func()) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return log.New(os.Stderr, "", log.LstdFlags), func() {}
	}
	return log.New(f, "", log.LstdFlags), func() { _ = f.Close() }
}
