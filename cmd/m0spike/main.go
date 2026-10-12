//go:build windows

// Command m0spike is the M0 validation tool. It embeds mieru, drives
// ProxyBridgeCore.dll and runs the network checks from the command line so
// each M0 assumption can be verified before the real launcher is built.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	mlog "github.com/enfein/mieru/v3/pkg/log"

	"github.com/mozziexwz/node_launcher/internal/engine"
	"github.com/mozziexwz/node_launcher/internal/probe"
	"github.com/mozziexwz/node_launcher/internal/profiles"
	"github.com/mozziexwz/node_launcher/internal/redirector"
	"github.com/mozziexwz/node_launcher/internal/winsys"
)

const defaultCore = `C:\Program Files\ProxyBridge\ProxyBridgeCore.dll`

func usage() {
	fmt.Print(`m0spike：MSBOOST 启动器 M0 验证工具

用法：
  m0spike svc                                   查看权限、服务、端口与防火墙规则名（无需管理员）
  m0spike probe  -config 直连.json [-game IP:端口]  只启动 MSBOOST 核心，执行三项网络检测（无需管理员）
  m0spike engine -config 直连.json               只启动 MSBOOST 本地 SOCKS5，打印地址与凭据，Ctrl+C 退出
  m0spike run    -config 直连.json [选项]         完整加速：MSBOOST 核心 + 按进程分流（需要管理员）

run 的选项：
  -core 路径        ProxyBridgeCore.dll（默认本机已装的官方 ProxyBridge）
  -profile 文件     改用 ProxyBridge 导出的 .pbprofile 中启用的 PROXY 规则
  -duration 时长    运行指定时长后自动停止，如 30m、1h（默认等待 Ctrl+C）
  -conns            打印每条连接的分流结果（诊断用）
  -discord          同时加速 Discord（可选项，默认关闭）
  -steam            同时加速 Steam（可选项，默认关闭）
  -keep-driver      停止后不卸载 WinDivert 驱动
`)
}

func main() {
	// Core library log lines carry their own text; tag them so the console
	// and log file show which component spoke.
	mlog.SetOutput(coreLogWriter{})
	mlog.SetLevel("INFO")
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "svc":
		err = cmdSvc()
	case "probe":
		err = cmdProbe(os.Args[2:])
	case "engine":
		err = cmdEngine(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		logf("[失败] %v", err)
		closeLog()
		os.Exit(1)
	}
	closeLog()
}

// ---- logging: console plus a log file next to the executable ----

var (
	logMu    sync.Mutex
	logOut   io.Writer = os.Stdout
	logFile  *os.File
	logClose sync.Once
)

type lockedWriter struct{}

func (lockedWriter) Write(p []byte) (int, error) {
	logMu.Lock()
	defer logMu.Unlock()
	return logOut.Write(p)
}

func openLog(command string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	name := fmt.Sprintf("m0spike-%s-%s.log", command, time.Now().Format("20060102-150405"))
	f, err := os.Create(filepath.Join(filepath.Dir(exe), name))
	if err != nil {
		logf("[警告] 无法创建日志文件：%v", err)
		return
	}
	logMu.Lock()
	logFile = f
	logOut = io.MultiWriter(os.Stdout, f)
	logMu.Unlock()
	logf("日志文件：%s", f.Name())
}

// coreLogWriter prefixes each core log entry with a timestamp and [核心].
type coreLogWriter struct{}

func (coreLogWriter) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\r\n")
	if text != "" {
		logf("[核心] %s", text)
	}
	return len(p), nil
}

func closeLog() {
	logClose.Do(func() {
		logMu.Lock()
		defer logMu.Unlock()
		if logFile != nil {
			logFile.Close()
		}
	})
}

func logf(format string, args ...any) {
	fmt.Fprintf(lockedWriter{}, time.Now().Format("15:04:05 ")+format+"\n", args...)
}

func buildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, dirty := "unknown", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				rev = s.Value[:7]
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	return rev + dirty + " " + info.GoVersion
}

// ---- svc ----

func cmdSvc() error {
	logf("m0spike %s", buildInfo())
	logf("管理员权限：%v", redirector.IsElevated())
	for _, name := range []string{redirector.ProxyBridgeService, redirector.WinDivertService} {
		state, err := redirector.ServiceState(name)
		if err != nil {
			logf("服务 %s：查询失败：%v", name, err)
			continue
		}
		logf("服务 %s：%s", name, state)
	}
	if busy := redirector.RelayPortConflicts(); len(busy) > 0 {
		logf("ProxyBridge 中继端口被占用：%s", strings.Join(busy, "、"))
	} else {
		logf("ProxyBridge 中继端口 34010/TCP：空闲")
	}
	if exe, err := os.Executable(); err == nil {
		logf("本程序对应的防火墙规则名：%s", redirector.FirewallRuleName(exe))
	}
	return nil
}

// ---- shared helpers ----

func loadConfig(path string) (*engine.Config, error) {
	if path == "" {
		return nil, errors.New("请用 -config 指定 MSBOOST 节点配置（.json 文件）")
	}
	cfg, err := engine.LoadConfigFile(path)
	if err != nil {
		return nil, err
	}
	logf("节点配置：%s（方案 %s）", path, cfg.Profile.GetProfileName())
	return cfg, nil
}

func startEngine(cfg *engine.Config) (*engine.Engine, probe.Proxy, error) {
	e, err := engine.Start(cfg, 0)
	if err != nil {
		return nil, probe.Proxy{}, err
	}
	user, password := e.Credential()
	logf("MSBOOST 本地 SOCKS5：%s（会话凭据已生成）", e.Addr())
	return e, probe.Proxy{Addr: e.Addr().String(), User: user, Password: password}, nil
}

func waitSignal() <-chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	return ch
}

// ---- probe ----

func cmdProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	configPath := fs.String("config", "", "MSBOOST 节点配置（.json 文件）")
	defaults := profiles.DefaultMapleStory()
	game := fs.String("game", net.JoinHostPort(defaults.TestAddress, strconv.Itoa(defaults.TestPort)), "全程测速目标 IP:端口")
	fs.Parse(args)
	openLog("probe")
	logf("m0spike %s", buildInfo())

	host, portText, err := net.SplitHostPort(*game)
	if err != nil {
		return fmt.Errorf("-game 格式应为 IP:端口：%w", err)
	}
	port, _ := strconv.Atoi(portText)
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	e, proxy, err := startEngine(cfg)
	if err != nil {
		return err
	}
	defer e.Close()
	runChecks(cfg, proxy, host, port)
	return nil
}

func runChecks(cfg *engine.Config, proxy probe.Proxy, gameAddr string, gamePort int) {
	logf("== 网络检测 ==")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if ip, err := probe.ExitIPv4(ctx, proxy); err != nil {
		logf("当前出口 IPv4：查询失败；%v", err)
	} else {
		logf("当前出口 IPv4：%s", ip)
	}

	target := probe.RelayTargetFromProfile(cfg.Profile)
	if target.Err != "" {
		logf("当前到中转服务器延迟：%s", target.Err)
	} else if ip, reason := probe.ResolveRelay(target.Address, probe.AttemptTimeout); reason != "" {
		logf("当前到中转服务器延迟：%s", reason)
	} else {
		r := probe.EntranceLatency(ip, target.Port, probe.DefaultSampleCount, probe.AttemptTimeout)
		logf("当前到中转服务器延迟：%s　［%s:%d，%s］", r.Compact(), ip, target.Port, r.Detail())
	}

	r := probe.FullPathLatency(proxy, gameAddr, gamePort, probe.FullPathOptions{})
	logf("当前到游戏服务器延迟：%s　［%s:%d，%s］", r.Compact(), gameAddr, gamePort, r.Detail())
}

// ---- engine ----

func cmdEngine(args []string) error {
	fs := flag.NewFlagSet("engine", flag.ExitOnError)
	configPath := fs.String("config", "", "MSBOOST 节点配置（.json 文件）")
	fs.Parse(args)
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	e, proxy, err := startEngine(cfg)
	if err != nil {
		return err
	}
	defer e.Close()
	logf("用户名：%s", proxy.User)
	logf("密码：%s", proxy.Password)
	logf("示例：curl --socks5-hostname %s:%s@%s https://api-ipv4.ip.sb/ip", proxy.User, proxy.Password, proxy.Addr)
	logf("按 Ctrl+C 退出")
	<-waitSignal()
	return nil
}

// ---- run ----

type rule struct {
	processes, hosts, ports, domains string
	protocol                         redirector.Protocol
}

func protocolOf(p string) (redirector.Protocol, error) {
	switch strings.ToUpper(p) {
	case "TCP":
		return redirector.ProtocolTCP, nil
	case "UDP":
		return redirector.ProtocolUDP, nil
	case "BOTH":
		return redirector.ProtocolBoth, nil
	}
	return 0, fmt.Errorf("未知协议 %q", p)
}

// pbProfile is the subset of ProxyBridge's .pbprofile format used here.
type pbProfile struct {
	ProxyConfigs []struct {
		ID                uint32 `json:"Id"`
		SendDomainToProxy bool
	}
	ProxyRules []struct {
		Name, ProcessName, TargetHosts, TargetPorts, TargetDomains, Protocol, Action string
		IsEnabled                                                                    bool
		ProxyConfigID                                                                uint32 `json:"ProxyConfigId"`
	}
}

func rulesFromPBProfile(path string) ([]rule, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	var p pbProfile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, false, fmt.Errorf("解析 %s 失败：%w", path, err)
	}
	sendDomain := true
	var rules []rule
	for _, r := range p.ProxyRules {
		if !r.IsEnabled || !strings.EqualFold(r.Action, "PROXY") {
			continue
		}
		proto, err := protocolOf(r.Protocol)
		if err != nil {
			return nil, false, err
		}
		for _, c := range p.ProxyConfigs {
			if c.ID == r.ProxyConfigID {
				sendDomain = c.SendDomainToProxy
			}
		}
		rules = append(rules, rule{
			processes: strings.Trim(r.ProcessName, "; "),
			hosts:     r.TargetHosts, ports: r.TargetPorts, domains: r.TargetDomains,
			protocol: proto,
		})
	}
	if len(rules) == 0 {
		return nil, false, errors.New("配置文件中没有启用的 PROXY 规则")
	}
	return rules, sendDomain, nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "", "MSBOOST 节点配置（.json 文件）")
	corePath := fs.String("core", defaultCore, "ProxyBridgeCore.dll 路径")
	profilePath := fs.String("profile", "", "ProxyBridge 导出的 .pbprofile（可选）")
	duration := fs.Duration("duration", 0, "运行时长，0 表示等待 Ctrl+C")
	conns := fs.Bool("conns", false, "打印连接分流结果")
	discord := fs.Bool("discord", false, "同时加速 Discord")
	steam := fs.Bool("steam", false, "同时加速 Steam")
	keepDriver := fs.Bool("keep-driver", false, "停止后不卸载 WinDivert 驱动")
	fs.Parse(args)
	openLog("run")
	logf("m0spike %s", buildInfo())

	if !redirector.IsElevated() {
		return errors.New("需要管理员权限：请用“以管理员身份运行”打开命令行后再执行")
	}
	if _, err := os.Stat(*corePath); err != nil {
		return fmt.Errorf("找不到核心 DLL：%s。-core 需要填写 ProxyBridgeCore.dll 的实际路径", *corePath)
	}

	// Rules: the tested default list (plus opted-in add-ons), or an exported .pbprofile.
	game := profiles.DefaultMapleStory()
	rules := []rule{{processes: game.ProcessList(), hosts: "*", ports: "*", domains: "*", protocol: redirector.ProtocolBoth}}
	sendDomain := game.SendDomainToProxy
	if *profilePath != "" {
		var err error
		if rules, sendDomain, err = rulesFromPBProfile(*profilePath); err != nil {
			return err
		}
		logf("规则来源：%s", *profilePath)
	} else {
		logf("规则来源：内置默认清单（%d 个进程，TCP+UDP）", len(game.Processes))
	}
	for _, addon := range profiles.Addons() {
		if (addon.ID == "discord" && *discord) || (addon.ID == "steam" && *steam) {
			rules = append(rules, rule{processes: addon.ProcessList(), hosts: "*", ports: "*", domains: "*", protocol: redirector.ProtocolBoth})
			logf("可选加速：%s（%s）", addon.Name, addon.ProcessList())
		}
	}

	logf("== 预检 ==")
	if state, err := redirector.ServiceState(redirector.ProxyBridgeService); err == nil && (state == "RUNNING" || state == "START_PENDING") {
		return fmt.Errorf("官方 ProxyBridge 服务 ProxyBridgeSvc 正在运行（%s），会与本工具冲突。请先在管理员命令行执行：sc stop ProxyBridgeSvc，并关闭 ProxyBridge 窗口", state)
	}
	if busy := redirector.RelayPortConflicts(); len(busy) > 0 {
		return fmt.Errorf("ProxyBridge 中继端口被占用：%s。请先关闭占用它的程序（通常是官方 ProxyBridge）", strings.Join(busy, "、"))
	}
	driverBefore, _ := redirector.ServiceState(redirector.WinDivertService)
	logf("WinDivert 驱动当前状态：%s", driverBefore)
	if running, err := winsys.RunningMatches(game.Processes); err == nil && len(running) > 0 {
		logf("[注意] 这些进程已在运行：%s。已建立的连接不会被加速，请在加速开始后重启游戏。", strings.Join(running, "、"))
	}

	logf("== 启动 MSBOOST 核心 ==")
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	eng, proxy, err := startEngine(cfg)
	if err != nil {
		return err
	}
	defer eng.Close()

	logf("== 启动分流 ==")
	core, err := redirector.Load(*corePath)
	if err != nil {
		return err
	}
	logf("核心 DLL：%s", core.Path)
	core.EnableLogs(true)
	core.SetTrafficLoggingEnabled(false)
	core.SetLocalhostViaProxy(false)
	core.EnableConnections(*conns)

	port := uint16(eng.Addr().Port)
	proxyID, err := core.AddProxyConfig(redirector.ProxySOCKS5, "127.0.0.1", port, proxy.User, proxy.Password, sendDomain)
	if err != nil {
		return err
	}
	var ruleIDs []uint32
	for _, r := range rules {
		id, err := core.AddRule(r.processes, r.hosts, r.ports, r.domains, r.protocol, redirector.ActionProxy, proxyID)
		if err != nil {
			return err
		}
		ruleIDs = append(ruleIDs, id)
	}
	cleanupCore := func() {
		for _, id := range ruleIDs {
			core.DeleteRule(id)
		}
		core.DeleteProxyConfig(proxyID)
	}
	if !core.Start() {
		drainLogs(500 * time.Millisecond)
		cleanupCore()
		return errors.New("分流启动失败，原因见上方 [分流] 日志")
	}
	started := time.Now()
	exe, _ := os.Executable()
	rule := redirector.FirewallRuleName(exe)
	logf("加速已开始。现在可以打开冒险岛。按 Ctrl+C 停止。")
	logf("说明：官方核心会为本程序添加防火墙入站规则“%s”，测试结束后可执行：", rule)
	logf("  netsh advfirewall firewall delete rule name=\"%s\"", rule)

	stop := waitSignal()
	var timeout <-chan time.Time
	if *duration > 0 {
		timeout = time.After(*duration)
		logf("将在 %v 后自动停止", *duration)
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var peak int64
loop:
	for {
		select {
		case line := <-redirector.Logs():
			logf("[分流] %s", line)
		case ev := <-redirector.Connections():
			logf("[连接] %s(%d) → %s:%d %s", ev.Process, ev.PID, ev.DestIP, ev.Port, ev.Info)
		case <-ticker.C:
			st := eng.Stats()
			if st.Active > peak {
				peak = st.Active
			}
			dl, dc := redirector.Dropped()
			logf("[状态] 已运行 %v｜经加速的 SOCKS5 连接：累计 %d，当前 %d｜丢弃日志 %d、连接事件 %d",
				time.Since(started).Round(time.Second), st.Accepted, st.Active, dl, dc)
		case <-stop:
			logf("收到停止信号")
			break loop
		case <-timeout:
			logf("已到设定时长")
			break loop
		}
	}

	logf("== 停止 ==")
	t0 := time.Now()
	stopped := core.Stop()
	stopTook := time.Since(t0)
	cleanupCore()
	drainLogs(300 * time.Millisecond)
	logf("ProxyBridge 停止：%v，用时 %v", stopped, stopTook.Round(time.Millisecond))
	st := eng.Stats()
	eng.Close()

	driverAfter := "未卸载（-keep-driver）"
	if !*keepDriver {
		state, err := redirector.StopService(redirector.WinDivertService, 10*time.Second)
		if err != nil {
			driverAfter = fmt.Sprintf("%s（停止失败：%v，可能被其他软件占用）", state, err)
		} else {
			driverAfter = state
		}
	}
	dl, dc := redirector.Dropped()

	logf("===== M0 结果摘要（请把这一段发给开发者）=====")
	logf("版本：m0spike %s", buildInfo())
	logf("核心 DLL：%s", core.Path)
	logf("运行时长：%v", time.Since(started).Round(time.Second))
	logf("SOCKS5 连接：累计 %d，10 秒采样峰值 %d", st.Accepted, peak)
	logf("ProxyBridge 停止：%v，用时 %v", stopped, stopTook.Round(time.Millisecond))
	logf("WinDivert：启动前 %s → 停止后 %s", driverBefore, driverAfter)
	logf("防火墙规则：%s", rule)
	logf("丢弃：日志 %d，连接事件 %d", dl, dc)
	logf("============================================")
	return nil
}

func drainLogs(wait time.Duration) {
	deadline := time.After(wait)
	for {
		select {
		case line := <-redirector.Logs():
			logf("[分流] %s", line)
		case <-deadline:
			return
		}
	}
}
