package engine

import (
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func loadExample(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/client-example.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseConfigActiveProfile(t *testing.T) {
	cfg, err := ParseConfig(loadExample(t))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := cfg.Profile.GetProfileName(); got != "default" {
		t.Fatalf("profile = %q, want default", got)
	}
}

func TestParseConfigRejectsInvalid(t *testing.T) {
	valid := string(loadExample(t))
	cases := map[string]string{
		"not json":         "{",
		"unknown field":    strings.Replace(valid, `"mtu": 1400`, `"mtu": 1400, "bogus": 1`, 1),
		"missing profile":  strings.Replace(valid, `"activeProfile": "default"`, `"activeProfile": "other"`, 1),
		"bad socks5 port":  strings.Replace(valid, `"socks5Port": 10086`, `"socks5Port": 70000`, 1),
		"no active marker": strings.Replace(valid, `"activeProfile": "default",`, ``, 1),
	}
	for name, text := range cases {
		if _, err := ParseConfig([]byte(text)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// socksAuth performs the SOCKS5 greeting and RFC 1929 authentication only.
func socksAuth(t *testing.T, addr, user, password string) (methodOK bool, authOK bool) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 2 {
		return false, false
	}
	req := append([]byte{1, byte(len(user))}, user...)
	req = append(append(req, byte(len(password))), password...)
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, reply); err != nil {
		return true, false
	}
	return true, reply[1] == 0
}

func TestEngineServesLoopbackSOCKS5WithSessionCredentials(t *testing.T) {
	cfg, err := ParseConfig(loadExample(t))
	if err != nil {
		t.Fatal(err)
	}
	e, err := Start(cfg, 0)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer e.Close()

	addr := e.Addr()
	if !addr.IP.IsLoopback() || addr.Port == 0 {
		t.Fatalf("listen address %v is not a loopback port", addr)
	}
	user, password := e.Credential()
	if len(user) != 32 || len(password) != 48 {
		t.Fatalf("unexpected credential lengths %d/%d", len(user), len(password))
	}
	other, err := Start(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if u2, p2 := other.Credential(); u2 == user || p2 == password {
		t.Fatal("credentials must differ between sessions")
	}

	if methodOK, authOK := socksAuth(t, addr.String(), user, "wrong-password"); !methodOK || authOK {
		t.Fatalf("wrong password: method=%v auth=%v, want method accepted and auth rejected", methodOK, authOK)
	}
	if methodOK, authOK := socksAuth(t, addr.String(), user, password); !methodOK || !authOK {
		t.Fatalf("correct credentials: method=%v auth=%v", methodOK, authOK)
	}
	if got := e.Stats().Accepted; got < 2 {
		t.Fatalf("accepted = %d, want >= 2", got)
	}

	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := net.DialTimeout("tcp", addr.String(), time.Second); err == nil {
		t.Fatal("listener still accepts connections after Close")
	}
}
