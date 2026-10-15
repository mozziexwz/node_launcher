package sysproxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// harness runs a Server whose dial function connects every target to one
// local backend and records what was asked for.
type harness struct {
	srv     *Server
	backend string // host:port every target is connected to
	mu      sync.Mutex
	dialed  []string
	failAll bool
}

func newHarness(t *testing.T, backend string) *harness {
	t.Helper()
	h := &harness{backend: backend}
	srv, err := NewServer(func(ctx context.Context, host string, port int) (net.Conn, error) {
		h.mu.Lock()
		h.dialed = append(h.dialed, net.JoinHostPort(host, fmt.Sprint(port)))
		fail := h.failAll
		h.mu.Unlock()
		if fail {
			return nil, errors.New("node unreachable")
		}
		var d net.Dialer
		return d.DialContext(ctx, "tcp", h.backend)
	}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	h.srv = srv
	return h
}

func (h *harness) lastDialed() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.dialed) == 0 {
		return ""
	}
	return h.dialed[len(h.dialed)-1]
}

// front connects like the redirector: SOCKS5 with username/password, CONNECT
// to the system proxy the game was reaching.
func (h *harness) front(t *testing.T, password string) (net.Conn, error) {
	t.Helper()
	c, err := net.Dial("tcp", h.srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	user, pass := h.srv.Credential()
	if password != "" {
		pass = password
	}
	write(t, c, []byte{5, 1, 2})
	if got := read(t, c, 2); got[1] != 2 {
		c.Close()
		return nil, fmt.Errorf("method %v", got)
	}
	auth := append([]byte{1, byte(len(user))}, user...)
	auth = append(append(auth, byte(len(pass))), pass...)
	write(t, c, auth)
	if got := read(t, c, 2); got[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("auth status %v", got)
	}
	write(t, c, []byte{5, 1, 0, 1, 127, 0, 0, 1, 0x2A, 0x30}) // 127.0.0.1:10800
	if got := read(t, c, 10); got[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("connect reply %v", got)
	}
	return c, nil
}

func write(t *testing.T, w io.Writer, b []byte) {
	t.Helper()
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, r io.Reader, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		t.Fatal(err)
	}
	return b
}

// echoServer echoes every connection.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func echoes(t *testing.T, c net.Conn) {
	t.Helper()
	write(t, c, []byte("ping"))
	if got := string(read(t, c, 4)); got != "ping" {
		t.Fatalf("echo %q", got)
	}
}

func TestHTTPConnect(t *testing.T) {
	h := newHarness(t, echoServer(t))
	c, err := h.front(t, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	write(t, c, []byte("CONNECT api.nexon.io:443 HTTP/1.1\r\nHost: api.nexon.io:443\r\nProxy-Connection: keep-alive\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT response %v, %v", resp, err)
	}
	echoes(t, c)
	if got := h.lastDialed(); got != "api.nexon.io:443" {
		t.Fatalf("dialed %q", got)
	}
}

func TestHTTPAbsoluteForm(t *testing.T) {
	var mu sync.Mutex
	var proxyHeaders []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		proxyHeaders = append(proxyHeaders, r.Header.Get("Proxy-Connection")+r.Header.Get("Proxy-Authorization"))
		mu.Unlock()
		fmt.Fprintf(w, "hello %s %s", r.Host, r.URL.Path)
	}))
	defer backend.Close()
	h := newHarness(t, strings.TrimPrefix(backend.URL, "http://"))
	c, err := h.front(t, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	br := bufio.NewReader(c)
	for _, path := range []string{"/a", "/b"} { // two requests on one connection
		fmt.Fprintf(c, "GET http://site.test%s HTTP/1.1\r\nHost: site.test\r\nProxy-Connection: keep-alive\r\nProxy-Authorization: Basic eDp5\r\n\r\n", path)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if want := "hello site.test " + path; string(body) != want {
			t.Fatalf("body %q, want %q", body, want)
		}
	}
	if got := h.lastDialed(); got != "site.test:80" {
		t.Fatalf("dialed %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range proxyHeaders {
		if p != "" {
			t.Fatalf("proxy headers must not be forwarded: %q", p)
		}
	}
}

func TestInnerSOCKS5(t *testing.T) {
	for _, withAuth := range []bool{false, true} {
		t.Run(fmt.Sprint("auth=", withAuth), func(t *testing.T) {
			h := newHarness(t, echoServer(t))
			c, err := h.front(t, "")
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if withAuth {
				write(t, c, []byte{5, 1, 2})
				if got := read(t, c, 2); got[1] != 2 {
					t.Fatalf("method %v", got)
				}
				write(t, c, []byte{1, 1, 'u', 1, 'p'})
				if got := read(t, c, 2); got[1] != 0 {
					t.Fatalf("auth %v", got)
				}
			} else {
				write(t, c, []byte{5, 1, 0})
				if got := read(t, c, 2); got[1] != 0 {
					t.Fatalf("method %v", got)
				}
			}
			req := append([]byte{5, 1, 0, 3, byte(len("game.test"))}, "game.test"...)
			req = binary.BigEndian.AppendUint16(req, 8585)
			write(t, c, req)
			if got := read(t, c, 10); got[1] != 0 {
				t.Fatalf("reply %v", got)
			}
			echoes(t, c)
			if got := h.lastDialed(); got != "game.test:8585" {
				t.Fatalf("dialed %q", got)
			}
		})
	}
}

func TestInnerSOCKS5RefusesUDP(t *testing.T) {
	h := newHarness(t, echoServer(t))
	c, err := h.front(t, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	write(t, c, []byte{5, 1, 0})
	read(t, c, 2)
	write(t, c, []byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	if got := read(t, c, 10); got[1] != 7 {
		t.Fatalf("UDP ASSOCIATE reply %v, want command not supported", got)
	}
}

func TestInnerSOCKS4a(t *testing.T) {
	h := newHarness(t, echoServer(t))
	c, err := h.front(t, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	req := []byte{4, 1, 0x01, 0xBB, 0, 0, 0, 1}
	req = append(append(req, "user\x00"...), "login.test\x00"...)
	write(t, c, req)
	if got := read(t, c, 8); got[1] != 0x5A {
		t.Fatalf("reply %v", got)
	}
	echoes(t, c)
	if got := h.lastDialed(); got != "login.test:443" {
		t.Fatalf("dialed %q", got)
	}
}

func TestFrontRejectsWrongPassword(t *testing.T) {
	h := newHarness(t, echoServer(t))
	if c, err := h.front(t, "wrong"); err == nil {
		c.Close()
		t.Fatal("a wrong password must be refused")
	}
}

func TestDialFailureAnswers502(t *testing.T) {
	h := newHarness(t, echoServer(t))
	h.failAll = true
	c, err := h.front(t, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	write(t, c, []byte("CONNECT api.nexon.io:443 HTTP/1.1\r\nHost: api.nexon.io:443\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("response %v, %v", resp, err)
	}
}

func TestCloseEndsActiveConnections(t *testing.T) {
	h := newHarness(t, echoServer(t))
	c, err := h.front(t, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	write(t, c, []byte("CONNECT a.test:443 HTTP/1.1\r\nHost: a.test:443\r\n\r\n"))
	if resp, err := http.ReadResponse(bufio.NewReader(c), nil); err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT %v, %v", resp, err)
	}
	done := make(chan error, 1)
	go func() { done <- h.srv.Close() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close must not wait for clients to hang up")
	}
	c.SetDeadline(time.Now().Add(time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("the client connection must be closed")
	}
}
