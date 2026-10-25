// Package rpc supplies cancellable HTTP/WebSocket JSON-RPC with retries and rate limits.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message) }

type Cache interface {
	CacheGet(context.Context, string) (json.RawMessage, bool, error)
	CachePut(context.Context, string, json.RawMessage) error
}

// AsyncCache is an optional admission contract for fire-and-forget cache writes.
// BeginCachePut must snapshot and validate its input, construct the actual write,
// and admit it to an owned write queue before returning. The buffered future
// receives exactly one completion error and is then closed. Starting a goroutine
// around CachePut alone does not implement this contract. Completion/commit order
// relative to other database work is deliberately not promised.
type AsyncCache interface {
	BeginCachePut(context.Context, string, json.RawMessage) <-chan error
}

type Client struct {
	URLs              []string
	HTTP              *http.Client
	Retries           int
	RequestsPerSecond int
	Cache             Cache
	ChainID           uint64
	next              atomic.Uint64
	mu                sync.Mutex
	nextTime          time.Time
	transport         *Client
	actions           *Actions
	concurrencyMu     sync.Mutex
	requestSlots      chan struct{}
	asyncMu           sync.Mutex
	asyncPending      []*asyncCallTask
	asyncRunning      bool
	asyncStarted      atomic.Bool
}

// ConfigureConcurrency sets the shared transport limit before its first call.
// Cache-scoped clients use the same transport slots and rate limiter.
func (c *Client) ConfigureConcurrency(limit int) error {
	base := c
	for base.transport != nil {
		base = base.transport
	}
	if limit < 1 {
		return fmt.Errorf("RPC concurrency must be a positive integer")
	}
	base.concurrencyMu.Lock()
	defer base.concurrencyMu.Unlock()
	if base.next.Load() != 0 || base.requestSlots != nil || base.asyncStarted.Load() {
		return fmt.Errorf("RPC concurrency must be configured before requests")
	}
	base.requestSlots = make(chan struct{}, limit)
	return nil
}

func (c *Client) acquireRequest(ctx context.Context) (func(), error) {
	c.concurrencyMu.Lock()
	slots := c.requestSlots
	c.concurrencyMu.Unlock()
	if slots == nil {
		return func() {}, nil
	}
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// WithCache scopes cache access to an indexing transaction while sharing the
// original transport's request IDs, retry settings and global rate limiter.
func (c *Client) WithCache(cache Cache) *Client {
	base := c
	for base.transport != nil {
		base = base.transport
	}
	return &Client{URLs: base.URLs, HTTP: base.HTTP, Retries: base.Retries, RequestsPerSecond: base.RequestsPerSecond, ChainID: base.ChainID, Cache: cache, transport: base}
}

func New(urls ...string) *Client {
	return &Client{URLs: urls, HTTP: &http.Client{Timeout: 30 * time.Second}, Retries: 3, RequestsPerSecond: 50}
}
func (c *Client) wait(ctx context.Context) error {
	if c.RequestsPerSecond <= 0 {
		return nil
	}
	c.mu.Lock()
	at := maxTime(c.nextTime, time.Now())
	c.nextTime = at.Add(time.Second / time.Duration(c.RequestsPerSecond))
	c.mu.Unlock()
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	return c.call(ctx, false, method, params, out)
}

// CallCached must only be used for immutable, hash-addressed or finalized requests.
func (c *Client) CallCached(ctx context.Context, method string, params any, out any) error {
	return c.call(ctx, true, method, params, out)
}
func (c *Client) call(ctx context.Context, cached bool, method string, params any, out any) error {
	return c.callWithAdmission(ctx, cached, method, params, out, nil)
}

func (c *Client) callWithAdmission(ctx context.Context, cached bool, method string, params any, out any, admitted *callAdmission) error {
	if admitted != nil {
		defer admitted.release()
	}
	base := c
	for base.transport != nil {
		base = base.transport
	}
	if len(base.URLs) == 0 {
		return fmt.Errorf("RPC endpoint is required")
	}
	key, _ := json.Marshal([]any{base.ChainID, method, params})
	if cached && c.Cache != nil {
		raw, ok, e := c.Cache.CacheGet(ctx, string(key))
		if e != nil {
			return e
		}
		if ok {
			return json.Unmarshal(raw, out)
		}
	}
	if admitted == nil {
		release, err := base.acquireRequest(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	var last error
	for attempt := 0; attempt <= base.Retries; attempt++ {
		var id uint64
		var url string
		if attempt == 0 && admitted != nil {
			id = admitted.id
			url = admitted.url
		} else {
			if e := base.wait(ctx); e != nil {
				return e
			}
			id = base.next.Add(1)
			url = base.URLs[(id-1)%uint64(len(base.URLs))]
		}
		request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
		data, _ := json.Marshal(request)
		raw, e := base.request(ctx, url, data)
		if e == nil {
			var response struct {
				ID     uint64          `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *Error          `json:"error"`
			}
			e = json.Unmarshal(raw, &response)
			if e == nil {
				if response.ID != id {
					e = fmt.Errorf("RPC response id mismatch")
				} else if response.Error != nil {
					e = response.Error
				} else if len(response.Result) == 0 {
					e = fmt.Errorf("RPC response missing result")
				} else {
					if cached && c.Cache != nil && string(response.Result) != "null" {
						if e = c.Cache.CachePut(ctx, string(key), response.Result); e != nil {
							return e
						}
					}
					return json.Unmarshal(response.Result, out)
				}
			}
		}
		last = e
		var rpcError *Error
		if errors.As(e, &rpcError) && (rpcError.Code == -32601 || rpcError.Code == -32602) {
			return e
		}
		if attempt < base.Retries {
			timer := time.NewTimer(time.Duration(1<<min(attempt, 6)) * 25 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return last
}
func (c *Client) request(ctx context.Context, url string, data []byte) ([]byte, error) {
	if strings.HasPrefix(url, "ws://") || strings.HasPrefix(url, "wss://") {
		ws, _, e := websocket.DefaultDialer.DialContext(ctx, url, nil)
		if e != nil {
			return nil, e
		}
		defer ws.Close()
		stop := context.AfterFunc(ctx, func() { ws.Close() })
		defer stop()
		if e = ws.WriteMessage(websocket.TextMessage, data); e != nil {
			return nil, e
		}
		_, raw, e := ws.ReadMessage()
		return raw, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("RPC HTTP status %d", response.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	return raw, e
}

// Subscribe uses a dedicated connection and closes it on cancellation.
func (c *Client) Subscribe(ctx context.Context, kind string, params ...any) (<-chan json.RawMessage, error) {
	url := ""
	for _, u := range c.URLs {
		if strings.HasPrefix(u, "ws") {
			url = u
			break
		}
	}
	if url == "" {
		return nil, fmt.Errorf("WebSocket endpoint required")
	}
	ws, _, e := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if e != nil {
		return nil, e
	}
	p := append([]any{kind}, params...)
	if e = ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "eth_subscribe", "params": p}); e != nil {
		ws.Close()
		return nil, e
	}
	var reply struct {
		Result string
		Error  *Error
	}
	if e = ws.ReadJSON(&reply); e != nil {
		ws.Close()
		return nil, e
	}
	if reply.Error != nil {
		ws.Close()
		return nil, reply.Error
	}
	out := make(chan json.RawMessage, 16)
	stop := context.AfterFunc(ctx, func() { ws.Close() })
	go func() {
		defer close(out)
		defer ws.Close()
		defer stop()
		for {
			var msg struct {
				Params struct {
					Subscription string
					Result       json.RawMessage
				}
			}
			if e := ws.ReadJSON(&msg); e != nil {
				return
			}
			if msg.Params.Subscription != reply.Result {
				continue
			}
			select {
			case out <- msg.Params.Result:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
