package engine

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Watching what local programs ask the node for, for the support log. With
// an observer set, the engine reads along the client side of each SOCKS5
// handshake to learn the requested target and counts the bytes that follow
// in each direction. Nothing is changed or held back: the bytes are looked at
// after the SOCKS5 server has read them, and only the opening ones are kept.

// Request describes one SOCKS5 connection a local program made to the
// engine. It is reported when the connection closes.
type Request struct {
	// Command is "CONNECT", "UDP" (UDP ASSOCIATE) or "BIND"; it is empty
	// when no request was seen, for example when the client gave up early.
	Command string
	Host    string // the requested domain name or IP address
	Port    int
	Domain  bool  // Host is a name, which the node resolves
	Up      int64 // bytes the program sent after its request
	Down    int64 // bytes sent back to the program after the reply
	Elapsed time.Duration
}

// IPv6 reports whether the request names an IPv6 address.
func (r Request) IPv6() bool {
	if r.Domain {
		return false
	}
	ip := net.ParseIP(r.Host)
	return ip != nil && ip.To4() == nil
}

// Target is host:port, with an IPv6 address in brackets.
func (r Request) Target() string {
	return net.JoinHostPort(r.Host, strconv.Itoa(r.Port))
}

// Observe makes the engine call fn once for every SOCKS5 connection it
// accepts from now on, when that connection closes; nil stops it for new
// connections. fn runs on the connection's goroutine and must return quickly.
func (e *Engine) Observe(fn func(Request)) {
	if fn == nil {
		e.listener.observe.Store(nil)
		return
	}
	e.listener.observe.Store(&fn)
}

// watch follows one observed connection.
type watch struct {
	report func(Request)
	start  time.Time

	mu    sync.Mutex
	sniff socksSniffer

	settled atomic.Bool // the handshake is over (or was given up)
	replied atomic.Bool // the reply to the request has been written
	up      atomic.Int64
	down    atomic.Int64
}

func newWatch(report func(Request)) *watch {
	return &watch{report: report, start: time.Now()}
}

// read takes the bytes the server has just read from the program.
func (w *watch) read(p []byte) {
	if w.settled.Load() {
		w.up.Add(int64(len(p)))
		return
	}
	w.mu.Lock()
	payload := w.sniff.feed(p)
	settled := w.sniff.done
	w.mu.Unlock()
	w.up.Add(int64(payload))
	if settled {
		w.settled.Store(true)
	}
}

// wrote takes the size of what the server has just written to the program:
// first the SOCKS5 replies, then what the node sends back.
func (w *watch) wrote(n int) {
	if !w.settled.Load() {
		return // the method choice and the authentication result
	}
	if !w.replied.Swap(true) {
		return // the reply to the request
	}
	w.down.Add(int64(n))
}

// done reports the connection; it is called once, when it closes.
func (w *watch) done() {
	w.mu.Lock()
	req := w.sniff.req
	w.mu.Unlock()
	req.Up, req.Down = w.up.Load(), w.down.Load()
	req.Elapsed = time.Since(w.start)
	w.report(req)
}

// maxHandshake bounds the client side of a SOCKS5 handshake: the greeting
// (2+255 bytes), RFC 1929 credentials (3+255+255) and the request (7+255).
const maxHandshake = 257 + 513 + 262

type handshakeState int

const (
	handshakePartial handshakeState = iota
	handshakeComplete
	handshakeInvalid
)

// socksSniffer follows the client side of a SOCKS5 handshake (RFC 1928 and
// RFC 1929) to find the request, and gives up on anything unexpected. The
// credentials are skipped, never kept.
type socksSniffer struct {
	buf  []byte
	done bool
	req  Request
}

// feed adds bytes read from the program and returns how many of them are
// payload, that is, came after the request.
func (s *socksSniffer) feed(p []byte) int {
	if s.done {
		return len(p)
	}
	take := min(len(p), maxHandshake-len(s.buf))
	s.buf = append(s.buf, p[:take]...)
	buffered := len(s.buf)
	n, req, state := parseHandshake(s.buf)
	switch state {
	case handshakeComplete:
		s.req, s.done, s.buf = req, true, nil
		return buffered - n + len(p) - take
	case handshakeInvalid:
		s.done, s.buf = true, nil
		return 0
	}
	if buffered >= maxHandshake {
		s.done, s.buf = true, nil
	}
	return 0
}

// parseHandshake reads a client's greeting, optional username/password and
// request from b, and returns their total length and the request.
func parseHandshake(b []byte) (int, Request, handshakeState) {
	var req Request
	if len(b) > 0 && b[0] != 5 {
		return 0, req, handshakeInvalid
	}
	if len(b) < 2 {
		return 0, req, handshakePartial
	}
	i := 2 + int(b[1]) // VER NMETHODS METHODS...
	if len(b) <= i {
		return 0, req, handshakePartial
	}
	if b[i] == 1 { // RFC 1929: VER ULEN UNAME PLEN PASSWD
		if len(b) < i+2 {
			return 0, req, handshakePartial
		}
		userLen := int(b[i+1])
		if len(b) < i+3+userLen {
			return 0, req, handshakePartial
		}
		i += 3 + userLen + int(b[i+2+userLen])
		if len(b) <= i {
			return 0, req, handshakePartial
		}
	}
	// The request: VER CMD RSV ATYP DST.ADDR DST.PORT
	if b[i] != 5 {
		return 0, req, handshakeInvalid
	}
	if len(b) < i+4 {
		return 0, req, handshakePartial
	}
	switch b[i+1] {
	case 1:
		req.Command = "CONNECT"
	case 2:
		req.Command = "BIND"
	case 3:
		req.Command = "UDP"
	default:
		return 0, req, handshakeInvalid
	}
	j := i + 4
	switch b[i+3] {
	case 1:
		if len(b) < j+4 {
			return 0, req, handshakePartial
		}
		req.Host = net.IP(b[j : j+4]).String()
		j += 4
	case 4:
		if len(b) < j+16 {
			return 0, req, handshakePartial
		}
		req.Host = net.IP(b[j : j+16]).String()
		j += 16
	case 3:
		if len(b) < j+1 {
			return 0, req, handshakePartial
		}
		nameLen := int(b[j])
		if len(b) < j+1+nameLen {
			return 0, req, handshakePartial
		}
		req.Host, req.Domain = printableHost(b[j+1:j+1+nameLen]), true
		j += 1 + nameLen
	default:
		return 0, req, handshakeInvalid
	}
	if len(b) < j+2 {
		return 0, req, handshakePartial
	}
	req.Port = int(b[j])<<8 | int(b[j+1])
	return j + 2, req, handshakeComplete
}

// printableHost keeps the characters a host name may have and replaces the
// rest, so a malformed name cannot garble the log.
func printableHost(b []byte) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == ':':
			return r
		}
		return '?'
	}, string(b))
}
