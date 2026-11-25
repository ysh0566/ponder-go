package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"
)

// NewSourceClient configures the per-provider admission and retry policy used
// by the source chain configuration. Transport slots are shared by cache views.
func NewSourceClient(concurrency int, urls ...string) (*Client, error) {
	client := New(urls...)
	if err := client.ConfigureConcurrency(concurrency); err != nil {
		return nil, err
	}
	client.Retries = 9
	client.RequestsPerSecond = 0
	client.HTTP.Timeout = 10 * time.Second
	pool := &sourceEndpointPool{rpsNow: time.Now}
	for _, url := range urls {
		pool.endpoints = append(pool.endpoints, &sourceEndpoint{
			url: url, limit: 20, latency: 200 * float64(time.Millisecond), delay: 100 * time.Millisecond,
		})
	}
	client.sourcePool = pool
	return client, nil
}

type sourceEndpointPool struct {
	mu        sync.Mutex
	endpoints []*sourceEndpoint
	// The upstream RPS window uses wall-clock seconds independently of timers.
	rpsNow func() time.Time
}

type requestSecond struct {
	at    int64
	count int
}

type latencySample struct {
	duration time.Duration
	success  bool
}

type sourceEndpoint struct {
	url                     string
	seconds                 []requestSecond
	limit                   float64
	successes, active       int
	until                   time.Time
	delay                   time.Duration
	warming                 bool
	latency                 float64
	samples                 [500]latencySample
	cursor, sampleSuccesses int
	sampleDuration          time.Duration
}

func (e *sourceEndpoint) available(second int64, now time.Time) bool {
	for len(e.seconds) > 0 && e.seconds[0].at <= second-10 {
		e.seconds = e.seconds[1:]
	}
	if !e.until.IsZero() {
		if now.Before(e.until) {
			return false
		}
		e.until = time.Time{}
		e.warming = true
	}
	if e.warming && e.active > 3 {
		return false
	}
	total := 0
	for _, s := range e.seconds {
		total += s.count
		if s.at == second && float64(s.count+1) > e.limit {
			return false
		}
	}
	return len(e.seconds) == 0 || e.seconds[0].at >= second || float64(total) <= e.limit*float64(1+second-e.seconds[0].at)
}

func (p *sourceEndpointPool) take() *sourceEndpoint {
	p.mu.Lock()
	defer p.mu.Unlock()
	now, second := time.Now(), p.rpsNow().Unix()
	var available []*sourceEndpoint
	var best *sourceEndpoint
	for _, e := range p.endpoints {
		if !e.available(second, now) {
			continue
		}
		available = append(available, e)
		if best == nil || e.latency < best.latency*0.9 || (e.latency <= best.latency && e.active < best.active) {
			best = e
		}
	}
	if best == nil {
		return nil
	}
	if len(available) > 1 && rand.Float64() < 0.1 {
		best = available[rand.IntN(len(available))]
	}
	best.active++
	last := len(best.seconds) - 1
	if last < 0 || best.seconds[last].at < second {
		best.seconds = append(best.seconds, requestSecond{second, 1})
	} else {
		best.seconds[last].count++
	}
	return best
}

func (p *sourceEndpointPool) finish(e *sourceEndpoint, duration time.Duration, err error, ignoreSample bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e.active--
	if ignoreSample {
		return
	}
	last := e.samples[e.cursor]
	e.samples[e.cursor] = latencySample{duration, err == nil}
	e.cursor = (e.cursor + 1) % len(e.samples)
	e.sampleDuration += duration - last.duration
	if last.success {
		e.sampleSuccesses--
	}
	if err == nil {
		e.sampleSuccesses++
	}
	e.latency = math.Inf(1)
	if e.sampleSuccesses > 0 {
		e.latency = float64(e.sampleDuration) / float64(e.sampleSuccesses)
	}
	if err == nil {
		e.successes++
		e.delay = 100 * time.Millisecond
		e.warming = false
		if len(e.seconds) == 10 && float64(e.successes) >= 5*e.limit {
			for _, s := range e.seconds {
				if float64(s.count) < 0.9*e.limit {
					return
				}
			}
			e.limit = math.Min(500, 1.05*e.limit)
			e.successes = 0
		}
		return
	}
	var httpErr *HTTPStatusError
	var rpcErr *Error
	var netErr net.Error
	timeout := errors.As(err, &netErr) && netErr.Timeout()
	rateLimited := errors.As(err, &httpErr) && httpErr.StatusCode == 429 || errors.As(err, &rpcErr) && rpcErr.Code == 429
	if !e.until.IsZero() || (!timeout && !rateLimited) {
		return
	}
	e.warming = false
	e.limit = math.Max(3, e.limit*0.95)
	e.successes = 0
	e.until = time.Now().Add(e.delay)
	if timeout {
		e.delay = 100 * time.Millisecond
	} else {
		e.delay = min(5*time.Second, e.delay*3/2)
	}
}

type rpcAttempt struct {
	id       uint64
	url      string
	endpoint *sourceEndpoint
}

func (c *Client) reserveAttempt(ctx context.Context) (rpcAttempt, error) {
	if err := c.wait(ctx); err != nil {
		return rpcAttempt{}, err
	}
	if c.sourcePool == nil {
		id := c.next.Add(1)
		return rpcAttempt{id: id, url: c.URLs[(id-1)%uint64(len(c.URLs))]}, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return rpcAttempt{}, err
		}
		if endpoint := c.sourcePool.take(); endpoint != nil {
			return rpcAttempt{id: c.next.Add(1), url: endpoint.url, endpoint: endpoint}, nil
		}
		if err := waitForRetry(ctx, 20*time.Millisecond); err != nil {
			return rpcAttempt{}, err
		}
	}
}

// A numeric-range rejection belongs to the pagination caller. The wire may
// originate from either typed filters or dynamically decoded request bodies.
func sourceLogRangeRejection(params any, err error) bool {
	var query map[string]any
	if values, ok := params.([]any); ok && len(values) > 0 {
		query, _ = values[0].(map[string]any)
	}
	if query == nil {
		var values []map[string]any
		raw, encodeErr := json.Marshal(params)
		if encodeErr != nil || json.Unmarshal(raw, &values) != nil || len(values) == 0 {
			return false
		}
		query = values[0]
	}
	plan, _ := logsRetryPlan(query, err, false)
	return plan.ShouldRetry
}

func sourcePermanentError(err error) bool {
	var protocol *Error
	if errors.As(err, &protocol) {
		switch protocol.Code {
		case -32700, -32601, -32004, -32006:
			return true
		}
		return strings.Contains(protocol.Message, "revert")
	}
	var status *HTTPStatusError
	if errors.As(err, &status) {
		switch status.StatusCode {
		case 404, 405, 501, 505:
			return true
		}
	}
	return false
}
