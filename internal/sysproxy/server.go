package sysproxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// DialFunc opens a TCP connection to host:port through MSBOOST. host may be
// a domain name; the node resolves it.
type DialFunc func(ctx context.Context, host string, port int) (net.Conn, error)

const (
	handshakeTimeout = 10 * time.Second
	dialTimeout      = 15 * time.Second
	maxLoggedHosts   = 200
)

// Server stands in for the system proxy. The redirector hands it each game
// connection to that proxy as a SOCKS5 CONNECT; inside it the game speaks the
// proxy protocol (HTTP, SOCKS5 or SOCKS4), which Server answers, dialing the
// real destination with dial.
type Server struct {
	ln             net.Listener
	user, password string
	dial           DialFunc
	logf           func(format string, args ...any)

	mu     sync.Mutex
	conns  map[net.Conn]bool
	closed bool
	wg     sync.WaitGroup

	logged   sync.Map // destination -> true, so each is logged once
	nLogged  atomic.Int32
	Accepted atomic.Int64
}

// NewServer listens on a random loopback port with fresh credentials. logf
// may be nil.
func NewServer(dial DialFunc, logf func(format string, args ...any)) (*Server, error) {
	user, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	password, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{ln: ln, user: user, password: password, dial: dial, logf: logf, conns: map[net.Conn]bool{}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Addr is the loopback address the redirector connects to.
func (s *Server) Addr() *net.TCPAddr { return s.ln.Addr().(*net.TCPAddr) }

// Credential returns the SOCKS5 username and password for the redirector.
func (s *Server) Credential() (user, password string) { return s.user, s.password }

// Close stops accepting, closes every connection and waits for them.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	err := s.ln.Close()
	s.wg.Wait()
	return err
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		if !s.track(c, true) {
			c.Close()
			return
		}
		s.Accepted.Add(1)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.track(c, false)
			defer c.Close()
			s.serve(c)
		}()
	}
}

func (s *Server) track(c net.Conn, add bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		if s.closed {
			return false
		}
		s.conns[c] = true
	} else {
		delete(s.conns, c)
	}
	return true
}

func (s *Server) serve(c net.Conn) {
	c.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := s.frontHandshake(c); err != nil {
		return
	}
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	switch first[0] {
	case 5:
		s.serveSOCKS5(c, br)
	case 4:
		s.serveSOCKS4(c, br)
	default:
		s.serveHTTP(c, br)
	}
}

// frontHandshake accepts the redirector's SOCKS5 CONNECT, which only names
// the system proxy the game was connecting to.
func (s *Server) frontHandshake(c net.Conn) error {
	var head [2]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return err
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return err
	}
	if head[0] != 5 || !contains(methods, 2) {
		c.Write([]byte{5, 0xFF})
		return errors.New("redirector did not offer username/password")
	}
	if _, err := c.Write([]byte{5, 2}); err != nil {
		return err
	}
	user, password, err := readUserPass(c)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(user), []byte(s.user)) != 1 ||
		subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) != 1 {
		c.Write([]byte{1, 1})
		return errors.New("bad credentials")
	}
	if _, err := c.Write([]byte{1, 0}); err != nil {
		return err
	}
	cmd, _, _, err := readSOCKS5Request(c)
	if err != nil {
		return err
	}
	if cmd != 1 {
		writeSOCKS5Reply(c, 7)
		return errors.New("only CONNECT is supported")
	}
	return writeSOCKS5Reply(c, 0)
}

func (s *Server) serveSOCKS5(c net.Conn, br *bufio.Reader) {
	var head [2]byte
	if _, err := io.ReadFull(br, head[:]); err != nil {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	switch {
	case contains(methods, 0):
		if _, err := c.Write([]byte{5, 0}); err != nil {
			return
		}
	case contains(methods, 2):
		// The game holds credentials for the other app's proxy; any will do.
		if _, err := c.Write([]byte{5, 2}); err != nil {
			return
		}
		if _, _, err := readUserPass(br); err != nil {
			return
		}
		if _, err := c.Write([]byte{1, 0}); err != nil {
			return
		}
	default:
		c.Write([]byte{5, 0xFF})
		return
	}
	cmd, host, port, err := readSOCKS5Request(br)
	if err != nil {
		return
	}
	if cmd != 1 {
		writeSOCKS5Reply(c, 7) // UDP ASSOCIATE and BIND are not offered
		return
	}
	up, err := s.dialTarget(host, port)
	if err != nil {
		writeSOCKS5Reply(c, 4)
		return
	}
	if err := writeSOCKS5Reply(c, 0); err != nil {
		up.Close()
		return
	}
	s.relay(c, br, up)
}

func (s *Server) serveSOCKS4(c net.Conn, br *bufio.Reader) {
	var head [8]byte
	if _, err := io.ReadFull(br, head[:]); err != nil {
		return
	}
	reply := func(ok bool) error {
		code := byte(0x5B)
		if ok {
			code = 0x5A
		}
		_, err := c.Write([]byte{0, code, 0, 0, 0, 0, 0, 0})
		return err
	}
	if _, err := readCString(br); err != nil { // user id
		return
	}
	port := int(binary.BigEndian.Uint16(head[2:4]))
	ip := net.IP(head[4:8])
	host := ip.String()
	if head[4] == 0 && head[5] == 0 && head[6] == 0 && head[7] != 0 { // SOCKS4a
		name, err := readCString(br)
		if err != nil {
			return
		}
		host = name
	}
	if head[1] != 1 {
		reply(false)
		return
	}
	up, err := s.dialTarget(host, port)
	if err != nil {
		reply(false)
		return
	}
	if err := reply(true); err != nil {
		up.Close()
		return
	}
	s.relay(c, br, up)
}

func (s *Server) serveHTTP(c net.Conn, br *bufio.Reader) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method == http.MethodConnect {
		host, port, err := splitTarget(req.Host, 443)
		if err != nil {
			writeHTTPStatus(c, http.StatusBadRequest)
			return
		}
		up, err := s.dialTarget(host, port)
		if err != nil {
			writeHTTPStatus(c, http.StatusBadGateway)
			return
		}
		if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			up.Close()
			return
		}
		s.relay(c, br, up)
		return
	}

	// Absolute-form requests ("GET http://host/path"): forward them one by
	// one, as a proxy does, until either side closes.
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, port, err := splitTarget(addr, 80)
			if err != nil {
				return nil, err
			}
			return s.dialTarget(host, port)
		},
		DisableCompression: true,
	}
	defer tr.CloseIdleConnections()
	c.SetDeadline(time.Time{})
	for {
		if req.URL == nil || req.URL.Host == "" {
			writeHTTPStatus(c, http.StatusBadRequest)
			return
		}
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authorization")
		resp, err := tr.RoundTrip(req)
		if err != nil {
			writeHTTPStatus(c, http.StatusBadGateway)
			return
		}
		err = resp.Write(c)
		resp.Body.Close()
		if err != nil || req.Close || resp.Close {
			return
		}
		if req, err = http.ReadRequest(br); err != nil {
			return
		}
	}
}

func (s *Server) dialTarget(host string, port int) (net.Conn, error) {
	target := net.JoinHostPort(host, strconv.Itoa(port))
	if _, done := s.logged.LoadOrStore(target, true); !done && s.nLogged.Add(1) <= maxLoggedHosts {
		s.logf("游戏经系统代理的请求已改由 MSBOOST 发出：%s", target)
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	up, err := s.dial(ctx, host, port)
	if err != nil {
		s.logf("经系统代理的请求连接失败：%s：%v", target, err)
		return nil, err
	}
	return up, nil
}

// relay copies both ways until both directions finish. Bytes the client sent
// after its request may already be buffered in br.
func (s *Server) relay(c net.Conn, br *bufio.Reader, up net.Conn) {
	if !s.track(up, true) {
		up.Close()
		return
	}
	defer s.track(up, false)
	c.SetDeadline(time.Time{})
	done := make(chan struct{})
	go func() {
		io.Copy(up, br)
		closeWrite(up)
		close(done)
	}()
	io.Copy(c, up)
	closeWrite(c)
	<-done
	up.Close()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
		return
	}
	c.Close()
}

func readUserPass(r io.Reader) (user, password string, err error) {
	var ver [2]byte
	if _, err = io.ReadFull(r, ver[:]); err != nil {
		return
	}
	if ver[0] != 1 {
		return "", "", errors.New("bad auth version")
	}
	u := make([]byte, ver[1])
	if _, err = io.ReadFull(r, u); err != nil {
		return
	}
	var n [1]byte
	if _, err = io.ReadFull(r, n[:]); err != nil {
		return
	}
	p := make([]byte, n[0])
	if _, err = io.ReadFull(r, p); err != nil {
		return
	}
	return string(u), string(p), nil
}

func readSOCKS5Request(r io.Reader) (cmd byte, host string, port int, err error) {
	var head [4]byte
	if _, err = io.ReadFull(r, head[:]); err != nil {
		return
	}
	if head[0] != 5 {
		return 0, "", 0, errors.New("not SOCKS5")
	}
	switch head[3] {
	case 1:
		var ip [4]byte
		if _, err = io.ReadFull(r, ip[:]); err != nil {
			return
		}
		host = net.IP(ip[:]).String()
	case 4:
		var ip [16]byte
		if _, err = io.ReadFull(r, ip[:]); err != nil {
			return
		}
		host = net.IP(ip[:]).String()
	case 3:
		var n [1]byte
		if _, err = io.ReadFull(r, n[:]); err != nil {
			return
		}
		name := make([]byte, n[0])
		if _, err = io.ReadFull(r, name); err != nil {
			return
		}
		host = string(name)
	default:
		return 0, "", 0, fmt.Errorf("unknown address type %d", head[3])
	}
	var p [2]byte
	if _, err = io.ReadFull(r, p[:]); err != nil {
		return
	}
	return head[1], host, int(binary.BigEndian.Uint16(p[:])), nil
}

func writeSOCKS5Reply(w io.Writer, code byte) error {
	_, err := w.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}

func readCString(br *bufio.Reader) (string, error) {
	s, err := br.ReadString(0)
	if err != nil {
		return "", err
	}
	if len(s) > 256 {
		return "", errors.New("string too long")
	}
	return s[:len(s)-1], nil
}

func writeHTTPStatus(w io.Writer, code int) {
	fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", code, http.StatusText(code))
}

// splitTarget splits "host:port", using def when the port is missing.
func splitTarget(hostport string, def int) (string, int, error) {
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		if hostport == "" {
			return "", 0, errors.New("empty host")
		}
		return hostport, def, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("bad port %q", portStr)
	}
	return host, port, nil
}

func contains(b []byte, v byte) bool {
	for _, x := range b {
		if x == v {
			return true
		}
	}
	return false
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
