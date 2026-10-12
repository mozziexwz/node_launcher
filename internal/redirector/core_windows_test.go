//go:build windows

package redirector

import (
	"os"
	"strings"
	"testing"
	"time"
)

// coreDLL locates a ProxyBridgeCore.dll for the ABI test: MSBOOST_CORE_DLL,
// then the repository build output, then an installed official ProxyBridge.
func coreDLL(t *testing.T) string {
	candidates := []string{
		os.Getenv("MSBOOST_CORE_DLL"),
		`..\..\build\core\ProxyBridgeCore.dll`,
		`C:\Program Files\ProxyBridge\ProxyBridgeCore.dll`,
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("ProxyBridgeCore.dll not found; set MSBOOST_CORE_DLL")
	return ""
}

// TestCoreABI exercises configuration calls without Start, so it needs no
// elevation and never touches WinDivert, the firewall or network traffic.
func TestCoreABI(t *testing.T) {
	core, err := Load(coreDLL(t))
	if err != nil {
		t.Fatal(err)
	}
	core.EnableLogs(true)
	defer core.EnableLogs(false)

	proxyID, err := core.AddProxyConfig(ProxySOCKS5, "127.0.0.1", 10086, "user", "password", true)
	if err != nil {
		t.Fatalf("AddProxyConfig: %v", err)
	}
	ruleID, err := core.AddRule("MapleStory.exe;Kaizen v92.exe;*.aes", "*", "*", "*", ProtocolBoth, ActionProxy, proxyID)
	if err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	other, err := core.AddRule("other.exe", "*", "*", "", ProtocolTCP, ActionDirect, proxyID)
	if err != nil || other == ruleID {
		t.Fatalf("second AddRule: id=%d err=%v", other, err)
	}
	if !core.DeleteRule(other) || !core.DeleteRule(ruleID) {
		t.Fatal("DeleteRule failed")
	}
	if core.DeleteRule(ruleID) {
		t.Fatal("deleting a deleted rule must fail")
	}
	if !core.DeleteProxyConfig(proxyID) {
		t.Fatal("DeleteProxyConfig failed")
	}

	// Drain whatever the core logged; the callback must have delivered
	// complete strings from its native thread.
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case line := <-Logs():
			if strings.ContainsRune(line, 0) {
				t.Fatalf("log line contains NUL: %q", line)
			}
			t.Logf("core log: %s", line)
		case <-timeout:
			return
		}
	}
}

func TestFirewallRuleName(t *testing.T) {
	a := FirewallRuleName(`C:\Program Files\MSBOOST\MSBOOST-Launcher.exe`)
	b := FirewallRuleName(`c:\program files\msboost\msboost-launcher.exe`)
	if a != b {
		t.Fatalf("rule name must ignore ASCII case: %s vs %s", a, b)
	}
	if !strings.HasPrefix(a, "ProxyBridge relay ") || len(a) != len("ProxyBridge relay ")+8 {
		t.Fatalf("unexpected rule name %q", a)
	}
	if a == FirewallRuleName(`F:\AI\Claude\MSBOOST启动器\m0spike.exe`) {
		t.Fatal("different paths should hash differently")
	}
}
