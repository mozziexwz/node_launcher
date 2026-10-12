//go:build windows

package app

import (
	"fmt"
	"os"
	"time"

	"github.com/mozziexwz/node_launcher/internal/engine"
	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/redirector"
	"github.com/mozziexwz/node_launcher/internal/store"
	"github.com/mozziexwz/node_launcher/internal/winsys"
)

// NewWindows builds a controller backed by the real core, the redirector DLL
// at corePath and the Windows system helpers.
func NewWindows(layout store.Layout, log *logx.Logger, corePath string) *Controller {
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
					log.Printf(logx.Redirect, "%s", line)
				}
			}()
			return coreAdapter{core}, nil
		},
		System: windowsSystem{},
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

func (a coreAdapter) DeleteRule(id uint32) bool { return a.core.DeleteRule(id) }
func (a coreAdapter) Start() bool               { return a.core.Start() }
func (a coreAdapter) Stop() bool                { return a.core.Stop() }

type windowsSystem struct{}

func (windowsSystem) Elevated() bool { return winsys.IsElevated() }

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
		out[i] = Conflict{Message: f.Message, Blocking: f.Blocking}
	}
	return out
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
	for _, name := range redirector.FirewallRuleNames(exePath) {
		deleted, err := winsys.DeleteFirewallRule(name)
		switch {
		case err != nil:
			report = append(report, err.Error())
		case deleted:
			report = append(report, "已删除防火墙规则 "+name)
		}
	}
	dir := winsys.ProgramDataDir()
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			report = append(report, fmt.Sprintf("删除 %s 失败：%v", dir, err))
		} else {
			report = append(report, "已删除 "+dir)
		}
	}
	return report
}
