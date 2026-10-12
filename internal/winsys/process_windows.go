//go:build windows

// Package winsys holds small Windows helpers shared by the launcher.
package winsys

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

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
