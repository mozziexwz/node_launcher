// Command MSBOOST-Launcher is the MSBOOST desktop launcher (Wails v2).
//
// It is portable: configs\, logs\, data\ and settings.json live beside the
// executable. Release builds embed ProxyBridgeCore.dll, WinDivert.dll and
// WinDivert64.sys and release them to %ProgramData%\MSBOOST (runtime.go);
// development builds load them from beside the executable.
package main

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	xwindows "golang.org/x/sys/windows"

	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/store"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/tray/idle.ico
var trayIdleIcon []byte

//go:embed build/tray/running.ico
var trayRunningIcon []byte

func main() {
	exe, err := os.Executable()
	if err != nil {
		fatal("无法确定程序位置：" + err.Error())
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	layout := store.NewLayout(filepath.Dir(exe))
	if err := layout.Ensure(); err != nil {
		fatal(err.Error() + "\n请把 MSBOOST 启动器放到可写入的文件夹后再运行。")
	}
	log, err := logx.New(layout.Logs, 7)
	if err != nil {
		fatal("无法创建日志：" + err.Error())
	}

	rt, err := loadRuntime(layout, log)
	if err != nil {
		fatal(err.Error())
	}
	a := NewApp(layout, log, rt, exe)
	err = wails.Run(&options.App{
		Title:            "MSBOOST 启动器",
		Width:            1040,
		Height:           700,
		MinWidth:         900,
		MinHeight:        620,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 246, G: 247, B: 249, A: 1},
		OnStartup:        a.startup,
		OnBeforeClose:    a.beforeClose,
		OnShutdown:       a.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "msboost-launcher-6f1d2c9e",
			OnSecondInstanceLaunch: a.onSecondInstance,
		},
		Windows: &windows.Options{
			WebviewUserDataPath: filepath.Join(layout.Data, "webview"),
		},
		Bind: []interface{}{a},
	})
	if err != nil {
		fatal("界面启动失败：" + err.Error())
	}
}

// fatal reports a startup failure before any window exists.
func fatal(message string) {
	title, _ := xwindows.UTF16PtrFromString("MSBOOST 启动器")
	text, _ := xwindows.UTF16PtrFromString(message)
	xwindows.MessageBox(0, text, title, xwindows.MB_OK|xwindows.MB_ICONERROR)
	os.Exit(1)
}
