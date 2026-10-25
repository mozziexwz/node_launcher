// Package netenv describes this computer's network for support cases: the
// Windows version and, for each adapter that is up, its kind, addresses,
// gateways and DNS servers. Public addresses of the computer are shortened,
// so the lines show what kind of network it is without identifying anyone.
package netenv

import (
	"fmt"
	"net"
	"strings"
)

// Adapter is one network adapter that is up.
type Adapter struct {
	Name        string // the name Windows shows, e.g. "WLAN"
	Description string // usually the hardware or driver, e.g. "Intel(R) Wi-Fi 6 AX201"
	Kind        string // "Wi-Fi", "以太网", "移动网络", "虚拟网卡", ...
	Addresses   []net.IP
	Gateways    []net.IP
	DNS         []net.IP
}

// Snapshot is the network as seen at one moment.
type Snapshot struct {
	Windows  string
	Adapters []Adapter
}

// Lines formats the adapters, one line each.
func (s Snapshot) Lines() []string {
	lines := make([]string, 0, len(s.Adapters))
	for _, a := range s.Adapters {
		lines = append(lines, a.Line())
	}
	return lines
}

// Line formats the adapter, for example
// "网卡 WLAN（Wi-Fi，Intel(R) Wi-Fi 6 AX201）：IPv4 192.168.43.25；IPv6 2409:8a1c:…（公网）；网关 192.168.43.1；DNS 192.168.43.1".
func (a Adapter) Line() string {
	var v4, v6 []string
	seen := map[string]bool{}
	for _, ip := range a.Addresses {
		s := Shorten(ip)
		if seen[s] {
			continue // e.g. a temporary IPv6 address next to the permanent one
		}
		seen[s] = true
		if ip.To4() != nil {
			v4 = append(v4, s)
		} else {
			v6 = append(v6, s)
		}
	}
	parts := []string{"IPv4 " + listOrNone(v4), "IPv6 " + listOrNone(v6)}
	if len(a.Gateways) > 0 {
		parts = append(parts, "网关 "+joinIPs(a.Gateways, Shorten))
	}
	if len(a.DNS) > 0 {
		parts = append(parts, "DNS "+joinIPs(a.DNS, net.IP.String))
	}
	kind := a.Kind
	if a.Description != "" {
		kind += "，" + a.Description
	}
	return fmt.Sprintf("网卡 %s（%s）：%s", a.Name, kind, strings.Join(parts, "；"))
}

// Shorten shows private and other local addresses in full, and only the
// network part of a public address: the first three parts of IPv4, the
// first two groups of IPv6.
func Shorten(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		if isLocalIPv4(v4) {
			return v4.String()
		}
		return fmt.Sprintf("%d.%d.%d.*（公网）", v4[0], v4[1], v4[2])
	}
	if ip.IsGlobalUnicast() && !isUniqueLocal(ip) {
		return fmt.Sprintf("%x:%x:…（公网）", uint16(ip[0])<<8|uint16(ip[1]), uint16(ip[2])<<8|uint16(ip[3]))
	}
	return ip.String()
}

// isLocalIPv4: private, carrier-grade NAT, link-local and loopback ranges.
func isLocalIPv4(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip[0] == 100 && ip[1]&0xc0 == 64 // 100.64.0.0/10
}

// isUniqueLocal: fc00::/7, the IPv6 counterpart of private addresses.
func isUniqueLocal(ip net.IP) bool { return len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc }

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "无"
	}
	return strings.Join(items, "、")
}

func joinIPs(ips []net.IP, show func(net.IP) string) string {
	items := make([]string, len(ips))
	for i, ip := range ips {
		items[i] = show(ip)
	}
	return strings.Join(items, "、")
}

// Interface types from ipifcons.h.
const (
	ifTypeEthernet    = 6
	ifTypePPP         = 23
	ifTypePropVirtual = 53
	ifTypeWiFi        = 71
	ifTypeTunnel      = 131
	ifTypeWWAN        = 243
	ifTypeWWAN2       = 244
)

// Kind names an interface type.
func Kind(ifType uint32) string {
	switch ifType {
	case ifTypeWiFi:
		return "Wi-Fi"
	case ifTypeEthernet:
		return "以太网"
	case ifTypeWWAN, ifTypeWWAN2:
		return "移动网络"
	case ifTypePropVirtual:
		return "虚拟网卡"
	case ifTypeTunnel:
		return "隧道"
	case ifTypePPP:
		return "拨号"
	}
	return fmt.Sprintf("其他（类型 %d）", ifType)
}

// WindowsName names a Windows version from its version numbers.
func WindowsName(major, minor, build uint32) string {
	name := fmt.Sprintf("Windows %d.%d", major, minor)
	if major == 10 && minor == 0 {
		name = "Windows 10"
		if build >= 22000 {
			name = "Windows 11"
		}
	}
	return fmt.Sprintf("%s（版本 %d.%d.%d）", name, major, minor, build)
}
