//go:build windows

// Package redirector drives ProxyBridgeCore.dll (ProxyBridge v4.1.0), which
// uses WinDivert to hand the traffic of selected processes to a SOCKS5 proxy.
// The DLL is called directly through its C API; no cgo is involved.
package redirector

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProxyType mirrors the ProxyType enum in ProxyBridge.h.
type ProxyType uint32

const (
	ProxyHTTP   ProxyType = 0
	ProxySOCKS5 ProxyType = 1
)

// Action mirrors RuleAction in ProxyBridge.h.
type Action uint32

const (
	ActionProxy  Action = 0
	ActionDirect Action = 1
	ActionBlock  Action = 2
)

// Protocol mirrors RuleProtocol in ProxyBridge.h.
type Protocol uint32

const (
	ProtocolTCP  Protocol = 0
	ProtocolUDP  Protocol = 1
	ProtocolBoth Protocol = 2
)

var exportNames = []string{
	"AddProxyConfig", "DeleteProxyConfig", "AddRule", "DeleteRule",
	"SetLocalhostViaProxy", "SetLogCallback", "SetConnectionCallback",
	"SetTrafficLoggingEnabled", "Start", "Stop",
}

// Core is a loaded ProxyBridgeCore.dll. The engine keeps global state, so
// only one Core should be used per process. Never unload it while worker
// threads may still run; the process exit releases it.
type Core struct {
	Path  string
	procs map[string]uintptr
}

// Load loads the DLL from an absolute path. Its dependency WinDivert.dll is
// resolved from the DLL's own directory and System32 only, which avoids
// picking up a planted copy from the current or PATH directories.
func Load(path string) (*Core, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	module, err := windows.LoadLibraryEx(abs, 0,
		windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, fmt.Errorf("加载 %s 失败：%w", abs, err)
	}
	c := &Core{Path: abs, procs: make(map[string]uintptr, len(exportNames))}
	for _, name := range exportNames {
		addr, err := windows.GetProcAddress(module, "ProxyBridge_"+name)
		if err != nil {
			return nil, fmt.Errorf("%s 缺少导出函数 ProxyBridge_%s", abs, name)
		}
		c.procs[name] = addr
	}
	return c, nil
}

func (c *Core) call(name string, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(c.procs[name], args...)
	return r
}

func cString(s string) (*byte, error) {
	p, err := windows.BytePtrFromString(s)
	if err != nil {
		return nil, fmt.Errorf("参数包含非法字符：%q", s)
	}
	return p, nil
}

func ptr(p *byte) uintptr { return uintptr(unsafe.Pointer(p)) }

func boolArg(v bool) uintptr {
	if v {
		return 1
	}
	return 0
}

// AddProxyConfig registers a proxy and returns its id.
func (c *Core) AddProxyConfig(t ProxyType, host string, port uint16, user, password string, sendDomain bool) (uint32, error) {
	h, err := cString(host)
	if err != nil {
		return 0, err
	}
	u, err := cString(user)
	if err != nil {
		return 0, err
	}
	p, err := cString(password)
	if err != nil {
		return 0, err
	}
	id := uint32(c.call("AddProxyConfig", uintptr(t), ptr(h), uintptr(port), ptr(u), ptr(p), boolArg(sendDomain)))
	runtime.KeepAlive(h)
	runtime.KeepAlive(u)
	runtime.KeepAlive(p)
	if id == 0 {
		return 0, errors.New("分流核心拒绝了代理配置")
	}
	return id, nil
}

// DeleteProxyConfig removes a proxy added with AddProxyConfig.
func (c *Core) DeleteProxyConfig(id uint32) bool {
	return int32(c.call("DeleteProxyConfig", uintptr(id))) != 0
}

// AddRule adds a rule. processes is a semicolon-separated list matched
// case-insensitively with wildcards; hosts, ports and domains use "*" for all.
func (c *Core) AddRule(processes, hosts, ports, domains string, protocol Protocol, action Action, proxyID uint32) (uint32, error) {
	args := make([]*byte, 4)
	for i, s := range []string{processes, hosts, ports, domains} {
		p, err := cString(s)
		if err != nil {
			return 0, err
		}
		args[i] = p
	}
	id := uint32(c.call("AddRule", ptr(args[0]), ptr(args[1]), ptr(args[2]), ptr(args[3]),
		uintptr(protocol), uintptr(action), uintptr(proxyID)))
	runtime.KeepAlive(args)
	if id == 0 {
		return 0, errors.New("分流核心拒绝了分流规则")
	}
	return id, nil
}

// DeleteRule removes a rule added with AddRule.
func (c *Core) DeleteRule(id uint32) bool {
	return int32(c.call("DeleteRule", uintptr(id))) != 0
}

// SetLocalhostViaProxy controls whether 127.0.0.0/8 and ::1 traffic may be proxied.
func (c *Core) SetLocalhostViaProxy(enable bool) {
	c.call("SetLocalhostViaProxy", boolArg(enable))
}

// SetTrafficLoggingEnabled toggles ProxyBridge's per-connection logging.
func (c *Core) SetTrafficLoggingEnabled(enable bool) {
	c.call("SetTrafficLoggingEnabled", boolArg(enable))
}

// Start begins interception. It returns false when WinDivert cannot be
// opened; the reason arrives through the log callback.
func (c *Core) Start() bool {
	return int32(c.call("Start")) != 0
}

// Stop ends interception. It blocks while relays drain, which upstream
// bounds at tens of seconds, so call it off any UI thread.
func (c *Core) Stop() bool {
	return int32(c.call("Stop")) != 0
}

// Callback plumbing. ProxyBridge invokes these on its own native threads, and
// the connection callback runs synchronously on the single packet thread: a
// slow callback stalls every intercepted packet on the machine. The Go side
// therefore only copies the strings and queues them without blocking,
// dropping entries when the queue is full.

// Connection is one connection event reported by ProxyBridge.
type Connection struct {
	Process string
	PID     uint32
	DestIP  string
	Port    uint16
	Info    string // e.g. "PROXY ...", "DIRECT"
}

var (
	logQueue    = make(chan string, 2048)
	connQueue   = make(chan Connection, 2048)
	logDropped  atomic.Uint64
	connDropped atomic.Uint64

	logCallback = windows.NewCallback(func(msg *byte) uintptr {
		select {
		case logQueue <- windows.BytePtrToString(msg):
		default:
			logDropped.Add(1)
		}
		return 0
	})
	connCallback = windows.NewCallback(func(process *byte, pid uint32, ip *byte, port uint16, info *byte) uintptr {
		ev := Connection{
			Process: windows.BytePtrToString(process),
			PID:     pid,
			DestIP:  windows.BytePtrToString(ip),
			Port:    port,
			Info:    windows.BytePtrToString(info),
		}
		select {
		case connQueue <- ev:
		default:
			connDropped.Add(1)
		}
		return 0
	})
)

// EnableLogs installs or removes the log callback.
func (c *Core) EnableLogs(on bool) {
	if on {
		c.call("SetLogCallback", logCallback)
	} else {
		c.call("SetLogCallback", 0)
	}
}

// EnableConnections installs or removes the connection callback. Leave it
// off unless diagnostics are being viewed (see the package comment).
func (c *Core) EnableConnections(on bool) {
	if on {
		c.call("SetConnectionCallback", connCallback)
	} else {
		c.call("SetConnectionCallback", 0)
	}
}

// Logs delivers queued log lines.
func Logs() <-chan string { return logQueue }

// Connections delivers queued connection events.
func Connections() <-chan Connection { return connQueue }

// Dropped reports how many log lines and connection events were discarded
// because their queues were full.
func Dropped() (logs, connections uint64) { return logDropped.Load(), connDropped.Load() }
