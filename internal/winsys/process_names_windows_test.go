//go:build windows

package winsys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessNamesIncludesThisProgram(t *testing.T) {
	names, err := ProcessNames()
	if err != nil {
		t.Fatal(err)
	}
	self := strings.ToLower(filepath.Base(os.Args[0]))
	seen := map[string]int{}
	for _, n := range names {
		seen[strings.ToLower(n)]++
	}
	if seen[self] != 1 {
		t.Fatalf("%s appears %d times among %d names", self, seen[self], len(names))
	}
}
