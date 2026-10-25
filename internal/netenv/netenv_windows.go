//go:build windows

package netenv

import (
	"bytes"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Collect reads the Windows version and the adapters that are up.
func Collect() (Snapshot, error) {
	v := windows.RtlGetVersion()
	s := Snapshot{Windows: WindowsName(v.MajorVersion, v.MinorVersion, v.BuildNumber)}
	adapters, err := upAdapters()
	s.Adapters = adapters
	return s, err
}

func upAdapters() ([]Adapter, error) {
	const flags = windows.GAA_FLAG_INCLUDE_GATEWAYS | windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST
	size := uint32(16 << 10)
	for attempt := 0; attempt < 4; attempt++ {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, first, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue // size now holds what is needed
		}
		if err != nil {
			return nil, err
		}
		var out []Adapter
		for a := first; a != nil; a = a.Next {
			if a.OperStatus != windows.IfOperStatusUp || a.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
				continue
			}
			ad := Adapter{
				Name:        windows.UTF16PtrToString(a.FriendlyName),
				Description: windows.UTF16PtrToString(a.Description),
				Kind:        Kind(a.IfType),
			}
			for u := a.FirstUnicastAddress; u != nil; u = u.Next {
				if ip := u.Address.IP(); ip != nil && !ip.IsLinkLocalUnicast() {
					ad.Addresses = append(ad.Addresses, ip)
				}
			}
			for g := a.FirstGatewayAddress; g != nil; g = g.Next {
				if ip := g.Address.IP(); ip != nil {
					ad.Gateways = append(ad.Gateways, ip)
				}
			}
			for d := a.FirstDnsServerAddress; d != nil; d = d.Next {
				if ip := d.Address.IP(); ip != nil && !isSiteLocalPlaceholder(ip) {
					ad.DNS = append(ad.DNS, ip)
				}
			}
			out = append(out, ad)
		}
		return out, nil
	}
	return nil, windows.ERROR_BUFFER_OVERFLOW
}

// isSiteLocalPlaceholder reports fec0:0:0:ffff::1-3, which Windows lists as
// DNS servers on adapters that have none.
func isSiteLocalPlaceholder(ip net.IP) bool {
	placeholder := net.ParseIP("fec0:0:0:ffff::")
	return len(ip) == net.IPv6len && ip[15] >= 1 && ip[15] <= 3 && bytes.Equal(ip[:15], placeholder[:15])
}
