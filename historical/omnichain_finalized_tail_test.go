package historical

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestRuntimeOmnichainFinalizedTailRecovery(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			g := loadOmniLogsGolden(t)
			all := omniLogsNamedCase(t, g, "small").Snapshots[0].Calls
			// The two nodes stop at height three. Chain two's timestamp is 130,
			// while chain one's last block closes at 140.
			p, _ := omniLogsRPCWithSelectors(t, g, nil, func(_ uint64, selector string) string {
				if selector == "latest" {
					return "0x3"
				}
				return selector
			})
			firstTail := slices.IndexFunc(all, func(e omniLogsEvent) bool { return strings.HasPrefix(e.Key, "1:3:") })
			if firstTail < 1 {
				t.Fatal("fixture lacks a finalized suffix")
			}
			expected := all
			var calls []string
			interrupted := errors.New("tail callback interrupted")
			fail := true
			h := omniLogsHandlers(func() []omniLogsEvent { return expected }, &calls, func(key string) error {
				if fail && key == "1:3:B:1" {
					fail = false
					return interrupted
				}
				return nil
			})
			f := newManagedFiniteFixture(t, backend, omniLogsBuild(t, g, p, h))
			omnichainGoldenStorage(&f)
			var epochs []uint64
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "finalized-tail-recovery", Requesters: runtimeFactory(p, &epochs)}
			o.Historical.QuerySize = 6
			var receipts []RuntimeCommit
			o.OnCommitted = func(_ context.Context, c RuntimeCommit) error { receipts = append(receipts, c); return nil }
			for pass := range 3 {
				calls, receipts = nil, nil
				o.OpenID = fmt.Sprintf("open-%d", pass)
				r := runtimeOpen(t, backend, o)
				out, err := r.Run(t.Context())
				if pass == 0 {
					if err == nil || !strings.Contains(err.Error(), interrupted.Error()) || out.Ready || out.Completed || len(calls) <= firstTail {
						t.Fatalf("failed tail: result=%+v calls=%v err=%v", out, calls, err)
					}
				} else if err != nil || !out.Ready || !out.Completed || !out.Recovered || !slices.Equal(calls, omniLogsKeys(expected)) {
					t.Fatalf("resume %d: result=%+v calls=%v err=%v", pass, out, calls, err)
				}
				if pass == 1 && (len(receipts) != 1 || receipts[0].ChainID != 1 || !receipts[0].Historical || !receipts[0].Safe || receipts[0].Events != len(expected)) {
					t.Fatalf("resumed tail receipts %+v", receipts)
				}
				if pass == 2 && len(receipts) != 0 {
					t.Fatal("completed tail committed twice", receipts)
				}
				control, err := r.Control(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if err = r.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				want := all
				if pass == 0 {
					want = all[:firstTail]
				}
				managedInspect(t, f, func(db store.Backend) {
					rows := managedRows(t, db, "SELECT value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'")
					if len(rows) != 1 || fmt.Sprint(rows[0]["value"]) != strings.Join(omniLogsKeys(want), "|") {
						t.Fatalf("pass %d durable rows %v", pass, rows)
					}
				})
				for _, chain := range g.Chains {
					height := 3
					if pass == 0 && chain.ID == 1 {
						height = 2
					}
					head, err := runtimeHead(chain.ID, chain.Blocks[height])
					if err != nil {
						t.Fatal(err)
					}
					if control.Cursors[chain.ID] != head || control.SafeHeads[chain.ID] != head {
						t.Fatalf("pass %d chain %d cursor %+v safe %+v want %+v", pass, chain.ID, control.Cursors[chain.ID], control.SafeHeads[chain.ID], head)
					}
				}
				expected = all[len(want):]
			}
		})
	}
}
