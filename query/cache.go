package query

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"weak"

	"ponder.local/ponder-go/store"
)

type queryEntry struct {
	version uint64
	pending *queryFlight
	result  weak.Pointer[store.Result]
	order   *list.Element
}

type queryFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	result  *store.Result
	err     error
}

type resultCache struct {
	mu      sync.Mutex
	entries map[string]*queryEntry
	order   list.List
}

// Read results can be reclaimed as soon as their responses are released. Cap
// completed keys as well, including when new queries arrive faster than GC.
const maxCompletedQueries = 512

func (c *resultCache) remove(key string, e *queryEntry) {
	delete(c.entries, key)
	if e.order != nil {
		c.order.Remove(e.order)
		e.order = nil
	}
}

func (c *resultCache) read(ctx context.Context, key string, version uint64, retain bool, load func(context.Context) (store.Result, error)) (*store.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]*queryEntry)
	}
	e := c.entries[key]
	if e != nil && e.version == version {
		if pending := e.pending; pending != nil {
			pending.waiters++
			c.mu.Unlock()
			return c.wait(ctx, key, e, pending)
		}
		if r := e.result.Value(); retain && r != nil {
			c.mu.Unlock()
			return r, nil
		}
	}
	if e != nil {
		c.remove(key, e)
	}
	run, cancel := context.WithCancel(context.WithoutCancel(ctx))
	f := &queryFlight{done: make(chan struct{}), cancel: cancel, waiters: 1}
	e = &queryEntry{version: version, pending: f}
	c.entries[key] = e
	c.mu.Unlock()
	go func() {
		defer cancel()
		result, err := func() (result store.Result, err error) {
			defer func() {
				if v := recover(); v != nil {
					err = fmt.Errorf("SQL read panicked: %v", v)
				}
			}()
			return load(run)
		}()
		c.mu.Lock()
		defer c.mu.Unlock()
		f.result, f.err = &result, err
		e.pending = nil
		if c.entries[key] == e {
			if err == nil && retain && f.waiters > 0 {
				e.result = weak.Make(&result)
				e.order = c.order.PushBack(key)
				if c.order.Len() > maxCompletedQueries {
					old := c.order.Front().Value.(string)
					c.remove(old, c.entries[old])
				}
			} else {
				c.remove(key, e)
			}
		}
		close(f.done)
	}()
	return c.wait(ctx, key, e, f)
}

func (c *resultCache) wait(ctx context.Context, key string, e *queryEntry, f *queryFlight) (*store.Result, error) {
	select {
	case <-f.done:
		return f.result, f.err
	case <-ctx.Done():
		c.mu.Lock()
		f.waiters--
		if f.waiters == 0 && e.pending == f {
			if c.entries[key] == e {
				c.remove(key, e)
			}
			f.cancel()
		}
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}
