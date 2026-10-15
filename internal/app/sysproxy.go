package app

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/probe"
	"github.com/mozziexwz/node_launcher/internal/profiles"
	"github.com/mozziexwz/node_launcher/internal/store"
	"github.com/mozziexwz/node_launcher/internal/sysproxy"
)

// Taking over the game's connections to the Windows system proxy. Apps such
// as v2rayN and the Clash family point the system proxy at a local port; the
// game's launcher follows it, so its region check would leave through their
// node. A redirector rule diverts those connections to sysproxy.Server, which
// answers in the proxy's place and sends the real requests through MSBOOST.
//
// The rule is added before the game rule because rules match in order, and is
// edited in place (never re-added) when the system proxy or the addons change.

const systemProxyRuleKey = "sysproxy"

// systemProxyRefresh is how often a running session re-reads the system proxy.
var systemProxyRefresh = 5 * time.Second

type proxyTakeover struct {
	server    *sysproxy.Server
	proxyID   uint32
	ruleID    uint32
	endpoints []sysproxy.Endpoint
	stop      chan struct{}
	done      chan struct{}
}

// proxiedProcesses lists the game's and the enabled addons' processes.
func proxiedProcesses(s store.Settings) string {
	list := []string{profiles.DefaultMapleStory().ProcessList()}
	for _, a := range profiles.Addons() {
		if s.Addons[a.ID] {
			list = append(list, a.ProcessList())
		}
	}
	return strings.Join(list, ";")
}

// ruleTargets returns the rule's hosts and ports; with no endpoints the rule
// keeps its place but is disabled.
func ruleTargets(eps []sysproxy.Endpoint) (hosts, ports string, enabled bool) {
	if len(eps) == 0 {
		return "127.0.0.1", "1", false
	}
	hosts, ports = sysproxy.RuleTargets(eps)
	return hosts, ports, true
}

// engineDialer dials through the session's core: a SOCKS5 CONNECT with the
// session credentials, so domain names are resolved on the node.
func engineDialer(eng CoreEngine) sysproxy.DialFunc {
	user, password := eng.Credential()
	p := probe.Proxy{Addr: eng.Addr().String(), User: user, Password: password}
	return func(ctx context.Context, host string, port int) (net.Conn, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(15 * time.Second)
		}
		conn, err := probe.DialSOCKS5(p, host, port, deadline)
		if err != nil {
			return nil, err
		}
		conn.SetDeadline(time.Time{})
		return conn, nil
	}
}

// startTakeover starts the stand-in proxy and adds its rule; it must run
// before the game rule is added. Everything it creates is registered in undo.
func (c *Controller) startTakeover(red Redirector, eng CoreEngine, processes string, undo *[]func(), secrets *[]string) (*proxyTakeover, error) {
	server, err := sysproxy.NewServer(engineDialer(eng), func(format string, args ...any) {
		c.log.Printf(logx.App, format, args...)
	})
	if err != nil {
		return nil, err
	}
	*undo = append(*undo, func() { server.Close() })
	user, password := server.Credential()
	for _, s := range []string{user, password} {
		c.log.AddSecret(s)
		*secrets = append(*secrets, s)
	}
	proxyID, err := red.AddSOCKS5("127.0.0.1", uint16(server.Addr().Port), user, password, false)
	if err != nil {
		return nil, err
	}
	*undo = append(*undo, func() { red.DeleteProxy(proxyID) })

	eps := c.deps.SystemProxies()
	hosts, ports, enabled := ruleTargets(eps)
	ruleID, err := red.AddTCPRule(processes, hosts, ports, proxyID)
	if err != nil {
		return nil, err
	}
	*undo = append(*undo, func() { red.DeleteRule(ruleID) })
	if !enabled {
		red.DisableRule(ruleID)
	}
	t := &proxyTakeover{server: server, proxyID: proxyID, ruleID: ruleID, endpoints: eps}
	c.setLoopbackPorts(red, eps)
	*undo = append(*undo, func() { red.SetLocalhostProxyPorts(nil) })
	c.logEndpoints(eps, true)
	return t, nil
}

// setLoopbackPorts passes loopback proxy ports to the redirector. A core
// without patch P4 still works for remote proxies, so a failure only logs.
func (c *Controller) setLoopbackPorts(red Redirector, eps []sysproxy.Endpoint) {
	ports := sysproxy.LoopbackPorts(eps)
	if err := red.SetLocalhostProxyPorts(ports); err != nil && len(ports) > 0 {
		c.log.Printf(logx.App, "%v；游戏发往本机系统代理的请求仍会经由其他代理软件", err)
	}
}

func (c *Controller) logEndpoints(eps []sysproxy.Endpoint, starting bool) {
	switch {
	case len(eps) > 0:
		var names []string
		for _, e := range eps {
			names = append(names, e.String())
		}
		c.log.Printf(logx.App, "检测到系统代理 %s，游戏发往它的请求将由 MSBOOST 接管", strings.Join(names, "、"))
	case !starting:
		c.log.Printf(logx.App, "系统代理已关闭")
	}
}

// watchSystemProxy re-reads the system proxy until stopped and updates the
// rule when it changes (for example when v2rayN is switched on mid-session).
func (c *Controller) watchSystemProxy(t *proxyTakeover) {
	t.stop = make(chan struct{})
	t.done = make(chan struct{})
	go func() {
		defer close(t.done)
		tick := time.NewTicker(systemProxyRefresh)
		defer tick.Stop()
		for {
			select {
			case <-t.stop:
				return
			case <-tick.C:
			}
			eps := c.deps.SystemProxies()
			c.mu.Lock()
			if c.state == StateRunning && c.session != nil && c.session.takeover == t && !sysproxy.Equal(eps, t.endpoints) {
				t.endpoints = eps
				c.updateTakeoverLocked(t, proxiedProcesses(c.settings))
				c.setLoopbackPorts(c.red, eps)
				c.logEndpoints(eps, false)
			}
			c.mu.Unlock()
		}
	}()
}

// updateTakeoverLocked rewrites the rule for the current endpoints and the
// given processes. c.mu must be held.
func (c *Controller) updateTakeoverLocked(t *proxyTakeover, processes string) {
	hosts, ports, enabled := ruleTargets(t.endpoints)
	if err := c.red.EditTCPRule(t.ruleID, processes, hosts, ports, t.proxyID); err != nil {
		c.log.Printf(logx.App, "更新系统代理接管规则失败：%v", err)
		return
	}
	if enabled {
		c.red.EnableRule(t.ruleID)
	} else {
		c.red.DisableRule(t.ruleID)
	}
}

// stopWatching ends watchSystemProxy and waits for it.
func (t *proxyTakeover) stopWatching() {
	if t.stop != nil {
		close(t.stop)
		<-t.done
		t.stop = nil
	}
}

// stopTakeover removes what startTakeover added, except the rule, which Stop
// deletes with the others.
func (c *Controller) stopTakeover(red Redirector, t *proxyTakeover) {
	red.SetLocalhostProxyPorts(nil)
	red.DeleteProxy(t.proxyID)
	t.server.Close()
}

func (t *proxyTakeover) endpointNames() []string {
	var names []string
	for _, e := range t.endpoints {
		names = append(names, e.String())
	}
	return names
}
