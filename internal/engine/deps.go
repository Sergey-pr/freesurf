package engine

import (
	"context"
	"time"

	"freesurf/internal/paths"
	"freesurf/internal/proxy"
	"freesurf/internal/store"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// process is the running unprivileged backend, an interface so tests can fake it.
type process interface {
	Stop(grace time.Duration)
	Exited() bool
}

// deps is everything the lifecycle reaches for outside the engine; tests swap fields.
type deps struct {
	ensureCore      func(context.Context) (string, error)
	ensureXray      func(context.Context) (string, error)
	writeXrayConfig func(*store.Node) (cfgPath, serverIP string, err error)
	singboxConfig   func(serverIP string) ([]byte, error)
	checkConfig     func(binPath string, cfg []byte) error
	helperInstalled func() bool
	ensureHelper    func(singboxBin string) error
	coreLog         func() (string, error)
	xrayLog         func() (string, error)
	runXray         func(binPath, cfgPath, logPath string) (process, error)
	startTunnel     func(serverIP string) (nonce string, err error)
	waitTunnelUp    func(nonce string, timeout time.Duration) error
	stopTunnel      func()
	emit            func(name string, data ...any)
}

func defaultDeps() deps {
	return deps{
		ensureCore:      proxy.EnsureCore,
		ensureXray:      proxy.EnsureXray,
		writeXrayConfig: proxy.WriteXrayConfig,
		singboxConfig:   proxy.SingboxConfig,
		checkConfig:     proxy.CheckConfig,
		helperInstalled: HelperInstalled,
		ensureHelper:    EnsureHelper,
		coreLog:         coreLogPath,
		xrayLog:         paths.XrayLog,
		runXray: func(binPath, cfgPath, logPath string) (process, error) {
			// Explicit: a typed nil would reach the caller as a non-nil interface.
			p, err := proxy.RunXray(binPath, cfgPath, logPath)
			if err != nil {
				return nil, err
			}
			return p, nil
		},
		startTunnel: startTunnel,
		waitTunnelUp: func(nonce string, timeout time.Duration) error {
			return waitTunnelUp(statusPath(), nonce, timeout)
		},
		stopTunnel: stopTunnel,
		emit:       func(name string, data ...any) { application.Get().Event.Emit(name, data...) },
	}
}
