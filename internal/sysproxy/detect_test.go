package sysproxy

import (
	"net"
	"strings"
	"testing"
)

func fakeLookup(host string) []net.IP {
	switch host {
	case "localhost":
		return []net.IP{net.ParseIP("::1"), net.ParseIP("127.0.0.1")}
	case "router.lan":
		return []net.IP{net.ParseIP("192.168.1.2")}
	}
	return nil
}

func endpointsString(eps []Endpoint) string {
	var s []string
	for _, e := range eps {
		s = append(s, e.String())
	}
	return strings.Join(s, " ")
}

func TestEndpointsFromSettings(t *testing.T) {
	for _, c := range []struct {
		name string
		s    Settings
		pac  string
		want string
	}{
		{"v2rayN", Settings{Enabled: true, Server: "127.0.0.1:10808"}, "", "127.0.0.1:10808"},
		{"disabled", Settings{Enabled: false, Server: "127.0.0.1:10808"}, "", ""},
		{"per protocol", Settings{Enabled: true, Server: "http=127.0.0.1:7890;https=127.0.0.1:7890;socks=127.0.0.1:7891"}, "", "127.0.0.1:7890 127.0.0.1:7891"},
		{"scheme prefix", Settings{Enabled: true, Server: "http://127.0.0.1:7897"}, "", "127.0.0.1:7897"},
		{"localhost", Settings{Enabled: true, Server: "localhost:2080"}, "", "127.0.0.1:2080 [::1]:2080"},
		{"ipv6", Settings{Enabled: true, Server: "[::1]:7890"}, "", "[::1]:7890"},
		{"LAN proxy", Settings{Enabled: true, Server: "router.lan:7890"}, "", "192.168.1.2:7890"},
		{"garbage", Settings{Enabled: true, Server: "nonsense;127.0.0.1;:0;127.0.0.1:99999"}, "", ""},
		{"PAC", Settings{PACURL: "http://127.0.0.1:7890/pac"},
			`function FindProxyForURL(url, host) {
  if (isPlainHostName(host)) return "DIRECT";
  return "PROXY 127.0.0.1:7890; SOCKS5 127.0.0.1:7891; DIRECT";
}`, "127.0.0.1:7890 127.0.0.1:7891"},
		{"PAC and server", Settings{Enabled: true, Server: "127.0.0.1:7890", PACURL: "x"}, `return 'HTTPS localhost:7890'`, "127.0.0.1:7890 [::1]:7890"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := endpointsString(Endpoints(c.s, c.pac, fakeLookup)); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestRuleTargetsAndLoopbackPorts(t *testing.T) {
	eps := Endpoints(Settings{Enabled: true, Server: "http=127.0.0.1:7890;socks=localhost:7891;https=router.lan:8080"}, "", fakeLookup)
	hosts, ports := RuleTargets(eps)
	if hosts != "127.0.0.1;192.168.1.2;::1" || ports != "7890;7891;8080" {
		t.Fatalf("hosts %q ports %q", hosts, ports)
	}
	if got := LoopbackPorts(eps); len(got) != 2 || got[0] != 7890 || got[1] != 7891 {
		t.Fatalf("loopback ports %v", got)
	}
	if !Equal(eps, Endpoints(Settings{Enabled: true, Server: "https=router.lan:8080;socks=localhost:7891;http=127.0.0.1:7890"}, "", fakeLookup)) {
		t.Fatal("the same endpoints in another order must be equal")
	}
	if Equal(eps, eps[:1]) {
		t.Fatal("different lists must not be equal")
	}
}
