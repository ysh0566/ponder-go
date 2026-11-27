package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/ysh0566/ponder-go/internal/devstatus"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/rpc"
)

// One watched application owns these counters, including all its chain workers.
// A hot reload starts a fresh set of samples.
type devMonitorKey struct{}
type devMonitor struct {
	mu     sync.Mutex
	chains map[uint64]*devChain
	events map[string]devEvent
}

type devEvent struct {
	count   uint64
	elapsed time.Duration
}

type devPoint struct {
	at    time.Time
	count uint64
}

type devChain struct {
	name               string
	client             *rpc.Client
	total, completed   uint64
	requests, backfill []devPoint
}

func newDevMonitor() *devMonitor {
	return &devMonitor{chains: make(map[uint64]*devChain), events: make(map[string]devEvent)}
}

func devMonitorFrom(ctx context.Context) *devMonitor {
	m, _ := ctx.Value(devMonitorKey{}).(*devMonitor)
	return m
}

func (m *devMonitor) register(e *Engine) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chains[e.chain.ID] == nil {
		m.chains[e.chain.ID] = &devChain{name: e.chain.Name, client: e.Client,
			requests: []devPoint{{at: time.Now(), count: e.Client.TransportRequests()}}}
		for name, handler := range e.Handlers {
			if _, ok := m.events[name]; !ok && handler != nil {
				m.events[name] = devEvent{}
			}
		}
	}
}

// Reconcile from persisted missing intervals on every scan. Resumed cache hits
// count toward progress but never toward measured processing speed.
func (m *devMonitor) setRange(id, total, completed uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.chains[id]
	if c.total == 0 || completed != c.completed || total < c.total || (c.completed == c.total && completed < total) {
		c.backfill = []devPoint{{at: time.Now(), count: completed}}
	}
	c.total, c.completed = total, completed
}

// A coordinated rewind can invalidate another chain's completed intervals even
// while its RPC is unavailable. Its next range scan supplies the new baseline.
func (m *devMonitor) rewind(id uint64, shared bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for chain, c := range m.chains {
		if shared || chain == id {
			c.total, c.completed, c.backfill = 0, 0, nil
		}
	}
}

func (m *devMonitor) completeBlock(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.chains[id]; c != nil && c.completed < c.total {
		c.completed++
	}
}

func invokeHandler(ctx context.Context, handler Handler, c *Context) error {
	m := devMonitorFrom(ctx)
	registry := metrics.Historical(ctx)
	timings := metrics.Handlers(ctx)
	if m == nil && registry == nil && timings == nil {
		return handler(ctx, c)
	}
	chain, name := c.Chain.Name, c.Event.Name
	finish := registry.StartTransform("index")
	start := time.Now()
	if err := handler(ctx, c); err != nil {
		return err
	}
	elapsed := time.Since(start)
	finish()
	timings.ObserveHandler(chain, name, elapsed)
	if m == nil {
		return nil
	}
	m.mu.Lock()
	event := m.events[name]
	event.count++
	event.elapsed += elapsed
	m.events[name] = event
	m.mu.Unlock()
	return nil
}

func appendDevPoint(points []devPoint, at time.Time, count uint64, limit int) []devPoint {
	if len(points) == limit {
		copy(points, points[1:])
		points = points[:limit-1]
	}
	return append(points, devPoint{at: at, count: count})
}

func (c *devChain) sample(now time.Time) devstatus.ChainMetrics {
	n := c.client.TransportRequests()
	c.requests = appendDevPoint(c.requests, now, n, 100)
	first := c.requests[0]
	var metrics devstatus.ChainMetrics
	if elapsed := now.Sub(first.at).Seconds(); elapsed > 0 {
		metrics.RequestsPerSecond = float64(n-first.count) / elapsed
	}
	if c.total == 0 {
		return metrics
	}
	progress := &devstatus.Backfill{Total: c.total, Completed: c.completed}
	metrics.Backfill = progress
	if c.completed == c.total {
		return metrics
	}
	last := c.backfill[len(c.backfill)-1]
	if now.Sub(last.at) >= 5*time.Second && c.completed > last.count {
		c.backfill = appendDevPoint(c.backfill, now, c.completed, 10)
	}
	// Require two advancing five-second observations before estimating. More
	// recent intervals carry more weight as RPC and callback costs change.
	if len(c.backfill) >= 3 {
		var sum, weights float64
		for i := 1; i < len(c.backfill); i++ {
			a, b := c.backfill[i-1], c.backfill[i]
			sum = sum/1.5 + b.at.Sub(a.at).Seconds()/float64(b.count-a.count)
			weights = weights/1.5 + 1
		}
		seconds := float64(c.total-c.completed) * sum / weights
		progress.ETASeconds = &seconds
	}
	return metrics
}

func (m *devMonitor) sample(now time.Time, aliases map[uint64]string, report *devstatus.Snapshot) map[uint64]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make(map[uint64]string, len(m.chains))
	report.Chains = make(map[string]devstatus.ChainMetrics, len(m.chains))
	for id, c := range m.chains {
		name := aliases[id]
		if name == "" {
			name = c.name
		}
		names[id] = name
		report.Chains[name] = c.sample(now)
	}
	report.Events = make(map[string]devstatus.EventMetrics, len(m.events))
	for name, e := range m.events {
		value := devstatus.EventMetrics{Count: e.count}
		if e.count != 0 {
			value.AverageMillis = float64(e.elapsed) / float64(time.Millisecond) / float64(e.count)
		}
		report.Events[name] = value
	}
	return names
}
