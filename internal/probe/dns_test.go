package probe

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

var gameQueries = []DNSQuery{
	{Host: "us-sso.nexon.io", Purpose: "游戏登录"},
	{Host: "x-csauth.ngs.nexon.com", Purpose: "反作弊"},
}

// fakeLookup answers from a table; a missing host fails with err.
func fakeLookup(answers map[string][]string, err error) LookupFunc {
	return func(_ context.Context, host string) ([]string, error) {
		if addrs, ok := answers[host]; ok {
			return addrs, nil
		}
		return nil, err
	}
}

var (
	timeout  = &net.DNSError{Err: "i/o timeout", IsTimeout: true}
	noSuch   = &net.DNSError{Err: "no such host", IsNotFound: true}
	resolved = map[string][]string{
		"us-sso.nexon.io":        {"54.68.216.33", "52.32.102.166"},
		"x-csauth.ngs.nexon.com": {"35.79.38.111"},
	}
)

// The 2026-10-26 case: the router stopped answering foreign names, while
// the public resolvers still did.
func TestCheckDNSLocalFailurePublicWorks(t *testing.T) {
	r := CheckDNS(context.Background(), gameQueries, fakeLookup(nil, timeout), fakeLookup(resolved, timeout))
	r.Servers = []string{"192.168.11.1"}
	if r.OK() || len(r.Failed()) != 2 {
		t.Fatalf("result %+v", r)
	}
	if got := r.Compact(); got != "解析失败（2 个中 2 个）" {
		t.Fatalf("compact %q", got)
	}
	if got := r.Detail(); got != "us-sso.nexon.io 超时、x-csauth.ngs.nexon.com 超时；公共 DNS 能解析" {
		t.Fatalf("detail %q", got)
	}
	advice := r.Advice()
	for _, want := range []string{"本机 DNS（192.168.11.1）解析不了游戏域名", "223.5.5.5 和 119.29.29.29", "重启路由器"} {
		if !strings.Contains(advice, want) {
			t.Fatalf("advice %q lacks %q", advice, want)
		}
	}
	lines := r.Lines()
	if lines[0] != "本机 DNS 解析游戏域名：解析失败（2 个中 2 个）；DNS 服务器 192.168.11.1" {
		t.Fatalf("first line %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "　us-sso.nexon.io（游戏登录）：本机 超时；公共 DNS 54.68.216.33、52.32.102.166（") {
		t.Fatalf("lookup line %q", lines[1])
	}
	if lines[len(lines)-1] != "　"+advice {
		t.Fatalf("last line %q must be the advice", lines[len(lines)-1])
	}
}

// When the public resolvers fail too, changing DNS would not help.
func TestCheckDNSPublicFailsToo(t *testing.T) {
	r := CheckDNS(context.Background(), gameQueries, fakeLookup(nil, timeout), fakeLookup(nil, timeout))
	if got := r.Detail(); !strings.HasSuffix(got, "；公共 DNS 也解析失败") {
		t.Fatalf("detail %q", got)
	}
	if advice := r.Advice(); strings.Contains(advice, "223.5.5.5") || !strings.Contains(advice, "网络没有连通") {
		t.Fatalf("advice %q must not suggest changing DNS", advice)
	}
}

func TestCheckDNSWorking(t *testing.T) {
	r := CheckDNS(context.Background(), gameQueries, fakeLookup(resolved, timeout), fakeLookup(resolved, timeout))
	if !r.OK() || r.Detail() != "" || r.Advice() != "" {
		t.Fatalf("result %+v", r)
	}
	if !strings.HasPrefix(r.Compact(), "正常（最慢 ") {
		t.Fatalf("compact %q", r.Compact())
	}
	if lines := r.Lines(); len(lines) != 3 || lines[0] != "本机 DNS 解析游戏域名："+r.Compact()+"；DNS 服务器 未知" {
		t.Fatalf("lines %q", lines)
	}
}

func TestDNSResultCompactShowsSlowestLookup(t *testing.T) {
	r := DNSResult{Lookups: []DNSLookup{
		{Local: DNSAnswer{Addrs: []string{"192.0.2.1"}, Duration: 300 * time.Microsecond}},
		{Local: DNSAnswer{Addrs: []string{"192.0.2.2"}, Duration: 34600 * time.Microsecond}},
	}}
	if got := r.Compact(); got != "正常（最慢 35 ms）" {
		t.Fatalf("compact %q", got)
	}
	r.Lookups = r.Lookups[:1]
	if got := r.Compact(); got != "正常（最慢 <1 ms）" {
		t.Fatalf("cached answer %q", got)
	}
}

// A name nobody can resolve has been retired by Nexon; it is not this
// computer's fault and must not raise the alarm.
func TestCheckDNSIgnoresRetiredNames(t *testing.T) {
	answers := map[string][]string{"us-sso.nexon.io": {"54.68.216.33"}}
	r := CheckDNS(context.Background(), gameQueries, fakeLookup(answers, noSuch), fakeLookup(answers, noSuch))
	if !r.OK() {
		t.Fatalf("retired name counted as a failure: %+v", r.Failed())
	}
	if line := r.Lines()[2]; !strings.Contains(line, "本机 查不到这个域名；公共 DNS 查不到这个域名") {
		t.Fatalf("the bundle still shows it: %q", line)
	}
	// Locally unknown but publicly resolvable is a DNS fault, not retirement.
	r = CheckDNS(context.Background(), gameQueries, fakeLookup(answers, noSuch), fakeLookup(resolved, noSuch))
	if r.OK() || r.Failed()[0].Host != "x-csauth.ngs.nexon.com" {
		t.Fatalf("result %+v", r)
	}
}

// A lookup that never returns is cut off when the check's time is up.
func TestCheckDNSStopsHangingLookups(t *testing.T) {
	hang := func(ctx context.Context, _ string) ([]string, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := CheckDNS(ctx, gameQueries, hang, fakeLookup(resolved, timeout))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("took %v", elapsed)
	}
	if r.Lookups[0].Local.Err != "超时" || !r.Lookups[0].Public.OK() {
		t.Fatalf("lookup %+v", r.Lookups[0])
	}
}

func TestDNSReason(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		err  error
		want string
	}{
		{timeout, "超时"},
		{context.DeadlineExceeded, "超时"},
		{noSuch, "查不到这个域名"},
		{&net.DNSError{Err: "server misbehaving", IsTemporary: true}, "DNS 服务器没有给出结果"},
		{&net.DNSError{Err: "unexpected reply"}, "失败（unexpected reply）"},
		{errors.New("boom"), "失败（boom）"},
	} {
		if got := dnsReason(ctx, c.err); got != c.want {
			t.Fatalf("dnsReason(%v) = %q, want %q", c.err, got, c.want)
		}
	}
	empty := resolve(ctx, fakeLookup(map[string][]string{"v6only.example": {}}, timeout), "v6only.example")
	if empty.Err != "没有 IPv4 地址" {
		t.Fatalf("empty answer %+v", empty)
	}
}
