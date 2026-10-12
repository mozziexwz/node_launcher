package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mozziexwz/node_launcher/internal/engine"
	"github.com/mozziexwz/node_launcher/internal/probe"
	"github.com/mozziexwz/node_launcher/internal/redirector"
)

// App holds the state exposed to the frontend.
type App struct {
	ctx      context.Context
	tray     *tray
	quitting atomic.Bool

	mu           sync.Mutex
	accelerating bool // simulated in M0
}

// Status is what the main page shows.
type Status struct {
	Elevated     bool `json:"elevated"`
	Accelerating bool `json:"accelerating"`
	Simulated    bool `json:"simulated"`
}

// ConfigInfo summarizes a node configuration without exposing credentials.
type ConfigInfo struct {
	Path     string `json:"path"`
	Profile  string `json:"profile"`
	Entrance string `json:"entrance"`
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.tray = startTray(trayIcon, trayActions{
		show:   a.ShowWindow,
		toggle: func() { a.ToggleAcceleration() },
		quit:   a.Quit,
	})
}

// beforeClose hides the window to the tray instead of exiting; only the
// tray's 退出 item (or Quit) really closes the app.
func (a *App) beforeClose(ctx context.Context) bool {
	if a.quitting.Load() {
		return false
	}
	runtime.WindowHide(ctx)
	return true
}

func (a *App) shutdown(context.Context) {
	a.tray.stop()
}

func (a *App) onSecondInstance(options.SecondInstanceData) {
	a.ShowWindow()
}

// Status reports elevation and the (simulated) acceleration state.
func (a *App) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Status{Elevated: redirector.IsElevated(), Accelerating: a.accelerating, Simulated: true}
}

// ToggleAcceleration flips the simulated state and syncs window and tray.
func (a *App) ToggleAcceleration() Status {
	a.mu.Lock()
	a.accelerating = !a.accelerating
	on := a.accelerating
	a.mu.Unlock()
	a.tray.setAccelerating(on)
	st := a.Status()
	runtime.EventsEmit(a.ctx, "status", st)
	return st
}

// PickConfig opens a file dialog and validates the chosen mieru client JSON
// with the real engine parser.
func (a *App) PickConfig() (*ConfigInfo, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "选择 MSBOOST 节点配置",
		Filters: []runtime.FileFilter{{DisplayName: "MSBOOST 节点配置 (*.json)", Pattern: "*.json"}},
	})
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("未选择文件")
	}
	cfg, err := engine.LoadConfigFile(path)
	if err != nil {
		return nil, err
	}
	info := &ConfigInfo{Path: path, Profile: cfg.Profile.GetProfileName()}
	if t := probe.RelayTargetFromProfile(cfg.Profile); t.Err == "" {
		info.Entrance = t.Address
	} else {
		info.Entrance = t.Err
	}
	return info, nil
}

// HideToTray hides the main window.
func (a *App) HideToTray() {
	runtime.WindowHide(a.ctx)
}

// ShowWindow brings the main window back from the tray.
func (a *App) ShowWindow() {
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

// Quit exits the app for real.
func (a *App) Quit() {
	a.quitting.Store(true)
	runtime.Quit(a.ctx)
}
