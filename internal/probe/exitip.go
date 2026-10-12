package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ExitIPURL returns the caller's public IPv4 as plain text.
const ExitIPURL = "https://api-ipv4.ip.sb/ip"

// ExitIPv4 asks ExitIPURL through the SOCKS5 proxy, letting the proxy side
// resolve the host name. It allows 10 s to connect and 20 s in total, like
// the legacy launcher's curl call.
func ExitIPv4(ctx context.Context, proxy Proxy) (string, error) {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, portText, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				return nil, err
			}
			deadline := time.Now().Add(10 * time.Second)
			if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
				deadline = d
			}
			conn, err := DialSOCKS5(proxy, host, port, deadline)
			if err != nil {
				return nil, err
			}
			if err := conn.SetDeadline(time.Time{}); err != nil {
				conn.Close()
				return nil, err
			}
			return conn, nil
		},
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ExitIPURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "MSBOOST-Launcher (+https://github.com/mozziexwz/node_launcher)")
	resp, err := client.Do(req)
	if err != nil {
		var pe *phaseError
		if errors.As(err, &pe) && pe.phase == phaseProxy {
			return "", errors.New("无法连接本地 SOCKS5，请确认已开始加速")
		}
		return "", fmt.Errorf("通过当前节点查询出口 IP 失败：%s", failureReason(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", fmt.Errorf("读取出口 IP 失败：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("出口 IP 查询服务返回 HTTP %d", resp.StatusCode)
	}
	text := strings.TrimSpace(string(body))
	if ip := net.ParseIP(text); ip == nil || ip.To4() == nil || strings.Contains(text, ":") {
		return "", errors.New("出口 IP 查询服务返回的不是 IPv4 地址")
	}
	return text, nil
}
