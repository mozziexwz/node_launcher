package main

import (
	"runtime"

	"fyne.io/systray"

	"github.com/mozziexwz/node_launcher/internal/app"
)

type trayActions struct {
	show, toggle, quit func()
}

// trayIcons are .ico files; the running one marks active acceleration.
type trayIcons struct {
	idle, running []byte
}

type tray struct {
	ready  chan struct{}
	icons  trayIcons
	toggle *systray.MenuItem
}

// startTray runs the tray icon on its own locked OS thread. On Windows the
// tray's hidden window belongs to the thread that creates it, and only that
// thread's message loop receives its clicks, so it must not share Wails's
// main-thread loop or float between threads.
func startTray(icons trayIcons, actions trayActions) *tray {
	t := &tray{ready: make(chan struct{}), icons: icons}
	go func() {
		runtime.LockOSThread()
		systray.Run(func() { t.build(actions) }, nil)
	}()
	return t
}

func (t *tray) build(actions trayActions) {
	systray.SetIcon(t.icons.idle)
	systray.SetTooltip("MSBOOST 启动器")
	systray.SetOnTapped(actions.show) // left click opens the window
	show := systray.AddMenuItem("显示主窗口", "")
	t.toggle = systray.AddMenuItem("开始加速", "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("退出", "")
	close(t.ready)

	go func() {
		for {
			select {
			case <-show.ClickedCh:
				actions.show()
			case <-t.toggle.ClickedCh:
				go actions.toggle() // may show a dialog; keep the menu loop free
			case <-quit.ClickedCh:
				go actions.quit()
			}
		}
	}()
}

// setState updates the menu and tooltip to match the acceleration state.
func (t *tray) setState(state app.State) {
	select {
	case <-t.ready:
	default:
		return // tray failed to start or is not ready; never block the caller
	}
	switch state {
	case app.StateRunning:
		t.toggle.SetTitle("停止加速")
		t.toggle.Enable()
		systray.SetIcon(t.icons.running)
		systray.SetTooltip("MSBOOST 启动器 - 加速中")
	case app.StateStarting:
		t.toggle.SetTitle("正在开始加速…")
		t.toggle.Disable()
	case app.StateStopping:
		t.toggle.SetTitle("正在停止…")
		t.toggle.Disable()
	default:
		t.toggle.SetTitle("开始加速")
		t.toggle.Enable()
		systray.SetIcon(t.icons.idle)
		systray.SetTooltip("MSBOOST 启动器")
	}
}

// stop removes the tray icon so none is left behind after exit.
func (t *tray) stop() {
	systray.Quit()
}
