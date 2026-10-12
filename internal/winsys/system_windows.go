//go:build windows

package winsys

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// Conflict is something that would interfere with acceleration.
type Conflict struct {
	Message  string
	Blocking bool // acceleration must not start while it remains
}

// ConflictInputs are the facts Conflicts reasons about; gathered separately
// so the rules can be tested without the real system.
type ConflictInputs struct {
	ProxyBridgeServiceState string   // state of ProxyBridgeSvc, "" if unknown
	RelayPortsBusy          []string // from redirector.RelayPortConflicts
	RunningProcesses        []string // names of running conflicting programs
}

// conflictingPrograms intercept traffic the same way and would double-proxy
// or fight over the relay ports.
var conflictingPrograms = []string{"ProxyBridge.exe", "ProxyBridge_CLI.exe", "Netch.exe"}

// ConflictingPrograms lists the process names Conflicts looks for.
func ConflictingPrograms() []string { return append([]string(nil), conflictingPrograms...) }

// Conflicts turns the gathered facts into user-facing findings. Third-party
// names are spelled out here because the user has to close those programs.
func Conflicts(in ConflictInputs) []Conflict {
	var out []Conflict
	if in.ProxyBridgeServiceState == "RUNNING" || in.ProxyBridgeServiceState == "START_PENDING" {
		out = append(out, Conflict{
			Message:  "检测到 ProxyBridge 的后台服务正在运行，请先关闭 ProxyBridge，并在管理员命令行执行 sc stop ProxyBridgeSvc",
			Blocking: true,
		})
	}
	running := make(map[string]bool)
	for _, p := range in.RunningProcesses {
		running[strings.ToLower(p)] = true
	}
	if running["proxybridge.exe"] || running["proxybridge_cli.exe"] {
		out = append(out, Conflict{Message: "检测到 ProxyBridge 正在运行，请先退出它", Blocking: true})
	}
	if running["netch.exe"] {
		out = append(out, Conflict{Message: "检测到 Netch 正在运行，同时使用可能重复代理游戏，建议先退出 Netch", Blocking: false})
	}
	if len(in.RelayPortsBusy) > 0 && in.ProxyBridgeServiceState != "RUNNING" {
		out = append(out, Conflict{
			Message:  "分流所需的本地端口被占用（" + strings.Join(in.RelayPortsBusy, "、") + "），请关闭占用它的程序后重试",
			Blocking: true,
		})
	}
	return out
}

// IsElevated reports whether the process runs with an elevated token.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// ErrAlreadyRunning means another launcher instance holds the lock.
var ErrAlreadyRunning = errors.New("另一个 MSBOOST 启动器正在运行")

// AcquireInstance takes a per-session named mutex so that only one launcher
// (window or command line) drives the core at a time.
func AcquireInstance(name string) (release func(), err error) {
	p, err := windows.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, p)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, err
	}
	return func() { windows.CloseHandle(h) }, nil
}

// DeleteFirewallRule removes an inbound rule by name with netsh (needs
// elevation). A rule that does not exist is not an error.
func DeleteFirewallRule(name string) (deleted bool, err error) {
	cmd := exec.Command("netsh.exe", "advfirewall", "firewall", "delete", "rule", "name="+name)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		// netsh exits 1 with "No rules match the specified criteria."
		return false, nil
	}
	return false, fmt.Errorf("删除防火墙规则失败：%v（%s）", err, strings.TrimSpace(string(out)))
}

// ProgramDataDir is %ProgramData%\MSBOOST, where released runtime files live.
func ProgramDataDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "MSBOOST")
}
