package probe

// Port of legacy/tests/Test-FullPathLatency.ps1, groups 1-16: the full-path
// probe against a loopback mock that plays both the local SOCKS5 proxy and
// the node-side v2 service. Groups 17-20 (the check page) live with the app
// orchestration tests. No real node or network is used.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	lGame = "51.222.56.192"
	lPort = 8484
)

type mockRecord struct {
	session       int
	operation     string
	nonce         string
	targetAddress string
	targetPort    int
}

// mockNode mirrors the C# MockServer of the PowerShell suite. Knobs are set
// through the constructor option before the listener starts accepting.
type mockNode struct {
	ln net.Listener
	wg sync.WaitGroup

	method         byte
	socksReplyCode byte
	greetingDelay  time.Duration
	socksDelay     time.Duration
	helloDelay     time.Duration
	connectDelay   time.Duration
	fragmentSize   int
	fragmentDelay  time.Duration
	connectDelays  []time.Duration
	helloMode      string
	helloModes     []string
	connectModes   []string

	accepted atomic.Int32
	hellos   atomic.Int32
	connects atomic.Int32

	mu           sync.Mutex
	clients      map[net.Conn]bool
	records      []mockRecord
	destinations []string
	faults       []string
}

var mockNonceRe = regexp.MustCompile(`\A[0-9a-f]{32}\z`)

func newMockNode(t *testing.T, configure func(*mockNode)) *mockNode {
	t.Helper()
	m := &mockNode{connectDelay: 70 * time.Millisecond, helloMode: "ok", clients: make(map[net.Conn]bool)}
	if configure != nil {
		configure(m)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m.ln = ln
	m.wg.Add(1)
	go m.acceptLoop()
	t.Cleanup(m.close)
	return m
}

func (m *mockNode) proxy() Proxy { return Proxy{Addr: m.ln.Addr().String()} }

func (m *mockNode) acceptLoop() {
	defer m.wg.Done()
	for {
		c, err := m.ln.Accept()
		if err != nil {
			return
		}
		session := int(m.accepted.Add(1)) - 1
		m.mu.Lock()
		m.clients[c] = true
		m.mu.Unlock()
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer func() {
				c.Close()
				m.mu.Lock()
				delete(m.clients, c)
				m.mu.Unlock()
			}()
			if err := m.handle(c, session); err != nil {
				m.mu.Lock()
				m.faults = append(m.faults, err.Error())
				m.mu.Unlock()
			}
		}()
	}
}

func (m *mockNode) close() {
	m.ln.Close()
	m.mu.Lock()
	for c := range m.clients {
		c.Close()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *mockNode) active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.clients)
}

func (m *mockNode) snapshot() ([]mockRecord, []string, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mockRecord(nil), m.records...), append([]string(nil), m.destinations...), append([]string(nil), m.faults...)
}

func (m *mockNode) handle(c net.Conn, session int) error {
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(c)
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(r, methods); err != nil {
		return nil
	}
	if head[0] != 5 || bytes.IndexByte(methods, 0) < 0 {
		return errors.New("expected the SOCKS5 no-auth greeting")
	}
	time.Sleep(m.greetingDelay)
	if _, err := c.Write([]byte{5, m.method}); err != nil || m.method != 0 {
		return nil
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(r, req); err != nil {
		return nil
	}
	if req[0] != 5 || req[1] != 1 || req[2] != 0 {
		return errors.New("expected a SOCKS CONNECT request")
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		io.ReadFull(r, ip)
		host = net.IP(ip).String()
	case 3:
		n, _ := r.ReadByte()
		name := make([]byte, n)
		io.ReadFull(r, name)
		host = string(name)
	case 4:
		ip := make([]byte, 16)
		io.ReadFull(r, ip)
		host = net.IP(ip).String()
	default:
		return errors.New("unknown SOCKS address type")
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(r, pb); err != nil {
		return nil
	}
	m.mu.Lock()
	m.destinations = append(m.destinations, fmt.Sprintf("%s:%d", host, int(pb[0])<<8|int(pb[1])))
	m.mu.Unlock()
	time.Sleep(m.socksDelay)
	if _, err := c.Write([]byte{5, m.socksReplyCode, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil || m.socksReplyCode != 0 {
		return nil
	}

	node := fmt.Sprintf("%032x", session+1)
	greeted := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil
		}
		if len(line) > 4097 {
			return errors.New("the client request exceeds its frame limit")
		}
		text := strings.TrimSuffix(line, "\n")
		if strings.Contains(text, "\\") || strings.HasPrefix(text, "\xef\xbb\xbf") {
			return errors.New("requests must use unescaped ASCII fields and no BOM")
		}
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		var request map[string]any
		if err := dec.Decode(&request); err != nil || request["version"] != json.Number("2") {
			return errors.New("expected protocol version 2")
		}
		op, _ := request["operation"].(string)
		nonce, _ := request["nonce"].(string)
		if !mockNonceRe.MatchString(nonce) {
			return errors.New("each request needs a lowercase 32-hex nonce")
		}
		if op != "hello" && op != "connect" {
			return errors.New("unknown request operation")
		}
		want := 3
		if op == "connect" {
			want = 5
		}
		if len(request) != want {
			return errors.New("unexpected request fields")
		}
		rec := mockRecord{session: session, operation: op, nonce: nonce}
		if op == "connect" {
			rec.targetAddress, _ = request["target_address"].(string)
			port, ok := request["target_port"].(json.Number)
			if !ok {
				return errors.New("target port must be an integer")
			}
			if _, err := fmt.Sscanf(string(port), "%d", &rec.targetPort); err != nil || strings.ContainsAny(string(port), ".eE") {
				return errors.New("target port must be an integer")
			}
		}
		m.mu.Lock()
		m.records = append(m.records, rec)
		m.mu.Unlock()

		if op == "hello" {
			if greeted {
				return errors.New("a healthy session must not repeat its hello")
			}
			greeted = true
			m.hellos.Add(1)
			time.Sleep(m.helloDelay)
			mode := m.helloMode
			if session < len(m.helloModes) {
				mode = m.helloModes[session]
			}
			if !m.respond(c, rec, node, mode) {
				return nil
			}
			continue
		}
		if !greeted {
			return errors.New("connect requires a successful session hello first")
		}
		index := int(m.connects.Add(1)) - 1
		delay := m.connectDelay
		if index < len(m.connectDelays) {
			delay = m.connectDelays[index]
		}
		time.Sleep(delay)
		mode := "ok"
		if index < len(m.connectModes) {
			mode = m.connectModes[index]
		}
		if !m.respond(c, rec, node, mode) {
			return nil
		}
	}
}

type field struct{ key, raw string }

func quoted(s string) string { return `"` + s + `"` }

// respond writes the reply for mode; false ends the session.
func (m *mockNode) respond(c net.Conn, rec mockRecord, node, mode string) bool {
	switch mode {
	case "eof":
		return false
	case "invalid-utf8":
		m.write(c, []byte{0xc3, 0x28, 0x0a})
		return true
	case "oversized":
		m.write(c, []byte(strings.Repeat(" ", 4097)+"\n"))
		return true
	}
	fields := []field{{"version", "2"}, {"operation", quoted(rec.operation)}, {"nonce", quoted(rec.nonce)}}
	if rec.operation == "connect" {
		fields = append(fields, field{"target_address", quoted(rec.targetAddress)}, field{"target_port", fmt.Sprint(rec.targetPort)})
	}
	fields = append(fields, field{"node_id", quoted(node)}, field{"success", "true"})
	set := func(key, raw string) {
		for i := range fields {
			if fields[i].key == key {
				fields[i].raw = raw
				return
			}
		}
		fields = append(fields, field{key, raw})
	}
	remove := func(key string) {
		for i := range fields {
			if fields[i].key == key {
				fields = append(fields[:i], fields[i+1:]...)
				return
			}
		}
	}
	switch {
	case strings.HasPrefix(mode, "error:"):
		set("success", "false")
		set("error", quoted(strings.TrimPrefix(mode, "error:")))
	case mode == "bad-node":
		set("node_id", quoted(strings.Repeat("f", 32)))
	case mode == "bad-node-format":
		set("node_id", quoted("not-a-node"))
	case mode == "bad-nonce":
		set("nonce", quoted(strings.Repeat("f", 32)))
	case mode == "bad-target":
		set("target_address", quoted("51.222.56.193"))
	case mode == "bad-target-port":
		set("target_port", "8485")
	case mode == "bad-operation":
		set("operation", quoted("other"))
	case mode == "bad-version":
		set("version", "1")
	case mode == "version-type":
		set("version", quoted("2"))
	case mode == "boolean-type":
		set("success", quoted("true"))
	case mode == "port-type":
		set("target_port", quoted("8484"))
	case mode == "unknown-field":
		set("milliseconds", "1")
	case mode == "nested":
		set("success", `{"value":true}`)
	case mode == "error-with-success":
		set("error", quoted("timeout"))
	case mode == "missing-node":
		remove("node_id")
	case mode == "missing-target":
		remove("target_address")
	case mode == "missing-error":
		set("success", "false")
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = quoted(f.key) + ":" + f.raw
	}
	text := "{" + strings.Join(parts, ",") + "}"
	switch mode {
	case "duplicate":
		text = text[:1] + `"version":2,` + text[1:]
	case "escaped":
		escaped := "\\" + "u" + fmt.Sprintf("%04x", rec.nonce[0]) + rec.nonce[1:]
		text = strings.Replace(text, `"nonce":"`+rec.nonce+`"`, `"nonce":"`+escaped+`"`, 1)
	}
	response := []byte(text + "\n")
	if mode == "bom" {
		response = append([]byte{0xef, 0xbb, 0xbf}, response...)
	}
	if mode == "truncate" {
		c.Write(response[:len(response)/2])
		return false
	}
	m.write(c, response)
	return true
}

func (m *mockNode) write(c net.Conn, b []byte) {
	if m.fragmentSize <= 0 {
		c.Write(b)
		return
	}
	for off := 0; off < len(b); off += m.fragmentSize {
		end := off + m.fragmentSize
		if end > len(b) {
			end = len(b)
		}
		if _, err := c.Write(b[off:end]); err != nil {
			return
		}
		if end < len(b) {
			time.Sleep(m.fragmentDelay)
		}
	}
}

func runProbe(m *mockNode, attempt, warmup time.Duration, game string, port int) Result {
	return FullPathLatency(m.proxy(), game, port, FullPathOptions{Count: 3, AttemptTimeout: attempt, WarmupTimeout: warmup})
}

func probeDefault(m *mockNode) Result {
	return runProbe(m, 1200*time.Millisecond, 2*time.Second, lGame, lPort)
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func assertClean(t *testing.T, m *mockNode) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for m.active() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := m.active(); n != 0 {
		t.Fatalf("every session socket must close after measurement; %d still open", n)
	}
	if _, _, faults := m.snapshot(); len(faults) > 0 {
		t.Fatalf("mock protocol faults: %s", strings.Join(faults, "; "))
	}
}

func assertFailed(t *testing.T, r Result) {
	t.Helper()
	if r.SuccessCount != 0 {
		t.Fatalf("an invalid or failed probe must not produce successes: %+v", r)
	}
	if r.Average != 0 {
		t.Fatal("a failed probe must not manufacture a numeric latency")
	}
	if len(r.Errors) == 0 {
		t.Fatal("a failed probe needs an error reason")
	}
}

func TestLatencyWarmSessionThreeProbes(t *testing.T) {
	m := newMockNode(t, nil)
	r := probeDefault(m)
	if r.SuccessCount != 3 || r.TotalCount != 3 || len(r.Samples) != 3 {
		t.Fatalf("result %+v", r)
	}
	if m.accepted.Load() != 1 || m.hellos.Load() != 1 || m.connects.Load() != 3 {
		t.Fatalf("accepted=%d hellos=%d connects=%d", m.accepted.Load(), m.hellos.Load(), m.connects.Load())
	}
	records, destinations, _ := m.snapshot()
	if strings.Join(destinations, ",") != "127.0.0.1:20424" {
		t.Fatalf("SOCKS destinations %v, want the node-side 127.0.0.1:20424 only", destinations)
	}
	nonces := make(map[string]bool)
	for _, rec := range records {
		nonces[rec.nonce] = true
		if rec.operation == "connect" && (rec.targetAddress != lGame || rec.targetPort != lPort) {
			t.Fatalf("connect target %s:%d", rec.targetAddress, rec.targetPort)
		}
	}
	if len(nonces) != 4 {
		t.Fatalf("hello and every connect need unique nonces; got %d unique", len(nonces))
	}
	if avg := ms(r.Average); avg < 55 || avg >= 350 {
		t.Fatalf("average %.1f ms, want the client-observed connect response time", avg)
	}
	assertClean(t, m)
}

func TestLatencyWarmupExcluded(t *testing.T) {
	m := newMockNode(t, func(m *mockNode) {
		m.greetingDelay = 120 * time.Millisecond
		m.socksDelay = 120 * time.Millisecond
		m.helloDelay = 350 * time.Millisecond
		m.connectDelay = 100 * time.Millisecond
	})
	start := time.Now()
	r := runProbe(m, 1200*time.Millisecond, 2*time.Second, lGame, lPort)
	if r.SuccessCount != 3 {
		t.Fatalf("result %+v", r)
	}
	if time.Since(start) < 850*time.Millisecond {
		t.Fatal("the warmup and server response delays must actually have elapsed")
	}
	if avg := ms(r.Average); avg < 85 || avg >= 300 {
		t.Fatalf("average %.1f ms must contain only measured connect replies", avg)
	}
	assertClean(t, m)
}

func TestLatencyChannelDelayChangesResult(t *testing.T) {
	fast := newMockNode(t, func(m *mockNode) { m.connectDelay = 60 * time.Millisecond })
	slow := newMockNode(t, func(m *mockNode) { m.connectDelay = 320 * time.Millisecond })
	a, b := probeDefault(fast), probeDefault(slow)
	if a.SuccessCount != 3 || b.SuccessCount != 3 {
		t.Fatalf("fast %+v slow %+v", a, b)
	}
	if avg := ms(a.Average); avg < 45 || avg >= 220 {
		t.Fatalf("fast average %.1f ms", avg)
	}
	if avg := ms(b.Average); avg < 300 || avg >= 550 {
		t.Fatalf("slow average %.1f ms", avg)
	}
	if ms(b.Average)-ms(a.Average) < 180 {
		t.Fatal("changing the complete channel delay must materially change the latency")
	}
	assertClean(t, fast)
	assertClean(t, slow)
}

func TestLatencyFragmentedResponses(t *testing.T) {
	m := newMockNode(t, func(m *mockNode) {
		m.connectDelay = 40 * time.Millisecond
		m.fragmentSize = 25
		m.fragmentDelay = 15 * time.Millisecond
	})
	r := probeDefault(m)
	if r.SuccessCount != 3 {
		t.Fatalf("result %+v", r)
	}
	if ms(r.Average) < 110 {
		t.Fatalf("average %.1f ms: timing must run through the final newline", ms(r.Average))
	}
	if m.accepted.Load() != 1 {
		t.Fatal("fragmentation must not invalidate a healthy session")
	}
	assertClean(t, m)
}

func TestLatencyLocalJSONWorkExcluded(t *testing.T) {
	origRequest, origDecode := newConnectRequest, decodeReply
	t.Cleanup(func() { newConnectRequest, decodeReply = origRequest, origDecode })
	newConnectRequest = func(nonce, addr string, port int) []byte {
		time.Sleep(140 * time.Millisecond)
		return origRequest(nonce, addr, port)
	}
	decodeReply = func(line []byte) (replyFields, error) {
		time.Sleep(140 * time.Millisecond)
		return origDecode(line)
	}
	m := newMockNode(t, func(m *mockNode) { m.connectDelay = 60 * time.Millisecond })
	start := time.Now()
	r := probeDefault(m)
	if r.SuccessCount != 3 {
		t.Fatalf("result %+v", r)
	}
	// 3 slowed requests + 4 slowed decodes (hello and three connects) + 3 x 60 ms.
	if time.Since(start) < 1100*time.Millisecond {
		t.Fatal("the deliberately slow serialization and parsing must have run")
	}
	if avg := ms(r.Average); avg < 45 || avg >= 180 {
		t.Fatalf("average %.1f ms must exclude serialization and validation", avg)
	}
	assertClean(t, m)
}

func TestLatencyValidFailureKeepsSession(t *testing.T) {
	for _, code := range []string{"timeout", "refused", "unreachable", "target_not_allowed", "failed"} {
		t.Run(code, func(t *testing.T) {
			m := newMockNode(t, func(m *mockNode) { m.connectModes = []string{"error:" + code, "ok", "ok"} })
			r := probeDefault(m)
			if r.SuccessCount != 2 || r.TotalCount != 3 {
				t.Fatalf("result %+v", r)
			}
			if r.Samples[0].OK || r.Samples[0].Duration != 0 || r.Samples[0].Err == "" {
				t.Fatalf("the failure needs a visible reason and no timing: %+v", r.Samples[0])
			}
			if m.accepted.Load() != 1 || m.hellos.Load() != 1 || m.connects.Load() != 3 {
				t.Fatalf("accepted=%d hellos=%d connects=%d", m.accepted.Load(), m.hellos.Load(), m.connects.Load())
			}
			assertClean(t, m)
		})
	}
}

func TestLatencyAllValidFailures(t *testing.T) {
	m := newMockNode(t, func(m *mockNode) {
		m.connectModes = []string{"error:timeout", "error:refused", "error:target_not_allowed"}
	})
	r := probeDefault(m)
	assertFailed(t, r)
	if r.TotalCount != 3 || m.accepted.Load() != 1 {
		t.Fatalf("result %+v accepted=%d", r, m.accepted.Load())
	}
	assertClean(t, m)
}

func TestLatencyInvalidConnectRepliesReconnect(t *testing.T) {
	modes := []string{"bad-node", "bad-node-format", "bad-nonce", "bad-target", "bad-target-port", "bad-operation",
		"bad-version", "version-type", "boolean-type", "port-type", "duplicate", "unknown-field", "nested",
		"error-with-success", "missing-node", "missing-target", "missing-error", "error:unknown",
		"escaped", "bom", "oversized", "invalid-utf8", "eof", "truncate"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			m := newMockNode(t, func(m *mockNode) {
				m.connectDelay = 5 * time.Millisecond
				m.connectModes = []string{mode, "ok", "ok"}
			})
			r := probeDefault(m)
			if r.SuccessCount != 2 || r.TotalCount != 3 || r.Samples[0].OK {
				t.Fatalf("must fail exactly the corrupted attempt: %+v", r)
			}
			if m.accepted.Load() != 2 || m.hellos.Load() != 2 || m.connects.Load() != 3 {
				t.Fatalf("accepted=%d hellos=%d connects=%d", m.accepted.Load(), m.hellos.Load(), m.connects.Load())
			}
			assertClean(t, m)
		})
	}
}

func TestLatencyInitialHelloFailureStops(t *testing.T) {
	for _, mode := range []string{"bad-node-format", "bad-nonce", "bad-version", "duplicate", "unknown-field", "nested", "eof", "invalid-utf8"} {
		t.Run(mode, func(t *testing.T) {
			m := newMockNode(t, func(m *mockNode) { m.helloMode = mode })
			r := probeDefault(m)
			assertFailed(t, r)
			if m.accepted.Load() != 1 || m.hellos.Load() != 1 || m.connects.Load() != 0 {
				t.Fatalf("accepted=%d hellos=%d connects=%d", m.accepted.Load(), m.hellos.Load(), m.connects.Load())
			}
			assertClean(t, m)
		})
	}
}

func TestLatencyFailedReconnectHelloKeepsPriorSamples(t *testing.T) {
	m := newMockNode(t, func(m *mockNode) {
		m.connectModes = []string{"ok", "bad-nonce", "ok"}
		m.helloModes = []string{"ok", "bad-node-format"}
	})
	r := probeDefault(m)
	if r.SuccessCount != 1 || ms(r.Average) < 55 {
		t.Fatalf("the first valid sample must survive: %+v", r)
	}
	if m.accepted.Load() != 2 || m.hellos.Load() != 2 || m.connects.Load() != 2 {
		t.Fatalf("accepted=%d hellos=%d connects=%d", m.accepted.Load(), m.hellos.Load(), m.connects.Load())
	}
	assertClean(t, m)
}

func TestLatencyLateRepliesCannotSatisfyNextRequest(t *testing.T) {
	m := newMockNode(t, func(m *mockNode) {
		m.connectDelays = []time.Duration{380 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond}
	})
	r := runProbe(m, 180*time.Millisecond, 2*time.Second, lGame, lPort)
	if r.SuccessCount != 2 || r.Samples[0].OK || !r.Samples[1].OK || !r.Samples[2].OK {
		t.Fatalf("result %+v", r)
	}
	if m.accepted.Load() != 2 || m.hellos.Load() != 2 {
		t.Fatalf("accepted=%d hellos=%d", m.accepted.Load(), m.hellos.Load())
	}
	if avg := ms(r.Average); avg < 25 || avg >= 180 {
		t.Fatalf("average %.1f ms: late data must not contaminate the next sample", avg)
	}
	assertClean(t, m)
}

func TestLatencyDeadlineCoversFragmentedResponse(t *testing.T) {
	m := newMockNode(t, func(m *mockNode) {
		m.connectDelay = 10 * time.Millisecond
		m.fragmentSize = 60
		m.fragmentDelay = 75 * time.Millisecond
	})
	start := time.Now()
	r := runProbe(m, 160*time.Millisecond, 1200*time.Millisecond, lGame, lPort)
	assertFailed(t, r)
	if m.connects.Load() != 3 || m.accepted.Load() != 3 {
		t.Fatalf("connects=%d accepted=%d", m.connects.Load(), m.accepted.Load())
	}
	if time.Since(start) >= 2200*time.Millisecond {
		t.Fatal("fragmentation must not turn bounded attempts into unbounded reads")
	}
	assertClean(t, m)
}

func TestLatencyWarmupSharesOneDeadline(t *testing.T) {
	m := newMockNode(t, func(m *mockNode) {
		m.greetingDelay = 110 * time.Millisecond
		m.socksDelay = 110 * time.Millisecond
		m.helloDelay = 180 * time.Millisecond
	})
	start := time.Now()
	r := runProbe(m, 1200*time.Millisecond, 280*time.Millisecond, lGame, lPort)
	assertFailed(t, r)
	if m.accepted.Load() != 1 || m.connects.Load() != 0 {
		t.Fatalf("accepted=%d connects=%d", m.accepted.Load(), m.connects.Load())
	}
	if time.Since(start) >= 600*time.Millisecond {
		t.Fatal("do not restart the warmup deadline for each phase")
	}
	assertClean(t, m)
}

func TestLatencyUnavailableProxyAndAuthentication(t *testing.T) {
	ln, _ := net.Listen("tcp4", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	assertFailed(t, FullPathLatency(Proxy{Addr: addr}, lGame, lPort, FullPathOptions{WarmupTimeout: 400 * time.Millisecond}))
	for _, method := range []byte{2, 0xFF} {
		t.Run(fmt.Sprintf("method %#x", method), func(t *testing.T) {
			m := newMockNode(t, func(m *mockNode) { m.method = method })
			r := probeDefault(m)
			assertFailed(t, r)
			if !strings.Contains(strings.Join(r.Errors, " "), "认证") {
				t.Fatalf("authentication failure must stay explicit: %v", r.Errors)
			}
			if m.accepted.Load() != 1 || m.hellos.Load() != 0 {
				t.Fatalf("accepted=%d hellos=%d", m.accepted.Load(), m.hellos.Load())
			}
			assertClean(t, m)
		})
	}
}

func TestLatencyTargetValidationBeforeProxy(t *testing.T) {
	m := newMockNode(t, nil)
	for _, addr := range []string{"example.com", "::1", "127.1", "051.222.56.192", "51.222.56.192 ", ""} {
		assertFailed(t, runProbe(m, 1200*time.Millisecond, 2*time.Second, addr, lPort))
	}
	for _, port := range []int{0, -1, 65536} {
		assertFailed(t, runProbe(m, 1200*time.Millisecond, 2*time.Second, lGame, port))
	}
	if m.accepted.Load() != 0 {
		t.Fatal("invalid targets must not open any proxy connection")
	}
}

func TestLatencyPrivateTargetReachesNodePolicy(t *testing.T) {
	m := newMockNode(t, func(m *mockNode) {
		m.connectModes = []string{"error:target_not_allowed", "error:target_not_allowed", "error:target_not_allowed"}
	})
	r := runProbe(m, 1200*time.Millisecond, 2*time.Second, "10.0.0.1", lPort)
	assertFailed(t, r)
	if m.connects.Load() != 3 {
		t.Fatal("the node, not a client substitution, decides target policy")
	}
	records, _, _ := m.snapshot()
	for _, rec := range records {
		if rec.operation == "connect" && rec.targetAddress != "10.0.0.1" {
			t.Fatalf("a denied target was rewritten to %s", rec.targetAddress)
		}
	}
	assertClean(t, m)
}
