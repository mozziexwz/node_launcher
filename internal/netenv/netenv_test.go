package netenv

import (
	"net"
	"runtime"
	"strings"
	"testing"
)

func TestShorten(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.43.25":             "192.168.43.25",
		"10.1.2.3":                  "10.1.2.3",
		"172.20.10.2":               "172.20.10.2",
		"100.64.12.7":               "100.64.12.7", // carrier-grade NAT
		"169.254.1.1":               "169.254.1.1",
		"203.0.113.77":              "203.0.113.*（公网）",
		"2409:8a1c:6c20:1::1234":    "2409:8a1c:…（公网）",
		"240e:3b0:1234:5678::9":     "240e:3b0:…（公网）",
		"fd00:abcd::1":              "fd00:abcd::1", // unique local
		"fe80::1c2d:3e4f:5a6b:7c8d": "fe80::1c2d:3e4f:5a6b:7c8d",
	} {
		if got := Shorten(net.ParseIP(in)); got != want {
			t.Fatalf("Shorten(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestAdapterLine(t *testing.T) {
	a := Adapter{
		Name: "WLAN", Description: "Intel(R) Wi-Fi 6 AX201 160MHz", Kind: "Wi-Fi",
		Addresses: []net.IP{net.ParseIP("192.168.43.25"), net.ParseIP("2409:8a1c:6c20:1::1234"), net.ParseIP("2409:8a1c:6c20:1:a:b:c:d")},
		Gateways:  []net.IP{net.ParseIP("192.168.43.1"), net.ParseIP("fe80::1")},
		DNS:       []net.IP{net.ParseIP("192.168.43.1"), net.ParseIP("2409:8088::a")},
	}
	want := "网卡 WLAN（Wi-Fi，Intel(R) Wi-Fi 6 AX201 160MHz）：IPv4 192.168.43.25；IPv6 2409:8a1c:…（公网）；网关 192.168.43.1、fe80::1；DNS 192.168.43.1、2409:8088::a"
	if got := a.Line(); got != want {
		t.Fatalf("Line:\n%s\nwant:\n%s", got, want)
	}
	bare := Adapter{Name: "以太网 2", Kind: "虚拟网卡"}
	if got := bare.Line(); got != "网卡 以太网 2（虚拟网卡）：IPv4 无；IPv6 无" {
		t.Fatalf("Line = %q", got)
	}
}

func TestDNSServers(t *testing.T) {
	s := Snapshot{Adapters: []Adapter{
		{Name: "以太网", DNS: []net.IP{net.ParseIP("192.168.11.1"), net.ParseIP("2409:8088::a")}},
		{Name: "VMnet8"},
		{Name: "WLAN", DNS: []net.IP{net.ParseIP("192.168.11.1"), net.ParseIP("223.5.5.5")}},
	}}
	if got := strings.Join(s.DNSServers(), " "); got != "192.168.11.1 2409:8088::a 223.5.5.5" {
		t.Fatalf("DNSServers = %q", got)
	}
	if got := (Snapshot{}).DNSServers(); got != nil {
		t.Fatalf("no adapters: %q", got)
	}
}

func TestKindAndWindowsName(t *testing.T) {
	for ifType, want := range map[uint32]string{71: "Wi-Fi", 6: "以太网", 243: "移动网络", 53: "虚拟网卡", 131: "隧道", 23: "拨号", 9: "其他（类型 9）"} {
		if got := Kind(ifType); got != want {
			t.Fatalf("Kind(%d) = %q, want %q", ifType, got, want)
		}
	}
	for _, c := range []struct {
		major, minor, build uint32
		want                string
	}{
		{10, 0, 19045, "Windows 10（版本 10.0.19045）"},
		{10, 0, 22631, "Windows 11（版本 10.0.22631）"},
		{6, 3, 9600, "Windows 6.3（版本 6.3.9600）"},
	} {
		if got := WindowsName(c.major, c.minor, c.build); got != c.want {
			t.Fatalf("WindowsName = %q, want %q", got, c.want)
		}
	}
}

func TestCollectOnThisComputer(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	s, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Windows, "Windows ") {
		t.Fatalf("Windows = %q", s.Windows)
	}
	for _, line := range s.Lines() {
		t.Log(line)
		if !strings.HasPrefix(line, "网卡 ") {
			t.Fatalf("line %q", line)
		}
	}
}
