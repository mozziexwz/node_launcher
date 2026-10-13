package engine

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestTrafficMeter(t *testing.T) {
	var up, down atomic.Int64
	up.Store(1000) // counters are process-wide: earlier engines count too
	down.Store(5000)
	m := newTrafficMeter(func() (int64, int64) { return up.Load(), down.Load() })

	start := time.Unix(100, 0)
	tick := make(chan time.Time)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		m.run(start, tick, stop)
		close(done)
	}()

	up.Add(2048)
	down.Add(10240)
	tick <- start.Add(2 * time.Second)
	// No time passed: the sample is skipped. Receiving it also means the
	// first tick has been handled.
	tick <- start.Add(2 * time.Second)
	up.Add(512) // totals are live; rates change only on the next tick
	close(stop)
	<-done

	if u, d := m.totals(); u != 2560 || d != 10240 {
		t.Fatalf("totals %d/%d, want 2560/10240", u, d)
	}
	if u, d := m.rates(); u != 1024 || d != 5120 {
		t.Fatalf("rates %d/%d per second, want 1024/5120", u, d)
	}
}
