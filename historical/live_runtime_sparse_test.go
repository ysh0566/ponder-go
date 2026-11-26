package historical

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"

	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

const sparseEnd = 13000
const sparseDistant = 12020

func sparseHash(id, n uint64, kind string) string {
	return fmt.Sprintf("0x%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", id, int64(n), kind))))
}

func sparseLogsRPC(t *testing.T, active int) (omniLogsGolden, *finiteRPC) {
	g := loadOmniLogOnlyGolden(t)[0].Data
	templates := []map[string]any{g.Chains[0].Blocks[0], g.Chains[1].Blocks[0]}
	header := func(id, n uint64) map[string]any {
		if id < 1 || id > 2 || n > sparseEnd+1 {
			return nil
		}
		b := maps.Clone(templates[id-1])
		b["transactions"] = []any{}
		b["number"], b["timestamp"] = fmt.Sprintf("0x%x", n), fmt.Sprintf("0x%x", 100+n)
		b["hash"], b["parentHash"] = sparseHash(id, n, "block"), sparseHash(id, n-1, "block")
		return b
	}
	for i := range g.Chains {
		c := &g.Chains[i]
		blocks, logs := c.Blocks, c.Logs
		c.Blocks = nil
		c.Logs = nil
		c.Latest = fmt.Sprintf("0x%x", sparseEnd+1)
		if i >= active {
			continue
		}
		for _, pair := range [][2]uint64{{1, 1}, {3, sparseDistant}} {
			original, n := pair[0], pair[1]
			b := header(c.ID, n)
			txs := []any{}
			for _, row := range blocks[original]["transactions"].([]any) {
				tx := maps.Clone(row.(map[string]any))
				tx["hash"], tx["blockHash"], tx["blockNumber"] = sparseHash(c.ID, n, "transaction"), b["hash"], b["number"]
				txs = append(txs, tx)
			}
			b["transactions"] = txs
			c.Blocks = append(c.Blocks, b)
			for _, row := range logs {
				number, _ := uintQuantity(row["blockNumber"])
				if number != original {
					continue
				}
				log := maps.Clone(row)
				log["transactionHash"], log["blockHash"], log["blockNumber"] = sparseHash(c.ID, n, "transaction"), b["hash"], b["number"]
				c.Logs = append(c.Logs, log)
			}
		}
	}
	p, _ := omniLogsRPCWithHeaders(t, g, header)
	return g, p
}

func seedSparseSource(t *testing.T, r *RuntimeRunner, g omniLogsGolden) {
	t.Helper()
	ctx, scope, err := r.domains.NewRuntimeScope(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Seal(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	err = r.domains.WithRuntimeSync(ctx, func(io context.Context, s *store.SourceSyncStore) error {
		for i, c := range g.Chains {
			var intervals []store.SourceFilterInterval
			for _, cb := range r.build.EventCallbacks[i] {
				f := cb["filter"].(map[string]any)
				intervals = append(intervals, store.SourceFilterInterval{Filter: f, Interval: store.SourceInterval{filter.FilterFromBlock(f), filter.FilterToBlock(f)}})
			}
			if err := s.InsertIntervals(io, c.ID, intervals, nil); err != nil {
				return err
			}
			for _, b := range c.Blocks {
				if err := s.InsertBlocks(io, c.ID, []map[string]any{b}); err != nil {
					return err
				}
				for _, tx := range b["transactions"].([]any) {
					if err := s.InsertTransactions(io, c.ID, []map[string]any{tx.(map[string]any)}); err != nil {
						return err
					}
				}
			}
			if len(c.Logs) > 0 {
				if err := s.InsertLogs(io, c.ID, c.Logs); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func sparseExpectedCalls(active int) []string {
	all := []string{}
	for _, n := range []int{1, sparseDistant} {
		for id := 1; id <= active; id++ {
			for i := range 4 {
				for _, k := range []string{"A", "B"} {
					all = append(all, fmt.Sprintf("%d:%d:%d:%s", id, n, i, k))
				}
			}
		}
	}
	return all
}

func sparseHandlers(calls *[]string, onWritten func(string) error) map[string]indexing.Handler {
	handlers := map[string]indexing.Handler{}
	for _, k := range []string{"A", "B"} {
		handlers[k] = func(ctx context.Context, a *indexing.HandlerArgs) error {
			block, err := omniLogsFields(a.Event.Get("block"), "number", "hash")
			if err != nil {
				return err
			}
			n, err := uintQuantity(block["number"])
			if err != nil {
				return err
			}
			id := finiteChain(a)
			if block["hash"] != sparseHash(id, n, "block") {
				return fmt.Errorf("unexpected callback block hash")
			}
			log, err := omniLogsFields(a.Event.Get("log"), "logIndex")
			if err != nil {
				return err
			}
			i, err := uintQuantity(log["logIndex"])
			if err != nil {
				return err
			}
			args, err := omniLogsFields(a.Event.Get("args"), "value")
			if err != nil {
				return err
			}
			original := uint64(1)
			if n == sparseDistant {
				original = 3
			}
			cp := checkpoint.Block(100+n, id, n, false)
			cp.EventType, cp.EventIndex = checkpoint.Logs, i
			if a.Event.Get("id") != checkpoint.MustEncode(cp) || fmt.Sprint(args["value"]) != fmt.Sprint(id*100+original*10+i) {
				return fmt.Errorf("callback checkpoint or ABI input differs at chain %d block %d log %d", id, n, i)
			}
			key := fmt.Sprintf("%d:%d:%d:%s", id, n, i, k)
			*calls = append(*calls, key)
			_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": key, "value": key}, store.Conflict{})
			if err == nil && onWritten != nil {
				return onWritten(key)
			}
			return err
		}
	}
	return handlers
}

func assertSparseRows(t *testing.T, f finiteFixture, expected []string) {
	t.Helper()
	managedInspect(t, f, func(b store.Backend) {
		rows := managedRows(t, b, "SELECT id,value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account")
		want := append([]string{}, expected...)
		slices.Sort(want)
		got := []string{}
		for _, row := range rows {
			if row["id"] != row["value"] {
				t.Fatal(row)
			}
			got = append(got, row["id"].(string))
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("committed rows %v want %v", got, want)
		}
	})
}

func TestRuntimeOmnichainSparseRanges(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		for _, query := range []int{12000, 6} {
			for _, scenario := range []string{"both", "one", "empty", "failure"} {
				t.Run(fmt.Sprintf("%s/%d/%s", dialect, query, scenario), func(t *testing.T) {
					continuousRoot(t)
					active := 2
					if scenario == "one" {
						active = 1
					} else if scenario == "empty" {
						active = 0
					}
					g, p := sparseLogsRPC(t, active)
					all := sparseExpectedCalls(active)
					failAt := fmt.Sprintf("2:%d:1:A", sparseDistant)
					fault := errors.New("controlled sparse callback failure")
					failing := scenario == "failure"
					var calls []string
					handlers := sparseHandlers(&calls, func(key string) error {
						if failing && key == failAt {
							return fault
						}
						return nil
					})
					build := omniLogOnlyRangeBuild(t, g, p, handlers, sparseEnd)
					f := newManagedFiniteFixture(t, dialect, build)
					omnichainGoldenStorage(&f)
					var epochs []uint64
					o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "native-sparse-source", Requesters: runtimeFactory(p, &epochs)}
					o.Historical.QuerySize = query
					prefix := 0
					for pass := 1; pass <= 2; pass++ {
						o.OpenID = fmt.Sprintf("sparse-%d", pass)
						r := runtimeOpen(t, dialect, o)
						if pass == 1 {
							seedSparseSource(t, r, g)
						}
						calls = []string{}
						out, err := r.Run(context.Background())
						wantCalls := all[prefix:]
						stored := len(all)
						if failing {
							if err == nil || !strings.Contains(err.Error(), fault.Error()) || out.Ready || out.Completed {
								t.Fatalf("expected callback rollback: %+v %v", out, err)
							}
							wantCalls = all[:slices.Index(all, failAt)+1]
							stored = 0
							if query == 6 {
								stored = 16
							}
						} else if err != nil || !out.Completed || !out.Ready {
							t.Fatalf("run %+v: %v", out, err)
						}
						if !slices.Equal(calls, wantCalls) {
							t.Fatalf("calls %v want %v", calls, wantCalls)
						}
						if pass == 2 && !out.Recovered {
							t.Fatal("expected cold recovery")
						}
						c, err := r.Control(context.Background())
						if err != nil {
							t.Fatal(err)
						}
						if c.Version != "managed-live-omnichain-logonly-v17" {
							t.Fatalf("format %s", c.Version)
						}
						for id := uint64(1); id <= 2; id++ {
							n, up := uint64(1), false
							if failing {
								up = stored != 0
							} else if int(id) <= active {
								n, up = sparseEnd, true
							}
							cp := checkpoint.MustEncode(checkpoint.Block(100+n, id, n, up))
							if c.LogicalSafe[id] != cp || c.LogicalCursors[id] != cp || c.LogicalFinality[id] != cp {
								t.Fatalf("chain %d positions %+v want %s", id, c, cp)
							}
						}
						if err := r.Close(context.Background()); err != nil {
							t.Fatal(err)
						}
						assertSparseRows(t, f, all[:stored])
						if !failing {
							if scenario == "empty" && query == 6 {
								assertSparseColdRejection(t, dialect, f, o)
							}
							break
						}
						failing = false
						prefix = stored
					}
				})
			}
		}
	}
}

// A Ready all-empty chain has no batch of event-bearing segments to revisit.
// Recovery must still read and validate the stored scan pages.
func assertSparseColdRejection(t *testing.T, dialect string, f finiteFixture, o RuntimeOptions) {
	t.Helper()
	managedInspect(t, f, func(db store.Backend) {
		table := schema.Quote(f.options.Owned.Storage.BusinessNamespace) + "." + schema.Quote("_ponder_go_live_lineage_pages")
		rows := managedRows(t, db, "DELETE FROM "+table+" WHERE chain_id=1 AND first_block=1 RETURNING first_block")
		if len(rows) != 1 {
			t.Fatalf("expected first scan page, removed %d", len(rows))
		}
	})
	o.OpenID = "sparse-corrupt-reopen"
	open := OpenRuntimePostgres
	if dialect == "pglite" {
		open = OpenRuntimePGlite
	}
	r, err := open(context.Background(), o)
	if r != nil {
		defer r.Close(context.Background())
	}
	if !errors.Is(err, store.ErrManagedLiveBatchConflict) {
		t.Fatalf("cold recovery accepted missing scan page: %v", err)
	}
}
