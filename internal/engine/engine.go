package engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/enfein/mieru/v3/pkg/appctl/appctlcommon"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"github.com/enfein/mieru/v3/pkg/protocol"
	"github.com/enfein/mieru/v3/pkg/socks5"
)

// Engine is a running embedded mieru client with its local SOCKS5 endpoint.
type Engine struct {
	mux      *protocol.Mux
	server   *socks5.Server
	listener *countingListener
	meter    *trafficMeter
	user     string
	password string
	served   chan struct{}
	stop     chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// Stats counts SOCKS5 connections accepted from local clients and the
// traffic with the node since the engine started, in bytes.
type Stats struct {
	Accepted     int64
	Active       int64
	Upload       int64
	Download     int64
	UploadRate   int64 // bytes per second over the last second
	DownloadRate int64
}

// Start builds the mieru multiplexer for cfg's active profile and serves
// SOCKS5 on 127.0.0.1 with fresh random credentials. A listenPort of 0
// picks a free port.
func Start(cfg *Config, listenPort int) (*Engine, error) {
	if cfg == nil || cfg.Profile == nil {
		return nil, errors.New("缺少节点配置")
	}
	user, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	password, err := randomHex(24)
	if err != nil {
		return nil, err
	}

	resolver := &net.Resolver{}
	mux, err := appctlcommon.NewClientMuxFromProfile(cfg.Profile, nil, nil, resolver, nil)
	if err != nil {
		return nil, fmt.Errorf("MSBOOST 核心启动失败：%w", err)
	}
	server, err := socks5.New(&socks5.Config{
		UseProxy:    true,
		ProxyDialer: mux,
		AuthOpts: socks5.Auth{
			ClientSideAuthentication: true,
			IngressCredentials:       []socks5.Credential{{User: user, Password: password}},
		},
		Resolver:         resolver,
		HandshakeTimeout: 10 * time.Second,
		HandshakeNoWait:  cfg.Profile.GetHandshakeMode() == appctlpb.HandshakeMode_HANDSHAKE_NO_WAIT,
	})
	if err != nil {
		_ = mux.Close()
		return nil, fmt.Errorf("创建本地 SOCKS5 服务失败：%w", err)
	}
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: listenPort})
	if err != nil {
		_ = mux.Close()
		return nil, fmt.Errorf("本地 SOCKS5 监听失败：%w", err)
	}

	e := &Engine{
		mux:      mux,
		server:   server,
		listener: &countingListener{TCPListener: ln},
		meter:    newTrafficMeter(nodeTraffic),
		user:     user,
		password: password,
		served:   make(chan struct{}),
		stop:     make(chan struct{}),
	}
	go func() {
		defer close(e.served)
		_ = server.Serve(e.listener)
	}()
	started := time.Now()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		e.meter.run(started, t.C, e.stop)
	}()
	return e, nil
}

// Addr is the loopback SOCKS5 address.
func (e *Engine) Addr() *net.TCPAddr {
	return e.listener.Addr().(*net.TCPAddr)
}

// Credential returns the per-session SOCKS5 username and password.
func (e *Engine) Credential() (user, password string) {
	return e.user, e.password
}

// Stats reports connection and traffic counters.
func (e *Engine) Stats() Stats {
	up, down := e.meter.totals()
	upRate, downRate := e.meter.rates()
	return Stats{
		Accepted: e.listener.accepted.Load(), Active: e.listener.active.Load(),
		Upload: up, Download: down, UploadRate: upRate, DownloadRate: downRate,
	}
}

// Close stops accepting SOCKS5 connections and closes the mieru multiplexer,
// which also ends the proxied connections still using it.
func (e *Engine) Close() error {
	e.closeOnce.Do(func() {
		close(e.stop)
		_ = e.server.Close()
		select {
		case <-e.served:
		case <-time.After(5 * time.Second):
		}
		e.closeErr = e.mux.Close()
	})
	return e.closeErr
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机凭据失败：%w", err)
	}
	return hex.EncodeToString(b), nil
}

// countingListener tracks accepted and still-open SOCKS5 client connections,
// and follows them for Observe when an observer is set.
type countingListener struct {
	*net.TCPListener
	accepted atomic.Int64
	active   atomic.Int64
	observe  atomic.Pointer[func(Request)]
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.TCPListener.Accept()
	if err != nil {
		return nil, err
	}
	l.accepted.Add(1)
	l.active.Add(1)
	conn := &countedConn{Conn: c, active: &l.active}
	if fn := l.observe.Load(); fn != nil {
		conn.watch = newWatch(*fn)
	}
	return conn, nil
}

type countedConn struct {
	net.Conn
	active *atomic.Int64
	once   sync.Once
	watch  *watch // nil unless observed
}

func (c *countedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.watch != nil && n > 0 {
		c.watch.read(p[:n])
	}
	return n, err
}

func (c *countedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if c.watch != nil && n > 0 {
		c.watch.wrote(n)
	}
	return n, err
}

// Close reports an observed connection before it stops counting as active,
// so a caller that waits for no active connections has every report.
func (c *countedConn) Close() error {
	c.once.Do(func() {
		if c.watch != nil {
			c.watch.done()
		}
		c.active.Add(-1)
	})
	return c.Conn.Close()
}
