package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"testing"
	"time"
)

func TestRuntimeContinuousBoundaryReopen(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, phase := range []string{"prepare", "business", "ack"} {
			t.Run(backend+"/"+phase, func(t *testing.T) {
				continuousRoot(t)
				s := newContinuousRPC(t, 3)
				b := runtimeBuild(t, s.p, 4, 1, indexing.Handler(finiteIncrement))
				for _, c := range b.Executable().Chains {
					c["reorgWindow"] = uint64(600)
				}
				f := newManagedFiniteFixture(t, backend, b)
				epochs := []uint64{}
				var r *RuntimeRunner
				forked, fired := false, false
				boundary := errors.New("controlled application boundary: " + phase)
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "continuous-boundary-v1", OpenID: "boundary-1", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnDurableReorg: func(ctx context.Context, e store.ManagedLiveDomainStage) error {
					if e.Phase == phase && !fired {
						fired = true
						call, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
						err := r.Close(call)
						cancel()
						if !errors.Is(err, store.ErrManagedLiveBusy) {
							return fmt.Errorf("durable observer Close did not reject self-wait: %v", err)
						}
						if ready, err := r.Ready(ctx); err != nil || ready {
							return fmt.Errorf("native boundary exposed ready: %v %v", ready, err)
						}
						return boundary
					}
					return nil
				}, OnProgress: func(ctx context.Context, e RuntimeLiveProgress) error {
					if e.Type != "block" || forked {
						return nil
					}
					c, err := r.Control(ctx)
					if err != nil {
						return err
					}
					both := true
					for _, h := range c.Cursors {
						cp, _ := checkpoint.Decode(h.Checkpoint)
						both = both && cp.BlockNumber >= 3
					}
					if both {
						forked = true
						s.fork(1, 3)
						s.setLatest(4)
					}
					return nil
				}}}
				r = runtimeOpen(t, backend, o)
				out, err := r.Run(context.Background())
				if !errors.Is(err, boundary) || out.Ready || out.Completed || !fired {
					t.Fatalf("boundary not fenced result=%+v fired=%v err=%v", out, fired, err)
				}
				if ready, err := r.Ready(context.Background()); err != nil || ready {
					t.Fatalf("failed boundary ready=%v %v", ready, err)
				}
				if err = r.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				o.OpenID = "boundary-2"
				o.Live = &RuntimeLiveOptions{PollingInterval: time.Millisecond}
				r2 := runtimeOpen(t, backend, o)
				c, err := r2.Control(context.Background())
				if err != nil || c.Serving || c.Recoveries != 1 || len(c.ReplayTargets) == 0 {
					t.Fatalf("nonempty boundary recovery=%+v err=%v", c, err)
				}
				out, err = r2.Run(context.Background())
				if err != nil || !out.Ready || !out.Completed || !out.Recovered {
					t.Fatalf("boundary retry %+v err=%v", out, err)
				}
				evidence, err := r2.domains.DomainEvidence(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var ledger struct{ Records []struct{ Kind string } }
				if err = json.Unmarshal(evidence, &ledger); err != nil {
					t.Fatal(err)
				}
				prepares, acks := 0, 0
				for _, record := range ledger.Records {
					if record.Kind == "prepare" {
						prepares++
					}
					if record.Kind == "ack" {
						acks++
					}
				}
				if prepares != 1 || acks != 1 {
					t.Fatalf("native intent/ack duplicated prepare=%d ack=%d ledger=%s", prepares, acks, evidence)
				}
				if err = r2.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
					if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "4" || fmt.Sprint(rows[1]["balance"]) != "4" {
						t.Fatalf("reopen lost/duplicated business writes: %v", rows)
					}
				})
			})
		}
	}
}
