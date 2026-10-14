package sysproxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Current returns the endpoints of the current user's system proxy, reading
// the PAC script when one is configured. It returns nil when none is set.
func Current() []Endpoint {
	s := ReadSettings()
	var pac string
	if s.PACURL != "" {
		pac = fetchPAC(s.PACURL)
	}
	return Endpoints(s, pac, lookup)
}

var (
	modwinhttp           = windows.NewLazySystemDLL("winhttp.dll")
	procGetIEProxyConfig = modwinhttp.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
	modkernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalFree       = modkernel32.NewProc("GlobalFree")
)

// ieProxyConfig is WINHTTP_CURRENT_USER_IE_PROXY_CONFIG.
type ieProxyConfig struct {
	autoDetect    int32
	autoConfigURL *uint16
	proxy         *uint16
	proxyBypass   *uint16
}

// ReadSettings reads the current user's proxy settings, which every "system
// proxy" app sets, through WinHTTP as browsers do. The proxy list is only
// returned while the manual proxy is switched on.
func ReadSettings() Settings {
	var c ieProxyConfig
	if r, _, _ := procGetIEProxyConfig.Call(uintptr(unsafe.Pointer(&c))); r == 0 {
		return Settings{}
	}
	var s Settings
	if c.proxy != nil {
		s.Enabled = true
		s.Server = windows.UTF16PtrToString(c.proxy)
	}
	if c.autoConfigURL != nil {
		s.PACURL = windows.UTF16PtrToString(c.autoConfigURL)
	}
	// WinHTTP allocates the strings; the caller frees them with GlobalFree.
	for _, p := range []*uint16{c.autoConfigURL, c.proxy, c.proxyBypass} {
		if p != nil {
			procGlobalFree.Call(uintptr(unsafe.Pointer(p)))
		}
	}
	return s
}

const maxPAC = 512 << 10

// fetchPAC reads a PAC script from an http(s) or file URL. It never goes
// through a proxy itself and gives up quickly: the script is only searched
// for proxy addresses.
func fetchPAC(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "file":
		path := strings.TrimPrefix(u.Path, "/")
		if u.Host != "" {
			path = `\\` + u.Host + `\` + path
		}
		f, err := os.Open(path)
		if err != nil {
			return ""
		}
		defer f.Close()
		b, _ := io.ReadAll(io.LimitReader(f, maxPAC))
		return string(b)
	case "http", "https":
		client := &http.Client{
			Timeout:   2 * time.Second,
			Transport: &http.Transport{Proxy: nil},
		}
		resp, err := client.Get(raw)
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxPAC))
		return string(b)
	}
	return ""
}

func lookup(host string) []net.IP {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	ips := make([]net.IP, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP
	}
	return ips
}
