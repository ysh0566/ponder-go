// Package query exposes the original @ponder/client SQL-over-HTTP/SSE protocol.
package query

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/sqlquery"
	"github.com/ysh0566/ponder-go/store"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Server struct {
	reader         store.QueryReader
	validatorOnce  sync.Once
	validator      *sqlquery.Validator
	results        resultCache
	queryNamespace string
	published      *store.QueryChanges
	subscription   *store.QueryNotifications
	Store          *store.Store
	Ready          atomic.Bool
	Readiness      Readiness
	Metrics        *metrics.Registry
	CollectMetrics func(context.Context) (string, error)
	MaxLive        int64
	live           atomic.Int64
	requests       atomic.Uint64
	ChainNames     map[uint64]string
	API            http.Handler
}

// BlockStatus is the latest indexed block recorded for a chain.
type BlockStatus struct {
	Number    uint64 `json:"number"`
	Timestamp uint64 `json:"timestamp"`
}

type ChainStatus struct {
	Block BlockStatus `json:"block"`
	ID    uint64      `json:"id"`
}

// Status reads the same committed checkpoints exposed by GET /status.
func (s *Server) Status(ctx context.Context) (map[string]ChainStatus, error) {
	if !s.HasDatabase() {
		return nil, fmt.Errorf("database unavailable")
	}
	progress, err := s.database().Progress(ctx)
	if err != nil {
		return nil, err
	}
	status := make(map[string]ChainStatus, len(progress))
	for chain, cursor := range progress {
		cp, err := checkpoint.Decode(cursor)
		if err != nil {
			return nil, err
		}
		name := s.ChainNames[cp.ChainID]
		if name == "" {
			name = chain
		}
		status[name] = ChainStatus{ID: cp.ChainID, Block: BlockStatus{Number: cp.BlockNumber, Timestamp: cp.BlockTimestamp}}
	}
	return status, nil
}

func New(s *store.Store) *Server {
	return &Server{Store: s, Metrics: metrics.New(), MaxLive: 1000, ChainNames: map[uint64]string{}}
}

// NewReader serves a borrowed business database and subscribes to its commits.
// Close the server after HTTP shutdown and before closing the database owner.
func NewReader(ctx context.Context, reader store.QueryReader) (*Server, error) {
	if !reader.Available() {
		return nil, fmt.Errorf("database unavailable")
	}
	server := New(nil)
	server.reader = reader
	return server.listenForChanges(ctx)
}

func (s *Server) database() store.QueryReader {
	if s.Store != nil {
		return s.Store.QueryReader()
	}
	return s.reader
}

func (s *Server) HasDatabase() bool { return s.database().Available() }

// NewAtNamespace serves SQL from an explicitly selected published schema and
// listens on its writer's live-query channel. Close it when serving ends.
func NewAtNamespace(ctx context.Context, s *store.Store, namespace string) (*Server, error) {
	if s == nil || !schema.Identifier(namespace) || len(namespace) > 63 {
		return nil, fmt.Errorf("invalid SQL namespace %q", namespace)
	}
	server := New(s)
	server.queryNamespace = namespace
	if namespace != s.Namespace {
		server.published = &store.QueryChanges{}
	}
	return server.listenForChanges(ctx)
}

func (server *Server) listenForChanges(ctx context.Context) (*Server, error) {
	watch, err := server.StartQueryNotifications(ctx)
	if err != nil {
		return nil, err
	}
	server.subscription = watch
	return server, nil
}

func (s *Server) changes() *store.QueryChanges {
	if s.published != nil {
		return s.published
	}
	return s.database().Changes()
}

// StartQueryNotifications subscribes to the writer's live-query channel.
func (s *Server) StartQueryNotifications(ctx context.Context) (*store.QueryNotifications, error) {
	if !s.HasDatabase() {
		return nil, fmt.Errorf("database unavailable")
	}
	namespace := s.queryNamespace
	if namespace == "" {
		namespace = s.database().Namespace()
	}
	return s.database().Listen(ctx, s.changes(), namespace)
}

// Close releases the database listener opened by NewReader or NewAtNamespace.
func (s *Server) Close() error {
	if s.subscription != nil {
		return s.subscription.Close()
	}
	return nil
}

func (s *Server) queryResult(ctx context.Context, key string, q request, relations []string, ready bool) (*store.Result, error) {
	namespace := s.queryNamespace
	if namespace == "" {
		namespace = s.database().Namespace()
	}
	load := func(read context.Context) (store.Result, error) {
		return s.database().QueryReadOnlyInNamespace(read, namespace, q.SQL, q.Params...)
	}
	if !ready {
		result, err := load(ctx)
		return &result, err
	}
	version, listening := s.changes().QueryVersion(relations)
	// Completed table results need notifications from the actual query schema.
	// Without that subscription overlapping readers still share one transaction.
	retain := len(relations) == 0 || listening
	return s.results.read(ctx, key, version, retain, load)
}

type request struct {
	SQL     string   `json:"sql"`
	Params  []any    `json:"params"`
	Typings []string `json:"typings"`
}

func decodeQuery(encoded string) (request, error) {
	var envelope struct {
		JSON request `json:"json"`
		Meta struct {
			Values map[string][]any `json:"values"`
		} `json:"meta"`
	}
	if encoded == "" {
		return request{}, fmt.Errorf("Missing \"sql\" query parameter")
	}
	d := json.NewDecoder(strings.NewReader(encoded))
	d.UseNumber()
	if e := d.Decode(&envelope); e != nil {
		return request{}, e
	}
	q := envelope.JSON
	if q.SQL == "" {
		return q, fmt.Errorf("Invalid query")
	}
	for key, types := range envelope.Meta.Values {
		if key == "typings" && len(types) > 0 && types[0] == "undefined" {
			continue
		}
		if !strings.HasPrefix(key, "params.") {
			return q, fmt.Errorf("unsupported SuperJSON metadata path %q", key)
		}
		i, e := strconv.Atoi(strings.TrimPrefix(key, "params."))
		if e != nil || i < 0 || i >= len(q.Params) || len(types) == 0 {
			return q, fmt.Errorf("invalid SuperJSON metadata")
		}
		switch types[0] {
		case "bigint", "Date":
		case "undefined":
			q.Params[i] = nil
		default:
			return q, fmt.Errorf("unsupported SuperJSON type %v", types[0])
		}
	}
	return q, nil
}
func (s *Server) validate(q request) ([]string, error) {
	s.validatorOnce.Do(func() { s.validator = sqlquery.NewValidator() })
	if e := s.validator.Validate(q.SQL); e != nil {
		return nil, e
	}
	set, e := s.validator.Relations(q.SQL)
	if e != nil {
		return nil, e
	}
	relations := set.Values()
	ctes, e := sqlquery.CTEs(q.SQL)
	if e != nil {
		return nil, e
	}
	allowed := map[string]bool{}
	for name := range s.database().Relations() {
		allowed[name] = true
	}
	for _, name := range ctes {
		allowed[name] = true
	}
	for _, r := range relations {
		if !allowed[r] {
			return nil, fmt.Errorf("relation %q does not exist", r)
		}
	}
	return s.database().QueryDependencies(relations)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) serveRoute(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/health":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	case "/ready":
		ready, err := s.IsReady(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("Historical indexing is not complete."))
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	case "/metrics":
		collect := s.CollectMetrics
		if collect == nil && s.Metrics != nil {
			collect = s.Metrics.Collect
		}
		var body string
		if collect != nil {
			var err error
			body, err = collect(r.Context())
			if err != nil {
				writeJSON(w, 500, struct{}{})
				return
			}
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		fmt.Fprintf(w, "%sponder_go_http_requests_total %d\nponder_go_live_queries %d\n", body, s.requests.Load(), s.live.Load())
		return
	case "/status":
		if !s.HasDatabase() {
			http.Error(w, "Database unavailable", 503)
			return
		}
		status, e := s.Status(r.Context())
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		writeJSON(w, 200, status)
		return
	}
	ready, readyErr := s.IsReady(r.Context())
	if r.URL.Path == "/sql/live" {
		err := readyErr
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if !ready {
			http.Error(w, "Live queries are not available until the backfill is complete", 503)
			return
		}
		maxLive := s.MaxLive
		if maxLive <= 0 {
			maxLive = 1000
		}
		if s.live.Add(1) > maxLive {
			s.live.Add(-1)
			http.Error(w, "Maximum number of live queries reached", 503)
			return
		}
		defer s.live.Add(-1)
	}
	if !s.HasDatabase() {
		http.Error(w, "Database unavailable", 503)
		return
	}
	queryKey := r.URL.Query().Get("sql")
	q, e := decodeQuery(queryKey)
	if e != nil {
		status := 500
		if r.URL.Query().Get("sql") == "" {
			status = 400
		}
		http.Error(w, e.Error(), status)
		return
	}
	relations, e := s.validate(q)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if r.URL.Path == "/sql/db" {
		result, e := s.queryResult(r.Context(), queryKey, q, relations, ready && readyErr == nil)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	s.stream(w, r, queryKey, q, relations)
}
func rowHash(result store.Result) string {
	raw, _ := json.Marshal(result.Rows)
	sum := md5.Sum(raw)
	return hex.EncodeToString(sum[:])[:10]
}
func (s *Server) stream(w http.ResponseWriter, r *http.Request, queryKey string, q request, relations []string) {
	var stopped <-chan struct{}
	if s.subscription != nil {
		stopped = s.subscription.Done()
	}
	changes, unsubscribe := s.changes().Subscribe()
	defer unsubscribe()
	initial, e := s.queryResult(r.Context(), queryKey, q, relations, true)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	last := rowHash(*initial)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)
	flusher.Flush()
	// Baseline Ponder evaluates the initial result only to establish the hash.
	// It emits a message only after a related table changes AND the rows differ.
	refs := map[string]bool{}
	for _, name := range relations {
		refs[name] = true
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-stopped:
			return
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, e := fmt.Fprint(w, ": keep-alive\n\n"); e != nil {
				return
			}
			flusher.Flush()
		case change := <-changes:
			related := false
			for _, name := range change.Tables {
				related = related || refs[name]
			}
			if !related {
				continue
			}
			result, e := s.queryResult(r.Context(), queryKey, q, relations, true)
			for stopped != nil && s.database().Dialect() == "postgres" && store.PostgresConnectionInterrupted(e) {
				select {
				case <-r.Context().Done():
					return
				case <-stopped:
					return
				case <-time.After(250 * time.Millisecond):
				}
				result, e = s.queryResult(r.Context(), queryKey, q, relations, true)
			}
			if e != nil {
				return
			}
			hash := rowHash(*result)
			if hash == last {
				continue
			}
			last = hash
			raw, e := json.Marshal(result)
			if e != nil {
				return
			}
			if _, e = fmt.Fprintf(w, "data: %s\n\n", raw); e != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// Client is a small Go SDK for the same SQL protocol.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c Client) Query(ctx context.Context, sql string, params ...any) (store.Result, error) {
	encoded, _ := json.Marshal(map[string]any{"json": request{SQL: sql, Params: params}})
	url, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.BaseURL, "/")+"/db", nil)
	if e != nil {
		return store.Result{}, e
	}
	q := url.URL.Query()
	q.Set("sql", string(encoded))
	url.URL.RawQuery = q.Encode()
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, e := client.Do(url)
	if e != nil {
		return store.Result{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return store.Result{}, fmt.Errorf("SQL HTTP status %d", resp.StatusCode)
	}
	var result store.Result
	d := json.NewDecoder(resp.Body)
	d.UseNumber()
	e = d.Decode(&result)
	return result, e
}
