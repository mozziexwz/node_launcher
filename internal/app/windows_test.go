//go:build windows

package app

import "testing"

func TestBrandRedirectorLine(t *testing.T) {
	for in, want := range map[string]string{
		"ProxyBridge started": "分流核心已启动",
		"ProxyBridge stopped": "分流核心已停止",
		"Packet capture keeps failing (5) - traffic is not being redirected. Restart ProxyBridge.": "Packet capture keeps failing (5) - traffic is not being redirected. Restart 分流核心.",
		"[TCP] MapleStory.exe -> 203.0.113.7:8585":                                                 "[TCP] MapleStory.exe -> 203.0.113.7:8585",
	} {
		if got := brandRedirectorLine(in); got != want {
			t.Errorf("brandRedirectorLine(%q) = %q, want %q", in, got, want)
		}
	}
}
