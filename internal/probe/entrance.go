package probe

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
)

// RelayTarget is the single TCP entrance of the active profile, or the reason
// none could be chosen.
type RelayTarget struct {
	Address string
	Port    int
	Err     string
}

// RelayTargetFromProfile follows the legacy Get-LatencyTargets rules: exactly
// one server with exactly one TCP binding and no port range.
func RelayTargetFromProfile(p *appctlpb.ClientProfile) RelayTarget {
	target := RelayTarget{Err: "无法唯一确定测试目标"}
	if p == nil {
		return target
	}
	var servers []*appctlpb.ServerEndpoint
	for _, s := range p.GetServers() {
		if s != nil {
			servers = append(servers, s)
		}
	}
	var tcp []*appctlpb.PortBinding
	hasUDP := false
	for _, s := range servers {
		for _, b := range s.GetPortBindings() {
			switch {
			case b == nil:
			case b.GetProtocol() == appctlpb.TransportProtocol_TCP:
				tcp = append(tcp, b)
			case b.GetProtocol() == appctlpb.TransportProtocol_UDP:
				hasUDP = true
			}
		}
	}
	if len(tcp) == 0 {
		if hasUDP {
			target.Err = "无 TCP 测试端口"
		}
		return target
	}
	if len(servers) != 1 || len(tcp) != 1 || strings.TrimSpace(tcp[0].GetPortRange()) != "" {
		return target
	}
	port := int(tcp[0].GetPort())
	address := strings.TrimSpace(servers[0].GetDomainName())
	if address == "" {
		address = strings.TrimSpace(servers[0].GetIpAddress())
	}
	if port < 1 || port > 65535 || address == "" {
		return target
	}
	return RelayTarget{Address: address, Port: port}
}

// ResolveRelay returns the entrance as one IPv4 literal or a reason.
func ResolveRelay(address string, timeout time.Duration) (string, string) {
	if ip := net.ParseIP(address); ip != nil {
		if v4 := ip.To4(); v4 != nil && !strings.Contains(address, ":") {
			return v4.String(), ""
		}
		return "", "当前节点测速仅支持 IPv4"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, address)
	if err != nil {
		if ctx.Err() != nil {
			return "", "中转域名解析超时"
		}
		return "", "中转域名解析失败"
	}
	unique := make(map[string]bool)
	for _, a := range addrs {
		if v4 := a.IP.To4(); v4 != nil {
			unique[v4.String()] = true
		}
	}
	switch len(unique) {
	case 0:
		return "", "中转域名未解析到 IPv4 地址"
	case 1:
		for ip := range unique {
			return ip, ""
		}
	}
	return "", "中转域名对应多个 IPv4，无法唯一确定测试目标"
}

// EntranceLatency measures direct TCP connect time to an IPv4 entrance.
func EntranceLatency(ipv4 string, port, count int, timeout time.Duration) Result {
	samples := make([]Sample, 0, count)
	for i := 0; i < count; i++ {
		samples = append(samples, measureTCPConnect(ipv4, port, timeout))
	}
	return NewResult(samples)
}

func measureTCPConnect(ipv4 string, port int, timeout time.Duration) Sample {
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.Dial("tcp4", net.JoinHostPort(ipv4, strconv.Itoa(port)))
	elapsed := time.Since(start)
	if err != nil {
		return Sample{Err: tcpFailureReason(err, timeout)}
	}
	conn.Close()
	if elapsed > timeout {
		return Sample{Err: fmt.Sprintf("连接超时（%d ms）", timeout.Milliseconds())}
	}
	return Sample{OK: true, Duration: elapsed}
}

func tcpFailureReason(err error, timeout time.Duration) string {
	if isTimeout(err) {
		return fmt.Sprintf("连接超时（%d ms）", timeout.Milliseconds())
	}
	if e, ok := errnoOf(err); ok {
		switch {
		case e == wsaeConnRefused:
			return "目标拒绝连接"
		case e == wsaeNetUnreach:
			return "目标网络不可达"
		case e == wsaeHostUnreach:
			return "目标主机不可达"
		case e == wsaeConnReset:
			return "连接被重置"
		}
	}
	return failureReason(err)
}
