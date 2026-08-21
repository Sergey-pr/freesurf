package engine

import (
	"context"
	"time"

	"freesurf/internal/paths"
	"freesurf/internal/proxy"
	"freesurf/internal/store"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// process is the running unprivileged backend. An interface, not *proxy.Process,
// so tests can drive connect/disconnect without spawning a real Xray.
type process interface {
	Kill()
	Exited() bool
}

// deps is everything the lifecycle reaches for outside the engine: core install,
// config generation, the privileged helper, the backend process and UI events.
// New wires the real implementations; tests replace individual fields.
type deps struct {
	ensureCore         func(context.Context) (string, error)
	ensureXray         func(context.Context) (string, error)
	writeXrayConfig    func(*store.Node) (cfgPath, serverIP string, err error)
	writeSingboxConfig func(serverIP string) (string, error)
	checkConfig        func(binPath, cfgPath string) error
	helperInstalled    func() bool
	ensureHelper       func(singboxBin string) error
	coreLog            func() (string, error)
	xrayLog            func() (string, error)
	runXray            func(binPath, cfgPath, logPath string) (process, error)
	startTunnel        func() error
	waitTunnelUp       func(logPath string, timeout time.Duration) error
	stopTunnel         func()
	emit               func(name string, data ...any)
}

func defaultDeps() deps {
	return deps{
		ensureCore:         proxy.EnsureCore,
		ensureXray:         proxy.EnsureXray,
		writeXrayConfig:    proxy.WriteXrayConfig,
		writeSingboxConfig: proxy.WriteSingboxConfig,
		checkConfig:        proxy.CheckConfig,
		helperInstalled:    HelperInstalled,
		ensureHelper:       EnsureHelper,
		coreLog:            paths.CoreLog,
		xrayLog:            paths.XrayLog,
		runXray: func(binPath, cfgPath, logPath string) (process, error) {
			// Returned explicitly: handing back a typed nil *proxy.Process would
			// give the caller a non-nil interface on error.
			p, err := proxy.RunXray(binPath, cfgPath, logPath)
			if err != nil {
				return nil, err
			}
			return p, nil
		},
		startTunnel:  startTunnel,
		waitTunnelUp: waitTunnelUp,
		stopTunnel:   stopTunnel,
		emit:         func(name string, data ...any) { application.Get().Event.Emit(name, data...) },
	}
}
