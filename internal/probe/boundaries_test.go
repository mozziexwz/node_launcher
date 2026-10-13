package probe

// Port of legacy/tests/Test-FullPathBoundaries.ps1: the 64 frame and reply
// boundary cases, run against in-memory connections only (no sockets).

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

const (
	bNonce     = "0123456789abcdef0123456789abcdef"
	bNextNonce = "abcdef0123456789abcdef0123456789"
	bNodeID    = "89abcdef0123456789abcdef01234567"
	bGame      = "35.155.204.207"
	bPort      = 8585
)

var (
	bHello   = `{"version":2,"operation":"hello","nonce":"` + bNonce + `","node_id":"` + bNodeID + `","success":true}`
	bConnect = `{"version":2,"operation":"connect","nonce":"` + bNonce + `","node_id":"` + bNodeID + `","success":true,"target_address":"35.155.204.207","target_port":8585}`
)

// scriptedConn serves fixed bytes, at most chunk bytes per Read, like the
// PowerShell ScriptedStream.
type scriptedConn struct {
	data  []byte
	pos   int
	chunk int
}

func (c *scriptedConn) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := len(p)
	if n > c.chunk {
		n = c.chunk
	}
	if rest := len(c.data) - c.pos; n > rest {
		n = rest
	}
	copy(p, c.data[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}

func (c *scriptedConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *scriptedConn) Close() error                     { return nil }
func (c *scriptedConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *scriptedConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *scriptedConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptedConn) SetWriteDeadline(time.Time) error { return nil }
func readFrame(data []byte, chunk int) ([]byte, time.Duration, error) {
	return readFromConn(&scriptedConn{data: data, chunk: chunk})
}

func readFromConn(c net.Conn) ([]byte, time.Duration, error) {
	start := time.Now()
	return readReplyLine(c, start, start.Add(2*time.Second), 2*time.Second)
}

func confirmHello(line []byte) error {
	f, err := parseReply(line)
	if err != nil {
		return err
	}
	_, _, _, err = checkReply(f, "hello", bNonce, "", "", 0)
	return err
}

func confirmConnect(line []byte, nonce string) (bool, string, error) {
	f, err := parseReply(line)
	if err != nil {
		return false, "", err
	}
	ok, code, _, err := checkReply(f, "connect", nonce, bNodeID, bGame, bPort)
	return ok, code, err
}

func mustReject(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected rejection, but the malformed input was accepted")
	}
	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Fatalf("unexpected rejection reason: %v", err)
	}
}

func TestBoundaryFrames(t *testing.T) {
	t.Run("LF frame validates and has a finite nonnegative client timestamp", func(t *testing.T) {
		line, elapsed, err := readFrame([]byte(bHello+"\n"), 512)
		if err != nil {
			t.Fatal(err)
		}
		if err := confirmHello(line); err != nil {
			t.Fatal(err)
		}
		if elapsed < 0 || elapsed > 2*time.Second {
			t.Fatalf("invalid arrival timestamp %v", elapsed)
		}
	})
	t.Run("CRLF preserves trailing CR as permitted JSON whitespace", func(t *testing.T) {
		line, _, err := readFrame([]byte(bHello+"\r\n"), 512)
		if err != nil {
			t.Fatal(err)
		}
		if line[len(line)-1] != '\r' {
			t.Fatal("CR must remain inside the line")
		}
		if err := confirmHello(line); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("one-byte fragmented JSON and LF are fully assembled", func(t *testing.T) {
		line, _, err := readFrame([]byte(bHello+"\n"), 1)
		if err != nil {
			t.Fatal(err)
		}
		if string(line) != bHello {
			t.Fatalf("reassembled %q", line)
		}
		if err := confirmHello(line); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("4096 content bytes followed by LF are accepted", func(t *testing.T) {
		padded := bHello + strings.Repeat(" ", 4096-len(bHello))
		line, _, err := readFrame([]byte(padded+"\n"), 512)
		if err != nil {
			t.Fatal(err)
		}
		if len(line) != 4096 {
			t.Fatalf("length %d", len(line))
		}
		if err := confirmHello(line); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("4096 content bytes including CR followed by LF are accepted", func(t *testing.T) {
		padded := bHello + strings.Repeat(" ", 4095-len(bHello)) + "\r"
		line, _, err := readFrame([]byte(padded+"\n"), 31)
		if err != nil {
			t.Fatal(err)
		}
		if len(line) != 4096 {
			t.Fatalf("CR counts in the maximum content length: got %d", len(line))
		}
		if err := confirmHello(line); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("4097 content bytes before LF are rejected", func(t *testing.T) {
		padded := bHello + strings.Repeat(" ", 4097-len(bHello))
		_, _, err := readFrame([]byte(padded+"\n"), 512)
		mustReject(t, err, "过长")
	})
	t.Run("4096 JSON bytes plus CR before LF exceed the limit", func(t *testing.T) {
		padded := bHello + strings.Repeat(" ", 4096-len(bHello))
		_, _, err := readFrame([]byte(padded+"\r\n"), 512)
		mustReject(t, err, "过长")
	})
	t.Run("EOF after a partial response is rejected", func(t *testing.T) {
		_, _, err := readFrame([]byte(bHello), 512)
		mustReject(t, err, "提前关闭")
	})
	t.Run("empty LF frame cannot validate as a hello reply", func(t *testing.T) {
		line, _, err := readFrame([]byte("\n"), 512)
		if err != nil {
			t.Fatal(err)
		}
		mustReject(t, confirmHello(line), "")
	})
	t.Run("extra byte after LF in the same read is rejected", func(t *testing.T) {
		_, _, err := readFrame([]byte(bHello+"\nX"), 512)
		mustReject(t, err, "额外数据")
	})
	t.Run("two complete merged reply lines in one read are rejected", func(t *testing.T) {
		_, _, err := readFrame([]byte(bHello+"\n"+bHello+"\n"), 512)
		mustReject(t, err, "额外数据")
	})
	t.Run("a separately arriving duplicate line remains visible and cannot match the next nonce", func(t *testing.T) {
		first := []byte(bConnect + "\n")
		conn := &scriptedConn{data: append(append([]byte(nil), first...), first...), chunk: len(first)}
		line, _, err := readFromConn(conn)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := confirmConnect(line, bNonce); err != nil {
			t.Fatal(err)
		}
		if conn.pos != len(first) {
			t.Fatalf("consumed %d bytes, want only the first frame (%d)", conn.pos, len(first))
		}
		line, _, err = readFromConn(conn)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = confirmConnect(line, bNextNonce)
		mustReject(t, err, "随机标识")
		if conn.pos != 2*len(first) {
			t.Fatal("the retained duplicate frame must be read rather than dropped")
		}
	})
	t.Run("JSON spacing outside strings accepts SP HT and CR", func(t *testing.T) {
		json := " \t\r" + strings.ReplaceAll(bHello, ":", "\t:\r") + "\r\t "
		if err := confirmHello([]byte(json)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestBoundaryMalformedJSON(t *testing.T) {
	escaped := "\\" + "u0030" + bNonce[1:]
	cases := []struct{ name, json string }{
		{"NUL outside strings", bHello + "\x00"},
		{"TAB inside nonce string", strings.Replace(bHello, bNonce, bNonce+"\t", 1)},
		{"CR inside nonce string", strings.Replace(bHello, bNonce, bNonce+"\r", 1)},
		{"trailing non-whitespace", bHello + "x"},
		{"two JSON objects without a line boundary", bHello + bHello},
		{"nested node identity", strings.Replace(bHello, `"`+bNodeID+`"`, `{"id":"x"}`, 1)},
		{"array root", "[" + bHello + "]"},
		{"null root", "null"},
		{"duplicate nonce field", strings.Replace(bHello, `"success":true`, `"nonce":"`+bNonce+`","success":true`, 1)},
		{"duplicate version field", strings.Replace(bHello, `"version":2`, `"version":2,"version":2`, 1)},
		{"uppercase field name", strings.Replace(bHello, `"version"`, `"Version"`, 1)},
		{"escaped unicode nonce", strings.Replace(bHello, bNonce, escaped, 1)},
		{"leading-zero version", strings.Replace(bHello, `"version":2`, `"version":02`, 1)},
	}
	for _, c := range cases {
		t.Run("reject malformed JSON: "+c.name, func(t *testing.T) {
			mustReject(t, confirmHello([]byte(c.json)), "")
		})
	}
	t.Run("UTF-8 BOM is rejected", func(t *testing.T) {
		mustReject(t, confirmHello(append([]byte{0xef, 0xbb, 0xbf}, bHello...)), "")
	})
	t.Run("malformed UTF-8 is rejected before JSON conversion", func(t *testing.T) {
		_, err := parseReply([]byte{'{', '"', 0xc3, '(', '"', '}'})
		mustReject(t, err, "UTF-8")
	})
	t.Run("a physical LF inside a string ends an invalid frame", func(t *testing.T) {
		json := strings.Replace(bHello, bNonce, "0\n"+bNonce[1:], 1)
		line, _, err := readFrame([]byte(json+"\n"), 1)
		if err == nil {
			err = confirmHello(line)
		}
		mustReject(t, err, "")
	})
}

func TestBoundaryInvalidHello(t *testing.T) {
	cases := []struct{ name, json string }{
		{"floating-point version", strings.Replace(bHello, `"version":2`, `"version":2.0`, 1)},
		{"exponent version", strings.Replace(bHello, `"version":2`, `"version":2e0`, 1)},
		{"boolean version", strings.Replace(bHello, `"version":2`, `"version":true`, 1)},
		{"string version", strings.Replace(bHello, `"version":2`, `"version":"2"`, 1)},
		{"old protocol version", strings.Replace(bHello, `"version":2`, `"version":1`, 1)},
		{"string success", strings.Replace(bHello, `"success":true`, `"success":"true"`, 1)},
		{"numeric success", strings.Replace(bHello, `"success":true`, `"success":1`, 1)},
		{"null success", strings.Replace(bHello, `"success":true`, `"success":null`, 1)},
		{"failed hello", strings.Replace(bHello, `"success":true`, `"success":false`, 1)},
		{"missing success", strings.Replace(bHello, `,"success":true`, ``, 1)},
		{"wrong operation", strings.Replace(bHello, `"hello"`, `"connect"`, 1)},
		{"wrong nonce", strings.Replace(bHello, bNonce, bNextNonce, 1)},
		{"uppercase node identity", strings.Replace(bHello, bNodeID, strings.ToUpper(bNodeID), 1)},
		{"unknown compatibility timing", strings.Replace(bHello, `"success":true`, `"success":true,"connect_ms":1`, 1)},
		{"error on successful hello", strings.Replace(bHello, `"success":true`, `"success":true,"error":"failed"`, 1)},
	}
	for _, c := range cases {
		t.Run("reject invalid hello reply: "+c.name, func(t *testing.T) {
			mustReject(t, confirmHello([]byte(c.json)), "")
		})
	}
}

func TestBoundaryConnect(t *testing.T) {
	t.Run("a complete matching connect success validates", func(t *testing.T) {
		ok, _, err := confirmConnect([]byte(bConnect), bNonce)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})
	cases := []struct{ name, json string }{
		{"wrong game IPv4", strings.Replace(bConnect, "35.155.204.207", "51.222.56.192", 1)},
		{"wrong game port", strings.Replace(bConnect, "8585", "8484", 1)},
		{"floating-point game port", strings.Replace(bConnect, "8585", "8585.0", 1)},
		{"exponent game port", strings.Replace(bConnect, "8585", "8585e0", 1)},
		{"boolean game port", strings.Replace(bConnect, "8585", "true", 1)},
		{"string game port", strings.Replace(bConnect, "8585", `"8585"`, 1)},
		{"wrong protocol version", strings.Replace(bConnect, `"version":2`, `"version":1`, 1)},
		{"wrong operation", strings.Replace(bConnect, `"connect"`, `"hello"`, 1)},
		{"changed node identity", strings.Replace(bConnect, bNodeID, bNextNonce, 1)},
		{"repeated prior nonce", strings.Replace(bConnect, bNonce, bNextNonce, 1)},
		{"error on successful connect", strings.Replace(bConnect, `"success":true`, `"success":true,"error":"failed"`, 1)},
		{"missing error on failed connect", strings.Replace(bConnect, `"success":true`, `"success":false`, 1)},
		{"unknown failure enum", strings.Replace(bConnect, `"success":true`, `"success":false,"error":"unknown"`, 1)},
		{"legacy samples field", strings.Replace(bConnect, `"success":true`, `"success":true,"samples":0`, 1)},
	}
	for _, c := range cases {
		t.Run("reject invalid connect reply: "+c.name, func(t *testing.T) {
			_, _, err := confirmConnect([]byte(c.json), bNonce)
			mustReject(t, err, "")
		})
	}
	for _, reason := range []string{"timeout", "refused", "unreachable", "target_not_allowed", "failed"} {
		t.Run("valid connect failure enum: "+reason, func(t *testing.T) {
			json := strings.Replace(bConnect, `"success":true`, `"success":false,"error":"`+reason+`"`, 1)
			ok, code, err := confirmConnect([]byte(json), bNonce)
			if err != nil || ok || code != reason {
				t.Fatalf("ok=%v code=%q err=%v", ok, code, err)
			}
		})
	}
}
