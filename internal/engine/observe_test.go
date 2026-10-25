package engine

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func greeting(methods ...byte) []byte {
	return append([]byte{5, byte(len(methods))}, methods...)
}

func credentials(user, password string) []byte {
	b := []byte{1, byte(len(user))}
	b = append(b, user...)
	b = append(b, byte(len(password)))
	return append(b, password...)
}

func request(cmd, atyp byte, addr []byte, port int) []byte {
	b := []byte{5, cmd, 0, atyp}
	if atyp == 3 {
		b = append(b, byte(len(addr)))
	}
	b = append(b, addr...)
	return append(b, byte(port>>8), byte(port))
}

func join(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestParseHandshake(t *testing.T) {
	ipv6 := net.ParseIP("2600:9000:21e2::1")
	cases := []struct {
		name  string
		in    []byte
		want  Request
		total int
	}{
		{"no auth, IPv4", join(greeting(0), request(1, 1, []byte{13, 35, 7, 81}, 443)),
			Request{Command: "CONNECT", Host: "13.35.7.81", Port: 443}, 13},
		{"credentials, domain", join(greeting(0, 2), credentials("session-user", "secret"), request(1, 3, []byte("www.nexon.com"), 443)),
			Request{Command: "CONNECT", Host: "www.nexon.com", Port: 443, Domain: true}, 4 + 21 + 20},
		{"IPv6", join(greeting(2), credentials("u", "p"), request(1, 4, ipv6, 8585)),
			Request{Command: "CONNECT", Host: "2600:9000:21e2::1", Port: 8585}, 3 + 5 + 22},
		{"UDP associate", join(greeting(0), request(3, 1, []byte{0, 0, 0, 0}, 0)),
			Request{Command: "UDP", Host: "0.0.0.0", Port: 0}, 13},
	}
	for _, c := range cases {
		n, req, state := parseHandshake(c.in)
		if state != handshakeComplete || n != c.total || req != c.want {
			t.Fatalf("%s: n=%d req=%+v state=%v, want n=%d %+v", c.name, n, req, state, c.total, c.want)
		}
		for cut := 0; cut < len(c.in); cut++ {
			if _, _, state := parseHandshake(c.in[:cut]); state != handshakePartial {
				t.Fatalf("%s: prefix of %d bytes gave state %v, want partial", c.name, cut, state)
			}
		}
	}
}

func TestParseHandshakeRejectsOtherProtocols(t *testing.T) {
	for name, in := range map[string][]byte{
		"SOCKS4":       {4, 1, 0, 80, 1, 2, 3, 4, 0},
		"TLS":          {0x16, 3, 1, 0, 0xc8},
		"bad command":  join(greeting(0), []byte{5, 9, 0, 1, 1, 2, 3, 4, 0, 80}),
		"bad address":  join(greeting(0), []byte{5, 1, 0, 2, 1, 2, 3, 4, 0, 80}),
		"bad version":  join(greeting(0), []byte{4, 1, 0, 1, 1, 2, 3, 4, 0, 80}),
		"auth garbage": join(greeting(2), credentials("u", "p"), []byte{9, 9, 9, 9}),
	} {
		if _, _, state := parseHandshake(in); state != handshakeInvalid {
			t.Fatalf("%s: state %v, want invalid", name, state)
		}
	}
}

func TestSnifferByteByByteAndPayload(t *testing.T) {
	handshake := join(greeting(0, 2), credentials("session-user", "secret"), request(1, 3, []byte("nxl.nxfs.nexon.com"), 443))
	var s socksSniffer
	for i, b := range handshake[:len(handshake)-1] {
		if got := s.feed([]byte{b}); got != 0 || s.done {
			t.Fatalf("byte %d: payload %d done %v", i, got, s.done)
		}
	}
	// The last handshake byte arrives with the first payload bytes.
	if got := s.feed(append([]byte{handshake[len(handshake)-1]}, "hello"...)); got != 5 || !s.done {
		t.Fatalf("payload %d done %v, want 5 true", got, s.done)
	}
	if got := s.feed([]byte("more")); got != 4 {
		t.Fatalf("later payload %d, want 4", got)
	}
	if s.buf != nil || s.req.Host != "nxl.nxfs.nexon.com" || s.req.Port != 443 || !s.req.Domain {
		t.Fatalf("sniffer kept %d bytes, request %+v", len(s.buf), s.req)
	}
}

func TestSnifferGivesUpOnGarbage(t *testing.T) {
	var s socksSniffer
	if got := s.feed([]byte("GET / HTTP/1.1\r\n")); got != 0 || !s.done || s.req.Command != "" {
		t.Fatalf("payload %d done %v request %+v", got, s.done, s.req)
	}
	if got := s.feed([]byte("Host: x\r\n")); got != 9 {
		t.Fatalf("after giving up, payload %d, want 9", got)
	}
}

func TestPrintableHost(t *testing.T) {
	if got := printableHost([]byte("a.b-c_d:1\n\x00x")); got != "a.b-c_d:1??x" {
		t.Fatalf("printableHost = %q", got)
	}
}

func TestRequestHelpers(t *testing.T) {
	v6 := Request{Host: "2600:9000:21e2::1", Port: 443}
	if !v6.IPv6() || v6.Target() != "[2600:9000:21e2::1]:443" {
		t.Fatalf("IPv6 %v target %q", v6.IPv6(), v6.Target())
	}
	if (Request{Host: "1.2.3.4", Port: 80}).IPv6() || (Request{Host: "a.b", Domain: true}).IPv6() {
		t.Fatal("IPv4 and names are not IPv6")
	}
}

// fakeSOCKS answers one no-auth CONNECT on conn, then echoes "pong!" for
// "ping", the way the real server relays the node's data.
func fakeSOCKS(t *testing.T, conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 64)
	if _, err := io.ReadFull(conn, buf[:3]); err != nil {
		t.Error(err)
		return
	}
	conn.Write([]byte{5, 0})
	if _, err := io.ReadFull(conn, buf[:10]); err != nil {
		t.Error(err)
		return
	}
	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		t.Error(err)
		return
	}
	conn.Write([]byte("pong!"))
}

func TestObserveReportsTargetAndBytes(t *testing.T) {
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	l := &countingListener{TCPListener: ln}
	defer l.Close()
	got := make(chan Request, 2)
	report := func(r Request) { got <- r }
	l.observe.Store(&report)

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go fakeSOCKS(t, c)
		}
	}()

	conn, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Write(join(greeting(0), request(1, 1, []byte{35, 155, 204, 207}, 8585)))
	reply := make([]byte, 12)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte("ping"))
	pong := make([]byte, 5)
	if _, err := io.ReadFull(conn, pong); err != nil || string(pong) != "pong!" {
		t.Fatalf("pong %q %v", pong, err)
	}
	conn.Close()

	select {
	case r := <-got:
		if r.Command != "CONNECT" || r.Target() != "35.155.204.207:8585" || r.Domain || r.Up != 4 || r.Down != 5 || r.Elapsed < 0 {
			t.Fatalf("report %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no report")
	}
	if l.active.Load() != 0 || l.accepted.Load() != 1 {
		t.Fatalf("accepted %d active %d", l.accepted.Load(), l.active.Load())
	}

	// Without an observer, connections are only counted.
	l.observe.Store(nil)
	conn, err = net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Write(join(greeting(0), request(1, 1, []byte{1, 2, 3, 4}, 80), []byte("ping")))
	io.ReadFull(conn, make([]byte, 17))
	conn.Close()
	select {
	case r := <-got:
		t.Fatalf("unexpected report %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
}
