// Package profiles holds game profiles: which processes to accelerate, over
// which protocols, and which game endpoint the full-path probe targets.
package profiles

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Protocol selects which traffic of the matched processes is proxied.
type Protocol string

const (
	ProtocolTCP  Protocol = "TCP"
	ProtocolUDP  Protocol = "UDP"
	ProtocolBoth Protocol = "BOTH"
)

// Game describes one accelerated game.
type Game struct {
	Name      string
	Processes []string // image names; wildcards allowed; matched case-insensitively
	Protocol  Protocol
	// SendDomainToProxy lets the node resolve host names it learns from the
	// game's own DNS lookups (socks5h semantics).
	SendDomainToProxy bool
	TestAddress       string // full-path probe target, canonical public IPv4
	TestPort          int
}

// DefaultMapleStory is the process list from the user's ProxyBridge profile
// that passed real MapleStory testing on 2026-10-12 (login, channels, channel
// changes, anti-cheat quiet). Entries are kept verbatim, including mr.dll.
func DefaultMapleStory() Game {
	return Game{
		Name: "MapleStory",
		Processes: []string{
			"MapleStory.exe", "msw.exe",
			"nexon_launcher.exe", "nexon_client.exe", "nexon_runtime.exe", "NexonPlug.exe",
			"nxsteam.exe", "NGM.exe", "NGM64.exe", "NMService.exe", "SIG.exe",
			"BlackCipher64.aes", "BlackCall64.aes", "NGClient64.aes", "NGCLIENT.AES",
			"UnityCrashHandler64.exe", "CrashReportClient.exe",
			"MapleRoyals.exe", "mr.dll", "MapleLegends.exe", "DreamMS.exe",
			"Kaizen v92.exe", "Kaizenv92.exe", "Fantasia.exe",
		},
		Protocol:          ProtocolBoth,
		SendDomainToProxy: true,
		TestAddress:       "35.155.204.207",
		TestPort:          8585,
	}
}

// ProcessList joins the processes into ProxyBridge's semicolon-separated form.
func (g Game) ProcessList() string {
	return strings.Join(g.Processes, ";")
}

// Addon is an optional app accelerated alongside the game. Like the game's,
// its process list is fixed: users can only switch an addon on or off.
type Addon struct {
	ID        string
	Name      string
	Processes []string
	Protocol  Protocol
	Note      string // shown next to the switch
}

// ProcessList joins the processes into ProxyBridge's semicolon-separated form.
func (a Addon) ProcessList() string {
	return strings.Join(a.Processes, ";")
}

// Addons lists the optional accelerations. All are off by default.
func Addons() []Addon {
	return []Addon{
		{
			ID:        "discord",
			Name:      "Discord",
			Processes: []string{"Discord.exe", "DiscordPTB.exe", "DiscordCanary.exe"},
			Protocol:  ProtocolBoth,
			Note:      "语音使用 UDP，一并加速",
		},
		{
			ID:        "steam",
			Name:      "Steam",
			Processes: []string{"steam.exe", "steamwebhelper.exe"},
			Protocol:  ProtocolBoth,
			Note:      "Steam 下载也会经过节点，会消耗节点流量",
		},
	}
}

// Validate checks the profile before it is handed to the redirector.
func (g Game) Validate() error {
	if len(g.Processes) == 0 {
		return errors.New("至少需要一个加速进程")
	}
	for _, p := range g.Processes {
		if err := ValidateProcessName(p); err != nil {
			return err
		}
	}
	switch g.Protocol {
	case ProtocolTCP, ProtocolUDP, ProtocolBoth:
	default:
		return fmt.Errorf("未知协议：%q", g.Protocol)
	}
	ip := net.ParseIP(g.TestAddress)
	if ip == nil || ip.To4() == nil || ip.To4().String() != g.TestAddress || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return fmt.Errorf("测速目标必须是规范的公网 IPv4：%q", g.TestAddress)
	}
	if g.TestPort < 1 || g.TestPort > 65535 {
		return fmt.Errorf("测速端口无效：%d", g.TestPort)
	}
	return nil
}

// ValidateProcessName rejects entries that would change the meaning of the
// semicolon-separated list or match everything.
func ValidateProcessName(name string) error {
	trimmed := strings.TrimSpace(name)
	switch {
	case trimmed == "":
		return errors.New("进程名不能为空")
	case trimmed != name:
		return fmt.Errorf("进程名首尾不能有空格：%q", name)
	case strings.ContainsAny(name, ";\x00"):
		return fmt.Errorf("进程名不能包含分号：%q", name)
	case strings.Trim(name, "*") == "":
		return fmt.Errorf("进程名不能只有通配符：%q", name)
	}
	return nil
}
