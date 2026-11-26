// Package rpc supplies cancellable HTTP/WebSocket JSON-RPC with retries and rate limits.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ysh0566/ponder-go/metrics"
)

// ErrHTTPResponseTooLarge reports that the decoded HTTP body exceeded the client's limit.
var ErrHTTPResponseTooLarge = errors.New("RPC HTTP response too large")

const defaultHTTPResponseBytes int64 = 32 << 20

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message) }

// HTTPStatusError retains bounded provider error details from a non-2xx response.
// Details contains the JSON error field, or a JSON string for a plain-text body.
type HTTPStatusError struct {
	StatusCode int             `json:"status"`
	Details    json.RawMessage `json:"details,omitempty"`
}

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("RPC HTTP status %d", e.StatusCode) }

type Cache interface {
	CacheGet(context.Context, string) (json.RawMessage, bool, error)
	CachePut(context.Context, string, json.RawMessage) error
}

// CacheBatchReader returns results in key order. A nil entry is a miss.
type CacheBatchReader interface {
	CacheGetMany(context.Context, []string) ([]json.RawMessage, error)
}

type CacheEntry struct {
	Key   string
	Value json.RawMessage
}

// AsyncCacheBatchWriter admits the entire batch as one owned write.
type AsyncCacheBatchWriter interface {
	BeginCachePutMany(context.Context, []CacheEntry) <-chan error
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
	// ChainName labels transport measurements. Configure it before making calls.
	ChainName         string
	URLs              []string
	HTTP              *http.Client
	Retries           int
	RequestsPerSecond int
	Cache             Cache
	ChainID           uint64
	next              atomic.Uint64
	requests          atomic.Uint64
	mu                sync.Mutex
	nextTime          time.Time
	custom            TransportFunc
	sourcePool        *sourceEndpointPool
	transport         *Client
	actions           *Actions
	concurrencyMu     sync.Mutex
	requestSlots      chan struct{}
	asyncMu           sync.Mutex
	asyncPending      []*asyncCallTask
	asyncRunning      bool
	asyncStarted      atomic.Bool

	// MaxHTTPResponseBytes limits the decoded size of each HTTP response. A
	// nonpositive value uses 32 MiB. Set it on the transport before use.
	MaxHTTPResponseBytes int64
	// FixedLogBlockRange keeps numeric range rejections in the source transport's
	// retry policy when the chain has an explicit ethGetLogsBlockRange.
	FixedLogBlockRange bool
}

// HasCustomTransport reports whether this client or its cache owner uses a Go transport.
func (c *Client) HasCustomTransport() bool {
	for c.transport != nil {
		c = c.transport
	}
	return c.custom != nil
}

// TransportRequests counts outbound RPC attempts, including retries. Cache-scoped
// clients share the transport count and cache hits never reach the transport.
func (c *Client) TransportRequests() uint64 {
	for c.transport != nil {
		c = c.transport
	}
	return c.requests.Load()
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
	return &Client{URLs: base.URLs, HTTP: base.HTTP, Retries: base.Retries, RequestsPerSecond: base.RequestsPerSecond, ChainName: c.ChainName, ChainID: base.ChainID, Cache: cache, transport: base}
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
	return c.CallWithOptions(ctx, method, params, out, RequestOptions{})
}

type RequestOptions struct {
	// RetryNullBlockRequest treats a null response to a block lookup as a
	// retryable error within the same budget as transport errors.
	RetryNullBlockRequest bool
}

func (c *Client) CallWithOptions(ctx context.Context, method string, params, out any, options RequestOptions) error {
	return c.callWithAdmission(ctx, false, method, params, out, options, nil)
}

// CallCached must only be used for immutable, hash-addressed or finalized requests.
func (c *Client) CallCached(ctx context.Context, method string, params any, out any) error {
	return c.callWithAdmission(ctx, true, method, params, out, RequestOptions{}, nil)
}

func (c *Client) callWithAdmission(ctx context.Context, cached bool, method string, params, out any, options RequestOptions, admitted *callAdmission) error {
	if admitted != nil {
		defer admitted.release()
	}
	base := c
	for base.transport != nil {
		base = base.transport
	}
	if len(base.URLs) == 0 && base.custom == nil {
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
	retries := max(0, base.Retries)
	for attempt := 0; attempt <= retries; attempt++ {
		var current rpcAttempt
		if attempt == 0 && admitted != nil {
			current = admitted.first
		} else {
			var err error
			current, err = base.reserveAttempt(ctx)
			if err != nil {
				return err
			}
		}
		start := time.Now()
		result, e := base.attemptResult(ctx, current, method, params, options)
		elapsed := time.Since(start)
		metrics.ObserveRPCAttempt(ctx, c.ChainName, method, elapsed, e)
		if current.endpoint != nil {
			rangeRejected := e != nil && method == "eth_getLogs" && !base.FixedLogBlockRange && sourceLogRangeRejection(params, e)
			base.sourcePool.finish(current.endpoint, elapsed, e, rangeRejected || ctx.Err() != nil)
			if rangeRejected || sourcePermanentError(e) {
				return e
			}
		}
		if e == nil {
			if cached && c.Cache != nil && string(result) != "null" {
				if e = c.Cache.CachePut(ctx, string(key), result); e != nil {
					return e
				}
			}
			return json.Unmarshal(result, out)
		}
		last = e
		if len(base.URLs) <= 1 && errors.Is(e, ErrHTTPResponseTooLarge) {
			return e
		}
		var rpcError *Error
		if base.sourcePool == nil && errors.As(e, &rpcError) && (rpcError.Code == -32601 || rpcError.Code == -32602) {
			return e
		}
		if attempt < retries {
			delay := 25 * time.Millisecond * time.Duration(1<<min(attempt, 6))
			if base.sourcePool != nil {
				delay = 125 * time.Millisecond * time.Duration(1<<min(attempt, 8))
			}
			if err := waitForRetry(ctx, delay); err != nil {
				return err
			}
		}
	}
	return last
}
func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ErrBlockNotFound is returned when an opted-in block lookup exhausts its retries on a null result.
var ErrBlockNotFound = errors.New("RPC block not found")

func (c *Client) attemptResult(ctx context.Context, attempt rpcAttempt, method string, params any, options RequestOptions) (json.RawMessage, error) {
	var result json.RawMessage
	var err error
	if c.custom != nil {
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		c.requests.Add(1)
		result, err = c.custom(call, method, params)
		if err != nil {
			return nil, markTransport(ctx, err)
		}
		if err := call.Err(); err != nil {
			return nil, markTransport(ctx, err)
		}
		if !json.Valid(result) {
			return nil, errors.New("custom RPC transport returned invalid JSON")
		}
		result = bytes.TrimSpace(result)
	} else {
		result, err = c.wireResult(ctx, attempt, method, params)
		if err != nil {
			return nil, err
		}
	}
	if options.RetryNullBlockRequest && string(result) == "null" && (method == "eth_getBlockByNumber" || method == "eth_getBlockByHash") {
		return nil, ErrBlockNotFound
	}
	return result, nil
}

func (c *Client) wireResult(ctx context.Context, attempt rpcAttempt, method string, params any) (json.RawMessage, error) {
	request := map[string]any{"jsonrpc": "2.0", "id": attempt.id, "method": method, "params": params}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	raw, err := c.request(ctx, attempt.url, data, attempt.id)
	if err != nil {
		return nil, markTransport(ctx, err)
	}
	var response struct {
		ID     uint64          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	switch {
	case response.ID != attempt.id:
		return nil, fmt.Errorf("RPC response id mismatch")
	case response.Error != nil:
		return nil, response.Error
	case len(response.Result) == 0:
		return nil, fmt.Errorf("RPC response missing result")
	default:
		return response.Result, nil
	}
}

func (c *Client) request(ctx context.Context, url string, data []byte, id uint64) ([]byte, error) {
	if strings.HasPrefix(url, "ws://") || strings.HasPrefix(url, "wss://") {
		c.requests.Add(1)
		ws, response, e := websocket.DefaultDialer.DialContext(ctx, url, nil)
		if e != nil {
			if response != nil {
				response.Body.Close()
				return nil, &HTTPStatusError{StatusCode: response.StatusCode}
			}
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
	c.requests.Add(1)
	response, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	limit := c.MaxHTTPResponseBytes
	if limit <= 0 {
		limit = defaultHTTPResponseBytes
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		status := &HTTPStatusError{StatusCode: response.StatusCode}
		// Error metadata stays small even when a caller permits large log results.
		raw, err := readHTTPBody(response, min(limit, 64<<10))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			status.Details = httpErrorDetails(raw, response.Header.Get("Content-Type"), id)
		}
		return nil, status
	}
	return readHTTPBody(response, limit)
}

func httpErrorDetails(raw []byte, contentType string, id uint64) json.RawMessage {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		// Gateways can reject a request before assigning a JSON-RPC id.
		if len(envelope.Result) != 0 || (len(envelope.ID) != 0 && string(envelope.ID) != "null" && string(envelope.ID) != strconv.FormatUint(id, 10)) {
			return nil
		}
		return envelope.Error
	}
	if len(raw) != 0 && strings.EqualFold(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]), "text/plain") {
		detail, _ := json.Marshal(string(raw))
		return detail
	}
	return nil
}

func readHTTPBody(response *http.Response, limit int64) ([]byte, error) {
	oversize := func() error { return fmt.Errorf("%w: limit %d bytes", ErrHTTPResponseTooLarge, limit) }
	if response.ContentLength > limit {
		return nil, oversize()
	}
	body := &io.LimitedReader{R: response.Body, N: limit}
	raw, err := io.ReadAll(body)
	if err != nil || body.N > 0 {
		return raw, err
	}
	// A chunked or transparently decompressed response may have no known
	// length. Read one extra byte to distinguish an exact fit from truncation.
	var extra [1]byte
	_, err = io.ReadFull(response.Body, extra[:])
	if err == io.EOF {
		return raw, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, oversize()
}

// Subscribe delivers notifications over a dedicated connection. If the transport
// drops, it registers a new subscription. Providers do not replay missed events.
func (c *Client) Subscribe(ctx context.Context, kind string, params ...any) (<-chan json.RawMessage, error) {
	var urls []string
	for _, u := range c.URLs {
		if strings.HasPrefix(u, "ws://") || strings.HasPrefix(u, "wss://") {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("WebSocket endpoint required")
	}
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "eth_subscribe", "params": append([]any{kind}, params...)})
	if err != nil {
		return nil, err
	}
	connect := func(url string) (*websocket.Conn, string, func(), error) {
		ws, response, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
		if err != nil {
			if response != nil {
				response.Body.Close()
				return nil, "", nil, &HTTPStatusError{StatusCode: response.StatusCode}
			}
			return nil, "", nil, markTransport(ctx, err)
		}
		stop := context.AfterFunc(ctx, func() { ws.Close() })
		closeConn := func() { stop(); ws.Close() }
		var reply struct {
			Result string
			Error  *Error
			ID     uint64
		}
		if err = ws.WriteMessage(websocket.TextMessage, request); err == nil {
			err = ws.ReadJSON(&reply)
		}
		if err != nil {
			closeConn()
			return nil, "", nil, markTransport(ctx, err)
		}
		if reply.Error != nil {
			closeConn()
			return nil, "", nil, reply.Error
		}
		if reply.ID != 1 || reply.Result == "" {
			closeConn()
			return nil, "", nil, fmt.Errorf("invalid subscription response")
		}
		return ws, reply.Result, closeConn, nil
	}
	endpoint := 0
	ws, id, closeConn, err := connect(urls[endpoint])
	for IsTransient(err) && endpoint+1 < len(urls) {
		endpoint++
		ws, id, closeConn, err = connect(urls[endpoint])
	}
	if err != nil {
		return nil, err
	}
	out := make(chan json.RawMessage, 16)
	go func() {
		defer close(out)
		defer func() {
			if closeConn != nil {
				closeConn()
			}
		}()
		delay := 100 * time.Millisecond
		for ctx.Err() == nil {
			var msg struct {
				Method string
				Params struct {
					Subscription string
					Result       json.RawMessage
				}
			}
			if err := ws.ReadJSON(&msg); err == nil {
				if msg.Method != "eth_subscription" || msg.Params.Subscription != id {
					continue
				}
				select {
				case out <- msg.Params.Result:
				case <-ctx.Done():
					return
				}
				delay = 100 * time.Millisecond
				continue
			} else if !IsTransient(markTransport(ctx, err)) {
				return
			}
			closeConn()
			closeConn = nil
			for {
				if waitForRetry(ctx, delay) != nil {
					return
				}
				delay = min(delay*2, 5*time.Second)
				endpoint = (endpoint + 1) % len(urls)
				ws, id, closeConn, err = connect(urls[endpoint])
				if err == nil {
					break
				}
				if !IsTransient(err) {
					return
				}
			}
		}
	}()
	return out, nil
}
