//go:build windows

package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mozziexwz/node_launcher/internal/engine"
	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/redirector"
	"github.com/mozziexwz/node_launcher/internal/store"
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
					log.Printf(logx.Redirect, "%s", brandRedirectorLine(line))
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
		if err := os.RemoveAll(dir); err != nil {
			report = append(report, fmt.Sprintf("删除 %s 失败：%v", dir, err))
		} else {
			report = append(report, "已删除 "+dir)
		}
	}
	return report
}
