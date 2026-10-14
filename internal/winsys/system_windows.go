//go:build windows

package winsys

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// Conflict is something that would interfere with acceleration.
type Conflict struct {
	Message  string
	Blocking bool   // acceleration must not start while it remains
	Fix      string // FixProxyBridge when the launcher can resolve it, else ""
}

// FixProxyBridge names the fix that ends the ProxyBridge app and stops its
// service.
const FixProxyBridge = "proxybridge"

// ProxyBridgePrograms are the official ProxyBridge app's processes.
var ProxyBridgePrograms = []string{"ProxyBridge.exe", "ProxyBridge_CLI.exe"}

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
			Message:  "检测到 ProxyBridge 的后台服务正在运行，它会和 MSBOOST 抢用分流驱动，需要先停止",
			Blocking: true,
			Fix:      FixProxyBridge,
		})
	}
	running := make(map[string]bool)
	for _, p := range in.RunningProcesses {
		running[strings.ToLower(p)] = true
	}
	if running["proxybridge.exe"] || running["proxybridge_cli.exe"] {
		out = append(out, Conflict{Message: "检测到 ProxyBridge 正在运行，需要先退出它", Blocking: true, Fix: FixProxyBridge})
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

// DeleteFirewallRulesWithPrefix removes every rule whose display name starts
// with prefix and returns the removed names (needs elevation). It goes
// through Get-NetFirewallRule because netsh's listing is localized.
func DeleteFirewallRulesWithPrefix(prefix string) ([]string, error) {
	if prefix == "" || strings.ContainsAny(prefix, `'*?[]`+"`$") {
		return nil, fmt.Errorf("无效的规则前缀：%q", prefix)
	}
	// A name is printed only after its rule is gone; a failed removal stops
	// the script with a non-zero exit code.
	script := "Get-NetFirewallRule -DisplayName '" + prefix + "*' -ErrorAction SilentlyContinue | " +
		"ForEach-Object { $_ | Remove-NetFirewallRule -ErrorAction Stop; $_.DisplayName }"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	names, rest := parseRuleNames(string(out), prefix)
	if err != nil {
		return names, fmt.Errorf("删除防火墙规则失败：%v（%s）", err, rest)
	}
	return names, nil
}

// parseRuleNames separates the printed rule names from any other output.
func parseRuleNames(output, prefix string) (names []string, rest string) {
	var other []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, prefix):
			names = append(names, line)
		case line != "":
			other = append(other, line)
		}
	}
	return names, strings.Join(other, " ")
}

// ProgramDataDir is %ProgramData%\MSBOOST, where released runtime files live.
// The path comes from the known-folder registry, not the ProgramData
// environment variable, which the user can redefine for elevated processes.
func ProgramDataDir() string {
	base, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil || base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "MSBOOST")
}
