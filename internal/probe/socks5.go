package probe

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

// Proxy is a local SOCKS5 endpoint. A non-empty User selects RFC 1929
// username/password authentication; otherwise no authentication is offered.
type Proxy struct {
	Addr     string
	User     string
	Password string
}

const (
	phaseProxy = "proxy" // connecting to the local SOCKS5 listener
	phaseSOCKS = "socks" // SOCKS5 negotiation and CONNECT reply
)

// phaseError records which step of DialSOCKS5 failed.
type phaseError struct {
	phase string
	err   error
}

func (e *phaseError) Error() string { return e.err.Error() }
func (e *phaseError) Unwrap() error { return e.err }

var errTimeout = errors.New("测速请求超时")

// DialSOCKS5 opens a CONNECT tunnel to host:port through p. All steps share
// deadline. A host that is not an IP literal is sent as a domain name, so the
// proxy side resolves it (socks5h semantics). The returned connection still
// carries deadline; callers set their own.
func DialSOCKS5(p Proxy, host string, port int, deadline time.Time) (net.Conn, error) {
	d := net.Dialer{Deadline: deadline}
	conn, err := d.Dial("tcp", p.Addr)
	if err != nil {
		return nil, &phaseError{phase: phaseProxy, err: err}
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, &phaseError{phase: phaseProxy, err: err}
	}
	if err := socks5Connect(conn, p, host, port); err != nil {
		conn.Close()
		return nil, &phaseError{phase: phaseSOCKS, err: err}
	}
	return conn, nil
}

func socks5Connect(rw io.ReadWriter, p Proxy, host string, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("测试端口无效")
	}
	method := byte(0x00)
	if p.User != "" {
		method = 0x02
	}
	if _, err := rw.Write([]byte{5, 1, method}); err != nil {
		return err
	}
	var greeting [2]byte
	if err := readFull(rw, greeting[:]); err != nil {
		return err
	}
	if greeting[0] != 5 {
		return errors.New("SOCKS5 协议回复无效")
	}
	if method == 0x00 && (greeting[1] == 0x02 || greeting[1] == 0xFF) {
		return errors.New("本地 SOCKS5 需要认证或不接受无认证连接")
	}
	if greeting[1] == 0xFF {
		return errors.New("本地 SOCKS5 不接受用户名密码认证")
	}
	if greeting[1] != method {
		return errors.New("SOCKS5 认证方式不支持")
	}
	if method == 0x02 {
		if len(p.User) > 255 || len(p.Password) == 0 || len(p.Password) > 255 {
			return errors.New("SOCKS5 认证凭据长度无效")
		}
		auth := append([]byte{1, byte(len(p.User))}, p.User...)
		auth = append(auth, byte(len(p.Password)))
		auth = append(auth, p.Password...)
		if _, err := rw.Write(auth); err != nil {
			return err
		}
		var status [2]byte
		if err := readFull(rw, status[:]); err != nil {
			return err
		}
		if status[0] != 1 {
			return errors.New("SOCKS5 认证回复无效")
		}
		if status[1] != 0 {
			return errors.New("本地 SOCKS5 认证失败")
		}
	}

	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(append(req, 1), v4...)
		} else {
			req = append(append(req, 4), ip.To16()...)
		}
	} else {
		if len(host) < 1 || len(host) > 255 || !isASCII(host) {
			return errors.New("测试域名长度无效")
		}
		req = append(append(req, 3, byte(len(host))), host...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := rw.Write(req); err != nil {
		return err
	}

	var head [4]byte
	if err := readFull(rw, head[:]); err != nil {
		return err
	}
	if head[0] != 5 || head[2] != 0 {
		return errors.New("SOCKS5 协议回复无效")
	}
	var tail int
	switch head[3] {
	case 1:
		tail = 6
	case 4:
		tail = 18
	case 3:
		var n [1]byte
		if err := readFull(rw, n[:]); err != nil {
			return err
		}
		if n[0] == 0 {
			return errors.New("SOCKS5 绑定地址无效")
		}
		tail = int(n[0]) + 2
	default:
		return errors.New("SOCKS5 地址类型不支持")
	}
	if err := readFull(rw, make([]byte, tail)); err != nil {
		return err
	}
	if head[1] != 0 {
		return errors.New(socksReplyReason(head[1]))
	}
	return nil
}

func socksReplyReason(code byte) string {
	switch code {
	case 1:
		return "代理连接失败"
	case 2:
		return "代理规则禁止连接"
	case 3:
		return "目标网络不可达"
	case 4:
		return "目标主机不可达"
	case 5:
		return "目标拒绝连接"
	case 6:
		return "代理连接超时"
	case 7:
		return "代理不支持 CONNECT 命令"
	case 8:
		return "代理不支持目标地址类型"
	default:
		return "SOCKS5 返回未知错误"
	}
}

func readFull(r io.Reader, buf []byte) error {
	if _, err := io.ReadFull(r, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errors.New("代理提前关闭连接")
		}
		return err
	}
	return nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 || s[i] <= 0x20 {
			return false
		}
	}
	return true
}

func isTimeout(err error) bool {
	if errors.Is(err, errTimeout) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// Windows socket error codes, matched alongside the portable syscall values.
const (
	wsaeConnRefused = syscall.Errno(10061)
	wsaeConnReset   = syscall.Errno(10054)
	wsaeNetUnreach  = syscall.Errno(10051)
	wsaeHostUnreach = syscall.Errno(10065)
	wsaeTimedOut    = syscall.Errno(10060)
)

func errnoOf(err error) (syscall.Errno, bool) {
	var e syscall.Errno
	if errors.As(err, &e) {
		return e, true
	}
	return 0, false
}

// failureReason turns an error into the launcher's short Chinese reason,
// following the legacy Get-FullPathFailureReason mapping.
func failureReason(err error) string {
	if isTimeout(err) {
		return "测速请求超时"
	}
	if e, ok := errnoOf(err); ok {
		switch {
		case e == wsaeConnRefused || e == syscall.ECONNREFUSED:
			return "连接被拒绝"
		case e == wsaeTimedOut || e == syscall.ETIMEDOUT:
			return "测速请求超时"
		case e == wsaeConnReset || e == syscall.ECONNRESET:
			return "测速连接被重置"
		case e == wsaeNetUnreach || e == syscall.ENETUNREACH:
			return "目标网络不可达"
		case e == wsaeHostUnreach || e == syscall.EHOSTUNREACH:
			return "目标主机不可达"
		default:
			return fmt.Sprintf("TCP 连接失败（%d）", uintptr(e))
		}
	}
	if errors.Is(err, errInvalidUTF8) {
		return "测速回复不是有效 UTF-8"
	}
	return err.Error()
}
