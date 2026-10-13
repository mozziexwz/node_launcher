package engine

import (
	"sync/atomic"
	"time"

	"github.com/enfein/mieru/v3/pkg/metrics"
)

// nodeTraffic reads the core's process-wide byte counters for traffic with
// the node. They include encryption and padding, which is what the node's
// traffic accounting sees.
func nodeTraffic() (up, down int64) {
	return metrics.UploadBytes.Load(), metrics.DownloadBytes.Load()
}

// trafficMeter turns cumulative counters into totals since it was created
// and per-second rates.
type trafficMeter struct {
	read             func() (up, down int64)
	baseUp, baseDown int64
	upRate, downRate atomic.Int64
}

func newTrafficMeter(read func() (up, down int64)) *trafficMeter {
	m := &trafficMeter{read: read}
	m.baseUp, m.baseDown = read()
	return m
}

func (m *trafficMeter) totals() (up, down int64) {
	u, d := m.read()
	return u - m.baseUp, d - m.baseDown
}

func (m *trafficMeter) rates() (up, down int64) {
	return m.upRate.Load(), m.downRate.Load()
}

// run samples on every tick until stop is closed. The rate is the traffic
// since the previous sample, scaled to one second; the first interval starts
// at start, when the meter was created.
func (m *trafficMeter) run(start time.Time, tick <-chan time.Time, stop <-chan struct{}) {
	prev := start
	var prevUp, prevDown int64
	for {
		select {
		case <-stop:
			return
		case now := <-tick:
			elapsed := now.Sub(prev).Seconds()
			if elapsed <= 0 {
				continue
			}
			up, down := m.totals()
			m.upRate.Store(int64(float64(up-prevUp) / elapsed))
			m.downRate.Store(int64(float64(down-prevDown) / elapsed))
			prev, prevUp, prevDown = now, up, down
		}
	}
}
