package probe

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Full-path probe contract (v2) limits.
const (
	FullPathProbePort = 20424
	WarmupTimeout     = 5 * time.Second
	AttemptTimeout    = 2 * time.Second
	maxReplyLine      = 4096 // bytes before LF; a trailing CR counts
)

// failureMessages maps the v2 "error" enum to user-facing reasons.
var failureMessages = map[string]string{
	"timeout":            "节点到游戏 TCP 连接超时",
	"refused":            "游戏端口拒绝连接",
	"unreachable":        "节点无法到达游戏服务器",
	"target_not_allowed": "节点不允许该目标，请使用公网 IPv4 和有效端口",
	"failed":             "节点到游戏 TCP 连接失败",
}

// Local JSON work is kept outside the timed window: the request is built
// before the clock starts and the reply is decoded after the arrival time is
// captured. Tests replace these with slowed-down versions to prove it.
var (
	newConnectRequest = func(nonce, gameAddr string, gamePort int) []byte {
		return []byte(`{"version":2,"operation":"connect","nonce":"` + nonce +
			`","target_address":"` + gameAddr + `","target_port":` + strconv.Itoa(gamePort) + "}\n")
	}
	decodeReply = parseReply
)

var (
	errInvalidUTF8 = errors.New("测速回复不是有效 UTF-8")

	// v2 replies are flat objects of scalars whose strings use only
	// unescaped [A-Za-z0-9_.-]. Checking this grammar and unique keys first
	// matters because encoding/json silently accepts duplicate keys.
	replyScalar  = `(?:"[A-Za-z0-9_.-]*"|true|false|null|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)`
	replyField   = `"[a-z_]+"[ \t\r]*:[ \t\r]*` + replyScalar
	flatObjectRe = regexp.MustCompile(`\A[ \t\r]*\{[ \t\r]*` + replyField + `(?:[ \t\r]*,[ \t\r]*` + replyField + `)*[ \t\r]*\}[ \t\r]*\z`)
	replyKeyRe   = regexp.MustCompile(`"([a-z_]+)"[ \t\r]*:`)
	nodeIDRe     = regexp.MustCompile(`\A[0-9a-f]{32}\z`)
)

// FullPathOptions tunes the probe. Zero values use the contract defaults;
// values beyond the contract limits make the probe refuse to run.
type FullPathOptions struct {
	ProbePort      int
	Count          int
	AttemptTimeout time.Duration
	WarmupTimeout  time.Duration
}

func (o FullPathOptions) withDefaults() FullPathOptions {
	if o.ProbePort == 0 {
		o.ProbePort = FullPathProbePort
	}
	if o.Count == 0 {
		o.Count = DefaultSampleCount
	}
	if o.AttemptTimeout == 0 {
		o.AttemptTimeout = AttemptTimeout
	}
	if o.WarmupTimeout == 0 {
		o.WarmupTimeout = WarmupTimeout
	}
	return o
}

// FullPathLatency measures, through proxy, the round trip from sending a v2
// connect request to receiving the final node's confirmation. The node only
// confirms after a real TCP connect to gameAddr:gamePort. Session warmup,
// request serialization and reply validation are excluded from timing.
func FullPathLatency(proxy Proxy, gameAddr string, gamePort int, opts FullPathOptions) Result {
	opts = opts.withDefaults()
	if !isCanonicalIPv4(gameAddr) || gamePort < 1 || gamePort > 65535 ||
		opts.ProbePort < 1 || opts.ProbePort > 65535 ||
		opts.Count < 1 || opts.Count > DefaultSampleCount ||
		opts.AttemptTimeout <= 0 || opts.AttemptTimeout > AttemptTimeout ||
		opts.WarmupTimeout <= 0 || opts.WarmupTimeout > WarmupTimeout {
		return Unavailable("测速参数无效，请使用规范 IPv4、有效端口及超时")
	}

	var session *fullPathSession
	defer func() { session.close() }()
	samples := make([]Sample, 0, opts.Count)
	for i := 0; i < opts.Count; i++ {
		if session == nil {
			s, err := openFullPathSession(proxy, opts.ProbePort, opts.WarmupTimeout)
			if err != nil {
				// A missing or old service must not cause repeated connection attempts.
				for ; i < opts.Count; i++ {
					samples = append(samples, Sample{Err: err.Error()})
				}
				break
			}
			session = s
		}

		nonce, err := newNonce()
		if err != nil {
			samples = append(samples, Sample{Err: err.Error()})
			continue
		}
		request := newConnectRequest(nonce, gameAddr, gamePort)

		start := time.Now()
		deadline := start.Add(opts.AttemptTimeout)
		sample, keep := session.connect(request, start, deadline, opts.AttemptTimeout, nonce, gameAddr, gamePort)
		samples = append(samples, sample)
		if !keep {
			// Protocol errors, EOF and timeouts break message boundaries:
			// discard the session and warm up a new one for the next attempt.
			session.close()
			session = nil
		}
	}
	return NewResult(samples)
}

type fullPathSession struct {
	conn   net.Conn
	nodeID string
}

func (s *fullPathSession) close() {
	if s != nil && s.conn != nil {
		s.conn.Close()
	}
}

// openFullPathSession runs the shared warmup: connect to the local SOCKS5,
// CONNECT the node-side 127.0.0.1:probePort and complete hello, all within
// one timeout budget.
func openFullPathSession(proxy Proxy, probePort int, timeout time.Duration) (*fullPathSession, error) {
	start := time.Now()
	deadline := start.Add(timeout)
	// This destination is the loopback service on the final MSBOOST node,
	// resolved by the node, not a local Windows port.
	conn, err := DialSOCKS5(proxy, "127.0.0.1", probePort, deadline)
	if err != nil {
		return nil, sessionError(err)
	}
	nodeID, err := hello(conn, start, deadline, timeout)
	if err != nil {
		conn.Close()
		return nil, sessionError(err)
	}
	return &fullPathSession{conn: conn, nodeID: nodeID}, nil
}

func hello(conn net.Conn, start, deadline time.Time, timeout time.Duration) (string, error) {
	nonce, err := newNonce()
	if err != nil {
		return "", err
	}
	request := []byte(`{"version":2,"operation":"hello","nonce":"` + nonce + "\"}\n")
	if err := writeAll(conn, request, deadline); err != nil {
		return "", err
	}
	line, _, err := readReplyLine(conn, start, deadline, timeout)
	if err != nil {
		return "", err
	}
	fields, err := decodeReply(line)
	if err != nil {
		return "", err
	}
	_, _, nodeID, err := checkReply(fields, "hello", nonce, "", "", 0)
	if err != nil {
		return "", err
	}
	if time.Now().After(deadline) {
		return "", errTimeout
	}
	return nodeID, nil
}

// connect sends one request and returns the sample plus whether the session
// may be reused. Only a valid success reply yields a duration.
func (s *fullPathSession) connect(request []byte, start, deadline time.Time, timeout time.Duration,
	nonce, gameAddr string, gamePort int) (Sample, bool) {
	if err := writeAll(s.conn, request, deadline); err != nil {
		return Sample{Err: failureReason(err)}, false
	}
	line, elapsed, err := readReplyLine(s.conn, start, deadline, timeout)
	if err != nil {
		return Sample{Err: failureReason(err)}, false
	}
	fields, err := decodeReply(line)
	if err != nil {
		return Sample{Err: failureReason(err)}, false
	}
	ok, code, _, err := checkReply(fields, "connect", nonce, s.nodeID, gameAddr, gamePort)
	if err != nil {
		return Sample{Err: failureReason(err)}, false
	}
	if !ok {
		// A valid failure reply keeps the message boundary intact.
		return Sample{Err: failureMessages[code]}, true
	}
	return Sample{OK: true, Duration: elapsed}, true
}

func sessionError(err error) error {
	reason := failureReason(err)
	var pe *phaseError
	if errors.As(err, &pe) {
		if pe.phase == phaseProxy {
			return errors.New("本地代理未启动、端口未监听或连接超时：" + reason)
		}
		if strings.Contains(reason, "认证") {
			return errors.New(reason)
		}
	}
	return errors.New("节点未提供兼容的版本 2 全程 TCP 测速服务或服务不可达：" + reason)
}

func writeAll(conn net.Conn, b []byte, deadline time.Time) error {
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err := conn.Write(b)
	return err
}

// readReplyLine reads exactly one LF-terminated reply and reports the time
// since start at which the chunk carrying the LF arrived. Bytes after the LF
// in the same read are a protocol error: one request gets exactly one line.
func readReplyLine(conn net.Conn, start, deadline time.Time, timeout time.Duration) ([]byte, time.Duration, error) {
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, 0, err
	}
	var data []byte
	buf := make([]byte, 512)
	for {
		limit := maxReplyLine + 1 - len(data)
		if limit > len(buf) {
			limit = len(buf)
		}
		n, err := conn.Read(buf[:limit])
		// Capture arrival before copying, decoding or validating.
		elapsed := time.Since(start)
		if n == 0 {
			if err == nil || errors.Is(err, io.EOF) {
				return nil, 0, errors.New("节点测速服务提前关闭连接")
			}
			return nil, 0, err
		}
		if elapsed > timeout {
			return nil, 0, errTimeout
		}
		if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
			if i != n-1 {
				return nil, 0, errors.New("测速服务发送了未请求的额外数据")
			}
			data = append(data, buf[:i]...)
			if len(data) > maxReplyLine {
				return nil, 0, errors.New("测速回复过长")
			}
			return data, elapsed, nil
		}
		data = append(data, buf[:n]...)
		if len(data) > maxReplyLine {
			return nil, 0, errors.New("测速回复过长")
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, 0, errors.New("节点测速服务提前关闭连接")
			}
			return nil, 0, err
		}
	}
}

type replyFields map[string]json.RawMessage

// parseReply enforces the v2 wire grammar before decoding.
func parseReply(line []byte) (replyFields, error) {
	if !utf8.Valid(line) {
		return nil, errInvalidUTF8
	}
	text := string(line)
	if !flatObjectRe.MatchString(text) {
		return nil, errors.New("测速回复不是有效的版本 2 扁平 JSON")
	}
	seen := make(map[string]bool)
	for _, m := range replyKeyRe.FindAllStringSubmatch(text, -1) {
		if seen[m[1]] {
			return nil, errors.New("测速回复包含重复字段")
		}
		seen[m[1]] = true
	}
	var fields replyFields
	if err := json.Unmarshal(line, &fields); err != nil {
		return nil, errors.New("测速回复不是有效的版本 2 扁平 JSON")
	}
	return fields, nil
}

func (f replyFields) str(key string) (string, bool) {
	raw, ok := f[key]
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func (f replyFields) boolean(key string) (value, ok bool) {
	switch string(f[key]) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// integer accepts only a plain JSON integer literal equal to want.
func (f replyFields) integer(key string, want int) bool {
	return string(f[key]) == strconv.Itoa(want)
}

// checkReply validates a decoded reply against the request it answers. For
// connect it also checks node identity, the echoed target and, on failure,
// the error enum. It returns success, the failure code and the node id.
func checkReply(f replyFields, operation, nonce, sessionNodeID, gameAddr string, gamePort int) (bool, string, string, error) {
	if !f.integer("version", 2) {
		return false, "", "", errors.New("节点测速协议不兼容，需要版本 2")
	}
	op, okOp := f.str("operation")
	gotNonce, okNonce := f.str("nonce")
	nodeID, okNode := f.str("node_id")
	success, okSuccess := f.boolean("success")
	if !okOp || op != operation || !okNonce || gotNonce != nonce ||
		!okNode || !nodeIDRe.MatchString(nodeID) || !okSuccess {
		return false, "", "", errors.New("测速回复的操作、随机标识或节点身份无效")
	}

	expected := []string{"version", "operation", "nonce", "node_id", "success"}
	code := ""
	if operation == "hello" {
		if !success {
			return false, "", "", errors.New("节点未完成版本 2 测速预热")
		}
	} else {
		if nodeID != sessionNodeID {
			return false, "", "", errors.New("测速回复节点与本次预热不一致")
		}
		addr, okAddr := f.str("target_address")
		if !okAddr || addr != gameAddr || !f.integer("target_port", gamePort) {
			return false, "", "", errors.New("测速回复的游戏目标不匹配")
		}
		expected = append(expected, "target_address", "target_port")
		if !success {
			expected = append(expected, "error")
			var okCode bool
			code, okCode = f.str("error")
			if !okCode || failureMessages[code] == "" {
				return false, "", "", errors.New("测速回复的错误类型无效")
			}
		}
	}
	if !sameKeys(f, expected) {
		return false, "", "", errors.New("测速回复包含未知、缺失或冲突字段")
	}
	return success, code, nodeID, nil
}

func sameKeys(f replyFields, expected []string) bool {
	if len(f) != len(expected) {
		return false
	}
	got := make([]string, 0, len(f))
	for k := range f {
		got = append(got, k)
	}
	sort.Strings(got)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("生成随机标识失败")
	}
	return hex.EncodeToString(b), nil
}

// isCanonicalIPv4 accepts dotted-quad IPv4 without leading zeros.
func isCanonicalIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil && !strings.Contains(s, ":") && ip.To4().String() == s
}
