package main

import (
	"runtime"

	"fyne.io/systray"
)

type trayActions struct {
	show, toggle, quit func()
}

type tray struct {
	ready  chan struct{}
	toggle *systray.MenuItem
}

// startTray runs the tray icon on its own locked OS thread. On Windows the
// tray's hidden window belongs to the thread that creates it, and only that
// thread's message loop receives its clicks, so it must not share Wails's
// main-thread loop or float between threads.
func startTray(icon []byte, actions trayActions) *tray {
	t := &tray{ready: make(chan struct{})}
	go func() {
		runtime.LockOSThread()
		systray.Run(func() { t.build(icon, actions) }, nil)
	}()
	return t
}

func (t *tray) build(icon []byte, actions trayActions) {
	systray.SetIcon(icon)
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
				actions.toggle()
			case <-quit.ClickedCh:
				actions.quit()
				return
			}
		}
	}()
}

func (t *tray) setAccelerating(on bool) {
	select {
	case <-t.ready:
	default:
		return // tray failed to start or is not ready; never block the caller
	}
	if on {
		t.toggle.SetTitle("停止加速")
		systray.SetTooltip("MSBOOST 启动器 - 加速中")
	} else {
		t.toggle.SetTitle("开始加速")
		systray.SetTooltip("MSBOOST 启动器")
	}
}

// stop removes the tray icon so none is left behind after exit.
func (t *tray) stop() {
	systray.Quit()
}
