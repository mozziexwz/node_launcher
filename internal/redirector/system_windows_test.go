//go:build windows

package redirector

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

// Concurrent probes from one process (the UI refreshing twice at startup)
// must not report each other as a conflict, while a real holder still is.
func TestPortConflictsConcurrentProbes(t *testing.T) {
	free, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()

	var mu sync.Mutex
	var busy []string
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := portConflicts(port)
			mu.Lock()
			busy = append(busy, b...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(busy) > 0 {
		t.Fatalf("free port reported busy by concurrent probes: %v", busy)
	}

	holder, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if b := portConflicts(port); len(b) == 0 {
		t.Fatal("a port held by another listener must be reported")
	}
}
