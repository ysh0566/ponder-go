package historical

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestRuntimeBoundedReorg(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, scenario := range []string{"last_block", "parent_changed", "reopen_during_replay", "finalized_changed"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				continuousRoot(t)
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				end := uint64(3)
				if scenario == "parent_changed" {
					end = 4
				}
				s := newContinuousRPC(t, end)
				first, second := make(chan struct{}), make(chan struct{})
				var running atomic.Bool
				s.gate = func(ctx context.Context, id uint64, method string, params []any) error {
					if !running.Load() || method != "eth_getBlockByNumber" || params[1] != true {
						return nil
					}
					var wait <-chan struct{}
					switch {
					case id == 2 && params[0] == "0x3":
						wait = first
					case id == 1 && params[0] == "0x4":
						wait = second
					default:
						return nil
					}
					select {
					case <-wait:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}

				handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
					n, err := continuousNumber(a)
					if err != nil {
						return err
					}
					delta := big.NewInt(1)
					if n == 3 {
						delta, err = a.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40))
						if err != nil {
							return err
						}
					}
					key := store.Row{"id": "shared"}
					row, err := a.Context.DB.Find(ctx, "account", key)
					if err != nil {
						return err
					}
					if row == nil {
						_, err = a.Context.DB.InsertOne(ctx, "account", store.Row{"id": "shared", "balance": delta}, store.Conflict{})
						return err
					}
					v, ok := new(big.Int).SetString(fmt.Sprint(row["balance"]), 10)
					if !ok {
						return fmt.Errorf("invalid stored balance %v", row)
					}
					_, err = a.Context.DB.Update(ctx, "account", key, store.Row{"balance": v.Add(v, delta)})
					return err
				})
				b := runtimeBuild(t, s.p, end, 1, handler)
				if scenario == "parent_changed" {
					for _, chain := range b.Executable().Chains {
						chain["reorgWindow"] = uint64(600)
					}
				}
				f := newManagedFiniteFixture(t, backend, b)
				var epochs []uint64
				var r *RuntimeRunner
				commits := map[uint64][]string{}
				activations := 0
				interrupted := errors.New("stop after first replay commit")
				opts := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "bounded-fork", OpenID: "first", Requesters: runtimeFactory(s.p, &epochs)}
				opts.OnActivated = func(ctx context.Context) error {
					activations++
					running.Store(true)
					return nil
				}
				opts.OnCommitted = func(ctx context.Context, c RuntimeCommit) error {
					cp, err := checkpoint.Decode(c.Head.Checkpoint)
					if err != nil || cp.BlockNumber != 3 {
						return err
					}
					id := c.ChainID
					commits[id] = append(commits[id], c.Head.Hash)
					if len(commits[id]) == 2 {
						if ready, err := r.Ready(ctx); err != nil || ready {
							return fmt.Errorf("served during replacement commits: ready=%v err=%v", ready, err)
						}
						if scenario == "reopen_during_replay" {
							return interrupted
						}
						return nil
					}
					if id == 1 {
						close(first)
					} else {
						from := uint64(3)
						if scenario == "finalized_changed" {
							from = 2
						}
						s.fork(1, from)
						close(second)
					}
					return nil
				}
				r = runtimeOpen(t, backend, opts)
				out, err := r.Run(ctx)
				c, ce := r.Control(ctx)
				if ce != nil {
					t.Fatal(ce)
				}
				if scenario == "finalized_changed" {
					if !errors.Is(err, store.ErrManagedLiveDeepReorg) || out.Completed || out.Ready || c.Serving || c.Reorgs != 0 {
						t.Fatalf("finalized fork result=%+v serving=%t reorgs=%d err=%v", out, c.Serving, c.Reorgs, err)
					}
					return
				}
				if scenario == "reopen_during_replay" {
					if !errors.Is(err, interrupted) || out.Completed || c.Serving || c.Reorgs != 1 || len(c.ReplayTargets) == 0 {
						t.Fatalf("interrupted result=%+v reorgs=%d targets=%v err=%v", out, c.Reorgs, c.ReplayTargets, err)
					}
					if err = r.Close(ctx); err != nil {
						t.Fatal(err)
					}
					// The replacement tip is one second older. Extend the chain so
					// the newly calculated finality cannot regress on cold open.
					s.setLatest(end + 1)
					opts.OpenID, opts.OnCommitted = "second", nil
					r = runtimeOpen(t, backend, opts)
					out, err = r.Run(ctx)
					c, ce = r.Control(ctx)
					if ce != nil || !out.Recovered {
						t.Fatalf("reopened result=%+v controlErr=%v runErr=%v", out, ce, err)
					}
				}
				if err != nil || !out.Completed || !out.Ready || !c.Serving || len(c.ReplayTargets) != 0 || c.Reorgs != 1 || activations != 2 {
					t.Fatalf("result=%+v reorgs=%d activations=%d commits=%v err=%v", out, c.Reorgs, activations, commits, err)
				}
				if scenario != "reopen_during_replay" && (len(commits[1]) != 2 || len(commits[2]) != 2 || commits[1][0] == commits[1][1] || commits[2][0] != commits[2][1]) {
					t.Fatalf("incorrect replacement or peer replay: %v", commits)
				}
				for id, cursor := range c.Cursors {
					if cursor.Hash != s.canonical[id][end]["hash"] {
						t.Fatalf("wrong chain %d tip: %+v", id, cursor)
					}
				}
				if err = r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'")
					want := fmt.Sprint(99 + 42 + (end-1)*2)
					if len(rows) != 1 || rows[0]["balance"] != want {
						t.Fatalf("shared balance=%v want %s", rows, want)
					}
				})
			})
		}
	}
}
