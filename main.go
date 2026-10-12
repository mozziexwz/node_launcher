// Command MSBOOST-Launcher is the MSBOOST desktop launcher (Wails v2).
//
// M0 status: this is the UI shell used to validate an elevated Wails window,
// the file dialog and the tray icon. Acceleration in the UI is simulated
// until M2 wires in internal/engine and internal/redirector.
package main

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:            "MSBOOST 启动器",
		Width:            960,
		Height:           640,
		MinWidth:         800,
		MinHeight:        560,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 246, G: 247, B: 249, A: 1},
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "msboost-launcher-6f1d2c9e",
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
		Windows: &windows.Options{
			WebviewUserDataPath: filepath.Join(localAppData(), "MSBOOST", "webview"),
		},
		Bind: []interface{}{app},
	})
	if err != nil {
		println("Error:", err.Error())
		os.Exit(1)
	}
}

func localAppData() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return dir
	}
	dir, _ := os.UserCacheDir()
	return dir
}
