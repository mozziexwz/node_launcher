// Package sysproxy lets the launcher take over a game's requests to the
// Windows system proxy.
//
// Apps such as v2rayN and the Clash family point the system proxy at a local
// port. The game's launcher follows that setting and sends its requests to
// the port, so they would leave through the other app's node; the region
// check then fails. The launcher finds the system proxy here, has the
// redirector divert the game's connections to it, and Server answers them in
// its place, sending the real requests through MSBOOST.
package sysproxy

import (
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Endpoint is an address the system proxy settings point applications at.
type Endpoint struct {
	IP   net.IP
	Port uint16
}

func (e Endpoint) String() string { return net.JoinHostPort(e.IP.String(), strconv.Itoa(int(e.Port))) }

// Loopback reports whether the endpoint is on this machine.
func (e Endpoint) Loopback() bool { return e.IP.IsLoopback() }

// Settings are the proxy values of the current user's Internet settings.
type Settings struct {
	Enabled bool   // ProxyEnable
	Server  string // ProxyServer, e.g. "127.0.0.1:10808" or "http=...;socks=..."
	PACURL  string // AutoConfigURL
}

// Endpoints resolves settings (and the PAC script, if any) to endpoints.
// Host names are resolved with lookup; the result is sorted and unique.
func Endpoints(s Settings, pac string, lookup func(host string) []net.IP) []Endpoint {
	var hosts []hostPort
	if s.Enabled {
		hosts = append(hosts, parseProxyServer(s.Server)...)
	}
	hosts = append(hosts, parsePAC(pac)...)
	seen := map[string]bool{}
	var out []Endpoint
	for _, hp := range hosts {
		ips := []net.IP{net.ParseIP(hp.host)}
		if ips[0] == nil {
			ips = lookup(hp.host)
		}
		for _, ip := range ips {
			if ip == nil || ip.IsUnspecified() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				ip = v4
			}
			e := Endpoint{IP: ip, Port: hp.port}
			if !seen[e.String()] {
				seen[e.String()] = true
				out = append(out, e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

type hostPort struct {
	host string
	port uint16
}

// parseProxyServer reads ProxyServer: either "host:port" for every protocol
// or "scheme=host:port;..." per protocol. Some apps add "http://".
func parseProxyServer(s string) []hostPort {
	var out []hostPort
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ' ' }) {
		if _, value, ok := strings.Cut(part, "="); ok {
			part = value
		}
		if hp, ok := splitHostPort(part); ok {
			out = append(out, hp)
		}
	}
	return out
}

// pacProxy finds proxy entries in a PAC script, e.g. "PROXY 127.0.0.1:7890"
// or "SOCKS5 127.0.0.1:7891".
var pacProxy = regexp.MustCompile(`(?i)\b(?:PROXY|HTTPS?|SOCKS[45]?)\s+([^\s;"',)]+)`)

func parsePAC(script string) []hostPort {
	var out []hostPort
	for _, m := range pacProxy.FindAllStringSubmatch(script, -1) {
		if hp, ok := splitHostPort(m[1]); ok {
			out = append(out, hp)
		}
	}
	return out
}

func splitHostPort(s string) (hostPort, bool) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimSuffix(s, "/")
	host, port, err := net.SplitHostPort(s)
	if err != nil || host == "" {
		return hostPort{}, false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return hostPort{}, false
	}
	return hostPort{host: host, port: uint16(n)}, true
}

// LoopbackPorts lists the ports of loopback endpoints; the redirector only
// proxies those with patch P4. Remote endpoints are ordinary rule targets.
func LoopbackPorts(eps []Endpoint) (ports []uint16) {
	seen := map[uint16]bool{}
	for _, e := range eps {
		if e.Loopback() && !seen[e.Port] {
			seen[e.Port] = true
			ports = append(ports, e.Port)
		}
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	return ports
}

// RuleTargets formats endpoints as the redirector's host and port lists. A
// rule matches every host with every port; with several endpoints that may
// cover a few extra combinations, which is harmless: loopback ports outside
// LoopbackPorts stay direct anyway.
func RuleTargets(eps []Endpoint) (hosts, ports string) {
	var hs, ps []string
	seenH, seenP := map[string]bool{}, map[uint16]bool{}
	for _, e := range eps {
		if h := e.IP.String(); !seenH[h] {
			seenH[h] = true
			hs = append(hs, h)
		}
		if !seenP[e.Port] {
			seenP[e.Port] = true
			ps = append(ps, strconv.Itoa(int(e.Port)))
		}
	}
	return strings.Join(hs, ";"), strings.Join(ps, ";")
}

// Equal reports whether two sorted endpoint lists are the same.
func Equal(a, b []Endpoint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].String() != b[i].String() {
			return false
		}
	}
	return true
}
