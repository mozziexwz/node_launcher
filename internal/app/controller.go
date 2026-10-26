// Package app is the launcher's orchestration layer: it owns the start/stop
// state machine (with rollback), live addon switching, the network check and
// cleanup. The UI and the developer CLI both drive acceleration through it.
//
// The embedded core, the redirector DLL and the operating system are reached
// through small interfaces so every step and every failure can be tested.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	mlog "github.com/enfein/mieru/v3/pkg/log"

	"github.com/mozziexwz/node_launcher/internal/engine"
	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/netenv"
	"github.com/mozziexwz/node_launcher/internal/probe"
	"github.com/mozziexwz/node_launcher/internal/profiles"
	"github.com/mozziexwz/node_launcher/internal/store"
	"github.com/mozziexwz/node_launcher/internal/sysproxy"
)

// CoreEngine is a running embedded core (internal/engine.Engine).
type CoreEngine interface {
	Addr() *net.TCPAddr
	Credential() (user, password string)
	Stats() engine.Stats
	Close() error
}

// Redirector is the process redirector (ProxyBridgeCore.dll).
type Redirector interface {
	// Prepare turns on log delivery, turns off per-connection traffic logging
	// and keeps localhost traffic direct.
	Prepare()
	AddSOCKS5(host string, port uint16, user, password string, sendDomain bool) (uint32, error)
	DeleteProxy(id uint32) bool
	// AddProxyRule proxies TCP and UDP of the listed processes to proxyID.
	AddProxyRule(processes string, proxyID uint32) (uint32, error)
	// AddTCPRule proxies the processes' TCP connections to the listed hosts
	// and ports (";"-separated) to proxyID; EditTCPRule changes such a rule
	// in place, keeping its position in the evaluation order.
	AddTCPRule(processes, hosts, ports string, proxyID uint32) (uint32, error)
	EditTCPRule(id uint32, processes, hosts, ports string, proxyID uint32) error
	EnableRule(id uint32) bool
	DisableRule(id uint32) bool
	DeleteRule(id uint32) bool
	// SetLocalhostProxyPorts lists loopback TCP ports that rules may proxy
	// while other localhost traffic stays direct (patch P4).
	SetLocalhostProxyPorts(ports []uint16) error
	// TraceConnections passes the redirector's connection decisions to fn
	// until stop is called. Call it before Start and call stop only after
	// Stop: the redirector reads the callback without a lock.
	TraceConnections(fn func(ConnEvent)) (stop func())
	Start() bool
	Stop() bool
}

// Conflict is something that would interfere with acceleration. Fix, when
// set, names a fix FixConflict can apply.
type Conflict struct {
	Message  string
	Blocking bool
	Fix      string
}

// System is the operating system as seen by the controller.
type System interface {
	Elevated() bool
	Conflicts() []Conflict
	RunningProcesses(names []string) []string
	// StopDriver stops the packet driver service and reports its state.
	StopDriver() (string, error)
	// DriverUnloading reports whether the packet driver is still being
	// removed (for example while another WinDivert program let go of it);
	// it cannot be started again until that finishes.
	DriverUnloading() bool
	// Cleanup removes everything the launcher may leave on the system and
	// reports what it did.
	Cleanup(exePath string) []string
	// FixConflict applies a conflict's fix and reports what it did.
	FixConflict(fix string) ([]string, error)
}

// CheckFuncs are the network checks; nil fields use the probe package.
type CheckFuncs struct {
	ExitIP          func(ctx context.Context, p probe.Proxy) (string, error)
	ResolveEntrance func(address string, timeout time.Duration) (ip, reason string)
	Entrance        func(ip string, port, count int, timeout time.Duration) probe.Result
	FullPath        func(p probe.Proxy, addr string, port int, opts probe.FullPathOptions) probe.Result
	GameDNS         func(ctx context.Context) probe.DNSResult
}

func (c *CheckFuncs) fillDefaults() {
	if c.ExitIP == nil {
		c.ExitIP = probe.ExitIPv4
	}
	if c.ResolveEntrance == nil {
		c.ResolveEntrance = probe.ResolveRelay
	}
	if c.Entrance == nil {
		c.Entrance = probe.EntranceLatency
	}
	if c.FullPath == nil {
		c.FullPath = probe.FullPathLatency
	}
	if c.GameDNS == nil {
		c.GameDNS = gameDNS
	}
}

// gameDNS resolves the game's host names through this computer's DNS, and
// through public resolvers for comparison.
func gameDNS(ctx context.Context) probe.DNSResult {
	domains := profiles.GameDomains()
	queries := make([]probe.DNSQuery, len(domains))
	for i, d := range domains {
		queries[i] = probe.DNSQuery{Host: d.Host, Purpose: d.Purpose}
	}
	r := probe.CheckDNS(ctx, queries, probe.SystemLookup, probe.PublicLookup)
	snap, _ := netenv.Collect()
	r.Servers = snap.DNSServers()
	return r
}

// Deps wires the controller to real or fake implementations.
type Deps struct {
	Layout         store.Layout
	Log            *logx.Logger
	StartEngine    func(*engine.Config) (CoreEngine, error)
	LoadRedirector func() (Redirector, error) // called once, on first start
	// PrepareRuntime, when set, checks the redirector's files before every
	// start and keeps them locked until release is called after the start.
	PrepareRuntime func() (release func(), err error)
	// SystemProxies, when set, returns the Windows system proxy endpoints;
	// the game's connections to them are then taken over (sysproxy.go).
	SystemProxies func() []sysproxy.Endpoint
	// Environment, when set, describes the network for the log at every
	// start: adapters, addresses, DNS servers and the system proxy.
	Environment func() []string
	System      System
	Checks      CheckFuncs
}

// State is the acceleration state.
type State string

const (
	StateIdle     State = "idle"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
)

// Status is a snapshot for the UI. Traffic is with the node, in bytes.
type Status struct {
	State        State
	Config       string
	Since        time.Time
	Accepted     int64
	Active       int64
	Upload       int64
	Download     int64
	UploadRate   int64 // bytes per second
	DownloadRate int64
	SystemProxy  []string // system proxy endpoints being taken over
	Addons       map[string]bool
	LastError    string
}

// Errors the UI may want to distinguish.
var (
	ErrBusy       = errors.New("正在启动或停止，请稍后再试")
	ErrRunning    = errors.New("加速进行中，请先停止加速")
	ErrNotRunning = errors.New("当前没有在加速")
	ErrNoConfig   = errors.New("请先选择节点配置")
)

const gameRuleKey = "game"

type session struct {
	config  string
	cfg     *engine.Config
	eng     CoreEngine
	proxyID uint32
	rules   map[string]uint32 // gameRuleKey, systemProxyRuleKey or addon ID -> rule id
	since   time.Time
	secrets []string

	takeover *proxyTakeover // nil when SystemProxies is not set

	diag      *connDiag
	stopTrace func() // ends TraceConnections; only after the redirector stopped
	observed  bool   // the engine reports its connections to diag
}

// Controller is safe for concurrent use.
type Controller struct {
	deps Deps
	log  *logx.Logger

	mu        sync.Mutex
	state     State
	settings  store.Settings
	session   *session
	red       Redirector
	lastErr   string
	watchers  map[int]func(Status)
	nextWatch int
}

// New loads the settings and prepares the controller. A damaged settings file
// is reported in the log and replaced by the defaults in memory.
func New(deps Deps) *Controller {
	deps.Checks.fillDefaults()
	log := deps.Log
	if log == nil {
		log, _ = logx.New("", 0)
		deps.Log = log
	}
	mlog.SetOutput(log.Writer(logx.Core))
	settings, err := deps.Layout.LoadSettings()
	if err != nil {
		log.Printf(logx.App, "%v", err)
	}
	return &Controller{deps: deps, log: log, state: StateIdle, settings: settings, watchers: make(map[int]func(Status))}
}

// Settings returns a copy of the current settings.
func (c *Controller) Settings() store.Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return copySettings(c.settings)
}

// Status returns the current state.
func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Controller) statusLocked() Status {
	st := Status{State: c.state, Config: c.settings.CurrentConfig, LastError: c.lastErr, Addons: copySettings(c.settings).Addons}
	if c.session != nil {
		st.Config = c.session.config
		st.Since = c.session.since
		stats := c.session.eng.Stats()
		st.Accepted, st.Active = stats.Accepted, stats.Active
		st.Upload, st.Download = stats.Upload, stats.Download
		st.UploadRate, st.DownloadRate = stats.UploadRate, stats.DownloadRate
		if t := c.session.takeover; t != nil {
			st.SystemProxy = t.endpointNames()
		}
	}
	return st
}

// Watch calls fn after every state change until cancel is called.
func (c *Controller) Watch(fn func(Status)) (cancel func()) {
	c.mu.Lock()
	id := c.nextWatch
	c.nextWatch++
	c.watchers[id] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.watchers, id)
		c.mu.Unlock()
	}
}

func (c *Controller) emit() {
	c.mu.Lock()
	st := c.statusLocked()
	fns := make([]func(Status), 0, len(c.watchers))
	for _, fn := range c.watchers {
		fns = append(fns, fn)
	}
	c.mu.Unlock()
	for _, fn := range fns {
		fn(st)
	}
}

// SelectConfig makes name (a file in configs\) the configuration to use.
func (c *Controller) SelectConfig(name string) error {
	if _, err := c.deps.Layout.LoadConfig(name); err != nil {
		return err
	}
	c.mu.Lock()
	if c.state != StateIdle {
		c.mu.Unlock()
		return ErrRunning
	}
	next := copySettings(c.settings)
	next.CurrentConfig = name
	err := c.deps.Layout.SaveSettings(next)
	if err == nil {
		c.settings = next
	}
	c.mu.Unlock()
	if err == nil {
		c.log.Printf(logx.App, "已选择节点配置 %s", name)
		c.emit()
	}
	return err
}

// SetProbeTarget changes the game endpoint used by the network check.
func (c *Controller) SetProbeTarget(address string, port int) error {
	c.mu.Lock()
	next := copySettings(c.settings)
	next.ProbeTarget = store.ProbeTarget{Address: address, Port: port}
	err := c.deps.Layout.SaveSettings(next)
	if err == nil {
		c.settings = next
	}
	c.mu.Unlock()
	return err
}

// SetAddon switches an optional acceleration. While accelerating, the rule
// is added or removed immediately and applies to new connections.
func (c *Controller) SetAddon(id string, on bool) error {
	addon, ok := findAddon(id)
	if !ok {
		return fmt.Errorf("未知的可选加速项：%q", id)
	}
	c.mu.Lock()
	defer func() {
		c.mu.Unlock()
		c.emit()
	}()
	if c.state == StateStarting || c.state == StateStopping {
		return ErrBusy
	}
	next := copySettings(c.settings)
	next.Addons[id] = on
	if c.state == StateRunning {
		rules := c.session.rules
		if ruleID, present := rules[id]; on && !present {
			newID, err := c.red.AddProxyRule(addon.ProcessList(), c.session.proxyID)
			if err != nil {
				return err
			}
			rules[id] = newID
			c.log.Printf(logx.App, "已开启 %s 加速（对新连接生效）", addon.Name)
		} else if !on && present {
			c.red.DeleteRule(ruleID)
			delete(rules, id)
			c.log.Printf(logx.App, "已关闭 %s 加速（已建立的连接不受影响）", addon.Name)
		}
		if t := c.session.takeover; t != nil {
			c.updateTakeoverLocked(t, proxiedProcesses(next))
		}
		if d := c.session.diag; d != nil {
			d.setPrograms(proxiedProcesses(next))
		}
	}
	if err := c.deps.Layout.SaveSettings(next); err != nil {
		return err
	}
	c.settings = next
	return nil
}

// Start begins acceleration. Warnings are non-fatal findings for the user.
// On failure every step already taken is undone in reverse order.
func (c *Controller) Start() (warnings []string, err error) {
	c.mu.Lock()
	switch c.state {
	case StateRunning:
		c.mu.Unlock()
		return nil, ErrRunning
	case StateStarting, StateStopping:
		c.mu.Unlock()
		return nil, ErrBusy
	}
	c.state = StateStarting
	c.lastErr = ""
	settings := copySettings(c.settings)
	c.mu.Unlock()
	c.emit()

	sess, warnings, err := c.startSession(settings)

	c.mu.Lock()
	if err != nil {
		c.state = StateIdle
		c.lastErr = err.Error()
	} else {
		c.state = StateRunning
		c.session = sess
		if sess.takeover != nil {
			c.watchSystemProxy(sess.takeover)
		}
	}
	c.mu.Unlock()
	c.emit()
	return warnings, err
}

func (c *Controller) startSession(s store.Settings) (sess *session, warnings []string, err error) {
	var undo []func()
	var secrets []string
	defer func() {
		if err == nil {
			return
		}
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		for _, s := range secrets {
			c.log.RemoveSecret(s)
		}
		c.log.Printf(logx.App, "开始加速失败：%v", err)
	}()

	sys := c.deps.System
	if !sys.Elevated() {
		return nil, nil, errors.New("需要以管理员身份运行 MSBOOST 启动器")
	}
	for _, conflict := range sys.Conflicts() {
		if conflict.Blocking {
			return nil, nil, errors.New(conflict.Message)
		}
		warnings = append(warnings, conflict.Message)
	}
	if s.CurrentConfig == "" {
		return nil, nil, ErrNoConfig
	}
	if err := c.waitForDriver(); err != nil {
		return nil, nil, err
	}
	if prepare := c.deps.PrepareRuntime; prepare != nil {
		release, err := prepare()
		if err != nil {
			return nil, nil, fmt.Errorf("分流组件检查失败：%w", err)
		}
		// Held until the DLL is loaded and the driver has started.
		defer release()
	}
	cfg, err := c.deps.Layout.LoadConfig(s.CurrentConfig)
	if err != nil {
		return nil, nil, err
	}
	if pw := cfg.Profile.GetUser().GetPassword(); pw != "" {
		c.log.AddSecret(pw)
		secrets = append(secrets, pw)
	}
	if env := c.deps.Environment; env != nil {
		for _, line := range env() {
			c.log.Printf(logx.App, "%s", line)
		}
	}

	eng, err := c.deps.StartEngine(cfg)
	if err != nil {
		return nil, nil, err
	}
	undo = append(undo, func() { eng.Close() })
	user, password := eng.Credential()
	for _, secret := range []string{user, password} {
		c.log.AddSecret(secret)
		secrets = append(secrets, secret)
	}
	c.log.Printf(logx.App, "MSBOOST 核心已启动，本地端口 %d", eng.Addr().Port)

	diag := newConnDiag(c.log)
	diag.setPrograms(proxiedProcesses(s))
	diag.setProxy(eng.Addr().Port, "加速")
	observed := false
	if o, ok := eng.(observer); ok {
		o.Observe(diag.request)
		observed = true
	}

	red, err := c.redirector()
	if err != nil {
		return nil, nil, err
	}
	red.Prepare()
	game := profiles.DefaultMapleStory()
	proxyID, err := red.AddSOCKS5("127.0.0.1", uint16(eng.Addr().Port), user, password, game.SendDomainToProxy)
	if err != nil {
		return nil, nil, err
	}
	undo = append(undo, func() { red.DeleteProxy(proxyID) })

	rules := make(map[string]uint32)
	addRule := func(key, processes string) error {
		id, err := red.AddProxyRule(processes, proxyID)
		if err != nil {
			return err
		}
		rules[key] = id
		undo = append(undo, func() { red.DeleteRule(id) })
		return nil
	}
	// The system-proxy rule must come first: rules match in order.
	var takeover *proxyTakeover
	if c.deps.SystemProxies != nil {
		if takeover, err = c.startTakeover(red, eng, proxiedProcesses(s), &undo, &secrets); err != nil {
			return nil, nil, err
		}
		rules[systemProxyRuleKey] = takeover.ruleID
		diag.setProxy(takeover.server.Addr().Port, "改由 MSBOOST 接管（系统代理）")
	}
	if err := addRule(gameRuleKey, game.ProcessList()); err != nil {
		return nil, nil, err
	}
	for _, addon := range profiles.Addons() {
		if s.Addons[addon.ID] {
			if err := addRule(addon.ID, addon.ProcessList()); err != nil {
				return nil, nil, err
			}
		}
	}
	stopTrace := red.TraceConnections(diag.decision)
	undo = append(undo, stopTrace)
	if !red.Start() {
		if sys.DriverUnloading() {
			return nil, nil, errDriverUnloading
		}
		return nil, nil, errors.New("分流启动失败，详细原因见日志")
	}

	if running := sys.RunningProcesses(game.Processes); len(running) > 0 {
		warnings = append(warnings, "这些游戏进程在加速开始前已在运行："+strings.Join(running, "、")+"。已建立的连接不会被加速，请重启游戏")
	}
	c.log.Printf(logx.App, "加速已开始（节点配置 %s）", s.CurrentConfig)
	for _, w := range warnings {
		c.log.Printf(logx.App, "提醒：%s", w)
	}
	return &session{
		config: s.CurrentConfig, cfg: cfg, eng: eng, proxyID: proxyID,
		rules: rules, since: time.Now(), secrets: secrets, takeover: takeover,
		diag: diag, stopTrace: stopTrace, observed: observed,
	}, warnings, nil
}

// observer is implemented by engines that report the connections they serve
// (engine.Engine); the fakes in tests may leave it out.
type observer interface {
	Observe(fn func(engine.Request))
}

// waitIdle waits up to max for an observed engine's connections to close, so
// their records are written before the session summary.
func waitIdle(eng CoreEngine, max time.Duration) {
	deadline := time.Now().Add(max)
	for eng.Stats().Active > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
}

// How long a start waits for a previous packet driver to finish unloading.
var (
	driverWait = 60 * time.Second
	driverPoll = time.Second
)

var errDriverUnloading = errors.New("分流驱动一直没有卸载完成。请关闭其他使用分流驱动的软件（如 ProxyBridge、GoodbyeDPI）后再试；如果仍然不行，重启电脑即可")

// waitForDriver waits while the packet driver is still being removed, which
// happens for a while after another WinDivert program (such as ProxyBridge)
// has let go of it; starting before that fails with error 1058.
func (c *Controller) waitForDriver() error {
	sys := c.deps.System
	if !sys.DriverUnloading() {
		return nil
	}
	c.log.Printf(logx.App, "分流驱动还在卸载（通常是刚关闭的其他分流软件还占用着它），正在等待，最多 %d 秒", int(driverWait/time.Second))
	deadline := time.Now().Add(driverWait)
	for sys.DriverUnloading() {
		if time.Now().After(deadline) {
			return errDriverUnloading
		}
		time.Sleep(driverPoll)
	}
	c.log.Printf(logx.App, "分流驱动已卸载完成，继续开始加速")
	return nil
}

func (c *Controller) redirector() (Redirector, error) {
	c.mu.Lock()
	red := c.red
	c.mu.Unlock()
	if red != nil {
		return red, nil
	}
	red, err := c.deps.LoadRedirector()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.red = red
	c.mu.Unlock()
	return red, nil
}

// Stop ends acceleration: interception first, then rules, the driver and the
// core. Existing game connections through the node are closed.
func (c *Controller) Stop() error {
	c.mu.Lock()
	switch c.state {
	case StateIdle:
		c.mu.Unlock()
		return ErrNotRunning
	case StateStarting, StateStopping:
		c.mu.Unlock()
		return ErrBusy
	}
	c.state = StateStopping
	sess, red := c.session, c.red
	c.mu.Unlock()
	c.emit()
	if sess.takeover != nil {
		sess.takeover.stopWatching() // outside c.mu: the watcher takes it
	}

	started := time.Now()
	if !red.Stop() {
		c.log.Printf(logx.App, "分流已处于停止状态")
	}
	if sess.stopTrace != nil {
		sess.stopTrace() // only now: the redirector no longer calls back
	}
	ids := make([]uint32, 0, len(sess.rules))
	for _, id := range sess.rules {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	for _, id := range ids {
		red.DeleteRule(id)
	}
	red.DeleteProxy(sess.proxyID)
	if sess.takeover != nil {
		c.stopTakeover(red, sess.takeover)
	}
	if state, err := c.deps.System.StopDriver(); err != nil {
		c.log.Printf(logx.App, "驱动未能卸载（%s）：%v。如果其他软件正在使用它，这是正常的", state, err)
	} else {
		c.log.Printf(logx.App, "驱动状态：%s", state)
	}
	stats := sess.eng.Stats()
	sess.eng.Close()
	if sess.observed {
		waitIdle(sess.eng, 2*time.Second)
	}
	for _, s := range sess.secrets {
		c.log.RemoveSecret(s)
	}
	c.log.Printf(logx.App, "加速已停止，用时 %v；本次已使用流量 %s",
		time.Since(started).Round(time.Millisecond), FormatBytes(stats.Upload+stats.Download))
	if sess.diag != nil {
		sess.diag.finish()
	}

	c.mu.Lock()
	c.state = StateIdle
	c.session = nil
	c.mu.Unlock()
	c.emit()
	return nil
}

// Shutdown stops acceleration if needed; call it before the program exits.
func (c *Controller) Shutdown() {
	if c.Status().State == StateRunning {
		c.Stop()
	}
}

// Preflight reports elevation and current conflicts without starting anything.
// Conflicts only matter before a start, and while a session is active our own
// relay holds the port the check looks for, so none are reported then.
func (c *Controller) Preflight() (elevated bool, conflicts []Conflict) {
	if c.Status().State != StateIdle {
		return c.deps.System.Elevated(), nil
	}
	return c.deps.System.Elevated(), c.deps.System.Conflicts()
}

// RunningGameProcesses lists game processes that are running now, so the UI
// can warn before stopping or starting acceleration.
func (c *Controller) RunningGameProcesses() []string {
	return c.deps.System.RunningProcesses(profiles.DefaultMapleStory().Processes)
}

// Cleanup removes what the launcher may leave on the system. It refuses to
// run while accelerating.
func (c *Controller) Cleanup(exePath string) ([]string, error) {
	c.mu.Lock()
	idle := c.state == StateIdle
	c.mu.Unlock()
	if !idle {
		return nil, ErrRunning
	}
	report := c.deps.System.Cleanup(exePath)
	for _, line := range report {
		c.log.Printf(logx.App, "清理：%s", line)
	}
	return report, nil
}

// FixConflict resolves a conflict that the launcher can resolve itself, such
// as stopping the official ProxyBridge. Only while idle.
func (c *Controller) FixConflict(fix string) ([]string, error) {
	c.mu.Lock()
	idle := c.state == StateIdle
	c.mu.Unlock()
	if !idle {
		return nil, ErrRunning
	}
	report, err := c.deps.System.FixConflict(fix)
	for _, line := range report {
		c.log.Printf(logx.App, "处理冲突：%s", line)
	}
	if err != nil {
		c.log.Printf(logx.App, "处理冲突失败：%v", err)
	}
	return report, err
}

// CheckResult holds the four independent network checks.
type CheckResult struct {
	ExitIP         string
	ExitIPError    string
	EntranceTarget string // "ip:port" when one entrance could be chosen
	Entrance       probe.Result
	GameTarget     string
	Game           probe.Result
	DNS            probe.DNSResult
}

// Check runs the exit-IP lookup, the entrance TCP latency, the full-path game
// latency and the game's DNS lookups. Each is independent: one failing never
// suppresses another. While idle a temporary core is started for the check
// (no elevation needed); the DNS lookups need no core and run alongside.
func (c *Controller) Check(ctx context.Context) (CheckResult, error) {
	c.mu.Lock()
	state, sess, s := c.state, c.session, copySettings(c.settings)
	c.mu.Unlock()

	var cfg *engine.Config
	var eng CoreEngine
	switch state {
	case StateRunning:
		cfg, eng = sess.cfg, sess.eng
	case StateIdle:
		if s.CurrentConfig == "" {
			return CheckResult{}, ErrNoConfig
		}
		var err error
		if cfg, err = c.deps.Layout.LoadConfig(s.CurrentConfig); err != nil {
			return CheckResult{}, err
		}
		if eng, err = c.deps.StartEngine(cfg); err != nil {
			return CheckResult{}, err
		}
		defer eng.Close()
	default:
		return CheckResult{}, ErrBusy
	}

	checks := c.deps.Checks
	dns := make(chan probe.DNSResult, 1)
	go func() { dns <- checks.GameDNS(ctx) }()

	user, password := eng.Credential()
	proxy := probe.Proxy{Addr: eng.Addr().String(), User: user, Password: password}
	result := CheckResult{GameTarget: fmt.Sprintf("%s:%d", s.ProbeTarget.Address, s.ProbeTarget.Port)}

	if ip, err := checks.ExitIP(ctx, proxy); err != nil {
		result.ExitIPError = err.Error()
	} else {
		result.ExitIP = ip
	}

	target := probe.RelayTargetFromProfile(cfg.Profile)
	switch {
	case target.Err != "":
		result.Entrance = probe.Unavailable(target.Err)
	default:
		if ip, reason := checks.ResolveEntrance(target.Address, probe.AttemptTimeout); reason != "" {
			result.Entrance = probe.Unavailable(reason)
		} else {
			result.EntranceTarget = fmt.Sprintf("%s:%d", ip, target.Port)
			result.Entrance = checks.Entrance(ip, target.Port, probe.DefaultSampleCount, probe.AttemptTimeout)
		}
	}

	result.Game = checks.FullPath(proxy, s.ProbeTarget.Address, s.ProbeTarget.Port, probe.FullPathOptions{})
	result.DNS = <-dns
	dnsText := result.DNS.Compact()
	if detail := result.DNS.Detail(); detail != "" {
		dnsText += "：" + detail
	}
	c.log.Printf(logx.App, "网络检测：到游戏服务器 IPv4 %s；到中转 %s；到游戏 %s；本机 DNS %s",
		orText(result.ExitIP, result.ExitIPError), result.Entrance.Compact(), result.Game.Compact(), dnsText)
	return result, nil
}

// GameDNS checks whether this computer resolves the game's host names; the
// support bundle runs it on its own.
func (c *Controller) GameDNS(ctx context.Context) probe.DNSResult {
	return c.deps.Checks.GameDNS(ctx)
}

func orText(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func findAddon(id string) (profiles.Addon, bool) {
	for _, a := range profiles.Addons() {
		if a.ID == id {
			return a, true
		}
	}
	return profiles.Addon{}, false
}

func copySettings(s store.Settings) store.Settings {
	addons := make(map[string]bool, len(s.Addons))
	for k, v := range s.Addons {
		addons[k] = v
	}
	s.Addons = addons
	return s
}
