package historical

import (
	"context"
	"errors"
	"fmt"
	"os"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
	"testing"
	"time"
)

func TestRuntimeSourceBoundaries(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, kind := range []string{"inclusive_start_end", "empty_segments", "historical_refetch", "learned_projection_without_parent"} {
			t.Run(backend+"/"+kind, func(t *testing.T) {
				root := os.Getenv("PONDER_M45_TEST_ROOT")
				if root == "" {
					t.Fatal("retained root required")
				}
				t.Setenv("PONDER_M44_TEST_ROOT", root)
				latest := uint64(3)
				interval := uint64(1)
				if kind == "inclusive_start_end" {
					latest = 4
				}
				if kind == "empty_segments" {
					interval = 4
				}
				p := newFiniteRPC(t, latest)
				count := 0
				h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					count++
					if kind == "historical_refetch" {
						if v, ok := a.Event.Property("block"); ok {
							if _, err := v.Get("gasUsed"); err != nil {
								return err
							}
						}
					}
					return finiteIncrement(ctx, a)
				})
				b := runtimeBuild(t, p, 3, interval, h)
				for _, group := range b.Executable().EventCallbacks {
					for _, cb := range group {
						f := cb["filter"].(map[string]any)
						if kind == "inclusive_start_end" {
							f["fromBlock"] = float64(3)
						}
						if kind == "historical_refetch" {
							f["include"] = []string{"block.number", "block.timestamp", "block.hash", "block.parentHash"}
						}
					}
				}
				f := newManagedFiniteFixture(t, backend, b)
				if kind == "learned_projection_without_parent" {
					for _, group := range b.Executable().EventCallbacks {
						for _, cb := range group {
							cb["filter"].(map[string]any)["include"] = []string{"block.number", "block.timestamp", "block.hash"}
						}
					}
					f.options.Owned.Service.ColumnAccessPattern = indexing.ColumnAccessPattern{"Blocks:block": &indexing.ColumnAccessProfile{Block: []string{"number"}, Count: 100, Resolved: true}}
				}
				epochs := []uint64{}
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "runtime-boundaries-v1", OpenID: "boundary-1", Requesters: runtimeFactory(p, &epochs)}
				r := runtimeOpen(t, backend, o)
				out, err := r.Run(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if !out.Ready || !out.Completed {
					t.Fatal(out)
				}
				c, err := r.Control(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "inclusive_start_end":
					if count != 2 || out.HistoricalBlocks != 2 || out.LiveBlocks != 0 {
						t.Fatalf("inclusive input skipped: %+v calls=%d", out, count)
					}
				case "empty_segments":
					if count != 2 || c.Tail != 6 || out.HistoricalBlocks != 4 || out.LiveBlocks != 2 {
						t.Fatalf("empty execution lost: %+v calls=%d control=%+v", out, count, c)
					}
				case "learned_projection_without_parent":
					if out.Refetches != 0 || count != 6 {
						t.Fatalf("learned projection broke header identity: %+v calls=%d", out, count)
					}
				case "historical_refetch":
					if out.Refetches < 1 || out.Refetches > 4 || count != 6+out.Refetches {
						t.Fatalf("actual refetch/retry=%+v calls=%d", out, count)
					}
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
func TestRuntimeCancellationRecovery(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			if root == "" {
				t.Fatal("retained root required")
			}
			t.Setenv("PONDER_M44_TEST_ROOT", root)
			p := newFiniteRPC(t, 3)
			entered := make(chan struct{}, 1)
			block := true
			h := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				if err := finiteIncrement(ctx, a); err != nil {
					return err
				}
				if block {
					select {
					case entered <- struct{}{}:
					default:
					}
					<-ctx.Done()
					return ctx.Err()
				}
				return nil
			})
			b := runtimeBuild(t, p, 3, 1, h)
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "runtime-cancel-v1", OpenID: "cancel-1", Requesters: runtimeFactory(p, &epochs)}
			r := runtimeOpen(t, backend, o)
			done := make(chan error, 1)
			go func() { _, err := r.Run(context.Background()); done <- err }()
			select {
			case <-entered:
			case <-time.After(30 * time.Second):
				t.Fatal("handler did not start")
			}
			if ready, err := r.Ready(context.Background()); err != nil || ready {
				t.Fatalf("ready during initial callback=%v err=%v", ready, err)
			}
			if err := r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation result", err)
			}
			block = false
			o.OpenID = "cancel-2"
			r = runtimeOpen(t, backend, o)
			out, err := r.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !out.Completed || !out.Ready {
				t.Fatal(out)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(b store.Backend) {
				rows := managedRows(t, b, "SELECT balance::text AS balance FROM "+fmt.Sprintf("%q", f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || rows[0]["balance"] != "3" || rows[1]["balance"] != "3" {
					t.Fatalf("rollback/replay business rows=%v", rows)
				}
			})
		})
	}
}
