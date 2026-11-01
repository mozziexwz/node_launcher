package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mozziexwz/node_launcher/internal/engine"
	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/probe"
	"github.com/mozziexwz/node_launcher/internal/profiles"
	"github.com/mozziexwz/node_launcher/internal/store"
	"github.com/mozziexwz/node_launcher/internal/sysproxy"
)

// ---- fakes ----

type fakeEngine struct {
	addr     *net.TCPAddr
	user     string
	password string
	mu       sync.Mutex
	closed   int
}

func (e *fakeEngine) Addr() *net.TCPAddr           { return e.addr }
func (e *fakeEngine) Credential() (string, string) { return e.user, e.password }
func (e *fakeEngine) Stats() engine.Stats {
	return engine.Stats{Accepted: 5, Active: 2, Upload: 3072, Download: 1 << 20, UploadRate: 100, DownloadRate: 2048}
}
func (e *fakeEngine) Close() error    { e.mu.Lock(); e.closed++; e.mu.Unlock(); return nil }
func (e *fakeEngine) closeCount() int { e.mu.Lock(); defer e.mu.Unlock(); return e.closed }

type fakeRedirector struct {
	mu           sync.Mutex
	calls        []string
	nextID       uint32
	failSOCKS    bool
	failRuleCall int // 1-based AddProxyRule call that fails; 0 = never
	ruleCalls    int
	startFails   bool
	rules        map[uint32]string
	proxies      map[uint32]bool

	// TraceConnections is kept out of calls so the existing call sequences
	// stay as they are; traceLog records where tracing began and ended.
	traceFn  func(ConnEvent)
	traceLog []string
}

func newFakeRedirector() *fakeRedirector {
	return &fakeRedirector{rules: make(map[uint32]string), proxies: make(map[uint32]bool)}
}

func (r *fakeRedirector) record(s string) { r.calls = append(r.calls, s) }

func (r *fakeRedirector) Prepare() { r.mu.Lock(); r.record("prepare"); r.mu.Unlock() }

func (r *fakeRedirector) AddSOCKS5(host string, port uint16, user, password string, sendDomain bool) (uint32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(fmt.Sprintf("add-socks5 %s:%d %s domain=%v", host, port, user, sendDomain))
	if r.failSOCKS {
		return 0, errors.New("socks5 rejected")
	}
	r.nextID++
	r.proxies[r.nextID] = true
	return r.nextID, nil
}

func (r *fakeRedirector) DeleteProxy(id uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(fmt.Sprintf("delete-proxy %d", id))
	ok := r.proxies[id]
	delete(r.proxies, id)
	return ok
}

func (r *fakeRedirector) AddProxyRule(processes string, proxyID uint32) (uint32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ruleCalls++
	r.record("add-rule " + processes)
	if r.ruleCalls == r.failRuleCall {
		return 0, errors.New("rule rejected")
	}
	r.nextID++
	r.rules[r.nextID] = processes
	return r.nextID, nil
}

func (r *fakeRedirector) AddTCPRule(processes, hosts, ports string, proxyID uint32) (uint32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(fmt.Sprintf("add-tcp-rule %s -> %s:%s via %d", processes, hosts, ports, proxyID))
	r.nextID++
	r.rules[r.nextID] = processes
	return r.nextID, nil
}

func (r *fakeRedirector) EditTCPRule(id uint32, processes, hosts, ports string, proxyID uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(fmt.Sprintf("edit-tcp-rule %d %s -> %s:%s via %d", id, processes, hosts, ports, proxyID))
	if _, ok := r.rules[id]; !ok {
		return errors.New("no such rule")
	}
	r.rules[id] = processes
	return nil
}

func (r *fakeRedirector) EnableRule(id uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(fmt.Sprintf("enable-rule %d", id))
	return true
}

func (r *fakeRedirector) DisableRule(id uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(fmt.Sprintf("disable-rule %d", id))
	return true
}

func (r *fakeRedirector) SetLocalhostProxyPorts(ports []uint16) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(fmt.Sprintf("localhost-ports %v", ports))
	return nil
}

func (r *fakeRedirector) DeleteRule(id uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(fmt.Sprintf("delete-rule %d", id))
	_, ok := r.rules[id]
	delete(r.rules, id)
	return ok
}

func (r *fakeRedirector) Start() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record("start")
	return !r.startFails
}

func (r *fakeRedirector) Stop() bool { r.mu.Lock(); r.record("stop"); r.mu.Unlock(); return true }

func (r *fakeRedirector) lastCall() string {
	if len(r.calls) == 0 {
		return "nothing"
	}
	return r.calls[len(r.calls)-1]
}

func (r *fakeRedirector) TraceConnections(fn func(ConnEvent)) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.traceFn = fn
	r.traceLog = append(r.traceLog, "on after "+r.lastCall())
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.traceFn = nil
		r.traceLog = append(r.traceLog, "off after "+r.lastCall())
	}
}

// emit delivers a connection decision the way the core's callback would.
func (r *fakeRedirector) emit(ev ConnEvent) {
	r.mu.Lock()
	fn := r.traceFn
	r.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

func (r *fakeRedirector) traces() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.traceLog...)
}

func (r *fakeRedirector) snapshot() ([]string, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...), len(r.rules), len(r.proxies)
}

type fakeSystem struct {
	elevated    bool
	conflicts   []Conflict
	running     []string
	driverStops int
	cleanups    int
	unloading   int // DriverUnloading answers true this many times; -1 forever
	mu          sync.Mutex
}

func (s *fakeSystem) DriverUnloading() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unloading == 0 {
		return false
	}
	if s.unloading > 0 {
		s.unloading--
	}
	return true
}

func (s *fakeSystem) Elevated() bool                     { return s.elevated }
func (s *fakeSystem) Conflicts() []Conflict              { return s.conflicts }
func (s *fakeSystem) RunningProcesses([]string) []string { return s.running }
func (s *fakeSystem) StopDriver() (string, error)        { s.driverStops++; return "STOPPED", nil }
func (s *fakeSystem) Cleanup(string) []string            { s.cleanups++; return []string{"done"} }
func (s *fakeSystem) FixConflict(fix string) ([]string, error) {
	if fix != "proxybridge" {
		return nil, errors.New("unknown fix")
	}
	s.conflicts = nil
	return []string{"stopped"}, nil
}

type harness struct {
	c        *Controller
	layout   store.Layout
	red      *fakeRedirector
	sys      *fakeSystem
	log      *logx.Logger
	engines  []*fakeEngine
	loads    int
	engErr   error
	checkLog []string
	mu       sync.Mutex

	// startedWith is the configuration of every core start; helloErr is
	// what the UDP start's hello returns.
	startedWith []*engine.Config
	helloErr    error

	runtimeErr error
	runtimeLog []string // "lock", then "unlock after <last redirector call>"

	// proxies, when not nil, enables the system proxy takeover.
	proxies func() []sysproxy.Endpoint

	// environment is Deps.Environment; observe makes the engines report
	// their connections (observedEngine).
	environment func() []string
	observe     bool
	observed    []*observedEngine
}

// observedEngine reports connections like engine.Engine does.
type observedEngine struct {
	*fakeEngine
	omu     sync.Mutex
	observe func(engine.Request)
}

func (e *observedEngine) Observe(fn func(engine.Request)) {
	e.omu.Lock()
	e.observe = fn
	e.omu.Unlock()
}

func (e *observedEngine) report(r engine.Request) {
	e.omu.Lock()
	fn := e.observe
	e.omu.Unlock()
	if fn != nil {
		fn(r)
	}
}

// Stats reports no open connections once closed, as the real engine does.
func (e *observedEngine) Stats() engine.Stats {
	st := e.fakeEngine.Stats()
	if e.closeCount() > 0 {
		st.Active = 0
	}
	return st
}

func exampleConfig(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "client-example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func writeConfig(t *testing.T, layout store.Layout, name string, m map[string]any) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.Configs, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newHarness(t *testing.T, mutate func(*harness)) *harness {
	t.Helper()
	h := &harness{red: newFakeRedirector(), sys: &fakeSystem{elevated: true}}
	h.layout = store.NewLayout(t.TempDir())
	if err := h.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, h.layout, "直连.json", exampleConfig(t))
	settings := store.DefaultSettings()
	settings.CurrentConfig = "直连.json"
	if err := h.layout.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	h.log, _ = logx.New("", 0)
	if mutate != nil {
		mutate(h)
	}
	h.c = New(Deps{
		Layout: h.layout,
		Log:    h.log,
		StartEngine: func(cfg *engine.Config) (CoreEngine, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.engErr != nil {
				return nil, h.engErr
			}
			h.startedWith = append(h.startedWith, cfg)
			e := &fakeEngine{
				addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000 + len(h.engines)},
				user: fmt.Sprintf("session-user-%d", len(h.engines)), password: fmt.Sprintf("session-pass-%d", len(h.engines)),
			}
			h.engines = append(h.engines, e)
			if h.observe {
				oe := &observedEngine{fakeEngine: e}
				h.observed = append(h.observed, oe)
				return oe, nil
			}
			return e, nil
		},
		LoadRedirector: func() (Redirector, error) { h.loads++; return h.red, nil },
		PrepareRuntime: func() (func(), error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.runtimeErr != nil {
				return nil, h.runtimeErr
			}
			h.runtimeLog = append(h.runtimeLog, "lock")
			return func() {
				calls, _, _ := h.red.snapshot()
				last := "nothing"
				if len(calls) > 0 {
					last = calls[len(calls)-1]
				}
				h.mu.Lock()
				h.runtimeLog = append(h.runtimeLog, "unlock after "+last)
				h.mu.Unlock()
			}, nil
		},
		SystemProxies: h.proxies,
		Environment:   h.environment,
		System:        h.sys,
		Checks:        h.fakeChecks(),
	})
	return h
}

func (h *harness) allEnginesClosed(t *testing.T) {
	t.Helper()
	for i, e := range h.engines {
		if e.closeCount() != 1 {
			t.Fatalf("engine %d closed %d times, want exactly once", i, e.closeCount())
		}
	}
}

// ---- start / stop ----

func TestStartStopHappyPath(t *testing.T) {
	h := newHarness(t, nil)
	var states []State
	cancel := h.c.Watch(func(s Status) { states = append(states, s.State) })
	defer cancel()

	warnings, err := h.c.Start()
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Start: warnings=%v err=%v", warnings, err)
	}
	st := h.c.Status()
	if st.State != StateRunning || st.Config != "直连.json" || st.Accepted != 5 || st.Active != 2 || st.Since.IsZero() {
		t.Fatalf("status %+v", st)
	}
	if st.Upload != 3072 || st.Download != 1<<20 || st.UploadRate != 100 || st.DownloadRate != 2048 {
		t.Fatalf("traffic %+v", st)
	}
	calls, _, _ := h.red.snapshot()
	game := profiles.DefaultMapleStory().ProcessList()
	want := []string{"prepare", "add-socks5 127.0.0.1:40000 session-user-0 domain=true", "add-rule " + game, "start"}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}

	if err := h.c.Stop(); err != nil {
		t.Fatal(err)
	}
	calls, rules, proxies := h.red.snapshot()
	tail := calls[len(want):]
	if strings.Join(tail, ",") != "stop,delete-rule 2,delete-proxy 1" || rules != 0 || proxies != 0 {
		t.Fatalf("stop sequence %v rules=%d proxies=%d", tail, rules, proxies)
	}
	if h.sys.driverStops != 1 || h.c.Status().State != StateIdle {
		t.Fatalf("driver stops %d state %s", h.sys.driverStops, h.c.Status().State)
	}
	h.allEnginesClosed(t)
	if got := fmt.Sprint(states); got != "[starting running stopping idle]" {
		t.Fatalf("observed states %s", got)
	}
	if h.loads != 1 {
		t.Fatalf("redirector loaded %d times", h.loads)
	}
}

func TestStartWithAddonsAndWarnings(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		s, _ := h.layout.LoadSettings()
		s.Addons["discord"] = true
		h.layout.SaveSettings(s)
		h.sys.conflicts = []Conflict{{Message: "检测到 Netch 正在运行", Blocking: false}}
		h.sys.running = []string{"MapleStory.exe"}
	})
	warnings, err := h.c.Start()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "Netch") || !strings.Contains(warnings[1], "MapleStory.exe") {
		t.Fatalf("warnings %v", warnings)
	}
	calls, rules, _ := h.red.snapshot()
	discord, _ := findAddon("discord")
	if rules != 2 || calls[3] != "add-rule "+discord.ProcessList() {
		t.Fatalf("calls %v", calls)
	}
	h.c.Stop()
}

func TestStartRollsBackEveryStep(t *testing.T) {
	cases := []struct {
		name         string
		setup        func(*harness)
		wantErr      string
		wantEngines  int
		wantRedLoads int
	}{
		{"not elevated", func(h *harness) { h.sys.elevated = false }, "管理员", 0, 0},
		{"blocking conflict", func(h *harness) {
			h.sys.conflicts = []Conflict{{Message: "检测到 ProxyBridge 正在运行，请先退出它", Blocking: true}}
		}, "ProxyBridge", 0, 0},
		{"no config selected", func(h *harness) {
			s, _ := h.layout.LoadSettings()
			s.CurrentConfig = ""
			h.layout.SaveSettings(s)
		}, "请先选择节点配置", 0, 0},
		{"broken config", func(h *harness) {
			os.WriteFile(filepath.Join(h.layout.Configs, "直连.json"), []byte("{"), 0o644)
		}, "节点配置", 0, 0},
		{"core fails", func(h *harness) { h.engErr = errors.New("MSBOOST 核心启动失败：boom") }, "核心启动失败", 0, 0},
		{"proxy config rejected", func(h *harness) { h.red.failSOCKS = true }, "socks5 rejected", 1, 1},
		{"game rule rejected", func(h *harness) { h.red.failRuleCall = 1 }, "rule rejected", 1, 1},
		{"addon rule rejected", func(h *harness) {
			s, _ := h.layout.LoadSettings()
			s.Addons["steam"] = true
			h.layout.SaveSettings(s)
			h.red.failRuleCall = 2
		}, "rule rejected", 1, 1},
		{"interception fails", func(h *harness) { h.red.startFails = true }, "分流启动失败", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.setup)
			_, err := h.c.Start()
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, c.wantErr)
			}
			st := h.c.Status()
			if st.State != StateIdle || st.LastError == "" {
				t.Fatalf("status after failure %+v", st)
			}
			if len(h.engines) != c.wantEngines || h.loads != c.wantRedLoads {
				t.Fatalf("engines=%d loads=%d", len(h.engines), h.loads)
			}
			h.allEnginesClosed(t)
			calls, rules, proxies := h.red.snapshot()
			if rules != 0 || proxies != 0 {
				t.Fatalf("leftover rules=%d proxies=%d after rollback; calls %v", rules, proxies, calls)
			}
			for _, call := range calls {
				if call == "stop" {
					t.Fatal("a failed start must not call Stop on interception that never started")
				}
			}
			// The session secrets are no longer masked once the start failed.
			h.log.Printf(logx.App, "probe session-pass-0")
			if lines := h.log.Recent(1); len(lines) == 1 && !strings.Contains(lines[0].Text, "session-pass-0") {
				t.Fatal("secrets of a failed start must be released")
			}
		})
	}
}

func TestSecretsMaskedOnlyDuringSession(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	h.log.Printf(logx.Core, "auth session-pass-0 node test-password")
	if text := h.log.Recent(1)[0].Text; strings.Contains(text, "session-pass-0") || strings.Contains(text, "test-password") {
		t.Fatalf("secrets leaked into the log: %q", text)
	}
	h.c.Stop()
	h.log.Printf(logx.Core, "after session-pass-0")
	if text := h.log.Recent(1)[0].Text; !strings.Contains(text, "session-pass-0") {
		t.Fatalf("old session secrets should be released after stop: %q", text)
	}
}

func TestAddonSwitchesLive(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	discord, _ := findAddon("discord")
	if err := h.c.SetAddon("discord", true); err != nil {
		t.Fatal(err)
	}
	calls, rules, _ := h.red.snapshot()
	if calls[len(calls)-1] != "add-rule "+discord.ProcessList() || rules != 2 {
		t.Fatalf("calls %v", calls)
	}
	if err := h.c.SetAddon("discord", true); err != nil {
		t.Fatal(err)
	}
	if _, rules, _ := h.red.snapshot(); rules != 2 {
		t.Fatal("switching on twice must not add a second rule")
	}
	if s, _ := h.layout.LoadSettings(); !s.Addons["discord"] {
		t.Fatal("the switch must be saved")
	}
	if err := h.c.SetAddon("discord", false); err != nil {
		t.Fatal(err)
	}
	calls, rules, _ = h.red.snapshot()
	if !strings.HasPrefix(calls[len(calls)-1], "delete-rule ") || rules != 1 {
		t.Fatalf("calls %v rules %d", calls, rules)
	}
	if err := h.c.SetAddon("vpn", true); err == nil {
		t.Fatal("unknown addon accepted")
	}
	h.c.Stop()
	if _, rules, proxies := h.red.snapshot(); rules != 0 || proxies != 0 {
		t.Fatal("stop must remove addon rules too")
	}
	// While idle the switch is only saved.
	if err := h.c.SetAddon("steam", true); err != nil {
		t.Fatal(err)
	}
	if calls, _, _ := h.red.snapshot(); strings.Contains(calls[len(calls)-1], "steam") {
		t.Fatal("an idle switch must not touch the redirector")
	}
}

func TestRuntimeCheckedAndLockedAroundEveryStart(t *testing.T) {
	h := newHarness(t, nil)
	for i := 0; i < 2; i++ {
		if _, err := h.c.Start(); err != nil {
			t.Fatal(err)
		}
		if err := h.c.Stop(); err != nil {
			t.Fatal(err)
		}
	}
	// The files stay locked until the driver has started, every time.
	if got := strings.Join(h.runtimeLog, ","); got != "lock,unlock after start,lock,unlock after start" {
		t.Fatalf("runtime log %q", got)
	}

	h.runtimeErr = errors.New("哈希不符")
	_, err := h.c.Start()
	if err == nil || !strings.Contains(err.Error(), "哈希不符") {
		t.Fatalf("start with damaged files: %v", err)
	}
	if len(h.engines) != 2 || h.c.Status().State != StateIdle {
		t.Fatalf("a failed check must stop the start before anything runs (%d engines)", len(h.engines))
	}
}

func TestPreflightSkipsConflictsWhileRunning(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	// Our own relay now holds the port that the conflict check looks for.
	h.sys.conflicts = []Conflict{{Message: "分流所需的本地端口被占用", Blocking: true}}
	if _, conflicts := h.c.Preflight(); len(conflicts) != 0 {
		t.Fatalf("conflicts while running: %v", conflicts)
	}
	if err := h.c.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, conflicts := h.c.Preflight(); len(conflicts) != 1 {
		t.Fatalf("conflicts while idle: %v", conflicts)
	}
}

func TestStateGuards(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.c.Stop(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stop while idle: %v", err)
	}
	if _, err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.Start(); !errors.Is(err, ErrRunning) {
		t.Fatalf("second start: %v", err)
	}
	writeConfig(t, h.layout, "日本中转.json", exampleConfig(t))
	if err := h.c.SelectConfig("日本中转.json"); !errors.Is(err, ErrRunning) {
		t.Fatalf("select while running: %v", err)
	}
	if _, err := h.c.Cleanup("x.exe"); !errors.Is(err, ErrRunning) {
		t.Fatalf("cleanup while running: %v", err)
	}
	h.c.Shutdown()
	if h.c.Status().State != StateIdle {
		t.Fatal("Shutdown must stop acceleration")
	}
	if err := h.c.SelectConfig("日本中转.json"); err != nil {
		t.Fatal(err)
	}
	if s, _ := h.layout.LoadSettings(); s.CurrentConfig != "日本中转.json" {
		t.Fatal("selection must be saved")
	}
	if err := h.c.SelectConfig("missing.json"); err == nil {
		t.Fatal("a missing config must not be selectable")
	}
	if _, err := h.c.Cleanup("x.exe"); err != nil || h.sys.cleanups != 1 {
		t.Fatalf("cleanup: %v (%d)", err, h.sys.cleanups)
	}
}

// ---- network check: port of Test-FullPathLatency.ps1 groups 17-20 ----

func samples(n int, d time.Duration) probe.Result {
	s := make([]probe.Sample, n)
	for i := range s {
		s[i] = probe.Sample{OK: true, Duration: d}
	}
	return probe.NewResult(s)
}

type checkMode struct {
	ipFails       bool
	dnsReason     string
	entranceFails bool
	missingV2     bool
	gameDNSFails  bool
}

var currentCheckMode checkMode

func (h *harness) fakeChecks() CheckFuncs {
	note := func(s string) {
		h.mu.Lock()
		h.checkLog = append(h.checkLog, s)
		h.mu.Unlock()
	}
	return CheckFuncs{
		ExitIP: func(_ context.Context, p probe.Proxy) (string, error) {
			note("ip " + p.Addr + " " + p.User)
			if currentCheckMode.ipFails {
				return "", errors.New("mock IP lookup failure")
			}
			return "198.51.100.9", nil
		},
		ResolveEntrance: func(address string, _ time.Duration) (string, string) {
			note("resolve " + address)
			if currentCheckMode.dnsReason != "" {
				return "", currentCheckMode.dnsReason
			}
			return address, ""
		},
		Entrance: func(ip string, port, count int, _ time.Duration) probe.Result {
			note(fmt.Sprintf("entrance %s:%d x%d", ip, port, count))
			if currentCheckMode.entranceFails {
				return probe.Unavailable("目标拒绝连接")
			}
			return samples(3, 165*time.Millisecond)
		},
		FullPath: func(p probe.Proxy, addr string, port int, _ probe.FullPathOptions) probe.Result {
			note(fmt.Sprintf("full %s:%d via %s as %s", addr, port, p.Addr, p.User))
			if currentCheckMode.missingV2 {
				return probe.Unavailable("节点未提供兼容的版本 2 全程 TCP 测速服务或服务不可达")
			}
			return samples(3, 221250*time.Microsecond)
		},
		Hello: func(p probe.Proxy, _ time.Duration) error {
			note("hello " + p.Addr + " " + p.User)
			return h.helloErr
		},
		// Not noted in checkLog: it runs alongside the others, in no fixed order.
		GameDNS: func(context.Context) probe.DNSResult {
			local := probe.DNSAnswer{Addrs: []string{"203.0.113.5"}, Duration: 12 * time.Millisecond}
			if currentCheckMode.gameDNSFails {
				local = probe.DNSAnswer{Err: "超时", Duration: probe.DNSTimeout}
			}
			return probe.DNSResult{Servers: []string{"192.168.1.1"}, Lookups: []probe.DNSLookup{{
				DNSQuery: probe.DNSQuery{Host: "us-sso.nexon.io", Purpose: "游戏登录"},
				Local:    local,
				Public:   probe.DNSAnswer{Addrs: []string{"203.0.113.5"}, Duration: 7 * time.Millisecond},
			}}}
		},
	}
}

func (h *harness) calls() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.checkLog, " | ")
}

func runCheck(t *testing.T, mode checkMode, setup func(*harness)) (*harness, CheckResult) {
	t.Helper()
	currentCheckMode = mode
	t.Cleanup(func() { currentCheckMode = checkMode{} })
	h := newHarness(t, setup)
	r, err := h.c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return h, r
}

func TestCheckShowsMeasuredFullPathWithoutAddingEntrance(t *testing.T) {
	h, r := runCheck(t, checkMode{}, nil)
	if r.ExitIP != "198.51.100.9" || r.Entrance.Compact() != "165 ms" || r.EntranceTarget != "192.0.2.10:8964" {
		t.Fatalf("result %+v", r)
	}
	if got := r.Game.Compact(); got != "221 ms" {
		t.Fatalf("game latency %q: must be the measured full path, never 165 + 221", got)
	}
	want := "ip 127.0.0.1:40000 session-user-0 | resolve 192.0.2.10 | entrance 192.0.2.10:8964 x3 | full 35.155.204.207:8585 via 127.0.0.1:40000 as session-user-0"
	if got := h.calls(); got != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, want)
	}
	h.allEnginesClosed(t) // the temporary core started for an idle check is closed
}

func TestCheckUsesConfiguredProbeTarget(t *testing.T) {
	h, r := runCheck(t, checkMode{}, func(h *harness) {
		s, _ := h.layout.LoadSettings()
		s.ProbeTarget = store.ProbeTarget{Address: "51.222.56.192", Port: 8484}
		h.layout.SaveSettings(s)
	})
	if r.GameTarget != "51.222.56.192:8484" || !strings.Contains(h.calls(), "full 51.222.56.192:8484") {
		t.Fatalf("target %s calls %s", r.GameTarget, h.calls())
	}
}

func TestCheckEntranceProblemsNeverSuppressGame(t *testing.T) {
	udpOnly := func(h *harness) {
		m := exampleConfig(t)
		server := m["profiles"].([]any)[0].(map[string]any)["servers"].([]any)[0].(map[string]any)
		server["portBindings"].([]any)[0].(map[string]any)["protocol"] = "UDP"
		writeConfig(t, h.layout, "直连.json", m)
	}
	twoServers := func(h *harness) {
		m := exampleConfig(t)
		profile := m["profiles"].([]any)[0].(map[string]any)
		profile["servers"] = append(profile["servers"].([]any), map[string]any{
			"ipAddress": "192.0.2.20", "portBindings": []any{map[string]any{"port": 9000, "protocol": "TCP"}},
		})
		writeConfig(t, h.layout, "直连.json", m)
	}
	domain := func(h *harness) {
		m := exampleConfig(t)
		server := m["profiles"].([]any)[0].(map[string]any)["servers"].([]any)[0].(map[string]any)
		delete(server, "ipAddress")
		server["domainName"] = "relay.example.invalid"
		writeConfig(t, h.layout, "直连.json", m)
	}
	cases := []struct {
		name          string
		mode          checkMode
		setup         func(*harness)
		entranceRuns  bool
		entranceShows string
	}{
		{"entrance TCP failure", checkMode{entranceFails: true}, nil, true, "目标拒绝连接"},
		{"entrance DNS failure", checkMode{dnsReason: "中转域名解析超时"}, domain, false, "中转域名解析超时"},
		{"UDP-only entrance", checkMode{}, udpOnly, false, "无 TCP 测试端口"},
		{"several servers", checkMode{}, twoServers, false, "无法唯一确定测试目标"},
		{"exit IP failure", checkMode{ipFails: true}, nil, true, "165 ms"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, r := runCheck(t, c.mode, c.setup)
			if got := r.Game.Compact(); got != "221 ms" {
				t.Fatalf("game %q must survive entrance/IP problems", got)
			}
			if strings.Contains(h.calls(), "entrance ") != c.entranceRuns {
				t.Fatalf("entrance measurement ran=%v; calls %s", !c.entranceRuns, h.calls())
			}
			if !strings.Contains(r.Entrance.Compact(), c.entranceShows) {
				t.Fatalf("entrance %q lacks %q", r.Entrance.Compact(), c.entranceShows)
			}
			if c.mode.ipFails && !strings.Contains(r.ExitIPError, "mock IP lookup failure") {
				t.Fatalf("IP failure must stay visible: %+v", r)
			}
		})
	}
}

func TestCheckReportsGameDNS(t *testing.T) {
	h, r := runCheck(t, checkMode{}, nil)
	if !r.DNS.OK() || r.DNS.Compact() != "正常（最慢 12 ms）" {
		t.Fatalf("dns %+v", r.DNS)
	}
	if got := h.log.Recent(1)[0].Text; !strings.HasSuffix(got, "；本机 DNS 正常（最慢 12 ms）") {
		t.Fatalf("log %q", got)
	}

	h, r = runCheck(t, checkMode{gameDNSFails: true}, nil)
	if r.DNS.OK() || r.Game.Compact() != "221 ms" || r.Entrance.Compact() != "165 ms" || r.ExitIP != "198.51.100.9" {
		t.Fatalf("a DNS failure must show without hiding the other checks: %+v", r)
	}
	want := "；本机 DNS 解析失败（1 个中 1 个）：us-sso.nexon.io 超时；公共 DNS 能解析"
	if got := h.log.Recent(1)[0].Text; !strings.HasSuffix(got, want) {
		t.Fatalf("log %q lacks %q", got, want)
	}
}

func TestCheckMissingV2ServiceIsExplicit(t *testing.T) {
	_, r := runCheck(t, checkMode{missingV2: true}, nil)
	game := r.Game.Compact()
	if !strings.Contains(game, "版本 2") || strings.Contains(game, "ms") {
		t.Fatalf("game %q must name the v2 requirement without inventing a latency", game)
	}
	if r.Entrance.Compact() != "165 ms" || r.ExitIP != "198.51.100.9" {
		t.Fatal("the other two checks stay available")
	}
}

// The legacy launcher refused the full-path check when the config asked for
// local SOCKS5 authentication. The launcher now always authenticates with its
// own session credentials, so such configs are checked like any other.
func TestCheckIgnoresConfiguredLocalAuthentication(t *testing.T) {
	h, r := runCheck(t, checkMode{}, func(h *harness) {
		m := exampleConfig(t)
		m["socks5Authentication"] = []any{map[string]any{"user": "test-only", "password": "test-only"}}
		writeConfig(t, h.layout, "直连.json", m)
	})
	if r.Game.Compact() != "221 ms" || !strings.Contains(h.calls(), "as session-user-0") {
		t.Fatalf("game %q calls %s", r.Game.Compact(), h.calls())
	}
}

func TestCheckWhileRunningReusesTheSessionCore(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.engines) != 1 || h.engines[0].closeCount() != 0 {
		t.Fatalf("engines=%d; the running core must be reused and stay open", len(h.engines))
	}
	h.c.Stop()
	h.allEnginesClosed(t)
}

// ---- system proxy takeover ----

func endpoint(s string) sysproxy.Endpoint {
	host, port, _ := net.SplitHostPort(s)
	var p int
	fmt.Sscan(port, &p)
	ip := net.ParseIP(host)
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return sysproxy.Endpoint{IP: ip, Port: uint16(p)}
}

// waitFor polls the redirector's calls until one contains want.
func (h *harness) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		calls, _, _ := h.red.snapshot()
		for _, c := range calls {
			if strings.Contains(c, want) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	calls, _, _ := h.red.snapshot()
	t.Fatalf("no call containing %q in:\n%s", want, strings.Join(calls, "\n"))
}

func TestSystemProxyTakeover(t *testing.T) {
	old := systemProxyRefresh
	systemProxyRefresh = 10 * time.Millisecond
	defer func() { systemProxyRefresh = old }()

	var mu sync.Mutex
	current := []sysproxy.Endpoint{endpoint("127.0.0.1:10808")}
	setProxies := func(eps ...sysproxy.Endpoint) { mu.Lock(); current = eps; mu.Unlock() }
	h := newHarness(t, func(h *harness) {
		h.proxies = func() []sysproxy.Endpoint {
			mu.Lock()
			defer mu.Unlock()
			return append([]sysproxy.Endpoint(nil), current...)
		}
	})

	if _, err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	game := profiles.DefaultMapleStory().ProcessList()
	calls, _, _ := h.red.snapshot()
	// Proxies 1 and 2, then the takeover rule (3) before the game rule (4).
	if len(calls) != 7 || calls[0] != "prepare" || !strings.HasPrefix(calls[1], "add-socks5 127.0.0.1:40000 ") ||
		!strings.HasPrefix(calls[2], "add-socks5 127.0.0.1:") || !strings.HasSuffix(calls[2], "domain=false") ||
		calls[3] != "add-tcp-rule "+game+" -> 127.0.0.1:10808 via 2" ||
		calls[4] != "localhost-ports [10808]" || calls[5] != "add-rule "+game || calls[6] != "start" {
		t.Fatalf("start sequence:\n%s", strings.Join(calls, "\n"))
	}
	var takeoverPort int
	fmt.Sscanf(calls[2], "add-socks5 127.0.0.1:%d", &takeoverPort)
	if st := h.c.Status(); len(st.SystemProxy) != 1 || st.SystemProxy[0] != "127.0.0.1:10808" {
		t.Fatalf("status system proxy %v", st.SystemProxy)
	}

	// v2rayN switched off, then another app on another port.
	setProxies()
	h.waitFor(t, "edit-tcp-rule 3 "+game+" -> 127.0.0.1:1 via 2")
	h.waitFor(t, "disable-rule 3")
	h.waitFor(t, "localhost-ports []")
	setProxies(endpoint("127.0.0.1:7890"))
	h.waitFor(t, "edit-tcp-rule 3 "+game+" -> 127.0.0.1:7890 via 2")
	h.waitFor(t, "enable-rule 3")
	h.waitFor(t, "localhost-ports [7890]")

	// Discord's own requests to the proxy are taken over too.
	if err := h.c.SetAddon("discord", true); err != nil {
		t.Fatal(err)
	}
	h.waitFor(t, "edit-tcp-rule 3 "+game+";Discord.exe;DiscordPTB.exe;DiscordCanary.exe -> 127.0.0.1:7890 via 2")

	if err := h.c.Stop(); err != nil {
		t.Fatal(err)
	}
	calls, rules, proxies := h.red.snapshot()
	if rules != 0 || proxies != 0 {
		t.Fatalf("left %d rules and %d proxies:\n%s", rules, proxies, strings.Join(calls, "\n"))
	}
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", takeoverPort), time.Second); err == nil {
		conn.Close()
		t.Fatal("the stand-in proxy must be closed after stopping")
	}
	if st := h.c.Status(); st.SystemProxy != nil {
		t.Fatalf("idle status still lists %v", st.SystemProxy)
	}
}

func TestSystemProxyRuleWaitsDisabledWithoutProxy(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.proxies = func() []sysproxy.Endpoint { return nil }
	})
	if _, err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.c.Stop()
	h.waitFor(t, "add-tcp-rule "+profiles.DefaultMapleStory().ProcessList()+" -> 127.0.0.1:1 via 2")
	h.waitFor(t, "disable-rule 3")
}

func TestFixConflictOnlyWhileIdle(t *testing.T) {
	h := newHarness(t, nil)
	h.sys.conflicts = []Conflict{{Message: "ProxyBridge", Blocking: true, Fix: "proxybridge"}}
	if _, err := h.c.Start(); err == nil {
		t.Fatal("a blocking conflict must stop the start")
	}
	report, err := h.c.FixConflict("proxybridge")
	if err != nil || len(report) != 1 {
		t.Fatalf("fix: %v, %v", report, err)
	}
	if _, err := h.c.Start(); err != nil {
		t.Fatalf("start after the fix: %v", err)
	}
	defer h.c.Stop()
	if _, err := h.c.FixConflict("proxybridge"); !errors.Is(err, ErrRunning) {
		t.Fatalf("fix while running: %v", err)
	}
}

func TestStartWaitsForUnloadingDriver(t *testing.T) {
	oldWait, oldPoll := driverWait, driverPoll
	driverWait, driverPoll = 200*time.Millisecond, 5*time.Millisecond
	defer func() { driverWait, driverPoll = oldWait, oldPoll }()

	h := newHarness(t, nil)
	h.sys.unloading = 3
	if _, err := h.c.Start(); err != nil {
		t.Fatalf("start after the driver unloaded: %v", err)
	}
	h.c.Stop()

	h.sys.unloading = -1
	_, err := h.c.Start()
	if !errors.Is(err, errDriverUnloading) {
		t.Fatalf("start with a stuck driver: %v", err)
	}
	if len(h.engines) != 1 {
		t.Fatalf("nothing may start while waiting (%d engines)", len(h.engines))
	}
}

// ---- transport ----

// dualConfig is the example as a node that serves UDP writes it.
func dualConfig(t *testing.T) map[string]any {
	t.Helper()
	m := exampleConfig(t)
	server := m["profiles"].([]any)[0].(map[string]any)["servers"].([]any)[0].(map[string]any)
	server["portBindings"] = append(server["portBindings"].([]any), map[string]any{"port": 8964, "protocol": "UDP"})
	return m
}

func boundProtocols(cfg *engine.Config) string {
	var out []string
	for _, s := range cfg.Profile.GetServers() {
		for _, b := range s.GetPortBindings() {
			out = append(out, b.GetProtocol().String())
		}
	}
	return strings.Join(out, ",")
}

// UDP is used only when chosen and offered. The core never gets both
// bindings, and a hello proves UDP before the redirector sends anything.
func TestStartPicksTheTransport(t *testing.T) {
	for _, c := range []struct {
		name      string
		dual      bool
		chosen    string
		helloErr  error
		started   string // bindings of each core start
		transport string
		hellos    int
		fellBack  bool
	}{
		{"TCP-only configuration", false, engine.TransportUDP, nil, "TCP", "tcp", 0, false},
		{"TCP chosen", true, engine.TransportTCP, nil, "TCP", "tcp", 0, false},
		{"UDP", true, engine.TransportUDP, nil, "UDP", "udp", 1, false},
		{"UDP falls back to TCP", true, engine.TransportUDP, errors.New("hello timed out"), "UDP TCP", "tcp", 1, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, func(h *harness) {
				if c.dual {
					writeConfig(t, h.layout, "直连.json", dualConfig(t))
				}
				h.helloErr = c.helloErr
			})
			if err := h.c.SetTransport(c.chosen); err != nil {
				t.Fatal(err)
			}
			warnings, err := h.c.Start()
			if err != nil {
				t.Fatal(err)
			}
			var started []string
			for _, cfg := range h.startedWith {
				started = append(started, boundProtocols(cfg))
			}
			if strings.Join(started, " ") != c.started {
				t.Fatalf("core started on %v, want %s", started, c.started)
			}
			if st := h.c.Status(); st.Transport != c.transport {
				t.Fatalf("status transport %q, want %q", st.Transport, c.transport)
			}
			if got := strings.Count(h.calls(), "hello "); got != c.hellos {
				t.Fatalf("%d hellos, want %d", got, c.hellos)
			}
			if fellBack := len(warnings) == 1 && strings.Contains(warnings[0], "改用 TCP"); fellBack != c.fellBack || (!c.fellBack && len(warnings) != 0) {
				t.Fatalf("warnings %v", warnings)
			}
			// The redirector points at the core that stays up.
			calls, _, _ := h.red.snapshot()
			if want := fmt.Sprintf("add-socks5 127.0.0.1:%d ", 40000+len(h.engines)-1); !strings.Contains(strings.Join(calls, "\n"), want) {
				t.Fatalf("redirector calls %v, want %s", calls, want)
			}
			if err := h.c.Stop(); err != nil {
				t.Fatal(err)
			}
			h.allEnginesClosed(t)
		})
	}
}

func TestSetTransportOnlyWhileIdle(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.c.SetTransport("quic"); err == nil {
		t.Fatal("an unknown transport was accepted")
	}
	if _, err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	if err := h.c.SetTransport(engine.TransportUDP); !errors.Is(err, ErrRunning) {
		t.Fatalf("changing the transport while accelerating: %v", err)
	}
	if err := h.c.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := h.c.SetTransport(engine.TransportUDP); err != nil || h.c.Settings().Transport != engine.TransportUDP {
		t.Fatalf("SetTransport: %v %q", err, h.c.Settings().Transport)
	}
	if s, err := h.layout.LoadSettings(); err != nil || s.Transport != engine.TransportUDP {
		t.Fatalf("saved transport %q %v", s.Transport, err)
	}
}

// An idle check runs on the chosen transport, without the start's fallback,
// and still measures the entrance with a TCP connect to the same port.
func TestCheckRunsOnTheChosenTransport(t *testing.T) {
	h := newHarness(t, func(h *harness) { writeConfig(t, h.layout, "直连.json", dualConfig(t)) })
	if err := h.c.SetTransport(engine.TransportUDP); err != nil {
		t.Fatal(err)
	}
	r, err := h.c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Transport != engine.TransportUDP || len(h.startedWith) != 1 || boundProtocols(h.startedWith[0]) != "UDP" {
		t.Fatalf("check transport %q, core on %v", r.Transport, h.startedWith)
	}
	if r.EntranceTarget != "192.0.2.10:8964" || strings.Contains(h.calls(), "hello ") {
		t.Fatalf("entrance %q, calls %s", r.EntranceTarget, h.calls())
	}
	h.allEnginesClosed(t)
}

// Seen on a real node whose UDP was dropped: the exit IP and the game check
// time out through the tunnel while the entrance answers TCP. Over UDP that
// is UDP being dropped; over TCP the original reasons stand.
func TestCheckBlamesUDPWhenOnlyTCPAnswers(t *testing.T) {
	dual := func(h *harness) { writeConfig(t, h.layout, "直连.json", dualConfig(t)) }
	for _, transport := range []string{engine.TransportUDP, engine.TransportTCP} {
		t.Run(transport, func(t *testing.T) {
			currentCheckMode = checkMode{ipFails: true, missingV2: true}
			t.Cleanup(func() { currentCheckMode = checkMode{} })
			h := newHarness(t, dual)
			if err := h.c.SetTransport(transport); err != nil {
				t.Fatal(err)
			}
			r, err := h.c.Check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			blamed := r.ExitIPError == udpBlocked && r.Game.Compact() == "未测得："+udpBlocked
			if blamed != (transport == engine.TransportUDP) || r.Entrance.SuccessCount == 0 {
				t.Fatalf("%s: exit IP %q, game %q", transport, r.ExitIPError, r.Game.Compact())
			}
		})
	}
	// A node that is down altogether keeps the original reasons over UDP too.
	currentCheckMode = checkMode{ipFails: true, missingV2: true, entranceFails: true}
	t.Cleanup(func() { currentCheckMode = checkMode{} })
	h := newHarness(t, dual)
	if err := h.c.SetTransport(engine.TransportUDP); err != nil {
		t.Fatal(err)
	}
	if r, err := h.c.Check(context.Background()); err != nil || r.ExitIPError == udpBlocked {
		t.Fatalf("down node blamed on UDP: %v %q", err, r.ExitIPError)
	}
}
