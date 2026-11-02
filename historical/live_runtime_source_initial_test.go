package historical

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
	"testing"
	"time"
)

type sourceInitialGolden struct {
	ChainID uint64 `json:"chainId"`
	Name    string `json:"name"`
	Initial string `json:"initial"`
}

func sourceInitialGoldens(t *testing.T, name string) map[uint64]string {
	t.Helper()
	raw, err := os.ReadFile("testdata/source-initial-17c6d5ba.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []sourceInitialGolden
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	out := map[uint64]string{}
	for _, r := range rows {
		if r.Name == name {
			out[r.ChainID] = r.Initial
		}
	}
	if len(out) != 2 {
		t.Fatalf("missing actual pinned oracle matrix %s: %v", name, out)
	}
	return out
}

func TestRuntimeSourceInitialCheckpoint(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			continuousRoot(t)
			s := newContinuousRPC(t, 3)
			calls := 0
			setup := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				calls++
				_, err := a.Context.DB.InsertOne(ctx, "account", store.Row{"id": fmt.Sprintf("setup-%d", finiteChain(a)), "balance": "99"}, store.Conflict{})
				return err
			})
			b := finiteBuild(t, s.p, 3, true, indexing.Handler(finiteIncrement), setup)
			for _, chain := range b.Executable().Chains {
				chain["reorgWindow"] = uint64(1)
			}
			f := newManagedFiniteFixture(t, backend, b)
			epochs := []uint64{}
			want := sourceInitialGoldens(t, "start1_final2")
			observed := 0
			var r *RuntimeRunner
			o := RuntimeOptions{SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "source-exact-initial-v9", OpenID: "source-exact-initial-first", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}, OnSetupCommitted: func(ctx context.Context) error {
				observed++
				got, err := r.PublicCheckpoints(ctx)
				if err != nil {
					return err
				}
				for id, cp := range want {
					if got[id] != cp {
						return fmt.Errorf("actual source initial checkpoint differs chain%d: got=%s oracle=%s", id, got[id], cp)
					}
				}
				if yes, e := r.Ready(ctx); e != nil || yes {
					return fmt.Errorf("ready before real source execution %v %v", yes, e)
				}
				return nil
			}}
			r = runtimeOpen(t, backend, o)
			out, err := r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || observed != 1 || calls != 2 {
				t.Fatalf("source initial runtime=%+v observed=%d setupcalls=%d err=%v", out, observed, calls, err)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			o.OpenID = "source-exact-initial-reopen"
			r = runtimeOpen(t, backend, o)
			out, err = r.Run(context.Background())
			if err != nil || !out.Ready || !out.Completed || !out.Recovered || observed != 1 || calls != 2 {
				t.Fatalf("source initial replay=%+v observed=%d setupcalls=%d err=%v", out, observed, calls, err)
			}
		})
	}
}
