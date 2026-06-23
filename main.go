package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
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
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "FreeSurf",
		Width:            400,
		Height:           600,
		MinWidth:         320,
		MinHeight:        480,
		BackgroundColour: application.NewRGB(0x12, 0x12, 0x14),
		URL:              "/",
	})

	// Error window
	errorWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:         "Error",
		Width:         420,
		Height:        160,
		AlwaysOnTop:   true,
		DisableResize: true,
		Hidden:        true,
		URL:           "/error.html",
	})
	appService.SetErrorWindow(errorWindow)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
