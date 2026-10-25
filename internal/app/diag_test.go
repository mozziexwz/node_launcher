package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mozziexwz/node_launcher/internal/engine"
	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/profiles"
	"github.com/mozziexwz/node_launcher/internal/sysproxy"
)

// connLines returns the text of the log's connection records.
func connLines(log *logx.Logger) []string {
	var out []string
	for _, l := range log.Recent(1000) {
		if l.Tag == logx.Conn {
			out = append(out, l.Text)
		}
	}
	return out
}

func appLines(log *logx.Logger) []string {
	var out []string
	for _, l := range log.Recent(1000) {
		if l.Tag == logx.App {
			out = append(out, l.Text)
		}
	}
	return out
}

func newTestDiag(t *testing.T) (*connDiag, *logx.Logger) {
	t.Helper()
	log, _ := logx.New("", 0)
	d := newConnDiag(log)
	d.setPrograms(profiles.DefaultMapleStory().ProcessList())
	d.setProxy(40000, "加速")
	d.setProxy(40001, "改由 MSBOOST 接管（系统代理）")
	return d, log
}

func TestDescribeDecision(t *testing.T) {
	proxies := map[int]string{40000: "加速", 40001: "接管"}
	cases := []struct {
		info    string
		verdict string
		udp     bool
	}{
		{"Proxy SOCKS5://127.0.0.1:40000", "加速", false},
		{"Proxy SOCKS5://127.0.0.1:40000 (UDP)", "加速", true},
		{"Proxy SOCKS5://127.0.0.1:40001", "接管", false},
		{"Proxy SOCKS5://127.0.0.1:50000", "加速", false},
		{"Proxy", "加速", false},
		{"Direct", "直连", false},
		{"Direct (UDP)", "直连", true},
		{"Blocked", "拦截", false},
		{"something else", "something else", false},
	}
	for _, c := range cases {
		if v, udp := describeDecision(c.info, proxies); v != c.verdict || udp != c.udp {
			t.Fatalf("%q: %q %v, want %q %v", c.info, v, udp, c.verdict, c.udp)
		}
	}
}

func TestConnDiagDecisions(t *testing.T) {
	d, log := newTestDiag(t)
	events := []ConnEvent{
		{"nexon_launcher.exe", "13.35.7.81", 443, "Proxy SOCKS5://127.0.0.1:40000"},
		{"nexon_launcher.exe", "13.35.7.81", 443, "Proxy SOCKS5://127.0.0.1:40000"}, // repeated
		{"NEXON_LAUNCHER.EXE", "2600:9000:21e2::1", 443, "Proxy SOCKS5://127.0.0.1:40000"},
		{"nexon_launcher.exe", "192.168.43.1", 53, "Proxy SOCKS5://127.0.0.1:40000 (UDP)"},
		{"nexon_launcher.exe", "127.0.0.1", 10808, "Proxy SOCKS5://127.0.0.1:40001"},
		{"MapleStory.exe", "224.0.0.251", 5353, "Direct (UDP)"},
		{"chrome.exe", "142.250.1.1", 443, "Direct"}, // not ours
		{"cefhelper.exe", "1.2.3.4", 443, "Direct"},  // pattern entry, not accelerated
		{"cefhelper.exe", "5.6.7.8", 443, "Proxy SOCKS5://127.0.0.1:40000"},
	}
	for _, ev := range events {
		d.decision(ev)
	}
	want := []string{
		"nexon_launcher.exe → 13.35.7.81:443 TCP，加速",
		"NEXON_LAUNCHER.EXE → [2600:9000:21e2::1]:443 TCP IPv6，加速",
		"nexon_launcher.exe → 192.168.43.1:53 UDP，加速",
		"nexon_launcher.exe → 127.0.0.1:10808 TCP，改由 MSBOOST 接管（系统代理）",
		"MapleStory.exe → 224.0.0.251:5353 UDP，直连",
		"cefhelper.exe → 5.6.7.8:443 TCP，加速",
	}
	if got := connLines(log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	d.finish()
	got := connLines(log)
	if summary := got[len(got)-1]; !strings.Contains(summary, "访问的目标 6 个（加速 5、直连 1、拦截 0，其中 IPv6 1 个）") ||
		!strings.Contains(summary, "经节点的连接 0 条") {
		t.Fatalf("summary %q", summary)
	}
	d.decision(ConnEvent{"MapleStory.exe", "8.31.99.1", 8484, "Proxy"})
	if n := len(connLines(log)); n != len(got) {
		t.Fatalf("records after finish: %d lines, want %d", n, len(got))
	}
}

func TestConnDiagRequests(t *testing.T) {
	d, log := newTestDiag(t)
	reqs := []engine.Request{
		{Command: "CONNECT", Host: "www.nexon.com", Port: 443, Domain: true, Up: 2048, Down: 50 << 10, Elapsed: 12 * time.Second},
		{Command: "CONNECT", Host: "www.nexon.com", Port: 443, Domain: true, Up: 100, Down: 200}, // same target
		{Command: "CONNECT", Host: "2600:9000:21e2::1", Port: 443, Up: 517, Down: 0, Elapsed: 30 * time.Second},
		{Command: "CONNECT", Host: "54.238.121.146", Port: 8585, Up: 0, Down: 0, Elapsed: 500 * time.Millisecond},
		{Command: "CONNECT", Host: "127.0.0.1", Port: 20424, Up: 100, Down: 100},     // network check
		{Command: "CONNECT", Host: "api-ipv4.ip.sb", Port: 443, Domain: true, Up: 1}, // network check
		{Command: "UDP", Host: "0.0.0.0", Port: 0},
		{Command: ""}, // no handshake
	}
	for _, r := range reqs {
		d.request(r)
	}
	want := []string{
		"经节点 www.nexon.com:443（域名）：上行 2 KiB，下行 50 KiB，用时 12 秒",
		"经节点 [2600:9000:21e2::1]:443（IPv6 地址，未拿到域名）：上行 517 B，下行 0 B，用时 30 秒，没有收到任何回应",
		"经节点 54.238.121.146:8585（IPv4 地址，未拿到域名）：上行 0 B，下行 0 B，用时 不到 1 秒，没有传输数据",
	}
	if got := connLines(log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	d.finish()
	got := connLines(log)
	tail := strings.Join(got[len(want):], "\n")
	if !strings.Contains(tail, "经节点的连接 4 条（域名 2、IPv4 地址 1、IPv6 地址 1），其中 1 条没有收到回应") ||
		!strings.Contains(tail, "1 条连接没有完成 SOCKS5 握手") {
		t.Fatalf("summary:\n%s", tail)
	}
}

func TestConnDiagLimits(t *testing.T) {
	d, log := newTestDiag(t)
	for i := 0; i < maxDecisionLines+30; i++ {
		d.decision(ConnEvent{"MapleStory.exe", fmt.Sprintf("10.0.%d.%d", i/250, i%250), 8585, "Proxy"})
	}
	for i := 0; i < maxRequestLines+20; i++ {
		d.request(engine.Request{Command: "CONNECT", Host: fmt.Sprintf("host%d.example", i), Port: 443, Domain: true, Up: 1, Down: 1})
	}
	if n := len(connLines(log)); n != maxDecisionLines+maxRequestLines {
		t.Fatalf("%d lines, want %d", n, maxDecisionLines+maxRequestLines)
	}
	d.finish()
	got := connLines(log)
	if !strings.Contains(strings.Join(got, "\n"), "另有 50 条记录超过每次加速的上限") {
		t.Fatalf("no overflow note in:\n%s", strings.Join(got[len(got)-3:], "\n"))
	}
}

func TestFormatElapsed(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                                 "不到 1 秒",
		999 * time.Millisecond:            "不到 1 秒",
		59 * time.Second:                  "59 秒",
		61 * time.Second:                  "1 分 1 秒",
		2*time.Hour + 5*time.Minute + 3e9: "2 小时 5 分",
	} {
		if got := formatElapsed(d); got != want {
			t.Fatalf("%v: %q, want %q", d, got, want)
		}
	}
}

func TestSystemProxyText(t *testing.T) {
	cases := map[string]sysproxy.Settings{
		"系统代理 关":                      {},
		"系统代理 关 ":                     {Enabled: true}, // switched on without a server
		"系统代理 127.0.0.1:10808":        {Enabled: true, Server: "127.0.0.1:10808"},
		"自动配置脚本 http://127.0.0.1/pac": {PACURL: "http://127.0.0.1/pac"},
		"系统代理 http=a:1;https=a:1，自动配置脚本 http://x/p": {Enabled: true, Server: "http=a:1;https=a:1", PACURL: "http://x/p"},
	}
	for want, s := range cases {
		if got := systemProxyText(s); got != strings.TrimSpace(want) {
			t.Fatalf("%+v: %q, want %q", s, got, strings.TrimSpace(want))
		}
	}
}

func TestRelatedProcesses(t *testing.T) {
	got := RelatedProcesses([]string{"explorer.exe", "nexon_launcher.exe", "Nexon_Launcher.exe", "NexonUpdater.exe", "MapleStory.exe", "msedgewebview2.exe", "MapleStoryWorlds.exe"})
	want := "nexon_launcher.exe、NexonUpdater.exe（不在加速名单）、MapleStory.exe、MapleStoryWorlds.exe（不在加速名单）"
	if strings.Join(got, "、") != want {
		t.Fatalf("got %q, want %q", strings.Join(got, "、"), want)
	}
}

// ---- in the controller ----

func TestStartLogsEnvironmentAndWarnings(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.environment = func() []string {
			return []string{"网络环境：Windows 11（版本 10.0.22631）；系统代理 关", "网卡 WLAN（Wi-Fi）：IPv4 192.168.43.25"}
		}
		h.sys.running = []string{"nexon_launcher.exe"}
		h.sys.conflicts = []Conflict{{Message: "检测到 Netch 正在运行"}}
	})
	warnings, err := h.c.Start()
	if err != nil || len(warnings) != 2 {
		t.Fatalf("Start: %v %v", warnings, err)
	}
	logged := strings.Join(appLines(h.log), "\n")
	for _, want := range []string{
		"网络环境：Windows 11（版本 10.0.22631）；系统代理 关",
		"网卡 WLAN（Wi-Fi）：IPv4 192.168.43.25",
		"提醒：检测到 Netch 正在运行",
		"提醒：这些游戏进程在加速开始前已在运行：nexon_launcher.exe",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log lacks %q:\n%s", want, logged)
		}
	}
	h.c.Stop()
}

func TestConnectionTracingBracketsTheRedirector(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	if traces := h.red.traces(); len(traces) != 1 || !strings.HasPrefix(traces[0], "on after add-rule ") {
		t.Fatalf("traces %v: tracing must begin just before start", traces)
	}
	h.red.emit(ConnEvent{"MapleStory.exe", "8.31.99.141", 8484, "Proxy SOCKS5://127.0.0.1:40000"})
	if err := h.c.Stop(); err != nil {
		t.Fatal(err)
	}
	if traces := h.red.traces(); len(traces) != 2 || traces[1] != "off after stop" {
		t.Fatalf("traces %v: tracing must end right after stop", traces)
	}
	lines := connLines(h.log)
	if len(lines) != 2 || lines[0] != "MapleStory.exe → 8.31.99.141:8484 TCP，加速" || !strings.HasPrefix(lines[1], "本次统计：") {
		t.Fatalf("connection lines %q", lines)
	}
}

func TestStartFailureEndsTracing(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.red.startFails = true })
	if _, err := h.c.Start(); err == nil {
		t.Fatal("Start succeeded")
	}
	if traces := h.red.traces(); len(traces) != 2 || traces[1] != "off after start" {
		t.Fatalf("traces %v", traces)
	}
}

func TestObservedRequestsAndAddonsInTheLog(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.observe = true })
	if _, err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	eng := h.observed[0]
	eng.report(engine.Request{Command: "CONNECT", Host: "nxl.nxfs.nexon.com", Port: 443, Domain: true, Up: 900, Down: 4096, Elapsed: 3 * time.Second})

	// Steam is not accelerated yet, so its connections are not recorded.
	steam := ConnEvent{"steam.exe", "23.1.2.3", 443, "Direct"}
	h.red.emit(steam)
	if err := h.c.SetAddon("steam", true); err != nil {
		t.Fatal(err)
	}
	h.red.emit(ConnEvent{"steam.exe", "23.1.2.3", 443, "Proxy SOCKS5://127.0.0.1:40000"})
	if err := h.c.Stop(); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(connLines(h.log), "\n")
	for _, want := range []string{
		"经节点 nxl.nxfs.nexon.com:443（域名）：上行 900 B，下行 4 KiB，用时 3 秒",
		"steam.exe → 23.1.2.3:443 TCP，加速",
		"经节点的连接 1 条（域名 1、IPv4 地址 0、IPv6 地址 0），其中 0 条没有收到回应",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("log lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "steam.exe → 23.1.2.3:443 TCP，直连") {
		t.Fatalf("steam's connection before the switch was recorded:\n%s", got)
	}
}
