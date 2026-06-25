package main

import (
	"embed"
	"log"
	"runtime"

	"freesurf/internal/engine"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// On Windows the same binary doubles as the privileged TUN service and its
	// elevated install/uninstall worker. When launched in one of those modes,
	// handle it and exit before touching Wails. No-op elsewhere.
	if engine.MaybeRunService() {
		return
	}

	appService := NewApp()

	app := application.New(application.Options{
		Name:        "FreeSurf",
		Description: "A minimalistic multi-platform VPN client",
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Services: []application.Service{
			application.NewService(appService),
		},
	})

	// Main window
	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "FreeSurf",
		Width:            400,
		Height:           600,
		MinWidth:         320,
		MinHeight:        480,
		BackgroundColour: application.NewRGB(0x12, 0x12, 0x14),
		URL:              "/",
	})
	// On macOS ApplicationShouldTerminateAfterLastWindowClosed quits the app; on
	// other platforms the hidden error/logs windows keep it alive, so closing the
	// main window would leave the app (and the tunnel) running. Quit explicitly so
	// ServiceShutdown tears the tunnel down.
	if runtime.GOOS != "darwin" {
		mainWindow.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			application.Get().Quit()
		})
	}

	// Error window
	errorWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:         "Error",
		Width:         420,
		Height:        260,
		AlwaysOnTop:   true,
		DisableResize: true,
		Hidden:        true,
		URL:           "/error.html",
	})
	// Hide instead of destroy so the window can be reopened.
	errorWindow.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		errorWindow.Hide()
	})
	appService.SetErrorWindow(errorWindow)

	// Logs window
	logsWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "FreeSurf - Logs",
		Width:            560,
		Height:           420,
		MinWidth:         360,
		MinHeight:        240,
		Hidden:           true,
		BackgroundColour: application.NewRGB(0x12, 0x12, 0x14),
		URL:              "/logs.html",
	})
	// Hide instead of destroy so the window can be reopened.
	logsWindow.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		logsWindow.Hide()
	})
	appService.SetLogsWindow(logsWindow)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
