package app

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mozziexwz/node_launcher/internal/engine"
	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/probe"
	"github.com/mozziexwz/node_launcher/internal/profiles"
	"github.com/mozziexwz/node_launcher/internal/sysproxy"
)

// Connection records for support cases, which are solved from exported logs.
// While accelerating, the log shows the connections the listed programs
// opened and what the redirector did with each (accelerated, direct), and
// what the node was asked for and whether anything came back. Each program
// and target is written once, up to a limit per session, and a summary
// follows when acceleration stops. Other programs' connections are never
// written.

// ConnEvent is a decision the redirector reports for a new connection.
type ConnEvent struct {
	Process string // image name, e.g. "nexon_launcher.exe"
	DestIP  string
	Port    uint16
	Info    string // e.g. "Proxy SOCKS5://127.0.0.1:52798 (UDP)", "Direct"
}

// Per-session limits on detailed lines; the summary still counts the rest.
const (
	maxDecisionLines = 200
	maxRequestLines  = 200
)

type connStats struct {
	accelerated, direct, blocked, ipv6Targets int // distinct program/target decisions
	requests, byName, byIPv4, byIPv6          int // node requests
	unanswered, unrecognized                  int
	unloggedDecisions, unloggedRequests       int
}

type connDiag struct {
	log *logx.Logger

	mu        sync.Mutex
	closed    bool
	exact     map[string]bool // lower-case image names of the accelerated programs
	patterned map[string]bool // image names of path patterns, e.g. cefhelper.exe
	proxies   map[int]string  // local proxy port -> what it stands for
	decisions map[string]bool
	targets   map[string]bool
	decLines  int
	reqLines  int
	stats     connStats
}

func newConnDiag(log *logx.Logger) *connDiag {
	return &connDiag{
		log:       log,
		exact:     map[string]bool{},
		patterned: map[string]bool{},
		proxies:   map[int]string{},
		decisions: map[string]bool{},
		targets:   map[string]bool{},
	}
}

// setPrograms takes the ";"-separated list of accelerated programs. Path
// patterns such as *\ToyWebView\cefhelper.exe are known only by their image
// name here, so for them only accelerated connections are written.
func (d *connDiag) setPrograms(list string) {
	exact, patterned := map[string]bool{}, map[string]bool{}
	for _, p := range strings.Split(list, ";") {
		name := strings.ToLower(strings.TrimSpace(p))
		if name == "" {
			continue
		}
		if strings.ContainsAny(name, `*\/`) {
			if i := strings.LastIndexAny(name, `\/`); i >= 0 {
				name = name[i+1:]
			}
			if !strings.Contains(name, "*") {
				patterned[name] = true
			}
			continue
		}
		exact[name] = true
	}
	d.mu.Lock()
	d.exact, d.patterned = exact, patterned
	d.mu.Unlock()
}

// setProxy names a local proxy port that decisions may point at.
func (d *connDiag) setProxy(port int, name string) {
	d.mu.Lock()
	d.proxies[port] = name
	d.mu.Unlock()
}

// decision records a redirector decision for an accelerated program.
func (d *connDiag) decision(ev ConnEvent) {
	name := strings.ToLower(ev.Process)
	accelerated := strings.HasPrefix(ev.Info, "Proxy")
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || !(d.exact[name] || accelerated && d.patterned[name]) {
		return
	}
	key := name + "|" + ev.DestIP + "|" + strconv.Itoa(int(ev.Port)) + "|" + ev.Info
	if d.decisions[key] {
		return
	}
	d.decisions[key] = true

	verdict, udp := describeDecision(ev.Info, d.proxies)
	switch {
	case accelerated:
		d.stats.accelerated++
	case strings.HasPrefix(ev.Info, "Direct"):
		d.stats.direct++
	default:
		d.stats.blocked++
	}
	ip := net.ParseIP(ev.DestIP)
	v6 := ip != nil && ip.To4() == nil
	if v6 {
		d.stats.ipv6Targets++
	}
	if d.decLines >= maxDecisionLines {
		d.stats.unloggedDecisions++
		return
	}
	d.decLines++
	proto := "TCP"
	if udp {
		proto = "UDP"
	}
	if v6 {
		proto += " IPv6"
	}
	d.log.Printf(logx.Conn, "%s → %s %s，%s", ev.Process,
		net.JoinHostPort(ev.DestIP, strconv.Itoa(int(ev.Port))), proto, verdict)
}

// request records a connection the node was asked for.
func (d *connDiag) request(r engine.Request) {
	if r.Command == "UDP" || r.Command == "BIND" || isCheckTarget(r) {
		return // the UDP channel itself, and the network check's own probes
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	if r.Command == "" {
		d.stats.unrecognized++
		return
	}
	d.stats.requests++
	kind := "域名"
	switch {
	case r.Domain:
		d.stats.byName++
	case r.IPv6():
		d.stats.byIPv6++
		kind = "IPv6 地址，未拿到域名"
	default:
		d.stats.byIPv4++
		kind = "IPv4 地址，未拿到域名"
	}
	unanswered := r.Up > 0 && r.Down == 0
	if unanswered {
		d.stats.unanswered++
	}
	key := r.Target()
	if unanswered {
		key += " unanswered"
	}
	if d.targets[key] {
		return
	}
	d.targets[key] = true
	if d.reqLines >= maxRequestLines {
		d.stats.unloggedRequests++
		return
	}
	d.reqLines++
	note := ""
	switch {
	case unanswered:
		note = "，没有收到任何回应"
	case r.Up == 0 && r.Down == 0:
		note = "，没有传输数据"
	}
	d.log.Printf(logx.Conn, "经节点 %s（%s）：上行 %s，下行 %s，用时 %s%s",
		r.Target(), kind, FormatBytes(r.Up), FormatBytes(r.Down), formatElapsed(r.Elapsed), note)
}

// finish writes the session summary; later records are ignored.
func (d *connDiag) finish() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	s := d.stats
	d.log.Printf(logx.Conn, "本次统计：名单内程序访问的目标 %d 个（加速 %d、直连 %d、拦截 %d，其中 IPv6 %d 个）；经节点的连接 %d 条（域名 %d、IPv4 地址 %d、IPv6 地址 %d），其中 %d 条没有收到回应",
		s.accelerated+s.direct+s.blocked, s.accelerated, s.direct, s.blocked, s.ipv6Targets,
		s.requests, s.byName, s.byIPv4, s.byIPv6, s.unanswered)
	if n := s.unloggedDecisions + s.unloggedRequests; n > 0 {
		d.log.Printf(logx.Conn, "另有 %d 条记录超过每次加速的上限，未逐条写出", n)
	}
	if s.unrecognized > 0 {
		d.log.Printf(logx.Conn, "%d 条连接没有完成 SOCKS5 握手", s.unrecognized)
	}
}

// describeDecision turns a redirector decision into words, naming the local
// proxy it points at when known, and reports whether it is UDP.
func describeDecision(info string, proxies map[int]string) (verdict string, udp bool) {
	udp = strings.HasSuffix(info, "(UDP)")
	switch {
	case strings.HasPrefix(info, "Proxy"):
		verdict = "加速"
		if i := strings.LastIndexByte(info, ':'); i >= 0 {
			port := info[i+1:]
			if j := strings.IndexByte(port, ' '); j >= 0 {
				port = port[:j]
			}
			if p, err := strconv.Atoi(port); err == nil && proxies[p] != "" {
				verdict = proxies[p]
			}
		}
	case strings.HasPrefix(info, "Direct"):
		verdict = "直连"
	case strings.HasPrefix(info, "Blocked"):
		verdict = "拦截"
	default:
		verdict = info
	}
	return verdict, udp
}

// exitIPHost is the host the network check asks for the exit IP.
var exitIPHost = func() string {
	u, err := url.Parse(probe.ExitIPURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}()

// isCheckTarget reports the network check's own requests.
func isCheckTarget(r engine.Request) bool {
	return r.Host == "127.0.0.1" && r.Port == probe.FullPathProbePort ||
		exitIPHost != "" && r.Host == exitIPHost
}

func formatElapsed(d time.Duration) string {
	switch {
	case d < time.Second:
		return "不到 1 秒"
	case d < time.Minute:
		return fmt.Sprintf("%d 秒", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%d 分 %d 秒", int(d/time.Minute), int(d%time.Minute/time.Second))
	default:
		return fmt.Sprintf("%d 小时 %d 分", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
}

// systemProxyText describes the user's proxy settings as browsers see them.
func systemProxyText(s sysproxy.Settings) string {
	var parts []string
	if s.Enabled && s.Server != "" {
		parts = append(parts, "系统代理 "+s.Server)
	}
	if s.PACURL != "" {
		parts = append(parts, "自动配置脚本 "+s.PACURL)
	}
	if len(parts) == 0 {
		return "系统代理 关"
	}
	return strings.Join(parts, "，")
}

// RelatedProcesses picks the running programs that belong to the game: those
// on the acceleration list and any other whose name mentions Nexon or Maple,
// which would show a launcher update that renamed its programs. Names not on
// the list are marked.
func RelatedProcesses(running []string) []string {
	listed := map[string]bool{}
	for _, p := range profiles.DefaultMapleStory().Processes {
		listed[strings.ToLower(p)] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, name := range running {
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		switch {
		case listed[key]:
			out = append(out, name)
		case strings.Contains(key, "nexon") || strings.Contains(key, "maple"):
			out = append(out, name+"（不在加速名单）")
		}
	}
	return out
}
