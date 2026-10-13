package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mozziexwz/node_launcher/internal/app"
	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/redirector"
	"github.com/mozziexwz/node_launcher/internal/store"
	"github.com/mozziexwz/node_launcher/internal/winsys"
)

// coreInstance is shared with msboostctl so only one program drives the core.
const coreInstance = "MSBOOST-Launcher-Core"

// App is bound to the frontend; its exported methods are the UI's API.
type App struct {
	ctx      context.Context
	layout   store.Layout
	log      *logx.Logger
	ctl      *app.Controller
	exePath  string
	tray     *tray
	quitting atomic.Bool
	stopBg   context.CancelFunc

	releaseLock func()
	lockErr     error
	settingsErr string
}

func NewApp(layout store.Layout, log *logx.Logger, corePath, exePath string) *App {
	a := &App{layout: layout, log: log, exePath: exePath, ctl: app.NewWindows(layout, log, corePath)}
	if _, err := layout.LoadSettings(); err != nil {
		a.settingsErr = err.Error()
	}
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.releaseLock, a.lockErr = winsys.AcquireInstance(coreInstance)
	if a.lockErr != nil {
		a.log.Printf(logx.App, "核心正被另一个 MSBOOST 程序使用（例如 msboostctl），本窗口暂不能开始加速")
	}
	a.tray = startTray(trayIcons{idle: trayIdleIcon, running: trayRunningIcon},
		trayActions{show: a.ShowWindow, toggle: a.toggleFromTray, quit: a.quitFromTray})
	a.ctl.Watch(func(st app.Status) {
		runtime.EventsEmit(ctx, "status", toStatusDTO(st))
		a.tray.setState(st.State)
	})

	bg, cancel := context.WithCancel(ctx)
	a.stopBg = cancel
	go a.pushStatus(bg)
	go a.pushLogs(bg)
	a.log.Printf(logx.App, "MSBOOST 启动器已启动，版本 %s，数据目录 %s", versionString(), a.layout.Root)
}

// pushStatus refreshes connection counts while accelerating.
func (a *App) pushStatus(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if st := a.ctl.Status(); st.State == app.StateRunning {
				runtime.EventsEmit(a.ctx, "status", toStatusDTO(st))
			}
		}
	}
}

// pushLogs forwards new log lines in small batches.
func (a *App) pushLogs(ctx context.Context) {
	lines, cancel := a.log.Subscribe()
	defer cancel()
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	var batch []LogLineDTO
	for {
		select {
		case <-ctx.Done():
			return
		case l, ok := <-lines:
			if !ok {
				return
			}
			batch = append(batch, toLogLineDTO(l))
		case <-t.C:
			if len(batch) > 0 {
				runtime.EventsEmit(a.ctx, "logs", batch)
				batch = nil
			}
		}
	}
}

// beforeClose hides the window to the tray; only an explicit quit exits.
func (a *App) beforeClose(ctx context.Context) bool {
	if a.quitting.Load() {
		return false
	}
	runtime.WindowHide(ctx)
	return true
}

func (a *App) shutdown(context.Context) {
	a.ctl.Shutdown()
	if a.stopBg != nil {
		a.stopBg()
	}
	a.tray.stop()
	if a.releaseLock != nil {
		a.releaseLock()
	}
	a.log.Printf(logx.App, "MSBOOST 启动器已退出")
	a.log.Close()
}

func (a *App) onSecondInstance(options.SecondInstanceData) {
	a.ShowWindow()
}

// ---- tray ----

func (a *App) toggleFromTray() {
	switch a.ctl.Status().State {
	case app.StateRunning:
		if running := a.ctl.RunningGameProcesses(); len(running) > 0 && !a.confirm("停止加速",
			"冒险岛正在运行，停止加速会断开游戏连接。确定停止吗？") {
			return
		}
		if err := a.Stop(); err != nil {
			a.notify("error", "停止加速失败："+err.Error())
		}
	case app.StateIdle:
		warnings, err := a.Start()
		if err != nil {
			a.notify("error", "开始加速失败："+err.Error())
		}
		for _, w := range warnings {
			a.notify("warn", w)
		}
	}
}

func (a *App) quitFromTray() {
	if a.ctl.Status().State != app.StateIdle && !a.confirm("退出", "正在加速，退出会停止加速并断开游戏连接。确定退出吗？") {
		return
	}
	a.Quit()
}

// confirm asks a yes/no question with a native dialog.
func (a *App) confirm(title, message string) bool {
	answer, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type: runtime.QuestionDialog, Title: title, Message: message,
	})
	return err == nil && (answer == "Yes" || answer == "是")
}

// NoticeDTO is a message for the main window from a tray action.
type NoticeDTO struct {
	Kind string `json:"kind"` // error | warn | info
	Text string `json:"text"`
}

// notify brings the window forward and shows a message in the UI.
func (a *App) notify(kind, text string) {
	a.ShowWindow()
	runtime.EventsEmit(a.ctx, "notice", NoticeDTO{Kind: kind, Text: text})
}

// ---- API used by the frontend ----

// Overview returns everything the pages need in one call.
func (a *App) Overview() OverviewDTO {
	settings := a.ctl.Settings()
	list, _ := a.layout.ListConfigs()
	elevated, conflicts := a.ctl.Preflight()
	dto := OverviewDTO{
		Status:        toStatusDTO(a.ctl.Status()),
		Configs:       toConfigDTOs(list, settings.CurrentConfig),
		Addons:        toAddonDTOs(settings.Addons),
		GameGroups:    toGroupDTOs(),
		ProbeAddress:  settings.ProbeTarget.Address,
		ProbePort:     settings.ProbeTarget.Port,
		Elevated:      elevated,
		Conflicts:     toConflictDTOs(conflicts),
		SettingsError: a.settingsErr,
		DataDir:       a.layout.Root,
		Version:       versionString(),
	}
	if a.lockErr != nil {
		dto.LockError = "另一个 MSBOOST 程序（例如 msboostctl）正在使用加速核心，请先关闭它，然后重新打开本程序"
	}
	return dto
}

// Start begins acceleration and returns non-fatal warnings.
func (a *App) Start() ([]string, error) {
	if a.lockErr != nil {
		return nil, errors.New("另一个 MSBOOST 程序正在使用加速核心，请先关闭它")
	}
	return a.ctl.Start()
}

// Stop ends acceleration.
func (a *App) Stop() error { return a.ctl.Stop() }

// RunningGameProcesses lists running game processes (to confirm a stop).
func (a *App) RunningGameProcesses() []string { return a.ctl.RunningGameProcesses() }

// SelectConfig makes a file in configs\ the current node configuration.
func (a *App) SelectConfig(name string) error { return a.ctl.SelectConfig(name) }

// PickConfigFile asks for a configuration file; "" means cancelled.
func (a *App) PickConfigFile() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "选择 MSBOOST 节点配置",
		Filters: []runtime.FileFilter{{DisplayName: "MSBOOST 节点配置 (*.json)", Pattern: "*.json"}},
	})
}

// ImportConfig copies a validated configuration into configs\. When a
// different file with the same name exists, Exists is set and nothing is
// written unless overwrite is true.
func (a *App) ImportConfig(path string, overwrite bool) (ImportResultDTO, error) {
	entry, err := a.layout.ImportConfig(path, overwrite)
	if errors.Is(err, store.ErrConfigExists) {
		return ImportResultDTO{Name: filepath.Base(path), Exists: true}, nil
	}
	if err != nil {
		return ImportResultDTO{}, err
	}
	a.log.Printf(logx.App, "已导入节点配置 %s", entry.Name)
	if a.ctl.Settings().CurrentConfig == "" && a.ctl.Status().State == app.StateIdle {
		a.ctl.SelectConfig(entry.Name)
	}
	return ImportResultDTO{Name: entry.Name}, nil
}

// DeleteConfig removes a configuration; the one in use while accelerating
// cannot be deleted.
func (a *App) DeleteConfig(name string) error {
	st := a.ctl.Status()
	if st.State != app.StateIdle && st.Config == name {
		return errors.New("正在使用这个配置加速，请先停止加速")
	}
	if err := a.layout.DeleteConfig(name); err != nil {
		return err
	}
	a.log.Printf(logx.App, "已删除节点配置 %s", name)
	return nil
}

// ImportLegacyFolder imports configurations from the old BAT launcher folder.
func (a *App) ImportLegacyFolder() (LegacyResultDTO, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择旧版 MSBOOST 启动器所在的文件夹"})
	if err != nil || dir == "" {
		return LegacyResultDTO{}, err
	}
	result, err := a.layout.ImportLegacy(dir)
	if err != nil {
		return LegacyResultDTO{}, err
	}
	dto := LegacyResultDTO{Current: result.Current}
	for _, c := range result.Imported {
		dto.Imported = append(dto.Imported, c.Name)
	}
	for _, c := range result.Skipped {
		dto.Skipped = append(dto.Skipped, fmt.Sprintf("%s：%v", c.Name, c.Err))
	}
	if result.Current != "" && a.ctl.Status().State == app.StateIdle {
		a.ctl.SelectConfig(result.Current)
	}
	a.log.Printf(logx.App, "从旧版导入了 %d 个配置", len(dto.Imported))
	return dto, nil
}

// OpenFolder opens configs, logs or the data folder in Explorer.
func (a *App) OpenFolder(kind string) error {
	dirs := map[string]string{"configs": a.layout.Configs, "logs": a.layout.Logs, "data": a.layout.Root}
	dir, ok := dirs[kind]
	if !ok {
		return fmt.Errorf("未知的文件夹：%q", kind)
	}
	return exec.Command("explorer.exe", dir).Start()
}

// SetAddon switches Discord or Steam acceleration.
func (a *App) SetAddon(id string, on bool) error { return a.ctl.SetAddon(id, on) }

// SetProbeTarget changes the full-path probe target.
func (a *App) SetProbeTarget(address string, port int) error {
	return a.ctl.SetProbeTarget(strings.TrimSpace(address), port)
}

// ResetProbeTarget restores the default probe target.
func (a *App) ResetProbeTarget() error {
	d := store.DefaultSettings().ProbeTarget
	return a.ctl.SetProbeTarget(d.Address, d.Port)
}

// RunCheck runs the three network checks.
func (a *App) RunCheck() (CheckDTO, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	r, err := a.ctl.Check(ctx)
	if err != nil {
		return CheckDTO{}, err
	}
	return toCheckDTO(r), nil
}

// RecentLogs returns the latest log lines for the log page.
func (a *App) RecentLogs() []LogLineDTO {
	lines := a.log.Recent(500)
	out := make([]LogLineDTO, len(lines))
	for i, l := range lines {
		out[i] = toLogLineDTO(l)
	}
	return out
}

// ExportDiagnostics saves a support bundle; "" means cancelled.
func (a *App) ExportDiagnostics() (string, error) {
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出诊断包",
		DefaultFilename: "MSBOOST-诊断-" + time.Now().Format("20060102-150405") + ".zip",
		Filters:         []runtime.FileFilter{{DisplayName: "压缩包 (*.zip)", Pattern: "*.zip"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	elevated, conflicts := a.ctl.Preflight()
	summary := []string{
		"版本：" + versionString(),
		"数据目录：" + a.layout.Root,
		fmt.Sprintf("管理员权限：%v", elevated),
		"加速状态：" + string(a.ctl.Status().State),
	}
	for _, name := range []string{redirector.WinDivertService, redirector.ProxyBridgeService} {
		state, err := redirector.ServiceState(name)
		if err != nil {
			state = err.Error()
		}
		summary = append(summary, fmt.Sprintf("服务 %s：%s", name, state))
	}
	for _, c := range conflicts {
		summary = append(summary, "冲突："+c.Message)
	}
	var buf bytes.Buffer
	if err := writeDiagnostics(&buf, a.layout, summary); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	a.log.Printf(logx.App, "已导出诊断包 %s", path)
	return path, nil
}

// Cleanup removes the driver service, firewall rules and runtime files.
func (a *App) Cleanup() ([]string, error) { return a.ctl.Cleanup(a.exePath) }

// About returns version and license information.
func (a *App) About() AboutDTO { return aboutInfo() }

// OpenURL opens an https link from the About page in the default browser.
func (a *App) OpenURL(url string) {
	if strings.HasPrefix(url, "https://") {
		runtime.BrowserOpenURL(a.ctx, url)
	}
}

// HideToTray hides the main window.
func (a *App) HideToTray() { runtime.WindowHide(a.ctx) }

// ShowWindow brings the main window back.
func (a *App) ShowWindow() {
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

// Quit exits the program; acceleration is stopped first during shutdown.
func (a *App) Quit() {
	a.quitting.Store(true)
	runtime.Quit(a.ctx)
}
