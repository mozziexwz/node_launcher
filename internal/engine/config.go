// Package engine embeds the mieru client. It loads a mieru client JSON
// configuration, builds the client multiplexer and serves a loopback-only
// SOCKS5 endpoint protected by per-session credentials. The wiring mirrors
// "mieru run", so proxied traffic takes the same code path as the standalone
// MSBOOST.exe client.
package engine

import (
	"fmt"
	"os"

	"github.com/enfein/mieru/v3/pkg/appctl"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"github.com/enfein/mieru/v3/pkg/common"
	"google.golang.org/protobuf/proto"
)

// Transports between the core and the node. A node that serves UDP lists a
// UDP binding next to its TCP one, on the same port.
const (
	TransportTCP = "tcp"
	TransportUDP = "udp"
)

// Config is a validated mieru client configuration and its active profile.
type Config struct {
	Client  *appctlpb.ClientConfig
	Profile *appctlpb.ClientProfile
}

// LoadConfigFile reads and validates a mieru client JSON file, the same
// format accepted by "mieru replace config".
func LoadConfigFile(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败：%w", err)
	}
	return ParseConfig(b)
}

// ParseConfig validates a complete mieru client JSON configuration with the
// same rules mieru applies before replacing its stored configuration.
// Fields the launcher manages itself (socks5Port, socks5Authentication,
// httpProxyPort, rpcPort) are still validated but ignored at runtime.
func ParseConfig(b []byte) (*Config, error) {
	client := &appctlpb.ClientConfig{}
	if err := common.UnmarshalJSON(b, client); err != nil {
		return nil, fmt.Errorf("不是有效的 MSBOOST 节点配置：%w", err)
	}
	if err := appctl.ValidateFullClientConfig(client); err != nil {
		return nil, fmt.Errorf("配置校验失败：%w", err)
	}
	profile, err := appctl.GetActiveProfileFromConfig(client, client.GetActiveProfile())
	if err != nil {
		return nil, fmt.Errorf("找不到当前使用的配置方案：%w", err)
	}
	return &Config{Client: client, Profile: profile}, nil
}

// Transports reports which transports the active profile has bindings for.
func (c *Config) Transports() (tcp, udp bool) {
	for _, s := range c.Profile.GetServers() {
		for _, b := range s.GetPortBindings() {
			switch b.GetProtocol() {
			case appctlpb.TransportProtocol_TCP:
				tcp = true
			case appctlpb.TransportProtocol_UDP:
				udp = true
			}
		}
	}
	return tcp, udp
}

// ForTransport returns a copy whose active profile keeps only the bindings
// of one transport. mieru picks a random binding for every underlying
// connection, so a profile with both would mix them. c is not changed.
func (c *Config) ForTransport(transport string) (*Config, error) {
	want := appctlpb.TransportProtocol_TCP
	if transport == TransportUDP {
		want = appctlpb.TransportProtocol_UDP
	}
	profile := proto.Clone(c.Profile).(*appctlpb.ClientProfile)
	var servers []*appctlpb.ServerEndpoint
	for _, s := range profile.GetServers() {
		var kept []*appctlpb.PortBinding
		for _, b := range s.GetPortBindings() {
			if b.GetProtocol() == want {
				kept = append(kept, b)
			}
		}
		if len(kept) > 0 {
			s.PortBindings = kept
			servers = append(servers, s)
		}
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("节点配置里没有 %s 端口", want)
	}
	profile.Servers = servers
	return &Config{Client: c.Client, Profile: profile}, nil
}
