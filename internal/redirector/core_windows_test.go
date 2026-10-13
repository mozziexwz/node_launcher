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

func TestFirewallRuleNames(t *testing.T) {
	a := FirewallRuleNames(`C:\Program Files\MSBOOST\MSBOOST-Launcher.exe`)
	b := FirewallRuleNames(`c:\program files\msboost\msboost-launcher.exe`)
	if len(a) != 2 || a[0] != b[0] || a[1] != b[1] {
		t.Fatalf("rule names must ignore ASCII case: %v vs %v", a, b)
	}
	if !strings.HasPrefix(a[0], "MSBOOST relay ") || len(a[0]) != len("MSBOOST relay ")+8 {
		t.Fatalf("unexpected patched rule name %q", a[0])
	}
	if !strings.HasPrefix(a[1], "ProxyBridge relay ") || a[0][len("MSBOOST relay "):] != a[1][len("ProxyBridge relay "):] {
		t.Fatalf("official rule name %q must share the hash of %q", a[1], a[0])
	}
	if a[0] == FirewallRuleNames(`F:\AI\Claude\MSBOOST启动器\m0spike.exe`)[0] {
		t.Fatal("different paths should hash differently")
	}
}
