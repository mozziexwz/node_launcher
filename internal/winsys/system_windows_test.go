//go:build windows

package winsys

import (
	"errors"
	"strings"
	"testing"
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
			[]string{"sc stop ProxyBridgeSvc"}, true},
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
