//go:build windows

// Package winsys holds small Windows helpers shared by the launcher.
package winsys

import (
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processIDs returns the IDs of running processes whose image name equals
// one of names (case-insensitive).
func processIDs(names []string) (map[uint32]bool, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.ToLower(n)] = true
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	pids := make(map[uint32]bool)
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if want[strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))] {
			pids[entry.ProcessID] = true
		}
	}
	return pids, nil
}

var (
	moduser32          = windows.NewLazySystemDLL("user32.dll")
	procPostMessageW   = moduser32.NewProc("PostMessageW")
	procGetWindowTextW = moduser32.NewProc("GetWindowTextW")

	// The enumeration callback is created once: callbacks are never freed.
	exitMu       sync.Mutex
	exitTargets  map[uint32]bool
	exitTitle    string
	exitCommand  uintptr
	exitCallback = windows.NewCallback(postExitCommand)
)

const wmCommand = 0x0111

// postExitCommand posts the exit command to a target process's top-level
// window whose title starts with exitTitle. Runs inside EnumWindows.
func postExitCommand(hwnd windows.HWND, _ uintptr) uintptr {
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || !exitTargets[pid] {
		return 1
	}
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if strings.HasPrefix(windows.UTF16ToString(buf[:n]), exitTitle) {
		procPostMessageW.Call(uintptr(hwnd), wmCommand, exitCommand, 0)
	}
	return 1 // keep enumerating
}

// ExitPrograms asks running programs named in names to exit through their
// own menu command: command is posted as WM_COMMAND to their top-level
// windows whose title starts with title, as if the user chose "Exit". It
// waits up to timeout and returns how many exited and how many still run.
// Programs without such a window (console apps) keep running.
func ExitPrograms(names []string, title string, command uintptr, timeout time.Duration) (exited, running int, err error) {
	pids, err := processIDs(names)
	if err != nil || len(pids) == 0 {
		return 0, 0, err
	}
	exitMu.Lock()
	exitTargets, exitTitle, exitCommand = pids, title, command
	windows.EnumWindows(exitCallback, nil)
	exitTargets = nil
	exitMu.Unlock()

	deadline := time.Now().Add(timeout)
	for pid := range pids {
		h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
		if err != nil {
			exited++ // already gone
			continue
		}
		wait := time.Until(deadline)
		if wait < 0 {
			wait = 0
		}
		event, _ := windows.WaitForSingleObject(h, uint32(wait.Milliseconds()))
		windows.CloseHandle(h)
		if event == windows.WAIT_OBJECT_0 {
			exited++
		} else {
			running++
		}
	}
	return exited, running, nil
}

// RunningMatches returns the running process image names that equal one of
// names (case-insensitive). Wildcard entries are ignored.
func RunningMatches(names []string) ([]string, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		if !strings.Contains(n, "*") {
			want[strings.ToLower(n)] = true
		}
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	seen := make(map[string]bool)
	var found []string
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		key := strings.ToLower(name)
		if want[key] && !seen[key] {
			seen[key] = true
			found = append(found, name)
		}
	}
	return found, nil
}
