package probe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testNodeID = "89abcdef0123456789abcdef01234567"
	testGame   = "35.155.204.207"
	testPort   = 8585
)

// fakeSOCKS is a SOCKS5 server that hands every successful CONNECT to node,
// standing in for "local mieru SOCKS5 + remote MSBOOST node".
type fakeSOCKS struct {
	ln       net.Listener
	user     string
	password string
	node     func(session int, conn net.Conn)
	sessions atomic.Int32
	targets  sync.Map // "host:port" -> true
	wg       sync.WaitGroup
}

func startFakeSOCKS(t *testing.T, user, password string, node func(int, net.Conn)) *fakeSOCKS {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSOCKS{ln: ln, user: user, password: password, node: node}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer c.Close()
				s.serve(c)
			}()
		}
	}()
	t.Cleanup(func() { ln.Close(); s.wg.Wait() })
	return s
}

func (s *fakeSOCKS) proxy() Proxy {
	return Proxy{Addr: s.ln.Addr().String(), User: s.user, Password: s.password}
}

func (s *fakeSOCKS) serve(c net.Conn) {
	r := bufio.NewReader(c)
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(r, methods); err != nil {
		return
	}
	want := byte(0)
	if s.user != "" {
		want = 2
	}
	if !strings.ContainsRune(string(methods), rune(want)) {
		c.Write([]byte{5, 0xFF})
		return
	}
	c.Write([]byte{5, want})
	if want == 2 {
		ver := make([]byte, 2)
		if _, err := io.ReadFull(r, ver); err != nil {
			return
		}
		user := make([]byte, ver[1])
		io.ReadFull(r, user)
		plen := make([]byte, 1)
		io.ReadFull(r, plen)
		pass := make([]byte, plen[0])
		io.ReadFull(r, pass)
		if string(user) != s.user || string(pass) != s.password {
			c.Write([]byte{1, 1})
			return
		}
		c.Write([]byte{1, 0})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(r, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		io.ReadFull(r, ip)
		host = net.IP(ip).String()
	case 3:
		n := make([]byte, 1)
		io.ReadFull(r, n)
		name := make([]byte, n[0])
		io.ReadFull(r, name)
		host = string(name)
	default:
		return
	}
	pb := make([]byte, 2)
	io.ReadFull(r, pb)
	s.targets.Store(fmt.Sprintf("%s:%d", host, int(pb[0])<<8|int(pb[1])), true)
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	session := int(s.sessions.Add(1))
	s.node(session, &bufferedConn{Conn: c, r: r})
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

type wireRequest struct {
	Version       int    `json:"version"`
	Operation     string `json:"operation"`
	Nonce         string `json:"nonce"`
	TargetAddress string `json:"target_address"`
	TargetPort    int    `json:"target_port"`
}

func helloReply(req wireRequest) string {
	return fmt.Sprintf(`{"version":2,"operation":"hello","nonce":"%s","node_id":"%s","success":true}`+"\n", req.Nonce, testNodeID)
}

func connectReply(req wireRequest, errCode string) string {
	if errCode == "" {
		return fmt.Sprintf(`{"version":2,"operation":"connect","nonce":"%s","target_address":"%s","target_port":%d,"node_id":"%s","success":true}`+"\n",
			req.Nonce, req.TargetAddress, req.TargetPort, testNodeID)
	}
	return fmt.Sprintf(`{"version":2,"operation":"connect","nonce":"%s","target_address":"%s","target_port":%d,"node_id":"%s","success":false,"error":"%s"}`+"\n",
		req.Nonce, req.TargetAddress, req.TargetPort, testNodeID, errCode)
}

// scriptedNode answers hello normally and lets reply decide each connect.
// attempt counts connect requests across sessions. An empty reply hangs.
func scriptedNode(helloDelay time.Duration, reply func(attempt int, req wireRequest) string) func(int, net.Conn) {
	var attempts atomic.Int32
	return func(_ int, conn net.Conn) {
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			var req wireRequest
			if json.Unmarshal([]byte(line), &req) != nil {
				return
			}
			if req.Operation == "hello" {
				time.Sleep(helloDelay)
				conn.Write([]byte(helloReply(req)))
				continue
			}
			out := reply(int(attempts.Add(1))-1, req)
			if out == "" {
				time.Sleep(3 * time.Second)
				return
			}
			conn.Write([]byte(out))
		}
	}
}

func fastOpts() FullPathOptions {
	return FullPathOptions{AttemptTimeout: 300 * time.Millisecond, WarmupTimeout: 2 * time.Second}
}

func TestFullPathExcludesWarmupAndTimesConfirmation(t *testing.T) {
	const connectDelay = 40 * time.Millisecond
	s := startFakeSOCKS(t, "u", "p", scriptedNode(250*time.Millisecond, func(_ int, req wireRequest) string {
		time.Sleep(connectDelay)
		return connectReply(req, "")
	}))
	r := FullPathLatency(s.proxy(), testGame, testPort, FullPathOptions{})
	if r.SuccessCount != 3 || r.TotalCount != 3 {
		t.Fatalf("result = %+v, want 3/3", r)
	}
	for i, sample := range r.Samples {
		if sample.Duration < connectDelay || sample.Duration >= 250*time.Millisecond {
			t.Errorf("sample %d = %v, want >= %v and excluding the 250ms warmup", i, sample.Duration, connectDelay)
		}
	}
	if got := s.sessions.Load(); got != 1 {
		t.Errorf("sessions = %d, want 1 persistent session", got)
	}
	if _, ok := s.targets.Load("127.0.0.1:20424"); !ok {
		t.Error("SOCKS CONNECT must target the node-side 127.0.0.1:20424")
	}
}

func TestFullPathValidFailureKeepsSession(t *testing.T) {
	s := startFakeSOCKS(t, "u", "p", scriptedNode(0, func(_ int, req wireRequest) string {
		return connectReply(req, "refused")
	}))
	r := FullPathLatency(s.proxy(), testGame, testPort, fastOpts())
	if r.SuccessCount != 0 || len(r.Errors) != 1 || r.Errors[0] != "游戏端口拒绝连接" {
		t.Fatalf("result = %+v", r)
	}
	if r.Compact() != "未测得：游戏端口拒绝连接" {
		t.Fatalf("compact = %q", r.Compact())
	}
	if got := s.sessions.Load(); got != 1 {
		t.Errorf("sessions = %d, want 1", got)
	}
}

func TestFullPathProtocolErrorReconnects(t *testing.T) {
	s := startFakeSOCKS(t, "u", "p", scriptedNode(0, func(attempt int, req wireRequest) string {
		if attempt == 0 {
			req.Nonce = strings.Repeat("0", 32) // stale reply
		}
		return connectReply(req, "")
	}))
	r := FullPathLatency(s.proxy(), testGame, testPort, fastOpts())
	if r.SuccessCount != 2 || r.Errors[0] != "测速回复的操作、随机标识或节点身份无效" {
		t.Fatalf("result = %+v", r)
	}
	if got := s.sessions.Load(); got != 2 {
		t.Errorf("sessions = %d, want a fresh session after the protocol error", got)
	}
}

func TestFullPathTimeoutReconnects(t *testing.T) {
	s := startFakeSOCKS(t, "u", "p", scriptedNode(0, func(attempt int, req wireRequest) string {
		if attempt == 0 {
			return "" // never answer
		}
		return connectReply(req, "")
	}))
	r := FullPathLatency(s.proxy(), testGame, testPort, fastOpts())
	if r.SuccessCount != 2 || r.Errors[0] != "测速请求超时" {
		t.Fatalf("result = %+v", r)
	}
	if got := s.sessions.Load(); got != 2 {
		t.Errorf("sessions = %d, want 2", got)
	}
}

func TestFullPathOldServiceDoesNotRetry(t *testing.T) {
	s := startFakeSOCKS(t, "u", "p", func(_ int, conn net.Conn) {
		bufio.NewReader(conn).ReadString('\n')
		conn.Write([]byte(`{"version":1,"nonce":"x","success":true}` + "\n"))
	})
	r := FullPathLatency(s.proxy(), testGame, testPort, fastOpts())
	want := "节点未提供兼容的版本 2 全程 TCP 测速服务或服务不可达：节点测速协议不兼容，需要版本 2"
	if r.SuccessCount != 0 || r.TotalCount != 3 || len(r.Errors) != 1 || r.Errors[0] != want {
		t.Fatalf("result = %+v", r)
	}
	if got := s.sessions.Load(); got != 1 {
		t.Errorf("sessions = %d, want no repeated attempts", got)
	}
}

func TestFullPathExtraDataAfterLine(t *testing.T) {
	s := startFakeSOCKS(t, "u", "p", scriptedNode(0, func(_ int, req wireRequest) string {
		return connectReply(req, "") + `{"version":2}`
	}))
	r := FullPathLatency(s.proxy(), testGame, testPort, fastOpts())
	if r.SuccessCount != 0 || r.Errors[0] != "测速服务发送了未请求的额外数据" {
		t.Fatalf("result = %+v", r)
	}
}

func TestFullPathWrongCredentials(t *testing.T) {
	s := startFakeSOCKS(t, "u", "p", scriptedNode(0, func(_ int, req wireRequest) string { return connectReply(req, "") }))
	p := s.proxy()
	p.Password = "wrong"
	r := FullPathLatency(p, testGame, testPort, fastOpts())
	if r.SuccessCount != 0 || r.Errors[0] != "本地 SOCKS5 认证失败" {
		t.Fatalf("result = %+v", r)
	}
	if got := s.sessions.Load(); got != 0 {
		t.Errorf("sessions = %d, want 0", got)
	}
}

func TestFullPathProxyDown(t *testing.T) {
	ln, _ := net.Listen("tcp4", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	r := FullPathLatency(Proxy{Addr: addr, User: "u", Password: "p"}, testGame, testPort, fastOpts())
	if r.SuccessCount != 0 || !strings.HasPrefix(r.Errors[0], "本地代理未启动、端口未监听或连接超时：") {
		t.Fatalf("result = %+v", r)
	}
}

func TestFullPathRejectsInvalidParameters(t *testing.T) {
	for _, addr := range []string{"035.155.204.207", "::1", "example.com", "35.155.204"} {
		r := FullPathLatency(Proxy{Addr: "127.0.0.1:1"}, addr, testPort, FullPathOptions{})
		if r.Errors[0] != "测速参数无效，请使用规范 IPv4、有效端口及超时" {
			t.Errorf("%q accepted: %+v", addr, r)
		}
	}
	r := FullPathLatency(Proxy{Addr: "127.0.0.1:1"}, testGame, testPort, FullPathOptions{AttemptTimeout: 3 * time.Second})
	if r.SuccessCount != 0 || !strings.HasPrefix(r.Errors[0], "测速参数无效") {
		t.Errorf("attempt timeout above the contract limit accepted: %+v", r)
	}
}

func TestCheckReplyContract(t *testing.T) {
	nonce := strings.Repeat("a", 32)
	base := fmt.Sprintf(`{"version":2,"operation":"connect","nonce":"%s","target_address":"%s","target_port":%d,"node_id":"%s","success":true}`,
		nonce, testGame, testPort, testNodeID)
	check := func(line string) error {
		f, err := parseReply([]byte(line))
		if err != nil {
			return err
		}
		_, _, _, err = checkReply(f, "connect", nonce, testNodeID, testGame, testPort)
		return err
	}
	if err := check(base); err != nil {
		t.Fatalf("valid reply rejected: %v", err)
	}
	if err := check(base + "\r"); err != nil {
		t.Fatalf("CRLF reply rejected: %v", err)
	}
	failure := strings.Replace(base, `"success":true}`, `"success":false,"error":"timeout"}`, 1)
	if err := check(failure); err != nil {
		t.Fatalf("valid failure rejected: %v", err)
	}

	bad := map[string]string{
		"duplicate key":      strings.Replace(base, `"success":true}`, `"success":true,"success":true}`, 1),
		"nested object":      strings.Replace(base, `"success":true}`, `"success":{"x":1}}`, 1),
		"escaped string":     strings.Replace(base, testNodeID, "\\"+"u0038"+testNodeID[1:], 1),
		"leading zero port":  strings.Replace(base, `:8585`, `:08585`, 1),
		"float version":      strings.Replace(base, `"version":2`, `"version":2.0`, 1),
		"string version":     strings.Replace(base, `"version":2`, `"version":"2"`, 1),
		"exponent port":      strings.Replace(base, `:8585`, `:8.585e3`, 1),
		"bom":                "\xef\xbb\xbf" + base,
		"control char":       strings.Replace(base, `{`, "{\x0b", 1),
		"trailing junk":      base + " x",
		"two objects":        base + base,
		"null success":       strings.Replace(base, `"success":true`, `"success":null`, 1),
		"unknown field":      strings.Replace(base, `"success":true}`, `"success":true,"connect_ms":12}`, 1),
		"missing field":      strings.Replace(base, `,"node_id":"`+testNodeID+`"`, ``, 1),
		"success with error": strings.Replace(base, `"success":true}`, `"success":true,"error":"timeout"}`, 1),
		"bad error code":     strings.Replace(base, `"success":true}`, `"success":false,"error":"boom"}`, 1),
		"wrong nonce":        strings.Replace(base, nonce, strings.Repeat("b", 32), 1),
		"other node":         strings.Replace(base, testNodeID, strings.Repeat("c", 32), 1),
		"upper node id":      strings.Replace(base, testNodeID, strings.ToUpper(testNodeID), 1),
		"other target":       strings.Replace(base, testGame, "51.222.56.192", 1),
		"other port":         strings.Replace(base, `:8585`, `:8484`, 1),
		"wrong operation":    strings.Replace(base, `"connect"`, `"hello"`, 1),
		"array":              `[` + base + `]`,
	}
	for name, line := range bad {
		if err := check(line); err == nil {
			t.Errorf("%s: accepted %s", name, line)
		}
	}
}

func TestReadReplyLineLengthLimit(t *testing.T) {
	readLine := func(payload string) error {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			server.Write([]byte(payload))
			server.Close()
		}()
		start := time.Now()
		_, _, err := readReplyLine(client, start, start.Add(2*time.Second), 2*time.Second)
		return err
	}
	if err := readLine(strings.Repeat("a", 4096) + "\n"); err != nil {
		t.Errorf("4096 bytes rejected: %v", err)
	}
	if err := readLine(strings.Repeat("a", 4095) + "\r\n"); err != nil {
		t.Errorf("4095 bytes + CR rejected: %v", err)
	}
	if err := readLine(strings.Repeat("a", 4096) + "\r\n"); err == nil || err.Error() != "测速回复过长" {
		t.Errorf("4096 bytes + CR: err = %v, want too long", err)
	}
	if err := readLine(strings.Repeat("a", 5000)); err == nil || err.Error() != "测速回复过长" {
		t.Errorf("unterminated long line: err = %v, want too long", err)
	}
	if err := readLine(`{"version":2`); err == nil || err.Error() != "节点测速服务提前关闭连接" {
		t.Errorf("EOF mid-line: err = %v", err)
	}
}

func TestResultCompactRounding(t *testing.T) {
	r := newResult([]Sample{{OK: true, Duration: 190500 * time.Microsecond}})
	if got := r.Compact(); got != "191 ms" {
		t.Errorf("Compact = %q, want 191 ms (half away from zero)", got)
	}
	if got := newResult([]Sample{{OK: true, Duration: 300 * time.Microsecond}}).Compact(); got != "<1 ms" {
		t.Errorf("Compact = %q, want <1 ms", got)
	}
	mixed := newResult([]Sample{{OK: true, Duration: 10 * time.Millisecond}, {Err: "x"}, {Err: "x"}})
	if mixed.SuccessCount != 1 || len(mixed.Errors) != 1 || mixed.Average != 10*time.Millisecond {
		t.Errorf("failed samples must not be averaged as zero: %+v", mixed)
	}
}
