package engine

import (
	"encoding/json"
	"testing"

	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
)

// withUDP adds what a node that serves UDP writes: a UDP binding on the TCP
// port, right after the TCP one.
func withUDP(t *testing.T, raw []byte) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	server := m["profiles"].([]any)[0].(map[string]any)["servers"].([]any)[0].(map[string]any)
	bindings := server["portBindings"].([]any)
	tcp := bindings[0].(map[string]any)
	server["portBindings"] = append(bindings, map[string]any{"port": tcp["port"], "protocol": "UDP"})
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// mieru picks a random binding for every underlying connection, so the core
// must run on the chosen transport's binding alone.
func TestForTransportKeepsOneTransport(t *testing.T) {
	cfg, err := ParseConfig(withUDP(t, loadExample(t)))
	if err != nil {
		t.Fatal(err)
	}
	if tcp, udp := cfg.Transports(); !tcp || !udp {
		t.Fatalf("transports tcp=%v udp=%v, want both", tcp, udp)
	}
	for _, c := range []struct {
		transport string
		want      appctlpb.TransportProtocol
	}{{TransportTCP, appctlpb.TransportProtocol_TCP}, {TransportUDP, appctlpb.TransportProtocol_UDP}, {"", appctlpb.TransportProtocol_TCP}} {
		run, err := cfg.ForTransport(c.transport)
		if err != nil {
			t.Fatal(err)
		}
		bindings := run.Profile.GetServers()[0].GetPortBindings()
		if len(bindings) != 1 || bindings[0].GetProtocol() != c.want || bindings[0].GetPort() != 8964 {
			t.Fatalf("%q kept %v", c.transport, bindings)
		}
		if run.Profile.GetUser().GetName() != "test-user" || run.Profile.GetMtu() != 1400 {
			t.Fatalf("%q lost profile settings", c.transport)
		}
	}
	if len(cfg.Profile.GetServers()[0].GetPortBindings()) != 2 {
		t.Fatal("ForTransport changed the loaded configuration")
	}

	// The embedded mieru accepts a UDP-only profile and serves SOCKS5 on it.
	udp, err := cfg.ForTransport(TransportUDP)
	if err != nil {
		t.Fatal(err)
	}
	e, err := Start(udp, 0)
	if err != nil {
		t.Fatalf("UDP core: %v", err)
	}
	e.Close()

	tcpOnly, err := ParseConfig(loadExample(t))
	if err != nil {
		t.Fatal(err)
	}
	if tcp, udp := tcpOnly.Transports(); !tcp || udp {
		t.Fatalf("TCP-only configuration: tcp=%v udp=%v", tcp, udp)
	}
	if _, err := tcpOnly.ForTransport(TransportUDP); err == nil {
		t.Fatal("UDP was taken from a configuration without a UDP binding")
	}
}
