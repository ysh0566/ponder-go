package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// These are supplemental production-boundary controls, not original source
// records. Native rollback/cancellation on PGlite is an explicit safety extension:
// the pinned upstream PGlite query-builder transaction is callback-only.
type localControlBackend struct {
	store.Backend
	mu              sync.Mutex
	enabled         bool
	begins          int
	commits         []store.SourceInterval
	rejectIntervals error
}
type localControlTx struct {
	store.Transaction
	owner *localControlBackend
	span  *store.SourceInterval
}

var localControlRange = regexp.MustCompile(`numrange\(([0-9]+),([0-9]+),`)

func (b *localControlBackend) Begin(ctx context.Context) (store.Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	b.mu.Lock()
	if b.enabled {
		b.begins++
	}
	b.mu.Unlock()
	return &localControlTx{Transaction: tx, owner: b}, nil
}
func (tx *localControlTx) Exec(ctx context.Context, q string, args ...any) error {
	if strings.HasPrefix(q, "INSERT INTO ") && strings.Contains(q, `"intervals"`) {
		tx.owner.mu.Lock()
		fail := tx.owner.rejectIntervals
		tx.owner.mu.Unlock()
		if fail != nil {
			return fail
		}
		if m := localControlRange.FindStringSubmatch(q); m != nil {
			a, _ := strconv.ParseFloat(m[1], 64)
			b, _ := strconv.ParseFloat(m[2], 64)
			s := store.SourceInterval{a, b - 1}
			tx.span = &s
		}
	}
	return tx.Transaction.Exec(ctx, q, args...)
}
func (tx *localControlTx) Commit(ctx context.Context) error {
	e := tx.Transaction.Commit(ctx)
	if e == nil && tx.span != nil {
		tx.owner.mu.Lock()
		if tx.owner.enabled {
			tx.owner.commits = append(tx.owner.commits, *tx.span)
		}
		tx.owner.mu.Unlock()
	}
	return e
}
func (b *localControlBackend) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.enabled = true
	b.begins = 0
	b.commits = nil
}
func (b *localControlBackend) snapshot() (int, []store.SourceInterval) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.begins, append([]store.SourceInterval{}, b.commits...)
}

func localControlRawBlock(t *testing.T) func(uint64) filter.Object {
	t.Helper()
	raw, e := os.ReadFile("../testdata/protocol/encoding.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Cases []struct {
			Name  string
			Input filter.Object
		}
	}
	if e = json.Unmarshal(raw, &f); e != nil {
		t.Fatal(e)
	}
	var base filter.Object
	for _, v := range f.Cases {
		if v.Name == "blocks/full" {
			base = v.Input
		}
	}
	if base == nil {
		t.Fatal("existing raw RPC block fixture missing")
	}
	return func(n uint64) filter.Object {
		b := filter.Object{}
		for k, v := range base {
			b[k] = v
		}
		b["number"] = Hex(n)
		b["timestamp"] = Hex(n + 100)
		b["hash"] = fmt.Sprintf("0x%064x", n+1)
		parent := n
		if n == 0 {
			parent = 0
		}
		b["parentHash"] = fmt.Sprintf("0x%064x", parent)
		b["transactions"] = []filter.Object{}
		return b
	}
}
func localControlSetup(t *testing.T, ctx context.Context, dialect string, filters []filter.Object, last uint64) (SourceLocalHistoricalOptions, *localControlBackend, *store.ProtocolCache) {
	t.Helper()
	b := &localControlBackend{Backend: stagedBackend(t, ctx, dialect, "memory://")}
	t.Cleanup(func() { b.Close() })
	db, e := store.New(ctx, b, stagedBlockModel(), fmt.Sprintf("ponder_go_local_control_%x", time.Now().UnixNano()), schema.Multichain)
	if e != nil {
		t.Fatal(e)
	}
	cache, e := db.EnableProtocolCache(ctx)
	if e != nil {
		t.Fatal(e)
	}
	s, e := cache.SourceSyncStore(0)
	if e != nil {
		t.Fatal(e)
	}
	blocks := localControlRawBlock(t)
	start, final := blocks(0), blocks(last)
	o := SourceLocalHistoricalOptions{ChainID: 1, Store: s, Children: NewSourceHistoricalChildren(nil), Progress: &SourceSyncProgress{Start: start, Finalized: final, ChainID: 1}, Limit: 10}
	for _, f := range filters {
		o.EventCallbacks = append(o.EventCallbacks, protocol.Object{"filter": f, "name": "Control:block", "type": "block"})
	}
	o.CachedIntervals, e = EmptySourceCachedIntervals(filters)
	if e != nil {
		t.Fatal(e)
	}
	o.From, e = sourceLocalCheckpoint(1, start)
	if e != nil {
		t.Fatal(e)
	}
	o.To, e = sourceLocalCheckpoint(1, final)
	if e != nil {
		t.Fatal(e)
	}
	b.reset()
	return o, b, cache
}
func localControlFilters() []filter.Object {
	return []filter.Object{
		{"type": "block", "chainId": float64(1), "fromBlock": float64(0), "interval": float64(1), "offset": float64(0), "include": []any{"block.number", "block.hash", "block.timestamp"}},
		{"type": "log", "chainId": float64(1), "sourceId": "control", "fromBlock": float64(0), "address": "0x" + strings.Repeat("1", 40), "topic0": "0x" + strings.Repeat("a", 64), "topic1": nil, "topic2": nil, "topic3": nil, "hasTransactionReceipt": false},
	}
}
func localControlWait(ctx context.Context, ch <-chan struct{}) error {
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func localControlCollect(ctx context.Context, o SourceLocalHistoricalOptions) ([]float64, error) {
	out := []float64{}
	for cursor, e := range LocalSourceSync(ctx, o) {
		if e != nil {
			return out, e
		}
		out = append(out, cursor)
	}
	return out, nil
}
func localControlCount(t *testing.T, ctx context.Context, b store.Backend, cache *store.ProtocolCache, table string, want int) {
	t.Helper()
	r, e := b.Query(ctx, "SELECT count(*)::text AS n FROM "+schema.Quote(cache.Namespace())+"."+schema.Quote(table))
	if e != nil || len(r.Rows) != 1 || fmt.Sprint(r.Rows[0]["n"]) != fmt.Sprint(want) {
		t.Fatalf("%s count %v %v want%d", table, r, e, want)
	}
}

func TestSourceLocalHistoricalControls(t *testing.T) {
	t.Run("cancelled buffered queue", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		q := &sourceLocalQueue{values: []float64{25, 51}, wake: make(chan struct{}, 1)}
		if value, err, ok := q.next(ctx); ok || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled queue yielded %v, %v, %v", value, err, ok)
		}
	})

	t.Run("estimator bounds", func(t *testing.T) {
		for _, v := range []struct {
			name            string
			span            store.SourceInterval
			elapsed, target time.Duration
			prev, want      float64
		}{
			{"minimum", store.SourceInterval{0, 25}, 100 * time.Second, 10 * time.Second, 25, 25},
			{"growth cap and rounding", store.SourceInterval{0, 25}, time.Millisecond, 10 * time.Second, 25, 38},
			{"zero elapsed", store.SourceInterval{0, 25}, 0, 10 * time.Second, 25, 38},
			{"maximum", store.SourceInterval{0, 100000}, time.Millisecond, 10 * time.Second, 100000, 100000},
			{"zero interval", store.SourceInterval{4, 4}, time.Second, 10 * time.Second, 38, 25},
		} {
			t.Run(v.name, func(t *testing.T) {
				if got := sourceLocalEstimate(v.span, v.elapsed, v.target, v.prev); got != v.want {
					t.Fatalf("got%v want%v", got, v.want)
				}
			})
		}
	})
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"range RPC prefetch and ordered commits", "interval failure rolls back rows", "prefetch error preserves prior interval", "cancel joins and rolls back", "consumer stop joins prefetch", "fully cached zero RPC and writes", "empty interval and empty event completion", "factory-only ranges"} {
				t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					filters := localControlFilters()
					last := uint64(51)
					if name == "empty interval and empty event completion" {
						last = 3
						filters = nil
					}
					if name == "factory-only ranges" {
						factory := filter.Object{"id": "factory-control", "type": "log", "sourceId": "factory-source", "chainId": float64(1), "address": "0x" + strings.Repeat("1", 40), "eventSelector": "0x" + strings.Repeat("a", 64), "childAddressLocation": "topic1", "fromBlock": float64(0)}
						filters = []filter.Object{{"type": "log", "chainId": float64(1), "fromBlock": float64(100), "address": factory, "topic0": "0x" + strings.Repeat("b", 64), "topic1": nil, "topic2": nil, "topic3": nil}}
					}
					o, b, cache := localControlSetup(t, ctx, dialect, filters, last)
					if name == "factory-only ranges" {
						o.Children.Ensure("factory-control")
					}
					block := localControlRawBlock(t)
					marker := errors.New("deliberate acquisition/interval failure")
					secondRange, firstBlock, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
					var rangeOnce, blockOnce sync.Once
					var calls, active atomic.Int64
					var mu sync.Mutex
					var ranges []store.SourceInterval
					o.Requester = sourceHistoricalTestRequester(func(requestCtx context.Context, method string, params, out any) error {
						calls.Add(1)
						active.Add(1)
						defer active.Add(-1)
						switch method {
						case "eth_getLogs":
							q := params.([]any)[0].(filter.Object)
							from, e := sourceHistoricalNumber(q["fromBlock"])
							if e != nil {
								return e
							}
							to, e := sourceHistoricalNumber(q["toBlock"])
							if e != nil {
								return e
							}
							mu.Lock()
							ranges = append(ranges, store.SourceInterval{float64(from), float64(to)})
							mu.Unlock()
							if from == 26 {
								rangeOnce.Do(func() { close(secondRange) })
								if name == "prefetch error preserves prior interval" {
									return marker
								}
								if name == "consumer stop joins prefetch" {
									<-requestCtx.Done()
									return requestCtx.Err()
								}
							}
							return sourceHistoricalTestResult([]filter.Object{}, out)
						case "eth_getBlockByNumber":
							n, e := sourceHistoricalNumber(params.([]any)[0])
							if e != nil {
								return e
							}
							if name == "range RPC prefetch and ordered commits" && n >= 26 {
								_, committed := b.snapshot()
								if len(committed) == 0 || committed[0] != (store.SourceInterval{0, 25}) {
									return fmt.Errorf("next block acquisition preceded first commit")
								}
							}
							if n == 0 {
								blockOnce.Do(func() { close(firstBlock) })
								switch name {
								case "range RPC prefetch and ordered commits":
									if e := localControlWait(requestCtx, release); e != nil {
										return e
									}
								case "cancel joins and rolls back":
									<-requestCtx.Done()
									return requestCtx.Err()
								}
							}
							return sourceHistoricalTestResult(block(n), out)
						default:
							return fmt.Errorf("unexpected control RPC %s", method)
						}
					})
					switch name {
					case "range RPC prefetch and ordered commits":
						type result struct {
							values []float64
							err    error
						}
						done := make(chan result, 1)
						go func() { v, e := localControlCollect(ctx, o); done <- result{v, e} }()
						if e := localControlWait(ctx, firstBlock); e != nil {
							t.Fatal(e)
						}
						if e := localControlWait(ctx, secondRange); e != nil {
							t.Fatal("second actual eth_getLogs did not overlap blocked first interval", e)
						}
						if _, commits := b.snapshot(); len(commits) != 0 {
							t.Fatal("interval committed while first block unfinished", commits)
						}
						close(release)
						var r result
						select {
						case r = <-done:
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						}
						if r.err != nil || !reflect.DeepEqual(r.values, []float64{25, 51}) {
							t.Fatal(r)
						}
						_, commits := b.snapshot()
						if !reflect.DeepEqual(commits, []store.SourceInterval{{0, 25}, {26, 51}}) {
							t.Fatal("unordered persisted intervals", commits)
						}
						actual, e := o.Store.GetIntervals(ctx, filters)
						if e != nil {
							t.Fatal(e)
						}
						for _, entry := range actual.Entries() {
							for _, f := range entry.Values {
								if !reflect.DeepEqual(f.Intervals, []store.SourceInterval{{0, 51}}) {
									t.Fatal("actual interval visibility", f.Intervals)
								}
							}
						}
						localControlCount(t, ctx, b, cache, "blocks", 52)
					case "interval failure rolls back rows":
						b.rejectIntervals = marker
						values, e := localControlCollect(ctx, o)
						if !errors.Is(e, marker) || len(values) != 0 {
							t.Fatal(values, e)
						}
						localControlCount(t, ctx, b, cache, "blocks", 0)
						localControlCount(t, ctx, b, cache, "intervals", 0)
					case "prefetch error preserves prior interval":
						values, e := localControlCollect(ctx, o)
						if !errors.Is(e, marker) || !reflect.DeepEqual(values, []float64{25}) {
							t.Fatal(values, e)
						}
						_, commits := b.snapshot()
						if !reflect.DeepEqual(commits, []store.SourceInterval{{0, 25}}) {
							t.Fatal(commits)
						}
						localControlCount(t, ctx, b, cache, "blocks", 26)
					case "cancel joins and rolls back":
						runCtx, stop := context.WithCancel(ctx)
						done := make(chan error, 1)
						go func() { _, e := localControlCollect(runCtx, o); done <- e }()
						if e := localControlWait(ctx, firstBlock); e != nil {
							t.Fatal(e)
						}
						stop()
						select {
						case e := <-done:
							if !errors.Is(e, context.Canceled) {
								t.Fatal(e)
							}
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						}
						localControlCount(t, ctx, b, cache, "blocks", 0)
						localControlCount(t, ctx, b, cache, "intervals", 0)
					case "consumer stop joins prefetch":
						var got []float64
						for v, e := range LocalSourceSync(ctx, o) {
							if e != nil {
								t.Fatal(e)
							}
							got = append(got, v)
							if e := localControlWait(ctx, secondRange); e != nil {
								t.Fatal(e)
							}
							break
						}
						if !reflect.DeepEqual(got, []float64{25}) {
							t.Fatal(got)
						}
						localControlCount(t, ctx, b, cache, "blocks", 26)
					case "fully cached zero RPC and writes":
						for _, f := range filters {
							if e := o.Store.InsertIntervals(ctx, 1, []store.SourceFilterInterval{{Filter: f, Interval: store.SourceInterval{0, 51}}}, nil); e != nil {
								t.Fatal(e)
							}
						}
						cached, e := o.Store.GetIntervals(ctx, filters)
						if e != nil {
							t.Fatal(e)
						}
						o.CachedIntervals = cached.Entries()
						o.Progress.Current = o.Progress.Finalized
						b.reset()
						got, e := localControlCollect(ctx, o)
						if e != nil || !reflect.DeepEqual(got, []float64{51}) {
							t.Fatal(got, e)
						}
						begins, commits := b.snapshot()
						if begins != 0 || len(commits) != 0 || calls.Load() != 0 {
							t.Fatal("full cache did work", begins, commits, calls.Load())
						}
					case "empty interval and empty event completion":
						got, e := localControlCollect(ctx, o)
						if e != nil || !reflect.DeepEqual(got, []float64{3}) {
							t.Fatal(got, e)
						}
						begins, _ := b.snapshot()
						if begins != 0 || calls.Load() != 0 {
							t.Fatal("empty intervals did work")
						}
						// An empty log result still advances the full requested checkpoint range.
						logFilter := localControlFilters()[1]
						o.EventCallbacks = []protocol.Object{{"filter": logFilter, "name": "Control:log", "type": "contract"}}
						o.CachedIntervals, e = EmptySourceCachedIntervals([]filter.Object{logFilter})
						if e != nil {
							t.Fatal(e)
						}
						o.Progress.Current = nil
						batches := []*SourceRawEventBatch{}
						for batch, e := range LocalSourceEvents(ctx, o) {
							if e != nil {
								t.Fatal(e)
							}
							batches = append(batches, batch)
						}
						if len(batches) != 1 || len(batches[0].Events) != 0 || batches[0].Checkpoint != o.To || batches[0].BlockRange != ([2]float64{0, 3}) {
							t.Fatal("empty event checkpoint lost", batches)
						}
					case "factory-only ranges":
						got, e := localControlCollect(ctx, o)
						if e != nil || !reflect.DeepEqual(got, []float64{51}) {
							t.Fatal(got, e)
						}
						mu.Lock()
						actualRanges := append([]store.SourceInterval{}, ranges...)
						mu.Unlock()
						if !reflect.DeepEqual(actualRanges, []store.SourceInterval{{0, 25}, {26, 51}}) || calls.Load() != 2 {
							t.Fatal("factory-only source RPC", actualRanges, calls.Load())
						}
						localControlCount(t, ctx, b, cache, "blocks", 0)
						localControlCount(t, ctx, b, cache, "intervals", 1)
					}
					if active.Load() != 0 {
						t.Fatal("RPC still active after iterator exit", active.Load())
					}
				})
			}
		})
	}
}
