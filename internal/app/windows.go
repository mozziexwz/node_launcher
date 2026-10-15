//go:build windows

package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mozziexwz/node_launcher/internal/engine"
	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/redirector"
	"github.com/mozziexwz/node_launcher/internal/store"
	"github.com/mozziexwz/node_launcher/internal/sysproxy"
	"github.com/mozziexwz/node_launcher/internal/winsys"
)

// The redirector names itself in a few log lines; the UI only says MSBOOST.
var redirectorMessages = map[string]string{
	"ProxyBridge started": "分流核心已启动",
	"ProxyBridge stopped": "分流核心已停止",
}

func brandRedirectorLine(line string) string {
	if m, ok := redirectorMessages[line]; ok {
		return m
	}
	return strings.ReplaceAll(line, "ProxyBridge", "分流核心")
}

// NewWindows builds a controller backed by the real core, the redirector DLL
// at corePath and the Windows system helpers. prepareRuntime, if not nil,
// checks the redirector's files before every start (release builds).
func NewWindows(layout store.Layout, log *logx.Logger, corePath string, prepareRuntime func() (func(), error)) *Controller {
	return New(Deps{
		Layout: layout,
		Log:    log,
		StartEngine: func(cfg *engine.Config) (CoreEngine, error) {
			return engine.Start(cfg, 0)
		},
		LoadRedirector: func() (Redirector, error) {
			core, err := redirector.Load(corePath)
			if err != nil {
				return nil, err
			}
			go func() {
				for line := range redirector.Logs() {
					log.Printf(logx.Redirect, "%s", brandRedirectorLine(line))
				}
			}()
			return coreAdapter{core}, nil
		},
		PrepareRuntime: prepareRuntime,
		SystemProxies:  sysproxy.Current,
		System:         windowsSystem{},
	})
}

type coreAdapter struct{ core *redirector.Core }

func (a coreAdapter) Prepare() {
	a.core.EnableLogs(true)
	a.core.SetTrafficLoggingEnabled(false)
	a.core.SetLocalhostViaProxy(false)
}

func (a coreAdapter) AddSOCKS5(host string, port uint16, user, password string, sendDomain bool) (uint32, error) {
	return a.core.AddProxyConfig(redirector.ProxySOCKS5, host, port, user, password, sendDomain)
}

func (a coreAdapter) DeleteProxy(id uint32) bool { return a.core.DeleteProxyConfig(id) }

func (a coreAdapter) AddProxyRule(processes string, proxyID uint32) (uint32, error) {
	return a.core.AddRule(processes, "*", "*", "*", redirector.ProtocolBoth, redirector.ActionProxy, proxyID)
}

func (a coreAdapter) AddTCPRule(processes, hosts, ports string, proxyID uint32) (uint32, error) {
	return a.core.AddRule(processes, hosts, ports, "*", redirector.ProtocolTCP, redirector.ActionProxy, proxyID)
}

func (a coreAdapter) EditTCPRule(id uint32, processes, hosts, ports string, proxyID uint32) error {
	return a.core.EditRule(id, processes, hosts, ports, "*", redirector.ProtocolTCP, redirector.ActionProxy, proxyID)
}

func (a coreAdapter) EnableRule(id uint32) bool  { return a.core.EnableRule(id) }
func (a coreAdapter) DisableRule(id uint32) bool { return a.core.DisableRule(id) }
func (a coreAdapter) DeleteRule(id uint32) bool  { return a.core.DeleteRule(id) }

func (a coreAdapter) SetLocalhostProxyPorts(ports []uint16) error {
	return a.core.SetLocalhostProxyPorts(ports)
}
func (a coreAdapter) Start() bool { return a.core.Start() }
func (a coreAdapter) Stop() bool  { return a.core.Stop() }

type windowsSystem struct{}

func (windowsSystem) Elevated() bool { return winsys.IsElevated() }

func (windowsSystem) DriverUnloading() bool {
	return redirector.ServiceUnloading(redirector.WinDivertService)
}

func (windowsSystem) Conflicts() []Conflict {
	state, _ := redirector.ServiceState(redirector.ProxyBridgeService)
	running, _ := winsys.RunningMatches(winsys.ConflictingPrograms())
	found := winsys.Conflicts(winsys.ConflictInputs{
		ProxyBridgeServiceState: state,
		RelayPortsBusy:          redirector.RelayPortConflicts(),
		RunningProcesses:        running,
	})
	out := make([]Conflict, len(found))
	for i, f := range found {
		out[i] = Conflict{Message: f.Message, Blocking: f.Blocking, Fix: f.Fix}
	}
	return out
}

// FixConflict ends the official ProxyBridge app and stops its service; they
// hold the relay ports and the packet driver the launcher needs.
func (windowsSystem) FixConflict(fix string) ([]string, error) {
	if fix != winsys.FixProxyBridge {
		return nil, fmt.Errorf("未知的冲突处理方式：%q", fix)
	}
	var report []string
	// Ask it to exit the way its tray menu does, which also stops its core.
	exited, running, err := winsys.ExitPrograms(winsys.ProxyBridgePrograms,
		winsys.ProxyBridgeTitle, winsys.ProxyBridgeExitCommand, 8*time.Second)
	if exited > 0 {
		report = append(report, "已让 ProxyBridge 程序退出")
	}
	if err != nil {
		return report, fmt.Errorf("无法让 ProxyBridge 退出：%w", err)
	}
	if running > 0 {
		return report, errors.New("ProxyBridge 没有退出。请在它的托盘图标上右键选择“退出”（命令行版请直接关闭窗口），然后重试")
	}
	switch state, _ := redirector.ServiceState(redirector.ProxyBridgeService); state {
	case "RUNNING", "START_PENDING", "PAUSED", "CONTINUE_PENDING", "PAUSE_PENDING":
		if state, err := redirector.StopService(redirector.ProxyBridgeService, 15*time.Second); err != nil {
			return report, fmt.Errorf("无法停止 ProxyBridge 后台服务（%s）：%w", state, err)
		}
		report = append(report, "已停止 ProxyBridge 后台服务（ProxyBridgeSvc）")
	}
	if len(report) == 0 {
		report = append(report, "ProxyBridge 已不在运行")
	}
	return report, nil
}

func (windowsSystem) RunningProcesses(names []string) []string {
	running, _ := winsys.RunningMatches(names)
	return running
}

func (windowsSystem) StopDriver() (string, error) {
	return redirector.StopService(redirector.WinDivertService, 10*time.Second)
}

func (windowsSystem) Cleanup(exePath string) []string {
	var report []string
	state, err := redirector.StopService(redirector.WinDivertService, 10*time.Second)
	switch {
	case err != nil:
		report = append(report, fmt.Sprintf("驱动未能卸载（%s）：%v。可能有其他软件在使用，重启电脑后即可清除", state, err))
	case state == "NOT_INSTALLED":
		report = append(report, "驱动未安装，无需处理")
	default:
		report = append(report, "驱动状态："+state)
	}
	// Our patched core names its rule "MSBOOST relay <path hash>"; a portable
	// copy moved between folders leaves one rule per folder, so remove them
	// all. The official core's name is only removed for this executable.
	removed, err := winsys.DeleteFirewallRulesWithPrefix(redirector.RulePrefixPatched + " ")
	if err != nil {
		report = append(report, err.Error())
	}
	for _, name := range removed {
		report = append(report, "已删除防火墙规则 "+name)
	}
	official := redirector.FirewallRuleNames(exePath)[1]
	deleted, officialErr := winsys.DeleteFirewallRule(official)
	if officialErr != nil {
		report = append(report, officialErr.Error())
	} else if deleted {
		report = append(report, "已删除防火墙规则 "+official)
	}
	if err == nil && officialErr == nil && len(removed) == 0 && !deleted {
		report = append(report, "没有残留的防火墙规则")
	}
	dir := winsys.ProgramDataDir()
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err == nil {
			report = append(report, "已删除 "+dir)
		} else if left, serr := deleteOnReboot(dir); serr == nil {
			// The DLLs stay loaded until the program exits.
			report = append(report, fmt.Sprintf("%s 中有 %d 个运行文件正在使用，已安排在下次重启电脑时删除", dir, left))
		} else {
			report = append(report, fmt.Sprintf("删除 %s 失败：%v", dir, err))
		}
	}
	return report
}

// deleteOnReboot schedules everything left under dir, files before their
// folders, for deletion at the next restart and returns how many files.
func deleteOnReboot(dir string) (files int, err error) {
	var paths []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		if !d.IsDir() {
			files++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	// WalkDir lists a folder before its contents; delete in reverse.
	for i := len(paths) - 1; i >= 0; i-- {
		if err := winsys.DeleteOnReboot(paths[i]); err != nil {
			return 0, err
		}
	}
	return files, nil
}
