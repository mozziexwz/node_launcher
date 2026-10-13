package probe

import (
	"net"
	"testing"
	"time"

	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"google.golang.org/protobuf/proto"
)

func binding(port int32, protocol appctlpb.TransportProtocol, portRange string) *appctlpb.PortBinding {
	b := &appctlpb.PortBinding{Protocol: protocol.Enum()}
	if port != 0 {
		b.Port = proto.Int32(port)
	}
	if portRange != "" {
		b.PortRange = proto.String(portRange)
	}
	return b
}

func server(ip, domain string, bindings ...*appctlpb.PortBinding) *appctlpb.ServerEndpoint {
	return &appctlpb.ServerEndpoint{IpAddress: proto.String(ip), DomainName: proto.String(domain), PortBindings: bindings}
}

func TestRelayTargetFromProfile(t *testing.T) {
	tcp := appctlpb.TransportProtocol_TCP
	udp := appctlpb.TransportProtocol_UDP
	cases := []struct {
		name    string
		servers []*appctlpb.ServerEndpoint
		want    RelayTarget
	}{
		{"single tcp", []*appctlpb.ServerEndpoint{server("192.0.2.10", "", binding(8964, tcp, ""))},
			RelayTarget{Address: "192.0.2.10", Port: 8964}},
		{"domain preferred", []*appctlpb.ServerEndpoint{server("192.0.2.10", " node.example ", binding(8964, tcp, ""))},
			RelayTarget{Address: "node.example", Port: 8964}},
		{"udp only", []*appctlpb.ServerEndpoint{server("192.0.2.10", "", binding(8964, udp, ""))},
			RelayTarget{Err: "无 TCP 测试端口"}},
		{"port range", []*appctlpb.ServerEndpoint{server("192.0.2.10", "", binding(0, tcp, "2012-2022"))},
			RelayTarget{Err: "无法唯一确定测试目标"}},
		{"two tcp bindings", []*appctlpb.ServerEndpoint{server("192.0.2.10", "", binding(1, tcp, ""), binding(2, tcp, ""))},
			RelayTarget{Err: "无法唯一确定测试目标"}},
		{"two servers", []*appctlpb.ServerEndpoint{server("192.0.2.10", "", binding(1, tcp, "")), server("192.0.2.20", "", binding(2, udp, ""))},
			RelayTarget{Err: "无法唯一确定测试目标"}},
		{"no servers", nil, RelayTarget{Err: "无法唯一确定测试目标"}},
	}
	for _, c := range cases {
		got := RelayTargetFromProfile(&appctlpb.ClientProfile{Servers: c.servers})
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestResolveRelayLiterals(t *testing.T) {
	if ip, reason := ResolveRelay("192.0.2.10", time.Second); ip != "192.0.2.10" || reason != "" {
		t.Errorf("IPv4 literal: %q %q", ip, reason)
	}
	for _, addr := range []string{"2001:db8::1", "::ffff:192.0.2.10"} {
		if _, reason := ResolveRelay(addr, time.Second); reason != "当前节点测速仅支持 IPv4" {
			t.Errorf("%s: reason %q", addr, reason)
		}
	}
}

func TestEntranceLatencyLocal(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	r := EntranceLatency("127.0.0.1", port, 3, 2*time.Second)
	if r.SuccessCount != 3 {
		t.Fatalf("result = %+v", r)
	}
	ln.Close()
	r = EntranceLatency("127.0.0.1", port, 1, 2*time.Second)
	if r.SuccessCount != 0 || r.Errors[0] != "目标拒绝连接" {
		t.Fatalf("closed port: %+v", r)
	}
}
