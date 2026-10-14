//go:build windows

package winsys

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConflicts(t *testing.T) {
	cases := []struct {
		name     string
		in       ConflictInputs
		messages []string
		blocking bool
	}{
		{"clean", ConflictInputs{ProxyBridgeServiceState: "STOPPED"}, nil, false},
		{"service running", ConflictInputs{ProxyBridgeServiceState: "RUNNING", RelayPortsBusy: []string{"TCP 34010（0.0.0.0）"}},
			[]string{"ProxyBridge 的后台服务"}, true},
		{"gui running", ConflictInputs{RunningProcesses: []string{"ProxyBridge.exe"}}, []string{"ProxyBridge 正在运行"}, true},
		{"netch is only a warning", ConflictInputs{RunningProcesses: []string{"netch.EXE"}}, []string{"Netch"}, false},
		{"port held by something else", ConflictInputs{ProxyBridgeServiceState: "NOT_INSTALLED", RelayPortsBusy: []string{"TCP 34010（::）"}},
			[]string{"端口被占用"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Conflicts(c.in)
			if len(got) != len(c.messages) {
				t.Fatalf("got %d conflicts %+v, want %d", len(got), got, len(c.messages))
			}
			blocking := false
			for i, want := range c.messages {
				if !strings.Contains(got[i].Message, want) {
					t.Errorf("message %q lacks %q", got[i].Message, want)
				}
				blocking = blocking || got[i].Blocking
			}
			if blocking != c.blocking {
				t.Errorf("blocking = %v, want %v", blocking, c.blocking)
			}
		})
	}
}

func TestParseRuleNames(t *testing.T) {
	out := "MSBOOST relay 2294d3a7\r\nMSBOOST relay 0badf00d\r\n\r\nWARNING: something else\r\n"
	got, rest := parseRuleNames(out, "MSBOOST relay ")
	if strings.Join(got, ",") != "MSBOOST relay 2294d3a7,MSBOOST relay 0badf00d" || rest != "WARNING: something else" {
		t.Fatalf("got %v, rest %q", got, rest)
	}
	if _, err := DeleteFirewallRulesWithPrefix("x' ; Remove-Item *"); err == nil {
		t.Fatal("a prefix that could break out of the PowerShell string must be refused")
	}
}

func TestAcquireInstance(t *testing.T) {
	name := "msboost-launcher-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	release, err := AcquireInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireInstance(name); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second acquire: %v, want ErrAlreadyRunning", err)
	}
	release()
	release2, err := AcquireInstance(name)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}

func TestTerminateProcesses(t *testing.T) {
	// A copy of ping.exe under a unique name, so nothing else is touched.
	name := "msboost-test-dummy.exe"
	path := filepath.Join(t.TempDir(), name)
	src, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Skipf("ping.exe not available: %v", err)
	}
	if err := os.WriteFile(path, src, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "-n", "30", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	n, err := TerminateProcesses([]string{"MSBOOST-Test-Dummy.exe"})
	if err != nil || n != 1 {
		t.Fatalf("ended %d processes, err %v", n, err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the process is still running")
	}
	if n, err := TerminateProcesses([]string{name}); n != 0 || err != nil {
		t.Fatalf("nothing left to end, got %d, %v", n, err)
	}
}
