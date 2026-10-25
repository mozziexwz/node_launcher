//go:build windows

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bridge's notices are matched by their exact text, so they must stay in
// step with what patch P5 logs.
func TestBridgeMessagesMatchPatch(t *testing.T) {
	patch, err := os.ReadFile(filepath.Join("..", "..", "third_party", "patches", "0003-impostor-bridge.patch"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for line := range redirectorMessages {
		if !strings.HasPrefix(line, "Bridge: ") {
			continue
		}
		found++
		if !strings.Contains(string(patch), `"`+line+`"`) {
			t.Errorf("patch P5 does not log %q", line)
		}
	}
	if found != 2 {
		t.Errorf("found %d bridge messages, want 2", found)
	}
}

func TestBrandRedirectorLine(t *testing.T) {
	for in, want := range map[string]string{
		"ProxyBridge started": "分流核心已启动",
		"ProxyBridge stopped": "分流核心已停止",
		"Bridge: another network driver re-sends packets, handing them back":                       "检测到本机有其他网络驱动转发数据包，已自动兼容",
		"Packet capture keeps failing (5) - traffic is not being redirected. Restart ProxyBridge.": "Packet capture keeps failing (5) - traffic is not being redirected. Restart 分流核心.",
		"[TCP] MapleStory.exe -> 203.0.113.7:8585":                                                 "[TCP] MapleStory.exe -> 203.0.113.7:8585",
	} {
		if got := brandRedirectorLine(in); got != want {
			t.Errorf("brandRedirectorLine(%q) = %q, want %q", in, got, want)
		}
	}
}
