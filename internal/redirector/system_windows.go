//go:build windows

package redirector

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// Service names relevant to interception.
const (
	WinDivertService   = "WinDivert"
	ProxyBridgeService = "ProxyBridgeSvc" // the official ProxyBridge app's service

	relayTCPPort = 34010 // ProxyBridge TCP relay (LOCAL_PROXY_PORT)
)

// IsElevated reports whether the process runs with an elevated token.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// ServiceState returns RUNNING, STOPPED, ... or NOT_INSTALLED. It needs only
// query rights, so it also works without elevation.
func ServiceState(name string) (string, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return "", err
	}
	defer windows.CloseServiceHandle(scm)
	svc, err := openService(scm, name, windows.SERVICE_QUERY_STATUS)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return "NOT_INSTALLED", nil
	}
	if err != nil {
		return "", err
	}
	defer windows.CloseServiceHandle(svc)
	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(svc, &status); err != nil {
		return "", err
	}
	return stateName(status.CurrentState), nil
}

// StopService asks a service (or driver) to stop and waits up to timeout.
// For WinDivert this unloads the driver once no other program holds a handle.
func StopService(name string, timeout time.Duration) (string, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return "", err
	}
	defer windows.CloseServiceHandle(scm)
	svc, err := openService(scm, name, windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return "NOT_INSTALLED", nil
	}
	if err != nil {
		return "", err
	}
	defer windows.CloseServiceHandle(svc)

	var status windows.SERVICE_STATUS
	if err := windows.ControlService(svc, windows.SERVICE_CONTROL_STOP, &status); err != nil &&
		!errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		state := "UNKNOWN"
		if windows.QueryServiceStatus(svc, &status) == nil {
			state = stateName(status.CurrentState)
		}
		return state, err
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := windows.QueryServiceStatus(svc, &status); err != nil {
			return "", err
		}
		if status.CurrentState == windows.SERVICE_STOPPED || time.Now().After(deadline) {
			return stateName(status.CurrentState), nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func openService(scm windows.Handle, name string, access uint32) (windows.Handle, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	return windows.OpenService(scm, n, access)
}

func stateName(state uint32) string {
	switch state {
	case windows.SERVICE_STOPPED:
		return "STOPPED"
	case windows.SERVICE_START_PENDING:
		return "START_PENDING"
	case windows.SERVICE_STOP_PENDING:
		return "STOP_PENDING"
	case windows.SERVICE_RUNNING:
		return "RUNNING"
	case windows.SERVICE_CONTINUE_PENDING:
		return "CONTINUE_PENDING"
	case windows.SERVICE_PAUSE_PENDING:
		return "PAUSE_PENDING"
	case windows.SERVICE_PAUSED:
		return "PAUSED"
	}
	return fmt.Sprintf("STATE_%d", state)
}

// RelayPortConflicts lists ProxyBridge relay ports that another program
// already holds (typically a running official ProxyBridge). Only the TCP
// relay can be probed this way: the UDP relay binds with SO_REUSEADDR, so a
// second bind succeeds even while another instance owns the port. Every
// ProxyBridge instance listens on the TCP relay, so it still reveals one.
func RelayPortConflicts() []string { return portConflicts(relayTCPPort) }

// probeMu serializes probes: two concurrent probes in this process would
// find each other's listener and report the port as busy.
var probeMu sync.Mutex

func portConflicts(port int) []string {
	probeMu.Lock()
	defer probeMu.Unlock()
	var busy []string
	for _, network := range []string{"tcp4", "tcp6"} {
		host := "0.0.0.0"
		if network == "tcp6" {
			host = "::"
		}
		ln, err := net.Listen(network, net.JoinHostPort(host, fmt.Sprint(port)))
		if err != nil {
			if network == "tcp6" && !hasIPv6() {
				continue
			}
			busy = append(busy, fmt.Sprintf("TCP %d（%s）", port, host))
			continue
		}
		ln.Close()
	}
	return busy
}

func hasIPv6() bool {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// Prefixes of the inbound firewall rule the core adds for its host program:
// our patched build (third_party/patches/0001) and the official build.
const (
	RulePrefixPatched  = "MSBOOST relay"
	RulePrefixOfficial = "ProxyBridge relay"
)

// FirewallRuleNames lists the rule names the core may have added for the
// executable at exePath, patched build first. The suffix is the FNV-1a hash
// of the ASCII-lower-cased UTF-16 path (fw_ensure_thread in ProxyBridge.c).
func FirewallRuleNames(exePath string) []string {
	h := uint32(2166136261)
	for _, c := range utf16.Encode([]rune(exePath)) {
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		h = (h ^ uint32(c)) * 16777619
	}
	return []string{
		fmt.Sprintf("%s %08x", RulePrefixPatched, h),
		fmt.Sprintf("%s %08x", RulePrefixOfficial, h),
	}
}
