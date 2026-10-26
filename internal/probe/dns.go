package probe

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"
)

// PublicDNS are the resolvers this computer's DNS is compared with. Both
// answer in mainland China and abroad; either one answering is enough.
var PublicDNS = []string{"223.5.5.5", "119.29.29.29"}

// DNSTimeout bounds the DNS check. A working resolver answers well within it,
// while Windows keeps a failing lookup going for about 10 s.
const DNSTimeout = 5 * time.Second

// LookupFunc resolves a host name to IPv4 addresses.
type LookupFunc func(ctx context.Context, host string) ([]string, error)

// DNSQuery is one host name to resolve, and what it is for.
type DNSQuery struct {
	Host    string
	Purpose string
}

// DNSAnswer is how one resolver answered.
type DNSAnswer struct {
	Addrs    []string
	Duration time.Duration
	Err      string // why it failed; "" when addresses were found
}

// OK reports whether the host name was resolved.
func (a DNSAnswer) OK() bool { return a.Err == "" }

// Text is "54.68.216.33、52.32.102.166（12 ms）" or the failure reason.
func (a DNSAnswer) Text() string {
	if !a.OK() {
		return a.Err
	}
	return fmt.Sprintf("%s（%s）", strings.Join(a.Addrs, "、"), formatMs(a.Duration))
}

// DNSLookup is one host name as resolved by this computer and by PublicDNS.
type DNSLookup struct {
	DNSQuery
	Local  DNSAnswer
	Public DNSAnswer
}

// DNSResult is whether this computer can resolve the game's host names.
type DNSResult struct {
	Servers []string // this computer's DNS servers, for the texts; may be empty
	Lookups []DNSLookup
}

// CheckDNS resolves every query through both lookups at once, so the whole
// check takes at most DNSTimeout.
func CheckDNS(ctx context.Context, queries []DNSQuery, local, public LookupFunc) DNSResult {
	ctx, cancel := context.WithTimeout(ctx, DNSTimeout)
	defer cancel()
	r := DNSResult{Lookups: make([]DNSLookup, len(queries))}
	var wg sync.WaitGroup
	for i, q := range queries {
		r.Lookups[i].DNSQuery = q
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.Lookups[i].Local = resolve(ctx, local, q.Host)
		}()
		go func() {
			defer wg.Done()
			r.Lookups[i].Public = resolve(ctx, public, q.Host)
		}()
	}
	wg.Wait()
	return r
}

func resolve(ctx context.Context, lookup LookupFunc, host string) DNSAnswer {
	start := time.Now()
	addrs, err := lookup(ctx, host)
	a := DNSAnswer{Addrs: addrs, Duration: time.Since(start)}
	switch {
	case err != nil:
		a.Addrs, a.Err = nil, dnsReason(ctx, err)
	case len(addrs) == 0:
		a.Err = "没有 IPv4 地址"
	}
	return a
}

const notFound = "查不到这个域名"

func dnsReason(ctx context.Context, err error) string {
	var de *net.DNSError
	isDNS := errors.As(err, &de)
	switch {
	case ctx.Err() != nil, errors.Is(err, context.DeadlineExceeded), isDNS && de.IsTimeout:
		return "超时"
	case isDNS && de.IsNotFound:
		return notFound
	case isDNS && de.IsTemporary:
		return "DNS 服务器没有给出结果"
	case isDNS && de.Err != "":
		return "失败（" + de.Err + "）"
	}
	return "失败（" + err.Error() + "）"
}

// SystemLookup resolves the way the game does: through Windows, with its
// hosts file, its DNS cache and the adapters' DNS servers.
func SystemLookup(ctx context.Context, host string) ([]string, error) {
	return lookupIPv4(ctx, net.DefaultResolver, host)
}

// PublicLookup asks every PublicDNS server at once, bypassing this computer's
// DNS servers, and returns the first answer.
func PublicLookup(ctx context.Context, host string) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		addrs []string
		err   error
	}
	answers := make(chan answer, len(PublicDNS))
	for _, server := range PublicDNS {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(server, "53"))
		}}
		go func() {
			addrs, err := lookupIPv4(ctx, r, host)
			answers <- answer{addrs, err}
		}()
	}
	var first error
	for range PublicDNS {
		a := <-answers
		if a.err == nil {
			return a.addrs, nil
		}
		if first == nil {
			first = a.err
		}
	}
	return nil, first
}

func lookupIPv4(ctx context.Context, r *net.Resolver, host string) ([]string, error) {
	addrs, err := r.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.Unmap().String())
	}
	return out, nil
}

// Failed lists the host names this computer could not resolve. A name the
// public resolvers do not know either is left out: Nexon has retired it, which
// is no fault of this computer.
func (r DNSResult) Failed() []DNSLookup {
	var failed []DNSLookup
	for _, l := range r.Lookups {
		retired := l.Local.Err == notFound && l.Public.Err == notFound
		if !l.Local.OK() && !retired {
			failed = append(failed, l)
		}
	}
	return failed
}

// OK reports whether this computer resolved every host name.
func (r DNSResult) OK() bool { return len(r.Failed()) == 0 }

// Compact is "正常（最慢 35 ms）" or "解析失败（4 个中 3 个）".
func (r DNSResult) Compact() string {
	failed := r.Failed()
	if len(failed) > 0 {
		return fmt.Sprintf("解析失败（%d 个中 %d 个）", len(r.Lookups), len(failed))
	}
	var slowest time.Duration
	for _, l := range r.Lookups {
		slowest = max(slowest, l.Local.Duration)
	}
	return fmt.Sprintf("正常（最慢 %s）", formatMs(slowest))
}

// Detail names the failed host names and says whether PublicDNS resolved
// them: "us-sso.nexon.io 超时、x-csauth.ngs.nexon.com 超时；公共 DNS 能解析".
func (r DNSResult) Detail() string {
	failed := r.Failed()
	if len(failed) == 0 {
		return ""
	}
	items := make([]string, len(failed))
	for i, l := range failed {
		items[i] = l.Host + " " + l.Local.Err
	}
	public := "公共 DNS 能解析"
	if !publicResolves(failed) {
		public = "公共 DNS 也解析失败"
	}
	return strings.Join(items, "、") + "；" + public
}

// Advice tells the player what to do when this computer's DNS fails, or ""
// when it works.
func (r DNSResult) Advice() string {
	failed := r.Failed()
	if len(failed) == 0 {
		return ""
	}
	if !publicResolves(failed) {
		return "公共 DNS 也解析不了这些域名：可能是网络没有连通，或者 Nexon 的服务出了问题。"
	}
	local := "本机 DNS"
	if len(r.Servers) > 0 {
		local += "（" + strings.Join(r.Servers, "、") + "）"
	}
	return local + "解析不了游戏域名，公共 DNS 能正常解析。游戏会登录不了，或在游戏中被反作弊断开，加速也帮不上忙。" +
		"请把网卡的 DNS 改为 " + strings.Join(PublicDNS, " 和 ") + "，或者重启路由器。"
}

// Lines describe every lookup, for the support bundle.
func (r DNSResult) Lines() []string {
	servers := "未知"
	if len(r.Servers) > 0 {
		servers = strings.Join(r.Servers, "、")
	}
	lines := []string{fmt.Sprintf("本机 DNS 解析游戏域名：%s；DNS 服务器 %s", r.Compact(), servers)}
	for _, l := range r.Lookups {
		lines = append(lines, fmt.Sprintf("　%s（%s）：本机 %s；公共 DNS %s", l.Host, l.Purpose, l.Local.Text(), l.Public.Text()))
	}
	if advice := r.Advice(); advice != "" {
		lines = append(lines, "　"+advice)
	}
	return lines
}

func publicResolves(lookups []DNSLookup) bool {
	for _, l := range lookups {
		if !l.Public.OK() {
			return false
		}
	}
	return true
}

// formatMs is "35 ms", or "<1 ms" for a cached answer.
func formatMs(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	if ms < 0.5 {
		return "<1 ms"
	}
	return fmt.Sprintf("%d ms", int64(math.Round(ms)))
}
