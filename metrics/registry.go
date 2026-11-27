// Package metrics collects indexing and HTTP measurements for one Ponder instance.
package metrics

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

type tableLabel struct{ table, operation string }

// Network durations use the upstream millisecond histogram boundaries.
var networkDurationBuckets = []float64{1, 5, 10, 50, 100, 500, 1000, 5000, 10000, 50000, 100000, 500000}

type Registry struct {
	mu                                  sync.Mutex
	reg                                 *prometheus.Registry
	info                                applicationInfo
	dbDuration                          *prometheus.HistogramVec
	dbErrors, postgresQueries           *prometheus.CounterVec
	syncBlock, syncTimestamp, indexTime *prometheus.GaugeVec
	syncRealtime, syncComplete          *prometheus.GaugeVec
	liveChains                          map[string]bool
	reorgs                              *prometheus.CounterVec
	blockArrival, realtimeLatency       *prometheus.HistogramVec
	rpcDuration, handlerDuration        *prometheus.HistogramVec
	rpcErrors                           *prometheus.CounterVec
	historyStart, historyEnd            *prometheus.GaugeVec
	extract, transform, groupDuration   *prometheus.GaugeVec
	completed                           *prometheus.GaugeVec
	cache, queries                      *prometheus.CounterVec
	pendingEvents                       map[string]uint64
	pendingCache, pendingQueries        map[tableLabel]uint64
	active                              *prometheus.GaugeVec
	duration, requestSize, responseSize *prometheus.HistogramVec
}

func New() *Registry {
	r := &Registry{
		reg:             prometheus.NewRegistry(),
		dbDuration:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ponder_database_method_duration", Help: "Duration of database operations", Buckets: networkDurationBuckets}, []string{"method"}),
		dbErrors:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ponder_database_method_error_total", Help: "Total number of errors encountered during database operations"}, []string{"method"}),
		postgresQueries: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ponder_postgres_query_total", Help: "Total number of queries submitted to the database"}, []string{"pool"}),
		indexTime:       chainGauge("ponder_indexing_timestamp", "Timestamp through which all events have been completed"),
		syncBlock:       chainGauge("ponder_sync_block", "Closest-to-tip synced block number"),
		syncTimestamp:   chainGauge("ponder_sync_block_timestamp", "Closest-to-tip synced block timestamp"),
		syncRealtime:    chainGauge("ponder_sync_is_realtime", "Boolean (0 or 1) indicating if the sync is realtime mode"),
		syncComplete:    chainGauge("ponder_sync_is_complete", "Boolean (0 or 1) indicating if the sync has synced all blocks"),
		liveChains:      make(map[string]bool),
		reorgs:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ponder_realtime_reorg_total", Help: "Count of how many re-orgs have occurred"}, []string{"chain"}),
		rpcDuration:     prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ponder_rpc_request_duration", Help: "Duration of successful RPC requests", Buckets: networkDurationBuckets}, []string{"chain", "method"}),
		rpcErrors:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ponder_rpc_request_error_total", Help: "Total count of failed RPC requests"}, []string{"chain", "method"}),
		historyStart:    chainGauge("ponder_historical_start_timestamp_seconds", "Timestamp at which historical indexing started"),
		historyEnd:      chainGauge("ponder_historical_end_timestamp_seconds", "Timestamp at which historical indexing ended"),
		groupDuration:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ponder_historical_concurrency_group_duration", Help: "Duration of historical concurrency groups"}, []string{"group"}),
		extract:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ponder_historical_extract_duration", Help: "Duration of historical extract phase"}, []string{"step"}),
		transform:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ponder_historical_transform_duration", Help: "Duration of historical transform phase"}, []string{"step"}),
		completed:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ponder_indexing_completed_events", Help: "Number of events that have been processed"}, []string{"event"}),
		cache:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ponder_indexing_cache_requests_total", Help: "Number of cache accesses"}, []string{"table", "type"}),
		queries:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ponder_indexing_store_queries_total", Help: "Number of indexing store operations"}, []string{"table", "method"}),
		active:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ponder_http_server_active_requests", Help: "Number of active HTTP requests"}, []string{"method", "path"}),
		pendingEvents:   make(map[string]uint64),
		pendingCache:    make(map[tableLabel]uint64),
		pendingQueries:  make(map[tableLabel]uint64),
	}
	histogram := func(name, help string, buckets []float64) *prometheus.HistogramVec {
		return prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}, []string{"method", "path", "status"})
	}
	sizes := []float64{10, 100, 1000, 5000, 10000, 50000, 100000, 500000, 1000000, 5000000, 10000000}
	realtimeBuckets := append(networkDurationBuckets, 1_000_000)
	realtime := func(name, help string) *prometheus.HistogramVec {
		return prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: realtimeBuckets}, []string{"chain"})
	}
	r.blockArrival = realtime("ponder_realtime_block_arrival_latency", "Time elapsed between mining a block and being received by the realtime sync")
	r.realtimeLatency = realtime("ponder_realtime_latency", "Time elapsed between receiving a block and fully processing it")
	r.requestSize = histogram("ponder_http_server_request_size_bytes", "Size of HTTP requests received by the server", sizes)
	r.responseSize = histogram("ponder_http_server_response_size_bytes", "Size of HTTP responses served by the server", sizes)
	r.duration = histogram("ponder_http_server_request_duration_ms", "Duration of HTTP responses served by the server", networkDurationBuckets)
	r.handlerDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "ponder_indexing_function_duration", Help: "Duration of indexing function execution",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 50, 100, 500, 1000, 5000, 10000, 50000, 100000},
	}, []string{"chain", "event"})
	r.reg.MustRegister(r.dbDuration, r.dbErrors, r.postgresQueries, r.syncBlock, r.syncTimestamp, r.indexTime, r.syncRealtime, r.syncComplete, &r.info, r.handlerDuration, r.blockArrival, r.realtimeLatency, r.reorgs, r.rpcDuration, r.rpcErrors, r.historyStart, r.historyEnd, r.groupDuration, r.extract, r.transform, r.completed, r.cache, r.queries, r.active, r.duration, r.requestSize, r.responseSize)
	return r
}

func (r *Registry) IncrementCompleted(event string, count uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingEvents[event] += count
}
func (r *Registry) IncrementCacheRequests(table, kind string, count uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingCache[tableLabel{table, kind}] += count
}
func (r *Registry) IncrementStoreQueries(table, method string, count uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingQueries[tableLabel{table, method}] += count
}

func (r *Registry) flush() {
	for name, n := range r.pendingEvents {
		r.completed.WithLabelValues(name).Add(float64(n))
	}
	for key, n := range r.pendingCache {
		r.cache.WithLabelValues(key.table, key.operation).Add(float64(n))
	}
	for key, n := range r.pendingQueries {
		r.queries.WithLabelValues(key.table, key.operation).Add(float64(n))
	}
	clear(r.pendingEvents)
	clear(r.pendingCache)
	clear(r.pendingQueries)
}

func (r *Registry) FlushIndexing() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flush()
}

// ResetIndexing clears per-run measurements, including database attempts and
// PostgreSQL submissions. Indexing cache and store operation counters are cumulative.
func (r *Registry) ResetIndexing() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flush()
	r.info.settings = nil
	r.dbDuration.Reset()
	r.dbErrors.Reset()
	r.postgresQueries.Reset()
	// The pinned indexing reset retains the last observed sync timestamp.
	r.syncBlock.Reset()
	r.indexTime.Reset()
	r.syncComplete.Reset()
	r.syncRealtime.Reset()
	clear(r.liveChains)
	r.historyStart.Reset()
	r.historyEnd.Reset()
	r.completed.Reset()
	r.handlerDuration.Reset()
	r.reorgs.Reset()
	r.rpcDuration.Reset()
	r.rpcErrors.Reset()
}

func (r *Registry) SetCompleted(event string, value uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed.WithLabelValues(event).Set(float64(value))
}

func (r *Registry) Gather() ([]*dto.MetricFamily, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flush()
	return r.reg.Gather()
}

func (r *Registry) Collect(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	families, err := r.Gather()
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	for _, family := range families {
		if _, err := expfmt.MetricFamilyToText(&b, family); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// BeginHTTP accepts a matched route pattern. Call finish after the response.
func (r *Registry) BeginHTTP(method, path string) func(int, int64, int64, time.Duration) {
	active := r.active.WithLabelValues(method, path)
	active.Inc()
	return func(status int, requestBytes, responseBytes int64, duration time.Duration) {
		active.Dec()
		class := "5XX"
		switch status / 100 {
		case 2:
			class = "2XX"
		case 3:
			class = "3XX"
		case 4:
			class = "4XX"
		}
		r.duration.WithLabelValues(method, path, class).Observe(float64(duration) / float64(time.Millisecond))
		r.requestSize.WithLabelValues(method, path, class).Observe(float64(max(0, requestBytes)))
		r.responseSize.WithLabelValues(method, path, class).Observe(float64(max(0, responseBytes)))
	}
}
