// Package query exposes the original @ponder/client SQL-over-HTTP/SSE protocol.
package query

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/sqlquery"
	"ponder.local/ponder-go/store"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Server struct {
	Store      *store.Store
	Ready      atomic.Bool
	MaxLive    int64
	live       atomic.Int64
	requests   atomic.Uint64
	ChainNames map[uint64]string
	API        http.Handler
}

func New(s *store.Store) *Server {
	return &Server{Store: s, MaxLive: 1000, ChainNames: map[uint64]string{}}
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
	if e := sqlquery.Validate(q.SQL); e != nil {
		return nil, e
	}
	relations, e := sqlquery.Relations(q.SQL)
	if e != nil {
		return nil, e
	}
	ctes, e := sqlquery.CTEs(q.SQL)
	if e != nil {
		return nil, e
	}
	allowed := map[string]bool{}
	for _, t := range s.Store.Schema.Tables {
		allowed[t.Name] = true
	}
	for _, v := range s.Store.Schema.Views {
		allowed[v.Name] = true
	}
	for _, name := range ctes {
		allowed[name] = true
	}
	for _, r := range relations {
		if !allowed[r] {
			return nil, fmt.Errorf("unknown query relation %q", r)
		}
	}
	return s.Store.QueryDependencies(relations)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	switch r.URL.Path {
	case "/health", "/ready", "/metrics", "/status", "/sql/db", "/sql/live":
	default:
		if s.API != nil {
			s.API.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	switch r.URL.Path {
	case "/health":
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	case "/ready":
		if !s.Ready.Load() {
			http.Error(w, "Not ready", 503)
			return
		}
		writeJSON(w, 200, map[string]bool{"ready": true})
		return
	case "/metrics":
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "ponder_go_http_requests_total %d\nponder_go_live_queries %d\n", s.requests.Load(), s.live.Load())
		return
	case "/status":
		p, e := s.Store.Progress(r.Context())
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		status := map[string]any{}
		for chain, cursor := range p {
			cp, e := checkpoint.Decode(cursor)
			if e != nil {
				http.Error(w, e.Error(), 500)
				return
			}
			name := s.ChainNames[cp.ChainID]
			if name == "" {
				name = chain
			}
			status[name] = map[string]any{"id": cp.ChainID, "block": map[string]any{"number": cp.BlockNumber, "timestamp": cp.BlockTimestamp}}
		}
		writeJSON(w, 200, status)
		return
	case "/sql/db", "/sql/live":
	default:
		if s.API != nil {
			s.API.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	if r.URL.Path == "/sql/live" && !s.Ready.Load() {
		http.Error(w, "Live queries are not available until the backfill is complete", 503)
		return
	}
	q, e := decodeQuery(r.URL.Query().Get("sql"))
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
		result, e := s.Store.QueryReadOnly(r.Context(), q.SQL, q.Params...)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	s.stream(w, r, q, relations)
}
func rowHash(result store.Result) string {
	raw, _ := json.Marshal(result.Rows)
	sum := md5.Sum(raw)
	return hex.EncodeToString(sum[:])[:10]
}
func (s *Server) stream(w http.ResponseWriter, r *http.Request, q request, relations []string) {
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
	changes, unsubscribe := s.Store.Subscribe()
	defer unsubscribe()
	initial, e := s.Store.QueryReadOnly(r.Context(), q.SQL, q.Params...)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	last := rowHash(initial)
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
			result, e := s.Store.QueryReadOnly(r.Context(), q.SQL, q.Params...)
			if e != nil {
				return
			}
			hash := rowHash(result)
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
