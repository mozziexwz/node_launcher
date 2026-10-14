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

	"golang.org/x/sys/windows/registry"
)

const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

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

// ReadSettings reads the proxy values every "system proxy" app writes.
func ReadSettings() Settings {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE)
	if err != nil {
		return Settings{}
	}
	defer k.Close()
	var s Settings
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		s.Enabled = v != 0
	}
	s.Server, _, _ = k.GetStringValue("ProxyServer")
	s.PACURL, _, _ = k.GetStringValue("AutoConfigURL")
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
			path = `\` + u.Host + `\` + path
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
