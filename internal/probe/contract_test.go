package probe

// Contract test against the real node-side implementation
// (installers/node/tcp_probe.py in github.com/mozziexwz/node). It runs the
// server through testdata/probe_contract_server.py and talks to it through a
// fake local SOCKS5, so both ends of the v2 protocol are exercised for real.
//
// The test is skipped unless Python 3 and tcp_probe.py are available:
// MSBOOST_NODE_PROBE points at tcp_probe.py (CI downloads it at a pinned
// commit); locally a sibling checkout ../MSBOOST.DE is used when present.

import (
	"bufio"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nodeProbeScript(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("MSBOOST_NODE_PROBE"),
		filepath.Join("..", "..", "..", "MSBOOST.DE", "installers", "node", "tcp_probe.py"),
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	t.Skip("tcp_probe.py not found; set MSBOOST_NODE_PROBE")
	return ""
}

func python(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			if out, err := exec.Command(p, "--version").CombinedOutput(); err == nil && strings.HasPrefix(string(out), "Python 3") {
				return p
			}
		}
	}
	t.Skip("Python 3 not found")
	return ""
}

// startNodeProbe launches the real node service and returns its address.
func startNodeProbe(t *testing.T) string {
	t.Helper()
	script := nodeProbeScript(t)
	py := python(t)
	wrapper, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "probe_contract_server.py"))
	cmd := exec.Command(py, "-I", wrapper, script)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	ports := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ports <- strings.TrimSpace(line)
	}()
	select {
	case port := <-ports:
		if port == "" {
			t.Fatal("node probe server did not report a port")
		}
		return net.JoinHostPort("127.0.0.1", port)
	case <-time.After(10 * time.Second):
		t.Fatal("node probe server did not start")
	}
	return ""
}

// pipeTo forwards a SOCKS-connected session to the real node service.
func pipeTo(addr string) func(int, net.Conn) {
	return func(_ int, client net.Conn) {
		upstream, err := net.Dial("tcp4", addr)
		if err != nil {
			return
		}
		defer upstream.Close()
		done := make(chan struct{}, 2)
		go func() {
			io.Copy(upstream, client)
			if tc, ok := upstream.(*net.TCPConn); ok {
				tc.CloseWrite()
			}
			done <- struct{}{}
		}()
		go func() {
			io.Copy(client, upstream)
			done <- struct{}{}
		}()
		<-done
	}
}

func TestContractWithNodeProbe(t *testing.T) {
	addr := startNodeProbe(t)
	cases := []struct {
		name      string
		game      string
		port      int
		successes int
		reason    string
	}{
		{"connected", "8.8.8.8", 1, 3, ""},
		{"refused", "8.8.8.8", 2, 0, "游戏端口拒绝连接"},
		{"timeout", "8.8.8.8", 3, 0, "节点到游戏 TCP 连接超时"},
		{"unreachable", "8.8.8.8", 4, 0, "节点无法到达游戏服务器"},
		{"private target denied by node policy", "10.0.0.1", 8484, 0, "节点不允许该目标，请使用公网 IPv4 和有效端口"},
		{"loopback target denied by node policy", "127.0.0.1", 1, 0, "节点不允许该目标，请使用公网 IPv4 和有效端口"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			socks := startFakeSOCKS(t, "u", "p", pipeTo(addr))
			r := FullPathLatency(socks.proxy(), c.game, c.port, FullPathOptions{})
			if r.SuccessCount != c.successes || r.TotalCount != 3 {
				t.Fatalf("result %+v", r)
			}
			if c.reason != "" && (len(r.Errors) != 1 || r.Errors[0] != c.reason) {
				t.Fatalf("errors %v, want [%s]", r.Errors, c.reason)
			}
			if got := socks.sessions.Load(); got != 1 {
				t.Fatalf("sessions = %d: valid node replies must keep one session", got)
			}
		})
	}
}
